package iam

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/thekrauss/kubemanager/internal/audit"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// ServiceAccount : identité machine d'un projet (iam.md §7).
type ServiceAccount struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	ProjectID uuid.UUID `gorm:"type:uuid" json:"project_id"`
	// ClusterID : compte système du watcher de ce cluster (iam.md §7.4) ;
	// vide pour un compte de projet. Exactement l'un des deux est renseigné.
	ClusterID   *string        `json:"cluster_id,omitempty"`
	AccountID   string         `gorm:"not null" json:"account_id"`
	DisplayName string         `json:"display_name"`
	Description string         `json:"description"`
	Disabled    bool           `json:"disabled"`
	CreatedBy   string         `json:"-"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-"`

	// Calculés pour l'affichage.
	Principal Principal    `gorm:"-" json:"principal"`
	Resource  ResourceName `gorm:"-" json:"resource"`
}

func (ServiceAccount) TableName() string { return "service_accounts" }

// KeyRestrictions : limites d'une clé, en plus des rôles de son compte.
type KeyRestrictions struct {
	// Permissions : si non vide, seules ces permissions sont utilisables.
	Permissions []Permission `json:"permissions,omitempty"`
	// Resources : si non vide, la clé ne vaut que sur ces ressources et leurs descendantes.
	Resources []ResourceName `json:"resources,omitempty"`
	// AllowedCIDRs : si non vide, adresses IP clientes autorisées.
	AllowedCIDRs []string `json:"allowed_cidrs,omitempty"`
}

// Allows : la clé peut-elle servir pour perm sur r ?
func (k KeyRestrictions) Allows(perm Permission, r ResourceName) bool {
	if len(k.Permissions) > 0 && !containsPermission(k.Permissions, perm) {
		return false
	}
	if len(k.Resources) == 0 {
		return true
	}
	for _, allowed := range k.Resources {
		if r == allowed || strings.HasPrefix(string(r), string(allowed)+"/") {
			return true
		}
	}
	return false
}

