package security

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/thekrauss/kubemanager/internal/audit"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/middleware"
)

// Authorizer : point de décision IAM (implémenté par *iam.Engine).
type Authorizer interface {
	Check(ctx context.Context, p iam.Principal, perm iam.Permission, r iam.ResourceName) (iam.Decision, error)
}

// ResourceResolver : retrouve la ressource visée par une requête.
// Erreurs : ErrResourceRefInvalid → 400, ErrResourceNotFound → 404, autre → 500.
type ResourceResolver func(c *gin.Context) (iam.ResourceName, error)

var (
	ErrResourceRefInvalid = errors.New("resource reference is missing or invalid")
	ErrResourceNotFound   = errors.New("resource not found")
)

// maxPeekBody : taille max du corps lu pour en extraire un identifiant.
const maxPeekBody = 1 << 20

// KeyAuthenticator : vérification des clés API v2 (implémenté par
// *iam.ServiceAccountService).
type KeyAuthenticator interface {
	Authenticate(ctx context.Context, raw, clientIP string) (*iam.AuthenticatedKey, error)
}

// SetKeyAuthenticator branche la vérification des clés API v2.
func (m *MiddlewareManager) SetKeyAuthenticator(k KeyAuthenticator) { m.keyAuth = k }

// handleAPIKeyV2 : authentifie une clé "btr_…" ; le principal est le compte
// de service, les restrictions de la clé sont posées pour Require.
func (m *MiddlewareManager) handleAPIKeyV2(c *gin.Context, raw string) {
	if m.keyAuth == nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
		return
	}
	key, err := m.keyAuth.Authenticate(c.Request.Context(), raw, c.ClientIP())
	if err != nil {
		if !errors.Is(err, iam.ErrAPIKeyInvalid) {
			m.logger.Errorw("api key v2 verification failed", "error", err)
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authentication unavailable"})
			return
		}
		if m.audit != nil {
			m.audit.LogAuthEvent(c, middleware.AuditAuthFailed, zap.String("reason", "invalid_api_key_v2"))
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
		return
	}
	c.Set(ctxPrincipal, string(key.Principal))
	c.Set(ctxKeyRestrictions, key.Restrictions)
	c.Set("apiKeyID", key.KeyID)
	c.Set("authMethod", "apikey_v2")
	c.Next()
}

const (
	ctxPrincipal       = "iamPrincipal"
	ctxKeyRestrictions = "apiKeyRestrictions"
)

// SetAuthorizer branche le moteur IAM (fait une fois à l'initialisation).
func (m *MiddlewareManager) SetAuthorizer(a Authorizer) { m.authorizer = a }

// SetPublicRoutes : routes (chemins gin, ex. "/kmanager/v1/identity/social/:provider")
// déclarées publiques par le routeur. L'authentification les laisse passer.
func (m *MiddlewareManager) SetPublicRoutes(routes map[string]bool) { m.publicRoutes = routes }

// ── Resolvers ────────────────────────────────────────────────────────────

// PlatformResource : routes d'exploitation de la plateforme (staff).
func PlatformResource(*gin.Context) (iam.ResourceName, error) { return iam.Platform, nil }

// ProjectParam : projet dans le paramètre d'URL name (ex. "id", "projectID").
func ProjectParam(name string) ResourceResolver {
	return func(c *gin.Context) (iam.ResourceName, error) {
		return projectRef(c.Param(name))
	}
}

// ProjectQuery : projet dans ?name=<uuid>.
func ProjectQuery(name string) ResourceResolver {
	return func(c *gin.Context) (iam.ResourceName, error) {
		return projectRef(c.Query(name))
	}
}

// ProjectJSONBody : projet dans le champ field du corps JSON. Le corps est
// remis en place pour le handler.
func ProjectJSONBody(field string) ResourceResolver {
	return func(c *gin.Context) (iam.ResourceName, error) {
		raw, err := peekJSONString(c, field)
		if err != nil {
			return "", err
		}
		return projectRef(raw)
	}
}

// ResourceQuery : nom de ressource IAM dans ?name=.
func ResourceQuery(name string) ResourceResolver {
	return func(c *gin.Context) (iam.ResourceName, error) {
		return resourceRef(c.Query(name))
	}
}

// ResourceJSONBody : nom de ressource IAM dans le champ field du corps JSON.
func ResourceJSONBody(field string) ResourceResolver {
	return func(c *gin.Context) (iam.ResourceName, error) {
		raw, err := peekJSONString(c, field)
		if err != nil {
			return "", err
		}
		return resourceRef(raw)
	}
}

