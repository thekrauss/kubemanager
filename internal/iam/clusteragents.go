package iam

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/audit"
)

// Comptes de service système des clusters (iam.md §7.4).
//
// Chaque cluster a son compte « cluster-agent », rattaché à la plateforme
// (pas à un projet) et lié à son cluster_id. Il reçoit roles/platform.clusterAgent
// sur "platform" ; la garde des routes du watcher vérifie en plus que le
// cluster visé est le sien : la clé d'un cluster ne peut pas parler au nom
// d'un autre.
//
// Les anciennes clés km_… de type cluster sont rattachées à ces comptes par
// la migration 000008 et restent utilisables jusqu'à leur échéance.

const clusterAgentAccountID = "cluster-agent"

// DefaultClusterKeyGraceDays : durée pendant laquelle l'ancienne clé reste
// valide après une rotation (le temps de redéployer le watcher).
const DefaultClusterKeyGraceDays = 7

// clusterKeyExpiringWithin : en deçà, une clé est signalée « expiring ».
const clusterKeyExpiringWithin = 14 * 24 * time.Hour

var clusterIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

var (
	ErrInvalidClusterID      = errors.New("cluster_id must be 1-100 chars: letters, digits, '.', '_' or '-'")
	ErrClusterKeyNotFound    = errors.New("cluster key not found")
	ErrNotClusterAgent       = errors.New("this call is reserved for a cluster's agent service account")
	ErrClusterAgentMismatch  = errors.New("this key belongs to another cluster")
	ErrInvalidClusterKeyDays = errors.New("expires_in_days must be between 1 and 90")
	ErrClusterKeyInactive    = errors.New("this key is already revoked or expired: create a new one")
)

// Clé legacy km_… : forme de son identifiant dans l'API (son préfixe public).
const legacyKeyPrefix = "km_"

// ClusterKey : vue staff d'une clé de cluster (jamais le secret).
type ClusterKey struct {
	ClusterID        string    `json:"cluster_id"`
	ServiceAccountID uuid.UUID `json:"service_account_id"`
	Principal        Principal `json:"principal"`
	// KeyID : identifiant public (v2) ou préfixe "km_…" (legacy).
	KeyID       string     `json:"key_id"`
	Format      string     `json:"format"` // v2 | legacy
	DisplayName string     `json:"display_name"`
	Status      string     `json:"status"` // active | expiring | expired | revoked
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP  string     `json:"last_used_ip,omitempty"`
}

// CreatedClusterKey : seule réponse qui contient le secret.
type CreatedClusterKey struct {
	ClusterKey
	Key string `json:"key"`
}

// ClusterAgents : clés et comptes système des clusters.
type ClusterAgents struct {
	SA *ServiceAccountService
	// RevokeLegacyPrefix : propage la révocation d'une clé km_… au cache de
	// validation legacy (sinon effective sous 30 s). Optionnel.
	RevokeLegacyPrefix func(ctx context.Context, prefix string)

	// clusterOf : compte de service → cluster. Immuable : mis en cache sans
	// expiration (un compte supprimé n'authentifie plus rien en amont).
	clusterOf sync.Map
}

func NewClusterAgents(sa *ServiceAccountService) *ClusterAgents {
	return &ClusterAgents{SA: sa}
}

// ClusterOf : cluster du compte de service p ; "" si p n'est pas un agent de cluster.
func (a *ClusterAgents) ClusterOf(ctx context.Context, p Principal) (string, error) {
	kind, id, err := p.Parse()
	if err != nil || kind != KindServiceAccount {
		return "", nil
	}
	if v, ok := a.clusterOf.Load(id); ok {
		return v.(string), nil
	}
	var cluster sql.NullString
	err = a.SA.DB.WithContext(ctx).Raw(
		`SELECT cluster_id FROM service_accounts WHERE id = ? AND deleted_at IS NULL`, id).Row().Scan(&cluster)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if cluster.Valid {
		a.clusterOf.Store(id, cluster.String)
	}
	return cluster.String, nil
}

