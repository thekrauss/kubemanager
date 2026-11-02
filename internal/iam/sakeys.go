package iam

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/audit"
)

// Clés asymétriques des comptes de service (iam.md §7.2) : le client garde la
// clé privée et signe une assertion JWT qu'il échange auprès du STS contre un
// jeton court (RFC 7523). betorun ne stocke que la clé publique ; une clé
// générée par betorun est remise une seule fois, dans un fichier de clé.

const (
	SAKeyAlgES256 = "ES256"
	SAKeyAlgRS256 = "RS256"

	DefaultSAKeyLifetimeDays = 90
	MaxSAKeyLifetimeDays     = 365
	// MaxActiveSAKeys : clés valides simultanées par compte (rotation à deux
	// clés possible, prolifération non).
	MaxActiveSAKeys = 10
	minRSABits      = 2048

	// KeyFileType : type du fichier de clé remis au client.
	KeyFileType = "betorun_service_account_key"
)

var (
	ErrInvalidPublicKey   = errors.New("public_key_pem must be a PEM \"PUBLIC KEY\": ECDSA P-256 or RSA of at least 2048 bits")
	ErrInvalidSAKeyExpiry = errors.New("expires_in_days must be between 1 and 365")
	ErrTooManySAKeys      = errors.New("this service account already has 10 valid keys: disable one first")
	ErrSAKeyNotFound      = errors.New("service account key not found")
)