func (k KeyRestrictions) allowsIP(ip string) bool {
	if len(k.AllowedCIDRs) == 0 {
		return true
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, c := range k.AllowedCIDRs {
		if _, n, err := net.ParseCIDR(c); err == nil && n.Contains(parsed) {
			return true
		}
	}
	return false
}

func (k KeyRestrictions) validate() error {
	for _, p := range k.Permissions {
		if !Known(p) {
			return fmt.Errorf("%w: unknown permission %q", ErrInvalidKeyRestrictions, p)
		}
	}
	for _, r := range k.Resources {
		if r.Validate() != nil {
			return fmt.Errorf("%w: invalid resource %q", ErrInvalidKeyRestrictions, r)
		}
	}
	for _, c := range k.AllowedCIDRs {
		if _, _, err := net.ParseCIDR(c); err != nil {
			return fmt.Errorf("%w: invalid CIDR %q", ErrInvalidKeyRestrictions, c)
		}
	}
	return nil
}

func containsPermission(list []Permission, p Permission) bool {
	for _, x := range list {
		if x == p {
			return true
		}
	}
	return false
}

// APIKey : métadonnées d'une clé (jamais le secret).
type APIKey struct {
	ID               uuid.UUID       `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	KeyID            string          `json:"key_id"`
	ServiceAccountID uuid.UUID       `gorm:"type:uuid" json:"service_account_id"`
	DisplayName      string          `json:"display_name"`
	Env              string          `json:"env"`
	SecretHMAC       string          `json:"-"`
	RestrictionsRaw  []byte          `gorm:"column:restrictions;type:jsonb" json:"-"`
	Restrictions     KeyRestrictions `gorm:"-" json:"restrictions"`
	ExpiresAt        time.Time       `json:"expires_at"`
	RevokedAt        *time.Time      `json:"revoked_at,omitempty"`
	LastUsedAt       *time.Time      `json:"last_used_at,omitempty"`
	LastUsedIP       string          `json:"last_used_ip,omitempty"`
	CreatedBy        string          `json:"-"`
	CreatedAt        time.Time       `json:"created_at"`
}

func (APIKey) TableName() string { return "api_keys_v2" }

// CreatedAPIKey : réponse de création, seule fois où le secret est visible.
type CreatedAPIKey struct {
	APIKey
	Key string `json:"key"`
}

// CreateKeyRequest : paramètres d'une nouvelle clé.
type CreateKeyRequest struct {
	DisplayName   string          `json:"display_name"`
	Env           string          `json:"env"`             // live (défaut) | test
	ExpiresInDays int             `json:"expires_in_days"` // 1..MaxAPIKeyLifetimeDays (défaut le maximum)
	Restrictions  KeyRestrictions `json:"restrictions"`
}

// MaxAPIKeyLifetimeDays : durée de vie maximale (politique d'organisation
// configurable en phase B).
const MaxAPIKeyLifetimeDays = 90

var (
	ErrServiceAccountNotFound = errors.New("service account not found")
	ErrInvalidAccountID       = errors.New("account_id must be 6-30 chars: lowercase letters, digits and hyphens, starting with a letter")
	ErrAccountIDTaken         = errors.New("a service account with this account_id already exists in the project")
	ErrInvalidKeyRestrictions = errors.New("invalid key restrictions")
	ErrInvalidKeyExpiry       = errors.New("expires_in_days must be between 1 and 90")
	ErrActAsRequired          = errors.New("permission iam.serviceAccounts.actAs on the service account is required to create its keys")
	ErrAPIKeyNotFound         = errors.New("api key not found")
)

var accountIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// ServiceAccountService : comptes de service et clés API v2.
type ServiceAccountService struct {
	DB     *gorm.DB
	Engine *Engine
	Pepper []byte
	// TokenURI : endpoint du STS, recopié dans les fichiers de clé.
	TokenURI string
	now      func() time.Time

	usageMu  sync.Mutex
	lastUsed map[string]time.Time // keyID → dernière écriture last_used_at
}

func NewServiceAccountService(db *gorm.DB, engine *Engine, pepper []byte) (*ServiceAccountService, error) {
	if len(pepper) < MinAPIKeyPepperLen {
		return nil, fmt.Errorf("iam: api key pepper must be at least %d bytes", MinAPIKeyPepperLen)
	}
	return &ServiceAccountService{DB: db, Engine: engine, Pepper: pepper, now: time.Now, lastUsed: map[string]time.Time{}}, nil
}

func (s *ServiceAccountService) decorate(sa *ServiceAccount) {
	sa.Principal = ServiceAccountPrincipal(sa.ID)
	sa.Resource = ProjectName(sa.ProjectID).Child("serviceAccounts", sa.ID)
}

// Create : nouveau compte de service, sans aucun rôle (à accorder via setIamPolicy).
func (s *ServiceAccountService) Create(ctx context.Context, caller Principal, projectID uuid.UUID, accountID, displayName, description string) (*ServiceAccount, error) {
	accountID = strings.TrimSpace(accountID)
	if !accountIDRe.MatchString(accountID) || strings.Contains(accountID, "--") {
		return nil, ErrInvalidAccountID
	}
	sa := &ServiceAccount{ID: uuid.New(), ProjectID: projectID, AccountID: accountID,
		DisplayName: strings.TrimSpace(displayName), Description: strings.TrimSpace(description), CreatedBy: string(caller)}
	if err := s.DB.WithContext(ctx).Create(sa).Error; err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAccountIDTaken
		}
		return nil, err
	}
	s.decorate(sa)
	audit.Annotate(ctx, "service_account", sa.Principal)
	return sa, nil
}

func (s *ServiceAccountService) List(ctx context.Context, projectID uuid.UUID) ([]ServiceAccount, error) {
	var out []ServiceAccount
	if err := s.DB.WithContext(ctx).Where("project_id = ?", projectID).Order("account_id").Find(&out).Error; err != nil {
		return nil, err
	}
	for i := range out {
		s.decorate(&out[i])
	}
	return out, nil
}

func (s *ServiceAccountService) Get(ctx context.Context, projectID, saID uuid.UUID) (*ServiceAccount, error) {
	var sa ServiceAccount
	err := s.DB.WithContext(ctx).Where("id = ? AND project_id = ?", saID, projectID).First(&sa).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrServiceAccountNotFound
	}
	if err != nil {
		return nil, err
	}
	s.decorate(&sa)
	return &sa, nil
}

// Delete : le compte disparaît avec ses clés et tous ses rôles, partout.
func (s *ServiceAccountService) Delete(ctx context.Context, caller Principal, projectID, saID uuid.UUID) error {
	sa, err := s.Get(ctx, projectID, saID)
	if err != nil {
		return err
	}
	principal := ServiceAccountPrincipal(sa.ID)
	now := s.now()
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&APIKey{}).Where("service_account_id = ? AND revoked_at IS NULL", sa.ID).
			Update("revoked_at", now).Error; err != nil {
			return err
		}
		// Clés legacy km_… rattachées au compte (migration 000007).
		if err := tx.Exec(`UPDATE api_keys SET revoked_at = ?, is_active = FALSE
			WHERE service_account_id = ? AND revoked_at IS NULL`, now, sa.ID).Error; err != nil {
			return err
		}
		var resources []string
		if err := tx.Model(&bindingRow{}).Where("principal = ?", string(principal)).
			Distinct().Pluck("resource_name", &resources).Error; err != nil {
			return err
		}
		for _, r := range resources {
			if err := RemovePrincipal(ctx, tx, ResourceName(r), principal, string(caller)); err != nil {
				return err
			}
		}
		// La policy portée par le compte lui-même (qui peut agir en son nom).
		if err := DeleteResourcePolicy(ctx, tx, sa.Resource); err != nil {
			return err
		}
		return tx.Delete(sa).Error
	})
	if err != nil {
		return err
	}
	notifyChanged(principal)
	audit.Annotate(ctx, "service_account", principal)
	return nil
}

// CreateKey : nouvelle clé pour le compte. Exige actAs sur le compte : une
// clé donne tous ses droits, la créer revient à agir en son nom.
func (s *ServiceAccountService) CreateKey(ctx context.Context, caller Principal, projectID, saID uuid.UUID, req CreateKeyRequest) (*CreatedAPIKey, error) {
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
	return s.issueKey(ctx, s.DB, sa.ID, string(caller), req)
}

// issueKey : génère et enregistre une clé pour le compte saID ; le secret
// n'est renvoyé qu'ici.
func (s *ServiceAccountService) issueKey(ctx context.Context, db *gorm.DB, saID uuid.UUID, createdBy string, req CreateKeyRequest) (*CreatedAPIKey, error) {
	env := req.Env
	if env == "" {
		env = APIKeyEnvLive
	}
	if env != APIKeyEnvLive && env != APIKeyEnvTest {
		return nil, fmt.Errorf("%w: env must be live or test", ErrInvalidKeyRestrictions)
	}
	days := req.ExpiresInDays
	if days == 0 {
		days = MaxAPIKeyLifetimeDays
	}
	if days < 1 || days > MaxAPIKeyLifetimeDays {
		return nil, ErrInvalidKeyExpiry
	}
	if err := req.Restrictions.validate(); err != nil {
		return nil, err
	}
	rawRestrictions, err := json.Marshal(req.Restrictions)
	if err != nil {
		return nil, err
	}

	full, keyID, secret, err := generateAPIKey(env)
	if err != nil {
		return nil, err
	}
	key := APIKey{
		ID: uuid.New(), KeyID: keyID, ServiceAccountID: saID, DisplayName: strings.TrimSpace(req.DisplayName),
		Env: env, SecretHMAC: hashSecret(s.Pepper, secret), RestrictionsRaw: rawRestrictions,
		Restrictions: req.Restrictions, ExpiresAt: s.now().Add(time.Duration(days) * 24 * time.Hour),
		CreatedBy: createdBy, CreatedAt: s.now(),
	}
	if err := db.WithContext(ctx).Create(&key).Error; err != nil {
		return nil, err
	}
	audit.Annotate(ctx, "api_key_id", key.KeyID) // jamais le secret
	audit.Annotate(ctx, "expires_at", key.ExpiresAt)
	return &CreatedAPIKey{APIKey: key, Key: full}, nil
}

func (s *ServiceAccountService) ListKeys(ctx context.Context, projectID, saID uuid.UUID) ([]APIKey, error) {
	sa, err := s.Get(ctx, projectID, saID)
	if err != nil {
		return nil, err
	}
	var keys []APIKey
	if err := s.DB.WithContext(ctx).Where("service_account_id = ?", sa.ID).Order("created_at DESC").Find(&keys).Error; err != nil {
		return nil, err
	}
	for i := range keys {
		_ = json.Unmarshal(keys[i].RestrictionsRaw, &keys[i].Restrictions)
	}
	return keys, nil
}

// RevokeKey : effet immédiat (pas de cache côté vérification).
func (s *ServiceAccountService) RevokeKey(ctx context.Context, projectID, saID uuid.UUID, keyID string) error {
	sa, err := s.Get(ctx, projectID, saID)
	if err != nil {
		return err
	}
	res := s.DB.WithContext(ctx).Model(&APIKey{}).
		Where("service_account_id = ? AND key_id = ? AND revoked_at IS NULL", sa.ID, keyID).
		Update("revoked_at", s.now())
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrAPIKeyNotFound
	}
	audit.Annotate(ctx, "api_key_id", keyID)
	return nil
}

// AuthenticatedKey : résultat d'une authentification par clé v2.
type AuthenticatedKey struct {
	Principal    Principal
	KeyID        string
	Restrictions KeyRestrictions
}

// Authenticate : vérifie une clé présentée par un client. Toute cause de
// refus renvoie ErrAPIKeyInvalid.
func (s *ServiceAccountService) Authenticate(ctx context.Context, raw, clientIP string) (*AuthenticatedKey, error) {
	env, keyID, secret, err := parseAPIKey(raw)
	if err != nil {
		return nil, ErrAPIKeyInvalid
	}
	var (
		saID                 uuid.UUID
		storedHMAC, keyEnv   string
		restrictions         []byte
		expiresAt            time.Time
		revokedAt, deletedAt sql.NullTime
		disabled             bool
	)
	err = s.DB.WithContext(ctx).Raw(`
		SELECT sa.id, k.secret_hmac, k.env, k.restrictions, k.expires_at, k.revoked_at, sa.disabled, sa.deleted_at
		FROM api_keys_v2 k JOIN service_accounts sa ON sa.id = k.service_account_id
		WHERE k.key_id = ?`, keyID).Row().
		Scan(&saID, &storedHMAC, &keyEnv, &restrictions, &expiresAt, &revokedAt, &disabled, &deletedAt)
	computed := hashSecret(s.Pepper, secret) // calculé même si la clé est inconnue (temps constant)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAPIKeyInvalid
		}
		return nil, err
	}
	if !hmac.Equal([]byte(computed), []byte(storedHMAC)) || env != keyEnv ||
		revokedAt.Valid || deletedAt.Valid || disabled || !s.now().Before(expiresAt) {
		return nil, ErrAPIKeyInvalid
	}
	var r KeyRestrictions
	if err := json.Unmarshal(restrictions, &r); err != nil {
		return nil, ErrAPIKeyInvalid
	}
	if !r.allowsIP(clientIP) {
		return nil, ErrAPIKeyInvalid
	}
	s.touchUsage(keyID, clientIP)
	return &AuthenticatedKey{Principal: ServiceAccountPrincipal(saID), KeyID: keyID, Restrictions: r}, nil
}

// touchUsage : last_used_at/IP au plus une fois par minute et par clé, hors
// du chemin de la requête (pas de ligne chaude).
func (s *ServiceAccountService) touchUsage(keyID, ip string) {
	now := s.now()
	s.usageMu.Lock()
	if last, ok := s.lastUsed[keyID]; ok && now.Sub(last) < time.Minute {
		s.usageMu.Unlock()
		return
	}
	s.lastUsed[keyID] = now
	s.usageMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.DB.WithContext(ctx).Model(&APIKey{}).Where("key_id = ?", keyID).
			Updates(map[string]any{"last_used_at": now, "last_used_ip": ip}).Error
	}()
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
