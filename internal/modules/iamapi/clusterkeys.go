package iamapi

import (
	"github.com/gin-gonic/gin"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
)

// ── Clés des watchers (/admin/cluster-keys, staff) ───────────────────────
//
// Chaque cluster a un compte de service système ; ses clés ne valent que pour
// lui (iam.md §7.4).

type ClusterKeyPath struct {
	KeyID string `path:"keyID" binding:"required" desc:"Identifiant public de la clé (v2) ou préfixe km_… (legacy)"`
}

type RotateClusterKeyInput struct {
	KeyID string `path:"keyID" binding:"required"`
	iam.RotateClusterKeyRequest
}

func (h *Controller) caller(c *gin.Context) (iam.Principal, error) {
	p, ok := security.PrincipalFromContext(c)
	if !ok {
		return "", betoerrors.New(betoerrors.CodeUnauthorized, "authentication required")
	}
	return p, nil
}

func (h *Controller) CreateClusterKey(c *gin.Context, in *iam.CreateClusterKeyRequest) (*iam.CreatedClusterKey, error) {
	caller, err := h.caller(c)
	if err != nil {
		return nil, err
	}
	return h.ClusterAgents.CreateKey(c.Request.Context(), caller, *in)
}

func (h *Controller) ListClusterKeys(c *gin.Context) ([]iam.ClusterKey, error) {
	return h.ClusterAgents.List(c.Request.Context())
}

func (h *Controller) RotateClusterKey(c *gin.Context, in *RotateClusterKeyInput) (*iam.CreatedClusterKey, error) {
	caller, err := h.caller(c)
	if err != nil {
		return nil, err
	}
	return h.ClusterAgents.Rotate(c.Request.Context(), caller, in.KeyID, in.RotateClusterKeyRequest)
}

func (h *Controller) RevokeClusterKey(c *gin.Context, in *ClusterKeyPath) error {
	return h.ClusterAgents.Revoke(c.Request.Context(), in.KeyID)
}
