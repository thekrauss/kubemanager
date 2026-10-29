package security

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/audit"
	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/iam/sts"
	"github.com/thekrauss/kubemanager/internal/middleware"
)

// TokenVerifier : jetons machine émis par le STS betorun (implémenté par
// *sts.Service). Vérification locale, sans appel réseau.
type TokenVerifier interface {
	Owns(raw string) bool
	Verify(ctx context.Context, raw string) (*sts.Verified, error)
}

// SetTokenVerifier branche la vérification des jetons du STS.
func (m *MiddlewareManager) SetTokenVerifier(v TokenVerifier) { m.sts = v }

// handleSTSToken : le principal est le compte de service du jeton.
func (m *MiddlewareManager) handleSTSToken(c *gin.Context, raw string) {
	v, err := m.sts.Verify(c.Request.Context(), raw)
	if err != nil {
		if !errors.Is(err, sts.ErrInvalidToken) {
			m.logger.Errorw("sts token verification failed", "error", err)
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authentication unavailable"})
			return
		}
		if m.audit != nil {
			m.audit.LogAuthEvent(c, middleware.AuditAuthFailed, zap.String("reason", "invalid_sts_token"))
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
		return
	}
	c.Set(ctxPrincipal, string(v.Principal))
	c.Set("authMethod", "sts")
	c.Set("tokenID", v.TokenID)
	if v.KeyID != "" {
		// Identifiant de connexion journalisé (colonne api_key_id de l'audit) :
		// la clé de compte de service qui a servi à obtenir le jeton.
		c.Set("apiKeyID", v.KeyID)
	}
	c.Next()
}

// SetAuditPrincipal : principal d'une route publique qui authentifie elle-même
// (endpoint de jeton), pour que la requête soit journalisée à son nom.
func SetAuditPrincipal(c *gin.Context, p iam.Principal) { c.Set(ctxPrincipal, string(p)) }

// MarkDenied : la requête est un refus d'authentification ou d'autorisation
// (journalisé comme tel).
func MarkDenied(c *gin.Context) { c.Set(ctxIAMDecision, audit.DecisionDeny) }
