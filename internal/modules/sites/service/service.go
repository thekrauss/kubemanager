package service

import (
	"slices"

	"context"
	"errors"
	"fmt"
	"github.com/thekrauss/kubemanager/internal/auth"
	"io"
	"net"
	"strings"
	"time"

	"github.com/thekrauss/kubemanager/internal/core/httpheader"
	"github.com/thekrauss/kubemanager/internal/core/i18n"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/infrastructure/objectstore"
	"github.com/thekrauss/kubemanager/internal/infrastructure/temporal"
	"github.com/thekrauss/kubemanager/internal/modules/sites/activities"
	"github.com/thekrauss/kubemanager/internal/modules/sites/domain"
	"github.com/thekrauss/kubemanager/internal/modules/sites/repository"
	"github.com/thekrauss/kubemanager/internal/modules/sites/workflows"
)

type ISiteService interface {
	CreateSite(ctx context.Context, projectID uuid.UUID, req domain.CreateSiteRequest) (*domain.SiteResponse, error)
	ListSites(ctx context.Context, projectID uuid.UUID) ([]domain.SiteResponse, error)
	GetSite(ctx context.Context, projectID, siteID uuid.UUID) (*domain.SiteResponse, error)
	UpdateSite(ctx context.Context, projectID, siteID uuid.UUID, req domain.UpdateSiteRequest) (*domain.SiteResponse, error)
	DeleteSite(ctx context.Context, projectID, siteID uuid.UUID) error

	// Deploy reçoit l'archive .zip, la stocke et lance DeploySiteWorkflow.
	Deploy(ctx context.Context, projectID, siteID uuid.UUID, userID *uuid.UUID, archive io.Reader, size int64) (*domain.SiteDeployment, error)
	ListDeployments(ctx context.Context, projectID, siteID uuid.UUID) ([]domain.SiteDeployment, error)
	// ActivateDeployment : rollback / roll-forward vers un déploiement READY.
	ActivateDeployment(ctx context.Context, projectID, siteID, deploymentID uuid.UUID) (*domain.SiteResponse, error)

	AddDomain(ctx context.Context, projectID, siteID uuid.UUID, hostname string) (*domain.DomainResponse, error)
	ListDomains(ctx context.Context, projectID, siteID uuid.UUID) ([]domain.DomainResponse, error)
	VerifyDomain(ctx context.Context, projectID, siteID, domainID uuid.UUID) (*domain.DomainResponse, error)
	DeleteDomain(ctx context.Context, projectID, siteID, domainID uuid.UUID) error

	MaxArchiveBytes() int64
}

// DNSResolver : sous-ensemble de *net.Resolver (mockable en test).
type DNSResolver interface {
	LookupCNAME(ctx context.Context, host string) (string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// challengePrefixValue : préfixe de la valeur TXT.
const challengePrefixValue = "betorun-verify="

type Config struct {
	BaseDomain string
	EdgeTarget string
	Limits     activities.Limits
}

type SiteService struct {
	// HostnameInUse : nom déjà vérifié ailleurs sur la plateforme (optionnel).
	HostnameInUse func(ctx context.Context, host string) (bool, error)

	repo        repository.SiteRepository
	store       objectstore.Store
	distributor temporal.TaskDistributor
	resolver    DNSResolver
	cfg         Config
	logger      *zap.SugaredLogger
}

func NewSiteService(repo repository.SiteRepository, store objectstore.Store, distributor temporal.TaskDistributor,
	resolver DNSResolver, cfg Config, logger *zap.SugaredLogger) *SiteService {
	cfg.BaseDomain = strings.TrimSuffix(strings.ToLower(cfg.BaseDomain), ".")
	cfg.EdgeTarget = strings.TrimSuffix(strings.ToLower(cfg.EdgeTarget), ".")
	if cfg.EdgeTarget == "" {
		cfg.EdgeTarget = cfg.BaseDomain
	}
	cfg.Limits = cfg.Limits.WithDefaults()
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &SiteService{repo: repo, store: store, distributor: distributor, resolver: resolver, cfg: cfg, logger: logger}
}

func (s *SiteService) MaxArchiveBytes() int64 { return s.cfg.Limits.MaxArchiveBytes }

// ── Sites ────────────────────────────────────────────────────────────────

func (s *SiteService) CreateSite(ctx context.Context, projectID uuid.UUID, req domain.CreateSiteRequest) (*domain.SiteResponse, error) {
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if err := domain.ValidateName(name); err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, err.Error())
	}
	sub := strings.ToLower(strings.TrimSpace(req.Subdomain))
	if sub == "" {
		sub = name
	}
	if err := domain.ValidateSubdomain(sub); err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, err.Error())
	}

	site := &domain.Site{
		ID:          uuid.New(),
		ProjectID:   projectID,
		Name:        name,
		Subdomain:   sub,
		SPAFallback: req.SPAFallback,
		Status:      domain.SiteStatusEmpty,
	}
	if err := s.repo.CreateSite(ctx, site); err != nil {
		if isUniqueViolation(err) {
			return nil, betoerrors.New(betoerrors.CodeConflict, "ce nom ou ce sous-domaine est déjà utilisé")
		}
		return nil, err
	}
	return s.siteResponse(site), nil
}

