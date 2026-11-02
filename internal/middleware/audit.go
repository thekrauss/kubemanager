package middleware

import (
	"math/rand"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AuditEvent types for structured logging.
const (
	AuditAuthSuccess      = "auth.success"
	AuditAuthFailed       = "auth.failed"
	AuditTokenRevoked     = "auth.token_revoked"
	AuditPermissionDenied = "auth.permission_denied"
	AuditPermissionOK     = "auth.permission_granted"
	AuditAPIKeyUsed       = "auth.apikey_used"
	AuditAPIKeyCreated    = "auth.apikey_created"
	AuditAPIKeyRevoked    = "auth.apikey_revoked"
	AuditRoleAssigned     = "auth.role_assigned"
	AuditMemberRemoved    = "auth.member_removed"
)

// alwaysLoggedEvents : events de sécurité critiques toujours loggés,
// JAMAIS samplés (fix P1). Les events "success" peuvent être samplés car
// ils sont répétitifs — un échec ou une révocation doit toujours être tracé.
var alwaysLoggedEvents = map[string]struct{}{
	AuditAuthFailed:       {},
	AuditTokenRevoked:     {},
	AuditPermissionDenied: {},
	AuditAPIKeyCreated:    {},
	AuditAPIKeyRevoked:    {},
	AuditRoleAssigned:     {},
	AuditMemberRemoved:    {},
}

// AuditLogger wraps a zap logger to emit structured auth audit events.
//
// Fix P1 : sampling configurable sur les events "normaux" (success, apikey_used)
// pour éviter de noyer les logs en prod à haut trafic. Les events de sécurité
// (failed, revoked, permission_denied, créations/révocations) sont toujours
// loggés quel que soit le sampling.
type AuditLogger struct {
	logger     *zap.Logger
	sampleRate int // 1 sur N pour les events non-critiques. 0 ou 1 = pas de sampling.
}

// NewAuditLogger creates an audit logger. Uses a dedicated named logger
// so audit events can be routed to a separate sink in zap config.
func NewAuditLogger(base *zap.Logger) *AuditLogger {
	return &AuditLogger{
		logger: base.Named("audit"),
	}
}

// NewAuditLoggerWithSampling : même chose + sampling pour les events non-critiques.
// sampleRate=100 → 1 event success sur 100 loggé (1% de volume).
// sampleRate=0 ou 1 → tout loggé (backwards-compat).
func NewAuditLoggerWithSampling(base *zap.Logger, sampleRate int) *AuditLogger {
	if sampleRate < 1 {
		sampleRate = 1
	}
	return &AuditLogger{
		logger:     base.Named("audit"),
		sampleRate: sampleRate,
	}
}

// shouldLog : true si l'event doit être loggé vu la policy de sampling.
func (a *AuditLogger) shouldLog(event string) bool {
	if a.sampleRate <= 1 {
		return true
	}
	if _, critical := alwaysLoggedEvents[event]; critical {
		return true
	}
	// Sample 1 sur sampleRate. rand.Intn est thread-safe depuis Go 1.20
	// (global rand source protégée par un mutex).
	return rand.Intn(a.sampleRate) == 0
}

// LogAuthEvent emits a structured audit log entry.
func (a *AuditLogger) LogAuthEvent(c *gin.Context, event string, fields ...zap.Field) {
	if !a.shouldLog(event) {
		return
	}
	base := []zap.Field{
		zap.String("event", event),
		zap.String("ip", c.ClientIP()),
		zap.String("method", c.Request.Method),
		zap.String("path", c.Request.URL.Path),
		zap.String("user_agent", c.Request.UserAgent()),
		zap.Time("timestamp", time.Now().UTC()),
	}

	if userID := c.GetString(ContextKeyUserID); userID != "" {
		base = append(base, zap.String("user_id", userID))
	}
	if email := c.GetString(ContextKeyEmail); email != "" {
		base = append(base, zap.String("email", email))
	}
	if traceID := c.GetString("trace_id"); traceID != "" {
		base = append(base, zap.String("trace_id", traceID))
	}

	base = append(base, fields...)
	a.logger.Info("audit", base...)
}

// LogEvent emits a structured audit log entry without a gin context
// (for background operations like key creation in services).
func (a *AuditLogger) LogEvent(event string, fields ...zap.Field) {
	if !a.shouldLog(event) {
		return
	}
	base := []zap.Field{
		zap.String("event", event),
		zap.Time("timestamp", time.Now().UTC()),
	}
	base = append(base, fields...)
	a.logger.Info("audit", base...)
}