// CreateKeyRequest : nouvelle clé pour le watcher d'un cluster.
type CreateClusterKeyRequest struct {
	ClusterID     string `json:"cluster_id" binding:"required" desc:"Identifiant du cluster (ex: prod-fr-north-1)"`
	DisplayName   string `json:"display_name" desc:"Libellé de la clé"`
	ExpiresInDays int    `json:"expires_in_days" desc:"1 à 90 (défaut 90)"`
}

// CreateKey : crée au besoin le compte système du cluster, puis une clé
// limitée à platform.clusters.report.
func (a *ClusterAgents) CreateKey(ctx context.Context, caller Principal, req CreateClusterKeyRequest) (*CreatedClusterKey, error) {
	clusterID := strings.TrimSpace(req.ClusterID)
	if !clusterIDRe.MatchString(clusterID) {
		return nil, ErrInvalidClusterID
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > MaxAPIKeyLifetimeDays {
		return nil, ErrInvalidClusterKeyDays
	}
	var created *CreatedClusterKey
	err := a.SA.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		saID, err := a.ensureAccount(ctx, tx, caller, clusterID)
		if err != nil {
			return err
		}
		created, err = a.issue(ctx, tx, caller, saID, clusterID, req.DisplayName, req.ExpiresInDays)
		return err
	})
	if err != nil {
		return nil, err
	}
	audit.Annotate(ctx, "cluster_id", clusterID)
	return created, nil
}

// ensureAccount : compte système du cluster, créé avec son rôle s'il manque.
func (a *ClusterAgents) ensureAccount(ctx context.Context, tx *gorm.DB, caller Principal, clusterID string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.Raw(`SELECT id FROM service_accounts WHERE cluster_id = ? AND deleted_at IS NULL`, clusterID).Row().Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, err
	}
	id = uuid.New()
	// ON CONFLICT : deux créations simultanées pour le même cluster.
	res := tx.Exec(`INSERT INTO service_accounts (id, cluster_id, account_id, display_name, description, created_by)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (cluster_id) WHERE deleted_at IS NULL AND cluster_id IS NOT NULL DO NOTHING`,
		id, clusterID, clusterAgentAccountID, truncate("Agent du cluster "+clusterID, 100),
		"Compte système du watcher : rend compte de ce seul cluster.", string(caller))
	if res.Error != nil {
		return uuid.Nil, res.Error
	}
	if res.RowsAffected == 0 {
		if err := tx.Raw(`SELECT id FROM service_accounts WHERE cluster_id = ? AND deleted_at IS NULL`, clusterID).
			Row().Scan(&id); err != nil {
			return uuid.Nil, err
		}
		return id, nil
	}
	if err := AddBinding(ctx, tx, Binding{Resource: Platform, Role: RoleClusterAgent,
		Principal: ServiceAccountPrincipal(id)}, string(caller)); err != nil {
		return uuid.Nil, err
	}
	audit.Annotate(ctx, "service_account", ServiceAccountPrincipal(id))
	return id, nil
}

func (a *ClusterAgents) issue(ctx context.Context, tx *gorm.DB, caller Principal, saID uuid.UUID, clusterID, name string, days int) (*CreatedClusterKey, error) {
	if strings.TrimSpace(name) == "" {
		name = "watcher " + clusterID
	}
	key, err := a.SA.issueKey(ctx, tx, saID, string(caller), CreateKeyRequest{
		DisplayName:   truncate(name, 100),
		Env:           APIKeyEnvLive,
		ExpiresInDays: days,
		// Défense en profondeur : même si le compte recevait d'autres rôles,
		// la clé ne sert qu'au watcher.
		Restrictions: KeyRestrictions{Permissions: []Permission{PlatformClustersReport}},
	})
	if err != nil {
		return nil, err
	}
	view := a.v2View(key.APIKey, clusterID)
	return &CreatedClusterKey{ClusterKey: view, Key: key.Key}, nil
}

