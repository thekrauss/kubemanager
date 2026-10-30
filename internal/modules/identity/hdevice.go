package identity

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	keycloakadmin "github.com/thekrauss/kubemanager/internal/modules/identity/keycloak"
	identitysvc "github.com/thekrauss/kubemanager/internal/modules/identity/service"
)

// ── CLI : flux device (RFC 8628), relayé vers Keycloak ───────────────────
//
// Handlers hors tonic : les réponses d'attente suivent RFC 8628 §3.5
// ({"error":"authorization_pending"} en 400) pour que tout client OAuth
// standard puisse s'en servir.

// DeviceCode : POST /identity/device/code.
func (ctrl *controller) DeviceCode(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	auth, err := ctrl.service.StartDeviceLogin(c.Request.Context())
	if err != nil {
		deviceError(c, err)
		return
	}
	c.JSON(http.StatusOK, auth)
}

// DeviceToken : POST /identity/device/token (device_code en formulaire ou JSON).
func (ctrl *controller) DeviceToken(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	code := c.PostForm("device_code")
	if code == "" {
		var body struct {
			DeviceCode string `json:"device_code"`
		}
		_ = c.ShouldBindJSON(&body)
		code = body.DeviceCode
	}
	tokens, err := ctrl.service.PollDeviceLogin(c.Request.Context(), code)
	if err != nil {
		deviceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"access_token": tokens.AccessToken, "refresh_token": tokens.RefreshToken,
		"expires_in": tokens.ExpiresIn, "token_type": "Bearer"})
}

func deviceError(c *gin.Context, err error) {
	var pending *keycloakadmin.DeviceTokenError
	switch {
	case errors.As(err, &pending):
		c.JSON(http.StatusBadRequest, gin.H{"error": pending.Code, "error_description": pending.Description})
	case errors.Is(err, identitysvc.ErrDeviceLoginDisabled):
		c.JSON(http.StatusNotImplemented, gin.H{"error": "unsupported_grant_type", "error_description": err.Error()})
	default:
		_ = c.Error(err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily_unavailable"})
	}
}
