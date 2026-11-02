// Package sts : service de jetons machine de betorun (docs/architecture/iam.md §7.3).
//
// Le STS émet des jetons d'accès courts (JWT ES256, ≤ 1 h) pour les comptes
// de service, en échange d'une preuve : assertion signée par une clé de
// compte de service (RFC 7523), puis jeton OIDC d'une CI fédérée (RFC 8693).
// Les clés publiques sont publiées en JWKS ; l'API vérifie les jetons
// localement, sans appel réseau.
package sts

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"gorm.io/gorm"
)

const (
	// Algorithm : signature des jetons émis.
	Algorithm = "ES256"
	// RotationPeriod : âge d'une clé de signature au-delà duquel on la remplace.
	RotationPeriod = 30 * 24 * time.Hour
	// PublishAhead : une nouvelle clé est publiée (JWKS) avant de signer, pour
	// que les vérificateurs qui mettent le JWKS en cache la connaissent déjà.
	PublishAhead = 24 * time.Hour
	// MaxTokenTTL : durée de vie maximale d'un jeton émis.
	MaxTokenTTL = time.Hour
	// retireMargin : une clé remplacée reste publiée tant que des jetons
	// signés par elle peuvent circuler, plus cette marge.
	retireMargin = time.Hour
	// purgeAfter : une clé retirée est supprimée de la base après ce délai.
	purgeAfter = 7 * 24 * time.Hour
	// missReloadInterval : un kid inconnu provoque au plus un rechargement
	// par intervalle (pas d'amplification par des jetons forgés).
	missReloadInterval = 10 * time.Second
)

var (
	ErrNoSigningKey = errors.New("sts: no active signing key")
	ErrInvalidKEK   = errors.New("sts: key encryption key must be 32 bytes, base64-encoded")
)

// signingKeyRow : table iam_signing_keys.
type signingKeyRow struct {
	Kid           string `gorm:"primaryKey"`
	Algorithm     string
	PublicJWK     []byte `gorm:"column:public_jwk;type:jsonb"`
	PrivateKeyEnc []byte `gorm:"column:private_key_enc"`
	CreatedAt     time.Time
	ActivateAt    time.Time
	RetireAt      *time.Time
}

func (signingKeyRow) TableName() string { return "iam_signing_keys" }

type signingKey struct {
	kid        string
	private    *ecdsa.PrivateKey
	jwk        JWK
	createdAt  time.Time
	activateAt time.Time
	retireAt   *time.Time
}

// JWK : clé publique EC au format RFC 7517.
type JWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

// JWKS : jeu de clés publié.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// KeyRing : clés de signature, chiffrées en base par une KEK hors base
// (remplaçable par un KMS : seul ce fichier connaît le format de stockage).
type KeyRing struct {
	db   *gorm.DB
	aead cipher.AEAD
	now  func() time.Time

	mu         sync.RWMutex
	keys       map[string]*signingKey
	lastReload time.Time
}