func (s *SiteService) ListSites(ctx context.Context, projectID uuid.UUID) ([]domain.SiteResponse, error) {
	sites, err := s.repo.ListSites(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.SiteResponse, 0, len(sites))
	for i := range sites {
		out = append(out, *s.siteResponse(&sites[i]))
	}
	return out, nil
}

func (s *SiteService) GetSite(ctx context.Context, projectID, siteID uuid.UUID) (*domain.SiteResponse, error) {
	site, err := s.getSite(ctx, projectID, siteID)
	if err != nil {
		return nil, err
	}
	return s.siteResponse(site), nil
}

func (s *SiteService) UpdateSite(ctx context.Context, projectID, siteID uuid.UUID, req domain.UpdateSiteRequest) (*domain.SiteResponse, error) {
	site, err := s.getLiveSite(ctx, projectID, siteID)
	if err != nil {
		return nil, err
	}
	if req.SPAFallback != nil {
		site.SPAFallback = *req.SPAFallback
	}
	site.UpdatedAt = time.Now()
	if err := s.repo.UpdateSite(ctx, site); err != nil {
		return nil, err
	}
	return s.siteResponse(site), nil
}

// DeleteSite : passe le site en DELETING (l'edge arrête de le servir) puis
// délègue la suppression des fichiers et de la ligne à DeleteSiteWorkflow.
func (s *SiteService) DeleteSite(ctx context.Context, projectID, siteID uuid.UUID) error {
	site, err := s.getSite(ctx, projectID, siteID)
	if err != nil {
		return err
	}
	if err := s.repo.SetSiteStatus(ctx, site.ID, domain.SiteStatusDeleting); err != nil {
		return err
	}
	_, err = s.distributor.ExecuteWorkflow(ctx,
		temporal.ExecuteOptions{WorkflowID: workflows.WorkflowIDDeletePrefix + site.ID.String()},
		workflows.DeleteSiteWorkflow, site.ID.String())
	if temporal.IsAlreadyStarted(err) {
		return nil // deuxième demande : la suppression est déjà en cours
	}
	if err != nil {
		// Aucun workflow ne terminera ce DELETING : le site cesserait d'être servi.
		s.compensate(ctx, "site status not restored", site.ID, func(ctx context.Context) error {
			return s.repo.SetSiteStatus(ctx, site.ID, site.Status)
		})
	}
	return err
}

// ── Déploiements ─────────────────────────────────────────────────────────

