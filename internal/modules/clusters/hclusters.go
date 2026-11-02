package clusters

import (
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/gin-gonic/gin"
)

type IClusterController interface {
	Enroll(c *gin.Context, in *EnrollRequest) (*EnrollResponse, error)
	GetConfig(c *gin.Context, in *ClusterIDPath) (*RemoteConfigResponse, error)
	OnPodEvent(c *gin.Context, in *PodEventInput) (*PodEventAck, error)
	// Admin : liste les clusters enrollés avec leur LastSeenAt (monitoring staleness).
	ListClusters(c *gin.Context) ([]Cluster, error)
}

type Controller struct {
	service IClusterService
}

func NewController(s IClusterService) *Controller {
	return &Controller{service: s}
}

var _ IClusterController = (*Controller)(nil)

// ── DTOs path params ──────────────────────────────────────────────────────

type ClusterIDPath struct {
	ClusterID string `path:"clusterID" binding:"required"`
}

// PodEventInput : path param + body JSON. Corps embarqué : encoding/json
// ignore « inline », un champ Event nommé restait vide (événements perdus).
type PodEventInput struct {
	ClusterID string `path:"clusterID" binding:"required"`
	PodEvent
}

// ── Handlers ──────────────────────────────────────────────────────────────

func (ctrl *Controller) Enroll(c *gin.Context, in *EnrollRequest) (*EnrollResponse, error) {
	if in == nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "body required")
	}
	return ctrl.service.Enroll(c.Request.Context(), *in)
}

func (ctrl *Controller) GetConfig(c *gin.Context, in *ClusterIDPath) (*RemoteConfigResponse, error) {
	return ctrl.service.GetConfig(c.Request.Context(), in.ClusterID)
}

func (ctrl *Controller) OnPodEvent(c *gin.Context, in *PodEventInput) (*PodEventAck, error) {
	if in == nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "body required")
	}
	return ctrl.service.OnPodEvent(c.Request.Context(), in.ClusterID, in.PodEvent)
}

func (ctrl *Controller) ListClusters(c *gin.Context) ([]Cluster, error) {
	return ctrl.service.ListClusters(c.Request.Context())
}