// NewKeyRing : kek est la clé de chiffrement (32 octets, base64).
func NewKeyRing(db *gorm.DB, kekBase64 string) (*KeyRing, error) {
	kek, err := base64.StdEncoding.DecodeString(kekBase64)
	if err != nil || len(kek) != 32 {
		return nil, ErrInvalidKEK
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &KeyRing{db: db, aead: aead, now: time.Now, keys: map[string]*signingKey{}}, nil
}

// Rotate garantit une clé active et prépare la suivante à l'échéance.
// Idempotent et sûr entre réplicas (verrou consultatif Postgres).
func (k *KeyRing) Rotate(ctx context.Context) error {
	err := k.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('iam_signing_keys'))`).Error; err != nil {
			return err
		}
		now := k.now()
		var rows []signingKeyRow
		if err := tx.Where("retire_at IS NULL OR retire_at > ?", now).Order("created_at DESC").Find(&rows).Error; err != nil {
			return err
		}
		activate := time.Time{}
		switch {
		case len(rows) == 0:
			activate = now // première clé : utilisable tout de suite
		case now.Sub(rows[0].CreatedAt) >= RotationPeriod:
			activate = now.Add(PublishAhead)
		}
		if !activate.IsZero() {
			// Les clés en place restent publiées jusqu'à ce que leurs derniers
			// jetons aient expiré.
			retire := activate.Add(MaxTokenTTL + retireMargin)
			if err := tx.Model(&signingKeyRow{}).Where("retire_at IS NULL").Update("retire_at", retire).Error; err != nil {
				return err
			}
			row, err := k.newKey(now, activate)
			if err != nil {
				return err
			}
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		return tx.Where("retire_at < ?", now.Add(-purgeAfter)).Delete(&signingKeyRow{}).Error
	})
	if err != nil {
		return err
	}
	return k.Reload(ctx)
}

func (k *KeyRing) newKey(now, activate time.Time) (*signingKeyRow, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	kidBytes := make([]byte, 12)
	if _, err := rand.Read(kidBytes); err != nil {
		return nil, err
	}
	kid := "sts-" + hex.EncodeToString(kidBytes)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	enc := k.aead.Seal(nonce, nonce, der, []byte(kid)) // kid en données associées
	jwk, err := json.Marshal(publicJWK(kid, &priv.PublicKey))
	if err != nil {
		return nil, err
	}
	return &signingKeyRow{Kid: kid, Algorithm: Algorithm, PublicJWK: jwk, PrivateKeyEnc: enc,
		CreatedAt: now, ActivateAt: activate}, nil
}

// Reload relit les clés publiées.
func (k *KeyRing) Reload(ctx context.Context) error {
	now := k.now()
	var rows []signingKeyRow
	if err := k.db.WithContext(ctx).Where("retire_at IS NULL OR retire_at > ?", now).Find(&rows).Error; err != nil {
		return err
	}
	keys := make(map[string]*signingKey, len(rows))
	for _, r := range rows {
		key, err := k.decrypt(r)
		if err != nil {
			return fmt.Errorf("sts: signing key %s: %w", r.Kid, err)
		}
		keys[r.Kid] = key
	}
	k.mu.Lock()
	k.keys = keys
	k.lastReload = now
	k.mu.Unlock()
	return nil
}

func (k *KeyRing) decrypt(r signingKeyRow) (*signingKey, error) {
	ns := k.aead.NonceSize()
	if len(r.PrivateKeyEnc) < ns {
		return nil, errors.New("encrypted key too short")
	}
	der, err := k.aead.Open(nil, r.PrivateKeyEnc[:ns], r.PrivateKeyEnc[ns:], []byte(r.Kid))
	if err != nil {
		return nil, errors.New("cannot decrypt (wrong key encryption key?)")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	priv, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an ECDSA key")
	}
	return &signingKey{kid: r.Kid, private: priv, jwk: publicJWK(r.Kid, &priv.PublicKey),
		createdAt: r.CreatedAt, activateAt: r.ActivateAt, retireAt: r.RetireAt}, nil
}

// active : clé de signature courante (la plus récente déjà active).
func (k *KeyRing) active() (*signingKey, error) {
	now := k.now()
	k.mu.RLock()
	defer k.mu.RUnlock()
	var best *signingKey
	for _, key := range k.keys {
		if key.activateAt.After(now) || (key.retireAt != nil && !now.Before(*key.retireAt)) {
			continue
		}
		if best == nil || key.activateAt.After(best.activateAt) {
			best = key
		}
	}
	if best == nil {
		return nil, ErrNoSigningKey
	}
	return best, nil
}

// publicKey : clé publique d'un kid publié ; un kid inconnu provoque un
// rechargement (au plus un par missReloadInterval).
func (k *KeyRing) publicKey(ctx context.Context, kid string) (*ecdsa.PublicKey, bool) {
	if pub, ok := k.lookup(kid); ok {
		return pub, true
	}
	k.mu.RLock()
	recent := k.now().Sub(k.lastReload) < missReloadInterval
	k.mu.RUnlock()
	if recent || k.Reload(ctx) != nil {
		return nil, false
	}
	return k.lookup(kid)
}

func (k *KeyRing) lookup(kid string) (*ecdsa.PublicKey, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	key, ok := k.keys[kid]
	if !ok || (key.retireAt != nil && !k.now().Before(*key.retireAt)) {
		return nil, false
	}
	return &key.private.PublicKey, true
}

// JWKS : clés publiques publiées (actives, à venir et en fin de vie).
func (k *KeyRing) JWKS() JWKS {
	k.mu.RLock()
	defer k.mu.RUnlock()
	out := JWKS{Keys: make([]JWK, 0, len(k.keys))}
	for _, key := range k.keys {
		out.Keys = append(out.Keys, key.jwk)
	}
	sort.Slice(out.Keys, func(i, j int) bool { return out.Keys[i].Kid < out.Keys[j].Kid })
	return out
}

// Run : rotation toutes les heures, rechargement toutes les minutes (une
// clé créée par un autre réplica est connue avant de servir : PublishAhead).
func (k *KeyRing) Run(ctx context.Context, onError func(error)) {
	rotate := time.NewTicker(time.Hour)
	reload := time.NewTicker(time.Minute)
	defer rotate.Stop()
	defer reload.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-rotate.C:
			if err := k.Rotate(ctx); err != nil && onError != nil {
				onError(err)
			}
		case <-reload.C:
			if err := k.Reload(ctx); err != nil && onError != nil {
				onError(err)
			}
		}
	}
}

func publicJWK(kid string, pub *ecdsa.PublicKey) JWK {
	// Coordonnées sur 32 octets (P-256), complétées à gauche.
	x, y := make([]byte, 32), make([]byte, 32)
	pub.X.FillBytes(x) //nolint:staticcheck // champs dépréciés mais seule API stable pour les coordonnées
	pub.Y.FillBytes(y) //nolint:staticcheck
	return JWK{Kty: "EC", Crv: "P-256", Kid: kid, Alg: Algorithm, Use: "sig",
		X: base64.RawURLEncoding.EncodeToString(x),
		Y: base64.RawURLEncoding.EncodeToString(y)}
}