func (s *SiteService) Deploy(ctx context.Context, projectID, siteID uuid.UUID, userID *uuid.UUID, archive io.Reader, size int64) (*domain.SiteDeployment, error) {
	if size <= 0 {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "archive vide")
	}
	if size > s.cfg.Limits.MaxArchiveBytes {
		return nil, betoerrors.Newf(betoerrors.CodeInvalidInput, "archive trop volumineuse (maximum %d Mo)", s.cfg.Limits.MaxArchiveBytes/domain.BytesPerMB)
	}
	site, err := s.getLiveSite(ctx, projectID, siteID)
	if err != nil {
		return nil, err
	}

	dep := &domain.SiteDeployment{
		ID:        uuid.New(),
		SiteID:    site.ID,
		Status:    domain.DeploymentStatusPending,
		CreatedBy: userID,
	}
	dep.ArchiveKey = domain.ArchiveKey(site.ID, dep.ID)
	if err := s.repo.CreateDeployment(ctx, dep); err != nil {
		return nil, err
	}

	if err := s.store.Put(ctx, dep.ArchiveKey, archive, size, httpheader.MIMEZip); err != nil {
		s.failDeployment(ctx, dep, "l'envoi de l'archive a échoué, réessayez")
		s.removeArchive(ctx, dep)
		return nil, fmt.Errorf("store archive: %w", err)
	}

	ref := activities.DeploymentRef{SiteID: site.ID.String(), DeploymentID: dep.ID.String()}
	if _, err := s.distributor.ExecuteWorkflow(ctx,
		temporal.ExecuteOptions{WorkflowID: workflows.WorkflowIDDeployPrefix + dep.ID.String()},
		workflows.DeploySiteWorkflow, ref); err != nil {
		s.failDeployment(ctx, dep, "le déploiement n'a pas pu démarrer, réessayez")
		s.removeArchive(ctx, dep)
		return nil, fmt.Errorf("start deploy workflow: %w", err)
	}
	return dep, nil
}

// compensate : annule une écriture dont la suite a échoué, y compris si la
// requête a été interrompue. Erreur journalisée : celle de l'appelant prime.
func (s *SiteService) compensate(ctx context.Context, msg string, id uuid.UUID, undo func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), compensationTimeout)
	defer cancel()
	if err := undo(ctx); err != nil && s.logger != nil {
		s.logger.Warnw(msg, "id", id, "error", err)
	}
}

func (s *SiteService) failDeployment(ctx context.Context, dep *domain.SiteDeployment, msg string) {
	s.compensate(ctx, "mark site deployment failed", dep.ID, func(ctx context.Context) error {
		return s.repo.UpdateDeploymentStatus(ctx, dep.ID, domain.DeploymentStatusFailed, msg)
	})
	dep.Status, dep.Error = domain.DeploymentStatusFailed, msg
}

// removeArchive : aucun workflow ne lira ni ne supprimera cette archive.
func (s *SiteService) removeArchive(ctx context.Context, dep *domain.SiteDeployment) {
	s.compensate(ctx, "orphan site archive not removed", dep.ID, func(ctx context.Context) error {
		return s.store.Delete(ctx, dep.ArchiveKey)
	})
}

func (s *SiteService) ListDeployments(ctx context.Context, projectID, siteID uuid.UUID) ([]domain.SiteDeployment, error) {
	site, err := s.getSite(ctx, projectID, siteID)
	if err != nil {
		return nil, err
	}
	deployments, err := s.repo.ListDeployments(ctx, site.ID, deploymentsListLimit)
	if err != nil {
		return nil, err
	}
	// Le message d'échec est enregistré une fois : traduit à la lecture,
	// dans la langue de l'écran.
	l := i18n.From(ctx)
	for i := range deployments {
		deployments[i].Error = i18n.Translate(l, deployments[i].Error)
	}
	return deployments, nil
}

func (s *SiteService) ActivateDeployment(ctx context.Context, projectID, siteID, deploymentID uuid.UUID) (*domain.SiteResponse, error) {
	site, err := s.getLiveSite(ctx, projectID, siteID)
	if err != nil {
		return nil, err
	}
	dep, err := s.repo.GetDeployment(ctx, site.ID, deploymentID)
	if err != nil {
		return nil, notFound(err, "déploiement introuvable")
	}
	if dep.Status != domain.DeploymentStatusReady {
		return nil, betoerrors.New(betoerrors.CodeConflict, "seul un déploiement réussi peut être activé")
	}
	if err := s.repo.ActivateDeployment(ctx, site.ID, dep.ID); err != nil {
		return nil, err
	}
	site.ActiveDeploymentID = &dep.ID
	site.Status = domain.SiteStatusLive
	return s.siteResponse(site), nil
}

