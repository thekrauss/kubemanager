package activities

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/infrastructure/objectstore"
	"github.com/thekrauss/kubemanager/internal/modules/sites/domain"
	"github.com/thekrauss/kubemanager/internal/modules/sites/repository"
)

// SiteActivities : activités Temporal des sites statiques.
//
// Idempotence : pas d'idempotency.ExecuteActivity ici. Chaque déploiement
// écrit sous son propre préfixe (sites/<site>/<deploy>/), donc rejouer
// l'extraction réécrit les mêmes objets ; les mises à jour DB sont des SET.
type SiteActivities struct {
	Repo   repository.SiteRepository
	Store  objectstore.Store
	Limits Limits
	Logger *zap.SugaredLogger
}

type DeploymentRef struct {
	SiteID       string
	DeploymentID string
}

func (r DeploymentRef) ids() (uuid.UUID, uuid.UUID, error) {
	siteID, err := uuid.Parse(r.SiteID)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	depID, err := uuid.Parse(r.DeploymentID)
	return siteID, depID, err
}

// ExtractDeployment télécharge l'archive et extrait ses fichiers sous le
// préfixe du déploiement. Une archive invalide renvoie une erreur non
// retryable dont le message est destiné à l'utilisateur.
func (a *SiteActivities) ExtractDeployment(ctx context.Context, ref DeploymentRef) (ExtractResult, error) {
	siteID, depID, err := ref.ids()
	if err != nil {
		return ExtractResult{}, temporal.NewNonRetryableApplicationError("invalid deployment reference", "InvalidInput", err)
	}
	if err := a.Repo.UpdateDeploymentStatus(ctx, depID, domain.DeploymentStatusProcessing, ""); err != nil {
		return ExtractResult{}, err
	}

	tmp, err := os.CreateTemp("", "site-*.zip")
	if err != nil {
		return ExtractResult{}, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if err := a.Store.Download(ctx, domain.ArchiveKey(siteID, depID), tmp); err != nil {
		if errors.Is(err, objectstore.ErrNotFound) {
			return ExtractResult{}, temporal.NewNonRetryableApplicationError("archive introuvable", "InvalidArchive", err)
		}
		return ExtractResult{}, fmt.Errorf("download archive: %w", err)
	}
	size, err := tmp.Seek(0, io.SeekEnd)
	if err != nil {
		return ExtractResult{}, err
	}
	zr, err := zip.NewReader(tmp, size)
	if err != nil {
		return ExtractResult{}, temporal.NewNonRetryableApplicationError("l'archive n'est pas un fichier .zip valide", "InvalidArchive", err)
	}

	prefix := domain.ObjectPrefix(siteID, depID)
	res, err := ExtractArchive(ctx, zr, a.Limits, func(ctx context.Context, rel string, r io.Reader, size int64, ct string) error {
		return a.Store.Put(ctx, prefix+rel, r, size, ct)
	})
	if err != nil {
		var ae *ArchiveError
		if errors.As(err, &ae) {
			return ExtractResult{}, temporal.NewNonRetryableApplicationError(ae.Msg, "InvalidArchive", err)
		}
		return ExtractResult{}, err
	}
	a.Logger.Infow("site deployment extracted", "site_id", siteID, "deployment_id", depID,
		"files", res.FileCount, "bytes", res.SizeBytes)
	return res, nil
}

type ReadyInput struct {
	DeploymentRef
	FileCount int
	SizeBytes int64
}

// MarkDeploymentReady active le déploiement : l'edge le sert dès la
// prochaine résolution du nom d'hôte (cache court côté edge).
func (a *SiteActivities) MarkDeploymentReady(ctx context.Context, in ReadyInput) error {
	siteID, depID, err := in.ids()
	if err != nil {
		return temporal.NewNonRetryableApplicationError("invalid deployment reference", "InvalidInput", err)
	}
	return a.Repo.MarkDeploymentReady(ctx, siteID, depID, in.FileCount, in.SizeBytes)
}

type FailedInput struct {
	DeploymentRef
	Message string
}

func (a *SiteActivities) MarkDeploymentFailed(ctx context.Context, in FailedInput) error {
	_, depID, err := in.ids()
	if err != nil {
		return temporal.NewNonRetryableApplicationError("invalid deployment reference", "InvalidInput", err)
	}
	return a.Repo.UpdateDeploymentStatus(ctx, depID, domain.DeploymentStatusFailed, in.Message)
}

// DeleteArchive supprime l'archive uploadée une fois extraite (ou refusée).
func (a *SiteActivities) DeleteArchive(ctx context.Context, ref DeploymentRef) error {
	siteID, depID, err := ref.ids()
	if err != nil {
		return temporal.NewNonRetryableApplicationError("invalid deployment reference", "InvalidInput", err)
	}
	return a.Store.Delete(ctx, domain.ArchiveKey(siteID, depID))
}

// DeleteSiteObjects supprime tous les fichiers et archives d'un site.
func (a *SiteActivities) DeleteSiteObjects(ctx context.Context, siteID string) error {
	id, err := uuid.Parse(siteID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("invalid site id", "InvalidInput", err)
	}
	if err := a.Store.DeletePrefix(ctx, domain.SitePrefix(id)); err != nil {
		return err
	}
	return a.Store.DeletePrefix(ctx, domain.UploadsPrefix(id))
}

// DeleteProjectSiteObjects : étape de DeleteProjectWorkflow. Les lignes des
// sites partent avec le projet (FK CASCADE) mais pas leurs fichiers : on coupe
// d'abord le service (DELETING) puis on vide le stockage de chaque site.
// Idempotent : rejouée, elle retrouve les mêmes sites et des préfixes vides.
func (a *SiteActivities) DeleteProjectSiteObjects(ctx context.Context, projectID string) error {
	id, err := uuid.Parse(projectID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("invalid project id", "InvalidInput", err)
	}
	sites, err := a.Repo.ListSites(ctx, id)
	if err != nil {
		return err
	}
	if len(sites) == 0 {
		return nil
	}
	if a.Store == nil {
		// Sites désactivés après avoir été utilisés : on ne peut pas nettoyer.
		a.Logger.Warnw("object store not configured — site files left in storage",
			"project_id", id, "sites", len(sites))
		return nil
	}
	for _, s := range sites {
		if err := a.Repo.SetSiteStatus(ctx, s.ID, domain.SiteStatusDeleting); err != nil {
			return err
		}
		if err := a.DeleteSiteObjects(ctx, s.ID.String()); err != nil {
			return err
		}
	}
	return nil
}

// DeleteSiteRecord supprime la ligne du site (déploiements et domaines en cascade).
func (a *SiteActivities) DeleteSiteRecord(ctx context.Context, siteID string) error {
	id, err := uuid.Parse(siteID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("invalid site id", "InvalidInput", err)
	}
	return a.Repo.DeleteSite(ctx, id)
}
