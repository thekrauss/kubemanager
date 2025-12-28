package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	authRepos "github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	"go.uber.org/zap"
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
	AuthRepo  authRepos.AuthRepository
}

func NewMiddlewareManager(cfg *configs.GlobalConfig, jwtMgr JWTManager, cacheRepo cache.CacheRedis, logger *zap.SugaredLogger, authRepo authRepos.AuthRepository) *MiddlewareManager {
	return &MiddlewareManager{
		Config:    cfg,
		JwtMgr:    jwtMgr,
		CacheRepo: cacheRepo,
		Logger:    logger,
		AuthRepo:  authRepo,
	}
}

func (m *MiddlewareManager) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {

		publicPaths := []string{
			"/swagger", "/swagger/", "/swagger/index.html", "/swagger/doc.json",
			"/swagger/swagger-ui.css", "/swagger/swagger-ui-bundle.js",
			"/swagger/swagger-ui-standalone-preset.js", "/swagger/favicon",
			"/docs", "/docs/", "/openapi.json", "/healthz", "/metrics",
			"/api/v1/health", "/kmanager/v1/auth/login", "/api/v1/auth/refresh",
		}

		path := c.Request.URL.Path
		for _, p := range publicPaths {
			if strings.HasPrefix(path, p) {
				c.Next()
				return
			}
		}

		authHeader := c.GetHeader("Authorization")

		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenString := strings.TrimPrefix(authHeader, "Bearer ")
			m.handleJWTAuth(c, tokenString)
			return
		}

		if strings.HasPrefix(authHeader, "ApiKey ") {
			rawKey := strings.TrimPrefix(authHeader, "ApiKey ")
			m.handleAPIKeyAuth(c, rawKey)
			return
		}
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

func (m *MiddlewareManager) handleJWTAuth(c *gin.Context, tokenString string) {
	claims, err := m.JwtMgr.VerifyAccessToken(tokenString)
	if err != nil {
		m.Logger.Warnw("JWT invalid", "error", err)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: Invalid token"})
		return
	}

	session, err := m.CacheRepo.GetSession(c.Request.Context(), claims.UserID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: Session expired"})
		return
	}

	if session.AccessToken != "" && session.AccessToken != tokenString {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: Token revoked"})
		return
	}

	c.Set(UserSessionKey, session)
	c.Set(UserIDKey, claims.UserID)
	c.Next()
}

func (m *MiddlewareManager) handleAPIKeyAuth(c *gin.Context, rawKey string) {
	if len(rawKey) < 10 || !strings.Contains(rawKey, "_") {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API Key format"})
		return
	}

	prefixLen := 7
	if len(rawKey) < prefixLen {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API Key"})
		return
	}
	prefix := rawKey[:prefixLen]

	apiKey, err := m.AuthRepo.GetAPIKeyByPrefix(c.Request.Context(), prefix)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API Key"})
		return
	}

	hash := sha256.Sum256([]byte(rawKey))
	hashedIncoming := hex.EncodeToString(hash[:])

	if apiKey.KeyHash != hashedIncoming {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API Key"})
		return
	}

	if apiKey.ExpiresAt != nil && apiKey.ExpiresAt.Before(time.Now()) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API Key expired"})
		return
	}

	go func() {
		_ = m.AuthRepo.UpdateAPIKeyUsage(context.Background(), apiKey.ID)
	}()

	user, err := m.AuthRepo.GetUserByID(c.Request.Context(), apiKey.UserID)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "User attached to key not found"})
		return
	}

	memberships, _ := m.AuthRepo.GetUserProjectMemberships(c.Request.Context(), user.ID)
	projectRoles := make(map[string]string)
	for _, mem := range memberships {
		projectRoles[mem.ProjectID.String()] = mem.Role.Name
	}

	virtualSession := &cache.SessionData{
		UserID:       user.ID.String(),
		Email:        user.Email,
		GlobalRole:   user.Role,
		ProjectRoles: projectRoles,
	}

	c.Set(UserSessionKey, virtualSession)
	c.Set(UserIDKey, user.ID.String())

	c.Next()
}
