package identity

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/thekrauss/kubemanager/internal/core/httpheader"
	identitysvc "github.com/thekrauss/kubemanager/internal/modules/identity/service"
)

// ── CLI : connexion par code d'appareil (RFC 8628), gérée par betorun ─────
//
// Handlers hors tonic : les réponses d'attente suivent RFC 8628 §3.5
// ({"error":"authorization_pending"} en 400) pour que tout client OAuth
// standard puisse s'en servir.

// DeviceCode : POST /identity/device/code.
func (ctrl *controller) DeviceCode(c *gin.Context) {
	c.Header(httpheader.CacheControl, httpheader.NoStore)
	out, err := ctrl.service.StartDevice(c.Request.Context(), clientOf(c))
	if err != nil {
		deviceError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// DeviceToken : POST /identity/device/token (device_code en formulaire ou JSON).
func (ctrl *controller) DeviceToken(c *gin.Context) {
	c.Header(httpheader.CacheControl, httpheader.NoStore)
	code := c.PostForm("device_code")
	if code == "" {
		var body struct {
			DeviceCode string `json:"device_code"`
		}
		_ = c.ShouldBindJSON(&body)
		code = body.DeviceCode
	}
	out, err := ctrl.service.PollDevice(c.Request.Context(), code, clientOf(c))
	if err != nil {
		deviceError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func deviceError(c *gin.Context, err error) {
	var de *identitysvc.DeviceError
	switch {
	case errors.As(err, &de):
		c.JSON(http.StatusBadRequest, gin.H{"error": de.Code})
	case errors.Is(err, identitysvc.ErrTooManyAttempts):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "slow_down"})
	default:
		_ = c.Error(err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "temporarily_unavailable"})
	}
}
