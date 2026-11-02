package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authdomain "github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	authrepos "github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	keycloakadmin "github.com/thekrauss/kubemanager/internal/modules/identity/keycloak"
)

type kcProfile struct{ keycloakadmin.KeycloakClient }

func (kcProfile) GetUserByID(context.Context, string) (*keycloakadmin.UserProfile, error) {
	return &keycloakadmin.UserProfile{Email: "awa@betorun.test", FullName: "Awa"}, nil
}

// noLocalUser : comme le vrai dépôt, (nil, nil) quand l'utilisateur n'existe pas.
type noLocalUser struct{ authrepos.AuthRepository }

func (noLocalUser) FindByKeycloakUserID(context.Context, string) (*authdomain.User, error) {
	return nil, nil
}

// Première visite (aucun profil local) : statut « à provisionner », pas de 500.
func TestBootstrapStatus_FirstVisit(t *testing.T) {
	s := NewService(Deps{Keycloak: kcProfile{}, Users: noLocalUser{}})
	status, err := s.BootstrapStatus(context.Background(), BootstrapPrincipal{KeycloakUserID: "kc-1"})
	require.NoError(t, err)
	assert.True(t, status.RequiresBootstrap)
	assert.False(t, status.Bootstrapped)
	assert.Equal(t, "awa@betorun.test", status.Email)
	assert.Nil(t, status.InternalUserID)
}
