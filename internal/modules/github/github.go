// Package github : API de la connexion GitHub (installations, dépôts, liens) et
// webhook de l'application.
package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/middleware/security"
	buildsDomain "github.com/thekrauss/kubemanager/internal/modules/builds/domain"
	"github.com/thekrauss/kubemanager/internal/modules/github/client"
	"github.com/thekrauss/kubemanager/internal/modules/github/domain"
	"github.com/thekrauss/kubemanager/internal/modules/github/service"
)

const (
	// maxWebhookBody : GitHub plafonne ses charges utiles à 25 Mo.
	maxWebhookBody = 25 << 20
	// webhookTimeout : GitHub coupe à 10 s ; le traitement ne dépend plus de la
	// connexion une fois la signature vérifiée.
	webhookTimeout = 8 * time.Second

	headerSignature = "X-Hub-Signature-256"
	headerDelivery  = "X-GitHub-Delivery"
	headerEvent     = "X-GitHub-Event"
	maxDeliveryID   = 100
)

// Controller : routes de la connexion GitHub.
type Controller struct {
	Service       *service.Service
	WebhookSecret string
	Logger        *zap.SugaredLogger
}

func parseProject(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid project id")
	}
	return id, nil
}

func parseWorkload(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, domain.ErrLinkNotFound
	}
	return id, nil
}

func userOf(c *gin.Context) *uuid.UUID {
	if uid, ok := security.InternalUserID(c); ok {
		return &uid
	}
	return nil
}

// requireUser : les preuves d'accès GitHub sont celles d'une personne, pas d'un
// compte de service.
func requireUser(c *gin.Context) (uuid.UUID, iam.Principal, error) {
	uid := userOf(c)
	caller, ok := security.PrincipalFromContext(c)
	if uid == nil || !ok {
		return uuid.Nil, "", domain.ErrInstallationDenied
	}
	return *uid, caller, nil
}

// Status : la fonction est-elle disponible ? (répond toujours, sans erreur)
func (h *Controller) Status(_ *gin.Context) (*domain.StatusResponse, error) {
	return &domain.StatusResponse{Enabled: h.Service.Enabled()}, nil
}

// InstallURL : page d'installation de GitHub.
func (h *Controller) InstallURL(c *gin.Context, in *domain.ProjectPath) (*domain.URLResponse, error) {
	project, err := parseProject(in.ProjectID)
	if err != nil {
		return nil, err
	}
	user, _, err := requireUser(c)
	if err != nil {
		return nil, err
	}
	return h.Service.InstallURL(c.Request.Context(), project, user)
}

// Callback : retour de GitHub, transmis par la console.
func (h *Controller) Callback(c *gin.Context, in *domain.CallbackRequest) (*domain.CallbackResponse, error) {
	user, caller, err := requireUser(c)
	if err != nil {
		return nil, err
	}
	return h.Service.Callback(c.Request.Context(), user, caller, *in)
}

// ListInstallations : installations du projet.
func (h *Controller) ListInstallations(c *gin.Context, in *domain.ProjectPath) ([]domain.Installation, error) {
	project, err := parseProject(in.ProjectID)
	if err != nil {
		return nil, err
	}
	return h.Service.ListInstallations(c.Request.Context(), project)
}

// DeleteInstallation : délie une installation du projet.
func (h *Controller) DeleteInstallation(c *gin.Context, in *domain.InstallationPath) error {
	project, err := parseProject(in.ProjectID)
	if err != nil {
		return err
	}
	return h.Service.DeleteInstallation(c.Request.Context(), project, in.InstallationID)
}

// ListRepositories : dépôts d'une installation.
func (h *Controller) ListRepositories(c *gin.Context, in *domain.ListReposRequest) (*domain.RepositoriesResponse, error) {
	project, err := parseProject(in.ProjectID)
	if err != nil {
		return nil, err
	}
	return h.Service.ListRepositories(c.Request.Context(), project, *in)
}

