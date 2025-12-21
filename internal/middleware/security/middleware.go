package security

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"

	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/infrastructure/cache"
)

const (
	UserSessionKey = "user_session"
	UserIDKey      = "user_id"
)

type MiddlewareManager struct {
	Config    *configs.GlobalConfig
	JwtMgr    JWTManager
	CacheRepo cache.CacheRedis
	Logger    *zap.SugaredLogger
}

func NewMiddlewareManager(cfg *configs.GlobalConfig, jwtMgr JWTManager, cacheRepo cache.CacheRedis, logger *zap.SugaredLogger) *MiddlewareManager {
	return &MiddlewareManager{
		Config:    cfg,
		JwtMgr:    jwtMgr,
		CacheRepo: cacheRepo,
		Logger:    logger,
	}
}

func (m *MiddlewareManager) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {

		publicPaths := []string{
			"/docs", "/metrics", "/healthz",
			"/api/v1/auth/login", "/api/v1/auth/refresh",
		}
		path := c.Request.URL.Path
		for _, p := range publicPaths {
			if strings.HasPrefix(path, p) {
				c.Next()
				return
			}
		}

		authHeader := c.GetHeader("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: Missing Bearer Token"})
			return
		}
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		claims, err := m.JwtMgr.VerifyAccessToken(tokenString)
		if err != nil {
			m.Logger.Warnw("JWT invalid", "error", err)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: Invalid token"})
			return
		}

		session, err := m.CacheRepo.GetSession(c.Request.Context(), claims.UserID)
		if err != nil {
			m.Logger.Warnw("Session validation failed", "user_id", claims.UserID, "error", err)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: Session expired or invalid"})
			return
		}

		if session.AccessToken != "" && session.AccessToken != tokenString {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: Token revoked"})
			return
		}

		claims, err = m.JwtMgr.VerifyAccessToken(tokenString)
		c.Set(UserSessionKey, session)
		c.Set(UserIDKey, claims.UserID)

		c.Next()
	}
}

func (m *MiddlewareManager) RequireProjectPermission(permSlug string) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionVal, exists := c.Get(UserSessionKey)
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		session := sessionVal.(*cache.SessionData)

		projectID := c.Param("project_id")
		if projectID == "" {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Project ID missing in URL"})
			return
		}

		roleName, hasRole := session.ProjectRoles[projectID]

		if session.GlobalRole == m.Config.Roles.PlatformAdmin {
			c.Next()
			return
		}

		if !hasRole {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Forbidden: You are not a member of this project"})
			return
		}

		if !domain.RoleHasPermission(roleName, permSlug) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": fmt.Sprintf("Forbidden: Missing permission '%s'", permSlug)})
			return
		}

		c.Next()
	}
}

func (m *MiddlewareManager) GetSessionFromCtx(c context.Context) (*cache.SessionData, error) {
	if ginCtx, ok := c.(*gin.Context); ok {
		val, exists := ginCtx.Get(UserSessionKey)
		if !exists {
			return nil, fmt.Errorf("session not found in context")
		}
		return val.(*cache.SessionData), nil
	}
	return nil, fmt.Errorf("context is not a gin context")
}