// ── Domaines personnalisés ───────────────────────────────────────────────

func (s *SiteService) AddDomain(ctx context.Context, projectID, siteID uuid.UUID, hostname string) (*domain.DomainResponse, error) {
	site, err := s.getLiveSite(ctx, projectID, siteID)
	if err != nil {
		return nil, err
	}
	host, err := domain.NormalizeHostname(hostname, s.cfg.BaseDomain)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, err.Error())
	}
	token, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}
	d := &domain.SiteDomain{ID: uuid.New(), SiteID: site.ID, Hostname: host, Token: challengePrefixValue + token}
	if err := s.repo.CreateDomain(ctx, d); err != nil {
		if isUniqueViolation(err) {
			return nil, betoerrors.New(betoerrors.CodeConflict, "ce domaine est déjà rattaché à ce site")
		}
		return nil, err
	}
	return s.domainResponse(d), nil
}

func (s *SiteService) ListDomains(ctx context.Context, projectID, siteID uuid.UUID) ([]domain.DomainResponse, error) {
	site, err := s.getSite(ctx, projectID, siteID)
	if err != nil {
		return nil, err
	}
	ds, err := s.repo.ListDomains(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.DomainResponse, 0, len(ds))
	for i := range ds {
		out = append(out, *s.domainResponse(&ds[i]))
	}
	return out, nil
}

// VerifyDomain : vérifie la preuve de propriété (TXT propre à la demande).
// Pointer vers la plateforme ne suffit pas : n'importe quel client pourrait
// capter un domaine qui nous vise déjà. Un domaine déjà vérifié reste servi si
// la preuve disparaît (une erreur DNS passagère ne doit pas couper un site),
// jusqu'à ce qu'un autre apporte la sienne : le domaine a changé de mains.
func (s *SiteService) VerifyDomain(ctx context.Context, projectID, siteID, domainID uuid.UUID) (*domain.DomainResponse, error) {
	site, err := s.getLiveSite(ctx, projectID, siteID)
	if err != nil {
		return nil, err
	}
	d, err := s.repo.GetDomain(ctx, site.ID, domainID)
	if err != nil {
		return nil, notFound(err, "domaine introuvable")
	}

	lookupCtx, cancel := context.WithTimeout(ctx, domainLookupTimeout)
	defer cancel()
	pointing := s.pointsToEdge(lookupCtx, d.Hostname, site.Subdomain+"."+s.cfg.BaseDomain)
	txt, _ := s.resolver.LookupTXT(lookupCtx, domain.ChallengePrefix+d.Hostname)
	for i := range txt {
		txt[i] = strings.TrimSpace(txt[i])
	}

	now := time.Now()
	if d.Token == "" || !slices.Contains(txt, d.Token) {
		if err := s.repo.TouchDomain(ctx, d.ID, now); err != nil {
			return nil, err
		}
		d.CheckedAt = &now
		return s.domainView(d, pointing), nil
	}
	if d.Verified {
		return s.domainView(d, pointing), nil
	}
	claimed := betoerrors.New(betoerrors.CodeConflict, "ce domaine est déjà rattaché à un site")
	if s.HostnameInUse != nil {
		used, err := s.HostnameInUse(ctx, d.Hostname)
		if err != nil {
			return nil, err
		}
		if used {
			return nil, claimed
		}
	}
	previous, err := s.repo.VerifiedDomain(ctx, d.Hostname, d.ID)
	if err != nil {
		return nil, err
	}
	if previous != nil {
		if previous.Token != "" && slices.Contains(txt, previous.Token) {
			return nil, claimed
		}
		if err := s.repo.SetDomainVerification(ctx, previous.ID, false, now); err != nil {
			return nil, err
		}
	}
	if err := s.repo.SetDomainVerification(ctx, d.ID, true, now); err != nil {
		if isUniqueViolation(err) {
			return nil, claimed
		}
		return nil, err
	}
	d.Verified, d.CheckedAt, d.VerifiedAt = true, &now, &now
	return s.domainView(d, pointing), nil
}