// ListBranches : branches d'un dépôt.
func (h *Controller) ListBranches(c *gin.Context, in *domain.ListBranchesRequest) (*domain.BranchesResponse, error) {
	project, err := parseProject(in.ProjectID)
	if err != nil {
		return nil, err
	}
	return h.Service.ListBranches(c.Request.Context(), project, *in)
}

// CreateWorkload : conteneur, lien et premier build.
func (h *Controller) CreateWorkload(c *gin.Context, in *domain.CreateWorkloadRequest) (*domain.CreateWorkloadResponse, error) {
	project, err := parseProject(in.ProjectID)
	if err != nil {
		return nil, err
	}
	caller, ok := security.PrincipalFromContext(c)
	if !ok {
		return nil, domain.ErrInstallationDenied
	}
	return h.Service.CreateWorkload(c.Request.Context(), caller, project, userOf(c), *in)
}

// GetLink : lien d'un conteneur.
func (h *Controller) GetLink(c *gin.Context, in *domain.WorkloadPath) (*domain.LinkView, error) {
	id, err := parseWorkload(in.ID)
	if err != nil {
		return nil, err
	}
	return h.Service.GetLink(c.Request.Context(), id)
}

// PutLink : créer ou remplacer le lien d'un conteneur.
func (h *Controller) PutLink(c *gin.Context, in *domain.PutLinkRequest) (*domain.LinkView, error) {
	id, err := parseWorkload(in.ID)
	if err != nil {
		return nil, err
	}
	return h.Service.PutLink(c.Request.Context(), id, userOf(c), in.LinkSource)
}

// PatchLink : modifier le lien d'un conteneur.
func (h *Controller) PatchLink(c *gin.Context, in *domain.PatchLinkRequest) (*domain.LinkView, error) {
	id, err := parseWorkload(in.ID)
	if err != nil {
		return nil, err
	}
	return h.Service.PatchLink(c.Request.Context(), id, *in)
}

// DeleteLink : supprimer le lien d'un conteneur.
func (h *Controller) DeleteLink(c *gin.Context, in *domain.WorkloadPath) error {
	id, err := parseWorkload(in.ID)
	if err != nil {
		return err
	}
	return h.Service.DeleteLink(c.Request.Context(), id)
}

// DeployNow : construire et déployer le dernier commit de la branche.
func (h *Controller) DeployNow(c *gin.Context, in *domain.WorkloadPath) (*buildsDomain.Build, error) {
	id, err := parseWorkload(in.ID)
	if err != nil {
		return nil, err
	}
	return h.Service.DeployNow(c.Request.Context(), id, userOf(c))
}

// Webhook : livraison de GitHub. Hors tonic : la signature porte sur le corps
// brut. Elle est vérifiée sur un corps de taille limitée, avant tout décodage ;
// seul ce secret authentifie la livraison.
func (h *Controller) Webhook(c *gin.Context) {
	if !h.Service.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"code": "GITHUB_DISABLED", "message": domain.ErrDisabled.Error(), "error": domain.ErrDisabled.Error()})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, maxWebhookBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		status := http.StatusBadRequest
		if errors.As(err, &tooBig) {
			status = http.StatusRequestEntityTooLarge
		}
		c.Status(status)
		return
	}
	if !client.SignatureValid(h.WebhookSecret, body, c.GetHeader(headerSignature)) {
		c.JSON(http.StatusUnauthorized, gin.H{"code": betoerrors.CodeUnauthorized, "message": "invalid signature", "error": "invalid signature"})
		return
	}
	delivery, event := c.GetHeader(headerDelivery), c.GetHeader(headerEvent)
	if delivery == "" || event == "" || len(delivery) > maxDeliveryID {
		c.JSON(http.StatusBadRequest, gin.H{"code": betoerrors.CodeInvalidInput, "message": "missing delivery headers", "error": "missing delivery headers"})
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), webhookTimeout)
	defer cancel()
	status, err := h.Service.HandleWebhook(ctx, delivery, event, body)
	if err != nil {
		h.Logger.Errorw("github webhook failed", "delivery", delivery, "event", event, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"code": betoerrors.CodeInternal, "message": "internal error", "error": "internal error"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}
