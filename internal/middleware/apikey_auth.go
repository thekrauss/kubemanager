package middleware

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
)


func APIKeyAuth(authRepo repository.AuthRepository, logger *zap.SugaredLogger) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := extractAPIKey(c)
		if raw == "" {
			c.Next()
			return
		}

		prefix := raw[:min(7, len(raw))]
		key, err := authRepo.GetAPIKeyByPrefix(c.Request.Context(), prefix)
		if err != nil || key == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
			return
		}

		if !key.IsActive {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API key has been revoked"})
			return
		}

		if key.RevokedAt != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "API key has been revoked"})
			return
		}

		// Constant-time hash comparison — évite les timing attacks.
		expected := utils.HashAPIKey(raw)
		if subtle.ConstantTimeCompare([]byte(expected), []byte(key.KeyHash)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
			return
		}

		// Update last_used_at + usage_count — ctx détaché du request HTTP
		// (sinon annulé au retour → update jamais persisté).
		go func(keyID uuid.UUID) {
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := authRepo.UpdateAPIKeyUsage(bgCtx, keyID); err != nil {
				logger.Warnw("failed to update api key usage", "key_id", keyID, "error", err)
			}
		}(key.ID)

		c.Set("userID", key.UserID.String())
		c.Set("projectID", key.ProjectID.String())
		c.Set("apiKeyID", key.ID.String())
		c.Set("apiKeyScopes", key.Scopes)

		c.Next()
	}
}

func RequireAPIKeyScope(requiredScope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		scopesVal, exists := c.Get("apiKeyScopes")
		if !exists {
			c.Next()
			return
		}

		scopes, ok := scopesVal.([]string)
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "invalid scope format"})
			return
		}

		for _, s := range scopes {
			if s == requiredScope {
				c.Next()
				return
			}
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "API key missing required scope: " + requiredScope,
		})
	}
}

func extractAPIKey(c *gin.Context) string {
	// Try X-API-Key header first
	if key := strings.TrimSpace(c.GetHeader("X-API-Key")); key != "" {
		return key
	}

	// Try Authorization: ApiKey <key>
	auth := strings.TrimSpace(c.GetHeader("Authorization"))
	if auth == "" {
		return ""
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "ApiKey") {
		return strings.TrimSpace(parts[1])
	}

	return ""
}