func (s *SiteService) pointsToEdge(ctx context.Context, host, siteHost string) bool {
	if cname, err := s.resolver.LookupCNAME(ctx, host); err == nil {
		target := strings.TrimSuffix(strings.ToLower(cname), ".")
		if target == s.cfg.EdgeTarget || target == siteHost {
			return true
		}
	}
	hostIPs, err := s.resolver.LookupHost(ctx, host)
	if err != nil || len(hostIPs) == 0 {
		return false
	}
	edgeIPs, err := s.resolver.LookupHost(ctx, s.cfg.EdgeTarget)
	if err != nil {
		return false
	}
	edge := make(map[string]bool, len(edgeIPs))
	for _, ip := range edgeIPs {
		edge[ip] = true
	}
	// Toutes les adresses doivent être celles de l'edge : un domaine qui
	// pointe aussi ailleurs n'est pas sous notre contrôle.
	for _, ip := range hostIPs {
		if !edge[ip] {
			return false
		}
	}
	return true
}

func (s *SiteService) DeleteDomain(ctx context.Context, projectID, siteID, domainID uuid.UUID) error {
	site, err := s.getSite(ctx, projectID, siteID)
	if err != nil {
		return err
	}
	return s.repo.DeleteDomain(ctx, site.ID, domainID)
}

// ── Helpers ──────────────────────────────────────────────────────────────

func (s *SiteService) getSite(ctx context.Context, projectID, siteID uuid.UUID) (*domain.Site, error) {
	site, err := s.repo.GetSite(ctx, projectID, siteID)
	if err != nil {
		return nil, notFound(err, "site introuvable")
	}
	return site, nil
}

// getLiveSite : refuse toute modification d'un site en cours de suppression.
func (s *SiteService) getLiveSite(ctx context.Context, projectID, siteID uuid.UUID) (*domain.Site, error) {
	site, err := s.getSite(ctx, projectID, siteID)
	if err != nil {
		return nil, err
	}
	if site.Status == domain.SiteStatusDeleting {
		return nil, betoerrors.New(betoerrors.CodeConflict, "site en cours de suppression")
	}
	return site, nil
}

func (s *SiteService) siteResponse(site *domain.Site) *domain.SiteResponse {
	return &domain.SiteResponse{Site: *site, URL: s.publicURL(site.Subdomain + "." + s.cfg.BaseDomain)}
}

func (s *SiteService) domainResponse(d *domain.SiteDomain) *domain.DomainResponse {
	records := []domain.DNSRecord{}
	if d.Token != "" {
		records = append(records, domain.DNSRecord{Type: "TXT", Name: domain.ChallengePrefix + d.Hostname, Value: d.Token})
	}
	records = append(records, domain.DNSRecord{Type: "CNAME", Name: d.Hostname, Value: s.cfg.EdgeTarget})
	return &domain.DomainResponse{SiteDomain: *d, CNAMETarget: s.cfg.EdgeTarget, Records: records}
}

func (s *SiteService) domainView(d *domain.SiteDomain, pointing bool) *domain.DomainResponse {
	view := s.domainResponse(d)
	view.Pointing = pointing
	return view
}

// publicURL : http en dev local (*.localhost), https partout ailleurs.
func (s *SiteService) publicURL(host string) string {
	if host == localhostHost || strings.HasSuffix(host, localhostSuffix) {
		return schemeHTTP + host
	}
	return schemeHTTPS + host
}

func notFound(err error, msg string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return betoerrors.New(betoerrors.CodeNotFound, msg)
	}
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}
