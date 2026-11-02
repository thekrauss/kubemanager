package service

import (
	"context"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/auth"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	sitesDomain "github.com/thekrauss/kubemanager/internal/modules/sites/domain"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/domain"
	"github.com/thekrauss/kubemanager/internal/modules/workloads/workflows"
)

// Domaines personnalisés. La propriété se prouve par un enregistrement TXT
// propre à chaque demande : pointer vers la plateforme ne suffit pas, sinon
// n'importe quel client pourrait capter un domaine qui nous vise déjà. Aucune
// route n'existe avant la preuve, et une demande non prouvée ne réserve rien.

const domainLookupTimeout = 8 * time.Second

// DNSResolver : ce qu'il faut savoir du DNS public (remplaçable en test).
type DNSResolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// DomainSettings : réglages des domaines.
type DomainSettings struct {
	Resolver DNSResolver
	// Target : adresse vers laquelle pointer (nom d'hôte : CNAME, IP : A).
	Target string
	// TLS : certificat pour chaque domaine (faux seulement en développement).
	TLS bool
}

// NewResolver : résolveur DNS ; server (hôte:port) pour viser un résolveur
// précis plutôt que celui de la machine.
func NewResolver(server string) DNSResolver {
	if server == "" {
		return net.DefaultResolver
	}
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, server)
	}}
}

func (s *WorkloadService) records(d *domain.WorkloadDomain) []domain.DNSRecord {
	kind := domain.RecordCNAME
	if net.ParseIP(s.DomainCfg.Target) != nil {
		kind = domain.RecordA
	}
	return []domain.DNSRecord{
		{Type: domain.RecordTXT, Name: domain.ChallengePrefix + d.Hostname, Value: d.Token},
		{Type: kind, Name: d.Hostname, Value: s.DomainCfg.Target},
	}
}

func (s *WorkloadService) domainView(d *domain.WorkloadDomain, pointing bool) domain.DomainResponse {
	return domain.DomainResponse{WorkloadDomain: *d, Records: s.records(d), Pointing: pointing}
}

func (s *WorkloadService) domainsReady() error {
	if s.Domains == nil || s.DomainCfg == nil || s.DomainCfg.Target == "" {
		return domain.ErrDomainsDisabled
	}
	return nil
}

// ListDomains : domaines du conteneur et enregistrements DNS à créer.
func (s *WorkloadService) ListDomains(ctx context.Context, workload uuid.UUID) ([]domain.DomainResponse, error) {
	if err := s.domainsReady(); err != nil {
		return nil, err
	}
	list, err := s.Domains.List(ctx, workload)
	if err != nil {
		return nil, err
	}
	out := make([]domain.DomainResponse, 0, len(list))
	for i := range list {
		out = append(out, s.domainView(&list[i], false))
	}
	return out, nil
}

// AddDomain : enregistre la demande, sans effet sur le routage.
func (s *WorkloadService) AddDomain(ctx context.Context, workload uuid.UUID, hostname string) (*domain.DomainResponse, error) {
	if err := s.domainsReady(); err != nil {
		return nil, err
	}
	w, err := s.Repo.GetByID(ctx, workload)
	if err != nil {
		return nil, err
	}
	if err := ensureNotDeleting(w); err != nil {
		return nil, err
	}
	host, err := sitesDomain.NormalizeHostname(hostname, configs.AppConfig.Sites.BaseDomain)
	if err != nil {
		return nil, err
	}
	existing, err := s.Domains.List(ctx, workload)
	if err != nil {
		return nil, err
	}
	if len(existing) >= domain.MaxDomainsPerWorkload {
		return nil, domain.ErrDomainLimit
	}
	token, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}
	d := &domain.WorkloadDomain{ID: uuid.New(), WorkloadID: workload, Hostname: host, Token: "betorun-verify=" + token, CreatedAt: time.Now()}
	if err := s.Domains.Create(ctx, d); err != nil {
		return nil, err
	}
	view := s.domainView(d, false)
	return &view, nil
}

