package iamapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/iam/sts"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
)

// ── STS (iam.md §7.3) ────────────────────────────────────────────────────
//
// Endpoints OAuth standard, hors tonic : corps application/x-www-form-urlencoded
// et erreurs au format RFC 6749 §5.2 ({error, error_description}).

// Token : POST <issuer>/token, grant jwt-bearer (RFC 7523).
func (h *Controller) Token(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	if h.STS == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily_unavailable"})
		return
	}
	grant := c.PostForm("grant_type")
	assertion := c.PostForm("assertion")
	if grant == "" { // tolère un corps JSON
		var body struct {
			GrantType string `json:"grant_type"`
			Assertion string `json:"assertion"`
		}
		if c.ShouldBindJSON(&body) == nil {
			grant, assertion = body.GrantType, body.Assertion
		}
	}

	var (
		tok       *sts.Token
		principal iam.Principal
		err       error
	)
	switch grant {
	case sts.GrantJWTBearer:
		tok, principal, err = h.STS.ExchangeAssertion(c.Request.Context(), assertion)
	case "":
		err = &sts.GrantError{Code: "invalid_request", Description: "grant_type is required"}
	default:
		err = &sts.GrantError{Code: "unsupported_grant_type", Description: "supported: " + sts.GrantJWTBearer}
	}
	// Audit : émission ou refus, au nom du compte dès que la clé est connue.
	if principal != "" {
		security.SetAuditPrincipal(c, principal)
	}
	var ge *sts.GrantError
	switch {
	case errors.As(err, &ge):
		security.MarkDenied(c)
		c.JSON(http.StatusBadRequest, gin.H{"error": ge.Code, "error_description": ge.Description})
	case err != nil:
		_ = c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server_error"})
	default:
		c.JSON(http.StatusOK, tok)
	}
}

// JWKS : clés publiques de signature des jetons.
func (h *Controller) JWKS(c *gin.Context) {
	if h.STS == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily_unavailable"})
		return
	}
	c.Header("Cache-Control", "public, max-age=300")
	c.JSON(http.StatusOK, h.STS.Keys.JWKS())
}

// Discovery : métadonnées de l'émetteur (forme OpenID Connect Discovery).
func (h *Controller) Discovery(c *gin.Context) {
	if h.STS == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily_unavailable"})
		return
	}
	c.Header("Cache-Control", "public, max-age=3600")
	c.JSON(http.StatusOK, gin.H{
		"issuer":                                h.STS.Issuer,
		"token_endpoint":                        h.STS.TokenURI(),
		"jwks_uri":                              h.STS.JWKSURI(),
		"grant_types_supported":                 []string{sts.GrantJWTBearer},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"token_endpoint_auth_signing_alg_values_supported": []string{iam.SAKeyAlgES256, iam.SAKeyAlgRS256},
		"access_token_signing_alg_values_supported":        []string{sts.Algorithm},
	})
}

// ── Clés asymétriques des comptes de service ─────────────────────────────

type CreateSAKeyInput struct {
	ProjectID string `path:"id" binding:"required"`
	SAID      string `path:"saID" binding:"required"`
	iam.CreateSAKeyRequest
}

type SAKeyPath struct {
	ProjectID string `path:"id" binding:"required"`
	SAID      string `path:"saID" binding:"required"`
	KeyID     string `path:"keyID" binding:"required" desc:"Identifiant de la clé (key_id)"`
}

// CreateSAKey : la clé privée (clé générée) n'apparaît que dans cette réponse.
func (h *Controller) CreateSAKey(c *gin.Context, in *CreateSAKeyInput) (*iam.CreatedSAKey, error) {
	p, err := caller(c)
	if err != nil {
		return nil, err
	}
	id, err := ids(in.ProjectID, in.SAID)
	if err != nil {
		return nil, err
	}
	return h.ServiceAccounts.CreateSAKey(c.Request.Context(), p, id[0], id[1], in.CreateSAKeyRequest)
}

func (h *Controller) ListSAKeys(c *gin.Context, in *ServiceAccountPath) ([]iam.ServiceAccountKey, error) {
	id, err := ids(in.ProjectID, in.SAID)
	if err != nil {
		return nil, err
	}
	return h.ServiceAccounts.ListSAKeys(c.Request.Context(), id[0], id[1])
}

func (h *Controller) DisableSAKey(c *gin.Context, in *SAKeyPath) error {
	id, err := ids(in.ProjectID, in.SAID)
	if err != nil {
		return err
	}
	return h.ServiceAccounts.DisableSAKey(c.Request.Context(), id[0], id[1], in.KeyID)
}
