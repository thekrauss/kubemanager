package security

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/thekrauss/kubemanager/internal/audit"
	"github.com/thekrauss/kubemanager/internal/iam"
)

// Double authentification des personnes (iam.md §9), vérifiée sur les jetons
// Keycloak : claim acr (niveau atteint) et auth_time (fraîcheur).
//
//   - action sensible (iam.PermissionInfo.Sensitive) : MFA de moins de
//     StepUpMaxAge exigé (step-up) ;
//   - organisation avec iam.requireMfa : MFA exigé pour toute action.
//
// Refus au format RFC 9470 (OAuth 2.0 Step Up Authentication Challenge) :
// 401, WWW-Authenticate: Bearer error="insufficient_user_authentication",
// acr_values, max_age ; corps JSON code STEP_UP_REQUIRED. La console relance
// alors la connexion avec step_up=true. Les comptes de service ne sont pas
// concernés : leur preuve est leur clé.

const (
	ctxAuthAcr  = "authAcr"
	ctxAuthTime = "authTime"

	// DefaultStepUpMaxAge : fraîcheur par défaut d'un step-up.
	DefaultStepUpMaxAge = 10 * time.Minute
)

// MFAPolicy : contraintes d'organisation (implémenté par *iam.OrgPolicyService).
type MFAPolicy interface {
	RequiresMFA(ctx context.Context, r iam.ResourceName) (bool, error)
}

// MFAConfig : niveaux Keycloak qui attestent une double authentification.
type MFAConfig struct {
	ACRValues []string
	MaxAge    time.Duration
	Policy    MFAPolicy
}

// SetMFA active la vérification ; sans ACRValues, rien n'est exigé (MFA non
// configuré sur la plateforme).
func (m *MiddlewareManager) SetMFA(cfg MFAConfig) {
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = DefaultStepUpMaxAge
	}
	m.mfa = &cfg
}

// checkMFA : true si la requête peut continuer ; sinon la réponse est écrite.
func (m *MiddlewareManager) checkMFA(c *gin.Context, perm iam.Permission, r iam.ResourceName) bool {
	if m.mfa == nil || len(m.mfa.ACRValues) == 0 || c.GetString("authMethod") != "bearer" {
		return true
	}
	info, _ := iam.Info(perm)
	mfa := slices.Contains(m.mfa.ACRValues, c.GetString(ctxAuthAcr))
	authTime := time.Unix(c.GetInt64(ctxAuthTime), 0)

	if info.Sensitive && (!mfa || time.Since(authTime) > m.mfa.MaxAge) {
		m.stepUpChallenge(c, perm, "recent two-factor authentication required for this action", true)
		return false
	}
	if !mfa && m.mfa.Policy != nil {
		required, err := m.mfa.Policy.RequiresMFA(c.Request.Context(), r)
		if err != nil {
			m.logger.Errorw("org policy check failed", "resource", r, "error", err)
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authorization unavailable"})
			return false
		}
		if required {
			m.stepUpChallenge(c, perm, "your organization requires two-factor authentication", false)
			return false
		}
	}
	return true
}

func (m *MiddlewareManager) stepUpChallenge(c *gin.Context, perm iam.Permission, msg string, fresh bool) {
	header := fmt.Sprintf(`Bearer error="insufficient_user_authentication", error_description=%q, acr_values=%q`,
		msg, m.mfa.ACRValues[0])
	body := gin.H{"code": "STEP_UP_REQUIRED", "error": msg, "message": msg, "permission": perm.String(),
		"acr_values": m.mfa.ACRValues[0]}
	if fresh {
		maxAge := strconv.Itoa(int(m.mfa.MaxAge.Seconds()))
		header += ", max_age=" + maxAge
		body["max_age"] = int(m.mfa.MaxAge.Seconds())
	}
	c.Header("WWW-Authenticate", header)
	c.Set(ctxIAMDecision, audit.DecisionDeny)
	audit.Annotate(c.Request.Context(), "reason", "step_up_required")
	c.AbortWithStatusJSON(http.StatusUnauthorized, body)
}