// VerifyDomain : vérifie la preuve TXT. Prouvé, le domaine est routé (nouveau
// déploiement). Il peut reprendre un nom réservé par un autre conteneur si la
// preuve de ce dernier a disparu : le domaine a changé de mains.
func (s *WorkloadService) VerifyDomain(ctx context.Context, workload, id uuid.UUID) (*domain.DomainResponse, error) {
	if err := s.domainsReady(); err != nil {
		return nil, err
	}
	d, err := s.Domains.Get(ctx, workload, id)
	if err != nil {
		return nil, err
	}
	w, err := s.Repo.GetByID(ctx, workload)
	if err != nil {
		return nil, err
	}
	if err := ensureNotDeleting(w); err != nil {
		return nil, err
	}
	lookup, cancel := context.WithTimeout(ctx, domainLookupTimeout)
	defer cancel()
	txt := s.challengeTXT(lookup, d.Hostname)
	pointing := s.pointsToPlatform(lookup, d.Hostname)

	if !slices.Contains(txt, d.Token) {
		// Preuve disparue sur un domaine déjà vérifié : il reste routé tant
		// que personne d'autre ne le prouve (une erreur DNS passagère ne doit
		// pas couper un service en production).
		if err := s.Domains.Touch(ctx, d.ID); err != nil {
			return nil, err
		}
		view := s.domainView(d, pointing)
		return &view, nil
	}
	if d.Verified {
		view := s.domainView(d, pointing)
		return &view, nil
	}
	if w.Status == utils.WorkloadStopped {
		return nil, domain.ErrWorkloadStopped
	}
	if err := s.ensureNoDeployRunning(ctx, workload); err != nil {
		return nil, err
	}
	taken, err := s.Domains.SiteHostnameTaken(ctx, d.Hostname)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, domain.ErrDomainClaimed
	}
	previous, err := s.Domains.VerifiedOwner(ctx, d.Hostname, d.ID)
	if err != nil {
		return nil, err
	}
	if previous != nil {
		if slices.Contains(txt, previous.Token) {
			return nil, domain.ErrDomainClaimed
		}
		if err := s.Domains.SetVerified(ctx, previous.ID, false); err != nil {
			return nil, err
		}
	}
	if err := s.Domains.SetVerified(ctx, d.ID, true); err != nil {
		return nil, err
	}
	if err := s.redeploy(ctx, w); err != nil {
		return nil, err
	}
	if previous != nil {
		s.releaseFrom(ctx, previous.WorkloadID)
	}
	updated, err := s.Domains.Get(ctx, workload, id)
	if err != nil {
		return nil, err
	}
	view := s.domainView(updated, pointing)
	return &view, nil
}

// releaseFrom : retire la route de l'ancien conteneur. Au mieux : le nouveau
// propriétaire a la preuve, et le prochain déploiement de l'ancien la retirera.
func (s *WorkloadService) releaseFrom(ctx context.Context, workload uuid.UUID) {
	w, err := s.Repo.GetByID(ctx, workload)
	if err == nil {
		err = s.ensureNoDeployRunning(ctx, workload)
	}
	if err == nil {
		err = s.redeploy(ctx, w)
	}
	if err != nil && s.Logger != nil {
		s.Logger.Warnw("custom domain: previous route not removed yet", "workload", workload, "error", err)
	}
}

// RemoveDomain : retire le domaine et sa route.
func (s *WorkloadService) RemoveDomain(ctx context.Context, workload, id uuid.UUID) error {
	if err := s.domainsReady(); err != nil {
		return err
	}
	d, err := s.Domains.Get(ctx, workload, id)
	if err != nil {
		return err
	}
	if !d.Verified {
		return s.Domains.Delete(ctx, workload, id)
	}
	w, err := s.Repo.GetByID(ctx, workload)
	if err != nil {
		return err
	}
	if err := ensureNotDeleting(w); err != nil {
		return err
	}
	if w.Status == utils.WorkloadStopped {
		return domain.ErrWorkloadStopped
	}
	if err := s.ensureNoDeployRunning(ctx, workload); err != nil {
		return err
	}
	if err := s.Domains.Delete(ctx, workload, id); err != nil {
		return err
	}
	return s.redeploy(ctx, w)
}

// redeploy : même configuration, routes à jour.
func (s *WorkloadService) redeploy(ctx context.Context, w *domain.Workload) error {
	// Sans image : le premier déploiement reprendra ce changement.
	if w.Image == "" {
		return nil
	}
	spec, err := s.specFor(w)
	if err != nil {
		return err
	}
	in, err := s.inputFor(ctx, w, spec)
	if err != nil {
		return err
	}
	return s.startDeploy(ctx, w, in)
}

// inputFor : entrée du workflow, avec les domaines vérifiés.
func (s *WorkloadService) inputFor(ctx context.Context, w *domain.Workload, spec domain.CreateWorkloadRequest) (workflows.DeployWorkloadInput, error) {
	in := s.deployInput(w, spec)
	if s.Domains != nil {
		hosts, err := s.Domains.VerifiedHosts(ctx, w.ID)
		if err != nil {
			return in, err
		}
		in.CustomDomains = hosts
		in.CustomDomainTLS = s.DomainCfg != nil && s.DomainCfg.TLS
	}
	return in, nil
}

func (s *WorkloadService) challengeTXT(ctx context.Context, host string) []string {
	records, err := s.DomainCfg.Resolver.LookupTXT(ctx, domain.ChallengePrefix+host)
	if err != nil {
		return nil
	}
	for i := range records {
		records[i] = strings.TrimSpace(records[i])
	}
	return records
}

// pointsToPlatform : toutes les adresses du domaine sont celles de la cible.
func (s *WorkloadService) pointsToPlatform(ctx context.Context, host string) bool {
	hostIPs, err := s.DomainCfg.Resolver.LookupHost(ctx, host)
	if err != nil || len(hostIPs) == 0 {
		return false
	}
	targets := []string{s.DomainCfg.Target}
	if net.ParseIP(s.DomainCfg.Target) == nil {
		if targets, err = s.DomainCfg.Resolver.LookupHost(ctx, s.DomainCfg.Target); err != nil {
			return false
		}
	}
	for _, ip := range hostIPs {
		if !slices.Contains(targets, ip) {
			return false
		}
	}
	return true
}
