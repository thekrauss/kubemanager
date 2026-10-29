package security

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/audit"
	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/middleware"
)

// ClusterAgentResolver : cluster d'un compte de service système (implémenté
// par *iam.ClusterAgents) ; "" si le principal n'est pas un agent de cluster.
type ClusterAgentResolver interface {
	ClusterOf(ctx context.Context, p iam.Principal) (string, error)
}

// SetClusterAgents branche la résolution des agents de cluster.
func (m *MiddlewareManager) SetClusterAgents(r ClusterAgentResolver) { m.clusterAgents = r }

// ClusterRef : cluster visé par une requête du watcher.
type ClusterRef func(*gin.Context) (string, error)

// ClusterParam : cluster dans le paramètre d'URL name.
func ClusterParam(name string) ClusterRef {
	return func(c *gin.Context) (string, error) { return strings.TrimSpace(c.Param(name)), nil }
}

// ClusterJSONBody : cluster dans le champ field du corps JSON (remis en place).
func ClusterJSONBody(field string) ClusterRef {
	return func(c *gin.Context) (string, error) {
		raw, err := peekJSONString(c, field)
		return strings.TrimSpace(raw), err
	}
}

// RequireClusterAgent : route du watcher. L'appelant doit être le compte de
// service système du cluster visé (iam.md §7.4) ET avoir
// platform.clusters.report : la clé d'un cluster ne peut ni parler au nom
// d'un autre cluster, ni servir à autre chose.
func (m *MiddlewareManager) RequireClusterAgent(ref ClusterRef) gin.HandlerFunc {
	permission := m.Require(iam.PlatformClustersReport, PlatformResource)
	return func(c *gin.Context) {
		principal, ok := PrincipalFromContext(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		target, err := ref(c)
		if err != nil || target == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid cluster reference"})
			return
		}
		if m.clusterAgents == nil {
			m.logger.Errorw("cluster agents resolver not configured", "route", c.FullPath())
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authorization unavailable"})
			return
		}
		own, err := m.clusterAgents.ClusterOf(c.Request.Context(), principal)
		if err != nil {
			m.logger.Errorw("cluster agent lookup failed", "principal", principal, "error", err)
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authorization unavailable"})
			return
		}
		if own != target {
			c.Set(ctxIAMPermission, iam.PlatformClustersReport.String())
			c.Set(ctxIAMDecision, audit.DecisionDeny)
			c.Set("iamResource", iam.Platform.String())
			audit.Annotate(c.Request.Context(), "reason", "cluster_agent_mismatch")
			audit.Annotate(c.Request.Context(), "cluster_id", target)
			if m.audit != nil {
				m.audit.LogAuthEvent(c, middleware.AuditPermissionDenied,
					zap.String("permission", iam.PlatformClustersReport.String()),
					zap.String("cluster_id", target), zap.String("agent_cluster_id", own))
			}
			msg := iam.ErrClusterAgentMismatch.Error()
			if own == "" {
				msg = iam.ErrNotClusterAgent.Error()
			}
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code": "PERMISSION_DENIED", "error": msg, "message": msg,
				"permission": iam.PlatformClustersReport.String(),
			})
			return
		}
		permission(c)
	}
}