// ServiceAccountKey : clé publique enregistrée (jamais de clé privée).
type ServiceAccountKey struct {
	ID               uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	KeyID            string     `json:"key_id"`
	ServiceAccountID uuid.UUID  `gorm:"type:uuid" json:"service_account_id"`
	Algorithm        string     `json:"algorithm"`
	PublicKeyPEM     string     `gorm:"column:public_key_pem" json:"public_key_pem"`
	Origin           string     `json:"origin"` // generated | uploaded
	DisplayName      string     `json:"display_name"`
	ValidAfter       time.Time  `json:"valid_after"`
	ValidBefore      time.Time  `json:"valid_before"`
	DisabledAt       *time.Time `json:"disabled_at,omitempty"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
	CreatedBy        string     `json:"-"`
	CreatedAt        time.Time  `json:"created_at"`
}

func (ServiceAccountKey) TableName() string { return "service_account_keys" }

// CreateSAKeyRequest : sans public_key_pem, betorun génère la paire (ES256).
type CreateSAKeyRequest struct {
	DisplayName   string `json:"display_name" description:"Libellé de la clé"`
	PublicKeyPEM  string `json:"public_key_pem" description:"Clé publique PEM à enregistrer (la clé privée ne quitte pas le client). Vide : betorun génère la paire et remet la clé privée une seule fois."`
	ExpiresInDays int    `json:"expires_in_days" description:"1 à 365 (défaut 90)"`
}

// KeyFile : fichier de clé remis une seule fois au client (clé générée).
type KeyFile struct {
	Type             string `json:"type"`
	ProjectID        string `json:"project_id,omitempty"`
	ClusterID        string `json:"cluster_id,omitempty"`
	ServiceAccountID string `json:"service_account_id"`
	Principal        string `json:"principal"`
	KeyID            string `json:"key_id"`
	Algorithm        string `json:"algorithm"`
	PrivateKey       string `json:"private_key"`
	TokenURI         string `json:"token_uri"`
}

// CreatedSAKey : clé enregistrée, avec le fichier de clé si betorun l'a générée.
type CreatedSAKey struct {
	ServiceAccountKey
	KeyFile *KeyFile `json:"key_file,omitempty"`
}

// ParsePublicKeyPEM : clé publique PKIX acceptée et algorithme de signature associé.
func ParsePublicKeyPEM(raw string) (crypto.PublicKey, string, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(raw)))
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, "", ErrInvalidPublicKey
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, "", ErrInvalidPublicKey
	}
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if k.Curve != elliptic.P256() {
			return nil, "", ErrInvalidPublicKey
		}
		return k, SAKeyAlgES256, nil
	case *rsa.PublicKey:
		if k.N.BitLen() < minRSABits {
			return nil, "", ErrInvalidPublicKey
		}
		return k, SAKeyAlgRS256, nil
	}
	return nil, "", ErrInvalidPublicKey
}

// keyFileMeta : rattachement du compte, recopié dans le fichier de clé.
type keyFileMeta struct {
	ProjectID string
	ClusterID string
}

// CreateSAKey : nouvelle clé asymétrique. Exige actAs sur le compte, comme
// une clé API : la clé permet d'agir en son nom.
func (s *ServiceAccountService) CreateSAKey(ctx context.Context, caller Principal, projectID, saID uuid.UUID, req CreateSAKeyRequest) (*CreatedSAKey, error) {
	sa, err := s.Get(ctx, projectID, saID)
	if err != nil {
		return nil, err
	}
	d, err := s.Engine.Check(ctx, caller, ServiceAccountsActAs, sa.Resource)
	if err != nil {
		return nil, err
	}
	if !d.Allowed {
		return nil, ErrActAsRequired
	}
	return s.createSAKey(ctx, s.DB, sa.ID, string(caller), req, keyFileMeta{ProjectID: projectID.String()})
}

func (s *ServiceAccountService) createSAKey(ctx context.Context, db *gorm.DB, saID uuid.UUID, createdBy string, req CreateSAKeyRequest, meta keyFileMeta) (*CreatedSAKey, error) {
	days := req.ExpiresInDays
	if days == 0 {
		days = DefaultSAKeyLifetimeDays
	}
	if days < 1 || days > MaxSAKeyLifetimeDays {
		return nil, ErrInvalidSAKeyExpiry
	}
	now := s.now()
	var active int64
	if err := db.WithContext(ctx).Model(&ServiceAccountKey{}).
		Where("service_account_id = ? AND disabled_at IS NULL AND valid_before > ?", saID, now).
		Count(&active).Error; err != nil {
		return nil, err
	}
	if active >= MaxActiveSAKeys {
		return nil, ErrTooManySAKeys
	}

	key := ServiceAccountKey{ID: uuid.New(), ServiceAccountID: saID, DisplayName: truncate(strings.TrimSpace(req.DisplayName), maxDisplayNameLen),
		ValidAfter: now, ValidBefore: now.Add(time.Duration(days) * day), CreatedBy: createdBy, CreatedAt: now}
	idBytes := make([]byte, saKeyIDBytes)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, err
	}
	key.KeyID = hex.EncodeToString(idBytes)

	var privatePEM string
	if strings.TrimSpace(req.PublicKeyPEM) != "" {
		_, alg, err := ParsePublicKeyPEM(req.PublicKeyPEM)
		if err != nil {
			return nil, err
		}
		key.Algorithm, key.Origin, key.PublicKeyPEM = alg, "uploaded", strings.TrimSpace(req.PublicKeyPEM)+"\n"
	} else {
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
		if err != nil {
			return nil, err
		}
		privDER, err := x509.MarshalPKCS8PrivateKey(priv)
		if err != nil {
			return nil, err
		}
		key.Algorithm, key.Origin = SAKeyAlgES256, "generated"
		key.PublicKeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
		privatePEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))
	}
	if err := db.WithContext(ctx).Create(&key).Error; err != nil {
		return nil, err
	}
	audit.Annotate(ctx, "service_account_key_id", key.KeyID)
	audit.Annotate(ctx, "valid_before", key.ValidBefore)

	out := &CreatedSAKey{ServiceAccountKey: key}
	if privatePEM != "" {
		out.KeyFile = &KeyFile{Type: KeyFileType, ProjectID: meta.ProjectID, ClusterID: meta.ClusterID,
			ServiceAccountID: saID.String(), Principal: string(ServiceAccountPrincipal(saID)), KeyID: key.KeyID,
			Algorithm: key.Algorithm, PrivateKey: privatePEM, TokenURI: s.TokenURI}
	}
	return out, nil
}

func (s *ServiceAccountService) ListSAKeys(ctx context.Context, projectID, saID uuid.UUID) ([]ServiceAccountKey, error) {
	sa, err := s.Get(ctx, projectID, saID)
	if err != nil {
		return nil, err
	}
	var keys []ServiceAccountKey
	err = s.DB.WithContext(ctx).Where("service_account_id = ?", sa.ID).Order("created_at DESC").Find(&keys).Error
	return keys, err
}

// DisableSAKey : la clé ne permet plus d'obtenir de jeton, et les jetons
// déjà émis avec elle sont refusés (vérification du STS, ≤ 30 s).
func (s *ServiceAccountService) DisableSAKey(ctx context.Context, projectID, saID uuid.UUID, keyID string) error {
	sa, err := s.Get(ctx, projectID, saID)
	if err != nil {
		return err
	}
	res := s.DB.WithContext(ctx).Model(&ServiceAccountKey{}).
		Where("service_account_id = ? AND key_id = ? AND disabled_at IS NULL", sa.ID, keyID).
		Update("disabled_at", s.now())
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrSAKeyNotFound
	}
	audit.Annotate(ctx, "service_account_key_id", keyID)
	return nil
}
