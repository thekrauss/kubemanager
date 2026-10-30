package security

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	authdomain "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
)

type usersByKeycloak struct {
	repository.AuthRepository
	byKC map[string]uuid.UUID
}

func (u usersByKeycloak) FindByKeycloakUserID(_ context.Context, kc string) (*authdomain.User, error) {
	if id, ok := u.byKC[kc]; ok {
		return &authdomain.User{ID: id}, nil
	}
	return nil, nil
}

// Les droits IAM portent sur l'identifiant interne : le principal d'une
// personne authentifiée par Keycloak doit être celui-là, pas l'identifiant
// Keycloak (sinon personne n'a jamais ses propres droits).
func TestPrincipal_UsesInternalUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	kc, internal := uuid.NewString(), uuid.New()
	m := &MiddlewareManager{authRepo: usersByKeycloak{byKC: map[string]uuid.UUID{kc: internal}}}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	c.Set("userID", kc)
	c.Set("authMethod", "bearer")
	m.applyInternalUser(c, kc)
	p, ok := PrincipalFromContext(c)
	assert.True(t, ok)
	assert.Equal(t, "user:"+internal.String(), string(p))
	id, ok := InternalUserID(c)
	assert.True(t, ok)
	assert.Equal(t, internal, id)

	// Première visite (pas encore de profil) : identifiant Keycloak, pas d'ID interne.
	other := uuid.NewString()
	c2, _ := gin.CreateTestContext(httptest.NewRecorder())
	c2.Request = httptest.NewRequest("GET", "/", nil)
	c2.Set("userID", other)
	c2.Set("authMethod", "bearer")
	m.applyInternalUser(c2, other)
	p, _ = PrincipalFromContext(c2)
	assert.Equal(t, "user:"+other, string(p))
	_, ok = InternalUserID(c2)
	assert.False(t, ok)
}
