package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authcache "github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
)

// fakeAuthRepo : implémentation minimale pour tester RevokeKey.
// Embed l'interface pour fail-loud sur les méthodes non-overridées.
type fakeAuthRepo struct {
	repository.AuthRepository
	keysByID map[uuid.UUID]*domain.APIKey
	revoked  []uuid.UUID // enregistre les IDs révoqués pour assertion
}

func (f *fakeAuthRepo) GetAPIKeyByID(ctx context.Context, keyID uuid.UUID) (*domain.APIKey, error) {
	if k, ok := f.keysByID[keyID]; ok {
		return k, nil
	}
	return nil, nil
}

func (f *fakeAuthRepo) RevokeAPIKey(ctx context.Context, keyID uuid.UUID, userID uuid.UUID) error {
	if k, ok := f.keysByID[keyID]; !ok || k.UserID != userID {
		return nil // rien à supprimer
	}
	f.revoked = append(f.revoked, keyID)
	delete(f.keysByID, keyID)
	return nil
}

// ── Tests du fix C3 : RevokeKey propage au cache + vérifie ownership ─────

func TestRevokeKey_PropagatesToCache(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	keyID := uuid.New()
	prefix := "km_prod_deadbeef0000"

	repo := &fakeAuthRepo{
		keysByID: map[uuid.UUID]*domain.APIKey{
			keyID: {
				ID:       keyID,
				UserID:   userID,
				Prefix:   prefix,
				Scopes:   pq.StringArray{"project:view"},
				IsActive: true,
			},
		},
	}
	cache, err := authcache.NewRistrettoAuthCache()
	require.NoError(t, err)

	svc := NewAPIKeyService(repo, WithCache(cache))

	// Pre: prefix pas révoqué dans le cache.
	revoked, err := cache.IsAPIKeyPrefixRevoked(ctx, prefix)
	require.NoError(t, err)
	assert.False(t, revoked, "pre: cache vierge")

	// Act : revoke.
	err = svc.RevokeKey(ctx, userID.String(), keyID.String())
	require.NoError(t, err)

	// Laisse Ristretto digérer l'écriture async.
	time.Sleep(20 * time.Millisecond)

	// Post 1 : la clé est bien supprimée DB.
	assert.Equal(t, []uuid.UUID{keyID}, repo.revoked)

	// Post 2 : le prefix est dans le cache des révoqués (fix C3).
	revoked, err = cache.IsAPIKeyPrefixRevoked(ctx, prefix)
	require.NoError(t, err)
	assert.True(t, revoked, "le prefix doit être marqué révoqué dans le cache")
}

func TestRevokeKey_CrossUser_NotFound(t *testing.T) {
	ctx := context.Background()
	ownerID := uuid.New()
	attackerID := uuid.New()
	keyID := uuid.New()

	repo := &fakeAuthRepo{
		keysByID: map[uuid.UUID]*domain.APIKey{
			keyID: {
				ID:       keyID,
				UserID:   ownerID, // appartient à ownerID
				Prefix:   "km_prod_owner1234567",
				IsActive: true,
			},
		},
	}
	cache, _ := authcache.NewRistrettoAuthCache()
	svc := NewAPIKeyService(repo, WithCache(cache))

	// Attacker tente de révoquer la clé du owner.
	err := svc.RevokeKey(ctx, attackerID.String(), keyID.String())

	require.Error(t, err, "attaquant doit recevoir une erreur")
	assert.Empty(t, repo.revoked, "aucune clé ne doit être révoquée DB")

	// La clé est toujours active.
	assert.Contains(t, repo.keysByID, keyID)
}

func TestRevokeKey_UnknownKey_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := &fakeAuthRepo{keysByID: map[uuid.UUID]*domain.APIKey{}}
	cache, _ := authcache.NewRistrettoAuthCache()
	svc := NewAPIKeyService(repo, WithCache(cache))

	err := svc.RevokeKey(ctx, uuid.New().String(), uuid.New().String())
	require.Error(t, err)
}

func TestRevokeKey_InvalidUUIDs(t *testing.T) {
	svc := NewAPIKeyService(&fakeAuthRepo{}, WithCache(nil))

	require.Error(t, svc.RevokeKey(context.Background(), "not-a-uuid", uuid.New().String()))
	require.Error(t, svc.RevokeKey(context.Background(), uuid.New().String(), "not-a-uuid"))
}
