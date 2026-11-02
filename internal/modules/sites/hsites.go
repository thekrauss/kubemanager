package sites

import (
	"errors"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/core/ginhttp"
	"github.com/thekrauss/kubemanager/internal/core/httpheader"
	"github.com/thekrauss/kubemanager/internal/modules/sites/domain"
	"github.com/thekrauss/kubemanager/internal/modules/sites/service"
)

// multipartOverhead : marge pour les en-têtes multipart autour de l'archive.
const multipartOverhead = domain.BytesPerMB

type ISiteController interface {
	CreateSite(c *gin.Context, in *domain.CreateSiteRequest) (*domain.SiteResponse, error)
	ListSites(c *gin.Context, in *domain.ProjectPath) ([]domain.SiteResponse, error)
	GetSite(c *gin.Context, in *domain.SitePath) (*domain.SiteResponse, error)
	UpdateSite(c *gin.Context, in *domain.UpdateSiteRequest) (*domain.SiteResponse, error)
	DeleteSite(c *gin.Context, in *domain.SitePath) error

	// UploadDeployment : multipart (champ "archive"), handler brut hors tonic.
	UploadDeployment(c *gin.Context, in *domain.UploadPath)
	ListDeployments(c *gin.Context, in *domain.SitePath) ([]domain.SiteDeployment, error)
	ActivateDeployment(c *gin.Context, in *domain.DeploymentPath) (*domain.SiteResponse, error)

	AddDomain(c *gin.Context, in *domain.CreateDomainRequest) (*domain.DomainResponse, error)
	ListDomains(c *gin.Context, in *domain.SitePath) ([]domain.DomainResponse, error)
	VerifyDomain(c *gin.Context, in *domain.DomainPath) (*domain.DomainResponse, error)
	DeleteDomain(c *gin.Context, in *domain.DomainPath) error
}

type SiteController struct {
	Service service.ISiteService
	Logger  *zap.SugaredLogger
}

func NewSiteController(svc service.ISiteService, logger *zap.SugaredLogger) *SiteController {
	return &SiteController{Service: svc, Logger: logger}
}

func parseIDs(raw ...string) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, len(raw))
	for i, r := range raw {
		id, err := uuid.Parse(r)
		if err != nil {
			return nil, betoerrors.New(betoerrors.CodeInvalidInput, "identifiant invalide")
		}
		ids[i] = id
	}
	return ids, nil
}

// ── Sites ────────────────────────────────────────────────────────────────

func (h *SiteController) CreateSite(c *gin.Context, in *domain.CreateSiteRequest) (*domain.SiteResponse, error) {
	ids, err := parseIDs(in.ProjectID)
	if err != nil {
		return nil, err
	}
	return h.Service.CreateSite(c.Request.Context(), ids[0], *in)
}

func (h *SiteController) ListSites(c *gin.Context, in *domain.ProjectPath) ([]domain.SiteResponse, error) {
	ids, err := parseIDs(in.ProjectID)
	if err != nil {
		return nil, err
	}
	return h.Service.ListSites(c.Request.Context(), ids[0])
}

func (h *SiteController) GetSite(c *gin.Context, in *domain.SitePath) (*domain.SiteResponse, error) {
	ids, err := parseIDs(in.ProjectID, in.SiteID)
	if err != nil {
		return nil, err
	}
	return h.Service.GetSite(c.Request.Context(), ids[0], ids[1])
}

func (h *SiteController) UpdateSite(c *gin.Context, in *domain.UpdateSiteRequest) (*domain.SiteResponse, error) {
	ids, err := parseIDs(in.ProjectID, in.SiteID)
	if err != nil {
		return nil, err
	}
	return h.Service.UpdateSite(c.Request.Context(), ids[0], ids[1], *in)
}

func (h *SiteController) DeleteSite(c *gin.Context, in *domain.SitePath) error {
	ids, err := parseIDs(in.ProjectID, in.SiteID)
	if err != nil {
		return err
	}
	return h.Service.DeleteSite(c.Request.Context(), ids[0], ids[1])
}