// CustomRoleQuery : ressource parente du rôle personnalisé nommé dans ?name=.
func CustomRoleQuery(name string) ResourceResolver {
	return func(c *gin.Context) (iam.ResourceName, error) {
		return customRoleParent(c.Query(name))
	}
}

// CustomRoleJSONBody : ressource parente du rôle nommé dans le champ field du corps JSON.
func CustomRoleJSONBody(field string) ResourceResolver {
	return func(c *gin.Context) (iam.ResourceName, error) {
		raw, err := peekJSONString(c, field)
		if err != nil {
			return "", err
		}
		return customRoleParent(raw)
	}
}

func customRoleParent(raw string) (iam.ResourceName, error) {
	parent, ok := iam.CustomRoleParent(strings.TrimSpace(raw))
	if !ok {
		return "", ErrResourceRefInvalid
	}
	return parent, nil
}

// OrganizationParam : organisation dans le paramètre d'URL name.
func OrganizationParam(name string) ResourceResolver {
	return func(c *gin.Context) (iam.ResourceName, error) {
		id, err := uuid.Parse(strings.TrimSpace(c.Param(name)))
		if err != nil {
			return "", ErrResourceRefInvalid
		}
		return iam.OrganizationName(id), nil
	}
}

// OrganizationChild : "organizations/{param org}/{collection}/{param id}".
func OrganizationChild(orgParam, collection, idParam string) ResourceResolver {
	return func(c *gin.Context) (iam.ResourceName, error) {
		org, err := OrganizationParam(orgParam)(c)
		if err != nil {
			return "", err
		}
		id, err := uuid.Parse(strings.TrimSpace(c.Param(idParam)))
		if err != nil {
			return "", ErrResourceRefInvalid
		}
		return org.Child(collection, id), nil
	}
}

func resourceRef(raw string) (iam.ResourceName, error) {
	r := iam.ResourceName(strings.TrimSpace(raw))
	if r.Validate() != nil {
		return "", ErrResourceRefInvalid
	}
	return r, nil
}

// ProjectChild : sous-ressource "projects/{param projet}/{collection}/{param id}".
func ProjectChild(projectParam, collection, idParam string) ResourceResolver {
	return func(c *gin.Context) (iam.ResourceName, error) {
		project, err := projectRef(c.Param(projectParam))
		if err != nil {
			return "", err
		}
		id, err := uuid.Parse(strings.TrimSpace(c.Param(idParam)))
		if err != nil {
			return "", ErrResourceRefInvalid
		}
		return project.Child(collection, id), nil
	}
}

func projectRef(raw string) (iam.ResourceName, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", ErrResourceRefInvalid
	}
	return iam.ProjectName(id), nil
}

func peekJSONString(c *gin.Context, field string) (string, error) {
	if c.Request.Body == nil {
		return "", ErrResourceRefInvalid
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxPeekBody+1))
	if err != nil || len(body) > maxPeekBody {
		return "", ErrResourceRefInvalid
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", ErrResourceRefInvalid
	}
	var raw string
	if err := json.Unmarshal(payload[field], &raw); err != nil {
		return "", ErrResourceRefInvalid
	}
	return raw, nil
}

// ── Principal ────────────────────────────────────────────────────────────

// PrincipalFromContext : principal IAM de la requête authentifiée : compte de
// service (clé v2), sinon l'utilisateur (JWT ou clé legacy, iam.md §8.4).
func PrincipalFromContext(c *gin.Context) (iam.Principal, bool) {
	if p := c.GetString(ctxPrincipal); p != "" {
		return iam.Principal(p), true
	}
	id, err := uuid.Parse(c.GetString("userID"))
	if err != nil {
		return "", false
	}
	return iam.UserPrincipal(id), true
}

// ── Garde ────────────────────────────────────────────────────────────────

// Require : refuse la requête si le principal n'a pas perm sur la ressource
// donnée par resolve. Pose "projectID" (si la ressource est sous un projet)
// et "iamResource" dans le contexte.
//
// Échec fermé : moteur IAM indisponible → 503, jamais un accès.
func (m *MiddlewareManager) Require(perm iam.Permission, resolve ResourceResolver) gin.HandlerFunc {
	if !iam.Known(perm) {
		panic("security.Require: unknown permission " + perm.String())
	}
	return m.RequireFor(func(iam.ResourceName) (iam.Permission, error) { return perm, nil }, resolve)
}

