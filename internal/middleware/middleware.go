package middleware

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Clés du gin.Context posées par les middlewares d'authentification et lues
// par les handlers.
const (
	ContextKeyUserID       = "userID"
	ContextKeyEmail        = "email"
	ContextKeyProjectID    = "projectID"
	ContextKeyAPIKeyID     = "apiKeyID"
	ContextKeyAPIKeyScopes = "apiKeyScopes"
	ContextKeyAuthMethod   = "authMethod"
	ContextKeyAuthTime     = "authTime"
	ContextKeyAuthAcr      = "authAcr"
)

const (
	// LoggerContextKey : clé utilisée pour stocker un SugaredLogger scopé au
	// request (enrichi avec trace_id) dans le gin.Context. Les handlers
	// récupèrent le logger via LoggerFrom(c) pour que leurs logs portent
	// automatiquement le trace_id de la requête.
	LoggerContextKey = "__logger"
)

// UserIDFromContext extracts the authenticated user ID from the Gin context.
// The value is set by the auth middleware in middleware/security.
func UserIDFromContext(c *gin.Context) (string, bool) {
	uid := c.GetString(ContextKeyUserID)
	if uid == "" {
		return "", false
	}
	return uid, true
}

// LoggerFrom : retourne le logger scopé au request (posé par le middleware
// de trace dans server.go). Si aucun logger scopé n'est trouvé, retourne
// `fallback` (typiquement app.Logger). À utiliser dans les handlers :
//
//	log := middleware.LoggerFrom(c, s.Logger)
//	log.Infow("deploy start", "workload_id", id)
func LoggerFrom(c *gin.Context, fallback *zap.SugaredLogger) *zap.SugaredLogger {
	if c == nil {
		return fallback
	}
	if v, ok := c.Get(LoggerContextKey); ok {
		if l, ok := v.(*zap.SugaredLogger); ok && l != nil {
			return l
		}
	}
	return fallback
}