// List : toutes les clés de cluster (v2 et legacy), cluster par cluster.
func (a *ClusterAgents) List(ctx context.Context) ([]ClusterKey, error) {
	db := a.SA.DB.WithContext(ctx)
	var v2 []struct {
		APIKey
		ClusterID string
	}
	if err := db.Raw(`SELECT k.*, sa.cluster_id FROM api_keys_v2 k
		JOIN service_accounts sa ON sa.id = k.service_account_id
		WHERE sa.cluster_id IS NOT NULL AND sa.deleted_at IS NULL`).Scan(&v2).Error; err != nil {
		return nil, err
	}
	out := make([]ClusterKey, 0, len(v2))
	for _, k := range v2 {
		out = append(out, a.v2View(k.APIKey, k.ClusterID))
	}

	legacy, err := a.legacyKeys(ctx, "")
	if err != nil {
		return nil, err
	}
	out = append(out, legacy...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ClusterID != out[j].ClusterID {
			return out[i].ClusterID < out[j].ClusterID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// RotateClusterKeyRequest : rotation sans coupure.
type RotateClusterKeyRequest struct {
	// GraceDays : validité restante de l'ancienne clé (défaut 7, 0 = immédiat
	// si Immediate, au plus 30).
	GraceDays int  `json:"grace_days" desc:"Jours de validité restants pour l'ancienne clé (défaut 7, max 30)"`
	Immediate bool `json:"immediate" desc:"Invalider l'ancienne clé tout de suite (clé compromise)"`
}

const maxClusterKeyGraceDays = 30

var ErrInvalidGraceDays = errors.New("grace_days must be between 0 and 30")

// Rotate : nouvelle clé v2 pour le même cluster ; l'ancienne (v2 ou legacy)
// expire au terme de la période de grâce.
func (a *ClusterAgents) Rotate(ctx context.Context, caller Principal, keyID string, req RotateClusterKeyRequest) (*CreatedClusterKey, error) {
	if req.GraceDays < 0 || req.GraceDays > maxClusterKeyGraceDays {
		return nil, ErrInvalidGraceDays
	}
	grace := time.Duration(req.GraceDays) * 24 * time.Hour
	if req.GraceDays == 0 && !req.Immediate {
		grace = DefaultClusterKeyGraceDays * 24 * time.Hour
	}
	if req.Immediate {
		grace = 0
	}
	old, err := a.find(ctx, keyID)
	if err != nil {
		return nil, err
	}
	if old.Status == "revoked" || old.Status == "expired" {
		return nil, ErrClusterKeyInactive
	}
	until := a.SA.now().Add(grace)

	var created *CreatedClusterKey
	err = a.SA.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		saID, err := a.ensureAccount(ctx, tx, caller, old.ClusterID)
		if err != nil {
			return err
		}
		created, err = a.issue(ctx, tx, caller, saID, old.ClusterID, old.DisplayName, 0)
		if err != nil {
			return err
		}
		if old.Format == "legacy" {
			return tx.Exec(`UPDATE api_keys SET expires_at = LEAST(COALESCE(expires_at, ?), ?) WHERE prefix = ? AND kind = 'cluster'`,
				until, until, old.KeyID).Error
		}
		return tx.Exec(`UPDATE api_keys_v2 SET expires_at = LEAST(expires_at, ?) WHERE key_id = ?`, until, old.KeyID).Error
	})
	if err != nil {
		return nil, err
	}
	if old.Format == "legacy" && grace == 0 && a.RevokeLegacyPrefix != nil {
		a.RevokeLegacyPrefix(ctx, old.KeyID)
	}
	audit.Annotate(ctx, "cluster_id", old.ClusterID)
	audit.Annotate(ctx, "rotated_key_id", old.KeyID)
	audit.Annotate(ctx, "old_key_valid_until", until)
	return created, nil
}

// Revoke : effet immédiat.
func (a *ClusterAgents) Revoke(ctx context.Context, keyID string) error {
	old, err := a.find(ctx, keyID)
	if err != nil {
		return err
	}
	now := a.SA.now()
	db := a.SA.DB.WithContext(ctx)
	if old.Format == "legacy" {
		if err := db.Exec(`UPDATE api_keys SET revoked_at = COALESCE(revoked_at, ?), is_active = FALSE WHERE prefix = ? AND kind = 'cluster'`,
			now, old.KeyID).Error; err != nil {
			return err
		}
		if a.RevokeLegacyPrefix != nil {
			a.RevokeLegacyPrefix(ctx, old.KeyID)
		}
	} else if err := db.Exec(`UPDATE api_keys_v2 SET revoked_at = COALESCE(revoked_at, ?) WHERE key_id = ?`,
		now, old.KeyID).Error; err != nil {
		return err
	}
	audit.Annotate(ctx, "cluster_id", old.ClusterID)
	audit.Annotate(ctx, "api_key_id", old.KeyID)
	return nil
}

// find : clé de cluster par identifiant public (v2) ou préfixe km_… (legacy).
func (a *ClusterAgents) find(ctx context.Context, keyID string) (*ClusterKey, error) {
	keyID = strings.TrimSpace(keyID)
	if strings.HasPrefix(keyID, legacyKeyPrefix) {
		keys, err := a.legacyKeys(ctx, keyID)
		if err != nil {
			return nil, err
		}
		if len(keys) == 0 {
			return nil, ErrClusterKeyNotFound
		}
		return &keys[0], nil
	}
	var row struct {
		APIKey
		ClusterID string
	}
	err := a.SA.DB.WithContext(ctx).Raw(`SELECT k.*, sa.cluster_id FROM api_keys_v2 k
		JOIN service_accounts sa ON sa.id = k.service_account_id
		WHERE k.key_id = ? AND sa.cluster_id IS NOT NULL AND sa.deleted_at IS NULL`, keyID).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.KeyID == "" {
		return nil, ErrClusterKeyNotFound
	}
	view := a.v2View(row.APIKey, row.ClusterID)
	return &view, nil
}

// legacyKeys : clés km_… de type cluster (toutes, ou celle de ce préfixe).
func (a *ClusterAgents) legacyKeys(ctx context.Context, prefix string) ([]ClusterKey, error) {
	var rows []struct {
		Prefix           string
		Name             string
		ClusterID        string
		ServiceAccountID *uuid.UUID
		IsActive         bool
		CreatedAt        time.Time
		ExpiresAt        *time.Time
		RevokedAt        *time.Time
		LastUsedAt       *time.Time
	}
	q := a.SA.DB.WithContext(ctx).Table("api_keys").
		Select("prefix, name, cluster_id, service_account_id, is_active, created_at, expires_at, revoked_at, last_used_at").
		Where("kind = ?", "cluster")
	if prefix != "" {
		q = q.Where("prefix = ?", prefix)
	}
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ClusterKey, 0, len(rows))
	for _, r := range rows {
		k := ClusterKey{ClusterID: r.ClusterID, KeyID: r.Prefix, Format: "legacy", DisplayName: r.Name,
			CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, RevokedAt: r.RevokedAt, LastUsedAt: r.LastUsedAt}
		if r.ServiceAccountID != nil {
			k.ServiceAccountID = *r.ServiceAccountID
			k.Principal = ServiceAccountPrincipal(*r.ServiceAccountID)
		}
		revoked := r.RevokedAt != nil || !r.IsActive
		k.Status = a.status(revoked, r.ExpiresAt)
		out = append(out, k)
	}
	return out, nil
}

func (a *ClusterAgents) v2View(k APIKey, clusterID string) ClusterKey {
	expires := k.ExpiresAt
	return ClusterKey{
		ClusterID: clusterID, ServiceAccountID: k.ServiceAccountID, Principal: ServiceAccountPrincipal(k.ServiceAccountID),
		KeyID: k.KeyID, Format: "v2", DisplayName: k.DisplayName, CreatedAt: k.CreatedAt, ExpiresAt: &expires,
		RevokedAt: k.RevokedAt, LastUsedAt: k.LastUsedAt, LastUsedIP: k.LastUsedIP,
		Status: a.status(k.RevokedAt != nil, &expires),
	}
}

func (a *ClusterAgents) status(revoked bool, expiresAt *time.Time) string {
	now := a.SA.now()
	switch {
	case revoked:
		return "revoked"
	case expiresAt != nil && !now.Before(*expiresAt):
		return "expired"
	case expiresAt != nil && expiresAt.Sub(now) < clusterKeyExpiringWithin:
		return "expiring"
	}
	return "active"
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