// RequireFor : comme Require, mais la permission dépend de la ressource
// (ex. getIamPolicy d'une organisation ou d'un projet). Une erreur de permFor
// est une référence invalide (400).
func (m *MiddlewareManager) RequireFor(permFor func(iam.ResourceName) (iam.Permission, error), resolve ResourceResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := PrincipalFromContext(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}

		resource, err := resolve(c)
		switch {
		case errors.Is(err, ErrResourceRefInvalid):
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid resource reference"})
			return
		case errors.Is(err, ErrResourceNotFound):
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		case err != nil:
			m.logger.Errorw("resource resolution failed", "route", c.FullPath(), "error", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "permission check failed"})
			return
		}
		perm, err := permFor(resource)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if projectID, ok := resource.Project(); ok {
			c.Set("projectID", projectID.String())
		}
		c.Set("iamResource", resource.String())
		c.Set(ctxIAMPermission, perm.String())

		// Restrictions de la clé v2 : en plus des rôles du compte de service.
		if v, ok := c.Get(ctxKeyRestrictions); ok {
			if r, ok := v.(iam.KeyRestrictions); ok && !r.Allows(perm, resource) {
				c.Set(ctxIAMDecision, audit.DecisionDeny)
				audit.Annotate(c.Request.Context(), "reason", "api_key_restrictions")
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"code": "PERMISSION_DENIED", "error": "API key restrictions do not allow this call",
					"message": "API key restrictions do not allow this call", "permission": perm.String(),
				})
				return
			}
		}

		if m.authorizer == nil {
			m.logger.Errorw("iam authorizer not configured", "route", c.FullPath())
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authorization unavailable"})
			return
		}
		decision, err := m.authorizer.Check(c.Request.Context(), principal, perm, resource)
		switch {
		case errors.Is(err, iam.ErrResourceNotFound):
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		case err != nil:
			m.logger.Errorw("iam check failed", "principal", principal, "permission", perm, "resource", resource, "error", err)
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "authorization unavailable"})
			return
		}
		if !decision.Allowed {
			c.Set(ctxIAMDecision, audit.DecisionDeny)
			if m.audit != nil {
				m.audit.LogAuthEvent(c, middleware.AuditPermissionDenied,
					zap.String("permission", perm.String()),
					zap.String("resource", resource.String()))
			}
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":       "PERMISSION_DENIED",
				"error":      "permission denied",
				"message":    "permission denied",
				"permission": perm.String(),
			})
			return
		}
		c.Next()
	}
}

const (
	ctxIAMPermission = "iamPermission"
	ctxIAMDecision   = "iamDecision"
)

// AuditMiddleware : journal d'audit (iam.md §11). À monter après
// AuthMiddleware. Journalise toute écriture d'un principal authentifié et tout
// refus d'autorisation, avec la permission et la ressource posées par Require.
// Les services enrichissent l'entrée via audit.Annotate.
func (m *MiddlewareManager) AuditMiddleware(w audit.Writer) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, rec := audit.WithRecorder(c.Request.Context())
		c.Request = c.Request.WithContext(ctx)
		c.Next()

		denied := c.GetString(ctxIAMDecision) == audit.DecisionDeny
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			if !denied {
				return // lectures autorisées : journal d'accès aux données (phase C)
			}
		}
		principal, ok := PrincipalFromContext(c)
		if !ok {
			return
		}
		decision := audit.DecisionAllow
		if denied {
			decision = audit.DecisionDeny
		}
		e := &audit.Entry{
			Principal:  string(principal),
			AuthMethod: c.GetString("authMethod"),
			APIKeyID:   c.GetString("apiKeyID"),
			Action:     c.Request.Method + " " + c.FullPath(),
			Permission: c.GetString(ctxIAMPermission),
			Resource:   c.GetString("iamResource"),
			Decision:   decision,
			Status:     c.Writer.Status(),
			ClientIP:   c.ClientIP(),
			UserAgent:  c.Request.UserAgent(),
			TraceID:    c.GetString("trace_id"),
			Metadata:   rec.Metadata(),
		}
		if pid, err := uuid.Parse(c.GetString("projectID")); err == nil {
			e.ProjectID = &pid
		}
		// La requête peut être annulée (client parti) : l'audit, lui, doit être écrit.
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if err := w.Write(wctx, e); err != nil {
			m.logger.Errorw("audit write failed", "action", e.Action, "principal", e.Principal, "error", err)
		}
	}
}
