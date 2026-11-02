package router

import (
	"github.com/thekrauss/kubemanager/internal/modules/builds"
	"github.com/thekrauss/kubemanager/internal/modules/github"
	githubClient "github.com/thekrauss/kubemanager/internal/modules/github/client"
	githubRepo "github.com/thekrauss/kubemanager/internal/modules/github/repository"
	githubSvc "github.com/thekrauss/kubemanager/internal/modules/github/service"
	workloadsSvc "github.com/thekrauss/kubemanager/internal/modules/workloads/service"
)

// newGitHubService : connexion GitHub. Client nil (fonction désactivée, sans
// erreur) si la configuration est incomplète ou si les builds sont désactivés ;
// une clé illisible désactive la fonction sans empêcher le reste de démarrer.
func (a *App) newGitHubService(buildCtl *builds.Controller, workloads *workloadsSvc.WorkloadService) *githubSvc.Service {
	c := a.Config.GitHubApp.WithDefaults()
	svc := &githubSvc.Service{Logger: a.Logger}
	if !c.Complete() || buildCtl == nil {
		a.Logger.Infow("github connection disabled: configuration incomplete")
		return svc
	}
	pem, err := githubClient.LoadKey(c.PrivateKeyFile)
	if err != nil {
		a.Logger.Errorw("github connection disabled: private key unreadable", "file", c.PrivateKeyFile)
		return svc
	}
	cl, err := githubClient.New(githubClient.Config{AppID: c.AppID, Slug: c.Slug, ClientID: c.ClientID, ClientSecret: c.ClientSecret,
		PrivateKeyPEM: pem, APIURL: c.APIURL, WebURL: c.WebURL})
	if err != nil {
		a.Logger.Errorw("github connection disabled", "error", err)
		return svc
	}
	svc.Client, svc.Repo, svc.State, svc.Authz = cl, githubRepo.New(a.DB), a.Cache, a.IAM
	svc.Builds, svc.Workloads = buildCtl.Service, workloads
	return svc
}

func (a *App) newGitHubController(svc *githubSvc.Service) *github.Controller {
	return &github.Controller{Service: svc, WebhookSecret: a.Config.GitHubApp.WebhookSecret, Logger: a.Logger}
}
