package security

import (
	"context"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/iam"
)

// Identité interne d'une personne. Les jetons Keycloak portent l'identifiant
// Keycloak (« userID » dans le contexte, utilisé pour parler à Keycloak),
// alors que les droits IAM, les projets et l'audit utilisent l'identifiant
// interne (table users). Après validation d'un jeton, le principal IAM est
// donc user:<identifiant interne>. Une personne sans profil local (première
// visite, avant le bootstrap) garde l'identifiant Keycloak.

const ctxInternalUserID = "internalUserID"

// internalIDs : Keycloak → interne. Une correspondance ne change jamais : cache
// sans expiration, seulement pour les personnes déjà provisionnées.
var internalIDs sync.Map

func (m *MiddlewareManager) internalUserID(ctx context.Context, keycloakID string) string {
	if keycloakID == "" || m.authRepo == nil {
		return ""
	}
	if v, ok := internalIDs.Load(keycloakID); ok {
		return v.(string)
	}
	user, err := m.authRepo.FindByKeycloakUserID(ctx, keycloakID)
	if err != nil || user == nil {
		return ""
	}
	id := user.ID.String()
	internalIDs.Store(keycloakID, id)
	return id
}

// applyInternalUser : principal IAM d'une personne authentifiée par Keycloak.
func (m *MiddlewareManager) applyInternalUser(c *gin.Context, keycloakID string) {
	if id := m.internalUserID(c.Request.Context(), keycloakID); id != "" {
		c.Set(ctxInternalUserID, id)
		c.Set(ctxPrincipal, string(iam.UserPrincipal(uuid.MustParse(id))))
	}
}

// InternalUserID : identifiant interne (table users) de la personne qui fait
// la requête ; false pour un compte de service ou une personne sans profil.
func InternalUserID(c *gin.Context) (uuid.UUID, bool) {
	if v := c.GetString(ctxInternalUserID); v != "" {
		id, err := uuid.Parse(v)
		return id, err == nil
	}
	// Clés API legacy : « userID » est déjà l'identifiant interne.
	if c.GetString("authMethod") == "apikey" && c.GetString(ctxPrincipal) == "" {
		id, err := uuid.Parse(c.GetString("userID"))
		return id, err == nil
	}
	return uuid.Nil, false
}