// ── Déploiements ─────────────────────────────────────────────────────────

func (h *SiteController) UploadDeployment(c *gin.Context, in *domain.UploadPath) {
	ids, err := parseIDs(in.ProjectID, in.SiteID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	max := h.Service.MaxArchiveBytes()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max+multipartOverhead)
	defer ginhttp.RemoveMultipart(c)

	fh, err := c.FormFile(domain.ArchiveFormField)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			h.writeError(c, betoerrors.Newf(betoerrors.CodeInvalidInput, "archive trop volumineuse (maximum %d Mo)", max/domain.BytesPerMB))
			return
		}
		h.writeError(c, betoerrors.New(betoerrors.CodeInvalidInput, `champ "archive" (.zip) manquant`))
		return
	}
	f, err := fh.Open()
	if err != nil {
		h.writeError(c, err)
		return
	}
	defer f.Close()

	// Auteur : identifiant interne (nil pour un compte de service).
	var userID *uuid.UUID
	if uid, ok := security.InternalUserID(c); ok {
		userID = &uid
	}
	dep, err := h.Service.Deploy(c.Request.Context(), ids[0], ids[1], userID, f, fh.Size)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, dep)
}

func (h *SiteController) ListDeployments(c *gin.Context, in *domain.SitePath) ([]domain.SiteDeployment, error) {
	ids, err := parseIDs(in.ProjectID, in.SiteID)
	if err != nil {
		return nil, err
	}
	return h.Service.ListDeployments(c.Request.Context(), ids[0], ids[1])
}

func (h *SiteController) ActivateDeployment(c *gin.Context, in *domain.DeploymentPath) (*domain.SiteResponse, error) {
	ids, err := parseIDs(in.ProjectID, in.SiteID, in.DeploymentID)
	if err != nil {
		return nil, err
	}
	return h.Service.ActivateDeployment(c.Request.Context(), ids[0], ids[1], ids[2])
}

// ── Domaines ─────────────────────────────────────────────────────────────

func (h *SiteController) AddDomain(c *gin.Context, in *domain.CreateDomainRequest) (*domain.DomainResponse, error) {
	ids, err := parseIDs(in.ProjectID, in.SiteID)
	if err != nil {
		return nil, err
	}
	return h.Service.AddDomain(c.Request.Context(), ids[0], ids[1], in.Hostname)
}

func (h *SiteController) ListDomains(c *gin.Context, in *domain.SitePath) ([]domain.DomainResponse, error) {
	ids, err := parseIDs(in.ProjectID, in.SiteID)
	if err != nil {
		return nil, err
	}
	return h.Service.ListDomains(c.Request.Context(), ids[0], ids[1])
}

func (h *SiteController) VerifyDomain(c *gin.Context, in *domain.DomainPath) (*domain.DomainResponse, error) {
	ids, err := parseIDs(in.ProjectID, in.SiteID, in.DomainID)
	if err != nil {
		return nil, err
	}
	return h.Service.VerifyDomain(c.Request.Context(), ids[0], ids[1], ids[2])
}

func (h *SiteController) DeleteDomain(c *gin.Context, in *domain.DomainPath) error {
	ids, err := parseIDs(in.ProjectID, in.SiteID, in.DomainID)
	if err != nil {
		return err
	}
	return h.Service.DeleteDomain(c.Request.Context(), ids[0], ids[1], ids[2])
}

// writeError : erreurs typées → statut HTTP ; le reste → 500 sans détail.
func (h *SiteController) writeError(c *gin.Context, err error) {
	status, body := betoerrors.ToHTTPError(err)
	if status >= http.StatusInternalServerError && h.Logger != nil {
		h.Logger.Errorw("site upload failed", "error", err)
	}
	c.Data(status, httpheader.MIMEJSON, body)
}
