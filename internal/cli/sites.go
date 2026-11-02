package cli

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/archive"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
	sitesdomain "github.com/thekrauss/kubemanager/internal/modules/sites/domain"
)

// Attente de la publication (extraction et mise en ligne côté serveur).
var (
	deployPollEvery = 2 * time.Second
	deployTimeout   = 5 * time.Minute
)

func newSites() *cobra.Command {
	c := &cobra.Command{Use: "sites", Short: i18n.T("sites.short"),
		Example: "# " + i18n.T("ex.sites.first") + "\n" +
			"betorun sites deploy ./dist --site vitrine --create\n" +
			"# " + i18n.T("ex.sites.domain") + "\n" +
			"betorun sites domains add www.exemple.sn --site vitrine"}
	c.AddCommand(newSitesList(), newSitesDeploy(), newDomains())
	return c
}

func newSitesList() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: i18n.T("sites.list.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := settings()
			if err != nil {
				return err
			}
			project, err := requireProject(s)
			if err != nil {
				return err
			}
			sites, err := client(s).ListSites(cmd.Context(), project)
			if err != nil {
				return err
			}
			if done, err := wantJSON(cmd, sites); done {
				return err
			}
			p := printer(cmd)
			if len(sites) == 0 {
				p.Hint(i18n.T("sites.list.empty"))
				return nil
			}
			t := p.Table(i18n.T("wl.col.name"), i18n.T("wl.col.status"), i18n.T("wl.col.url"))
			for _, site := range sites {
				t.Row(p.Bold(site.Name), siteStatus(p, site.Status), p.Link(site.URL))
			}
			return t.Flush()
		},
	}
}

func newSitesDeploy() *cobra.Command {
	var (
		siteName string
		create   bool
		spa      bool
		noWait   bool
	)
	c := &cobra.Command{
		Use:   "deploy " + i18n.T("arg.dir"),
		Short: i18n.T("sites.deploy.short"),
		Example: "betorun sites deploy ./dist --site vitrine --create  # " + i18n.T("ex.sites.create") + "\n" +
			"betorun sites deploy ./build --site app --spa  # " + i18n.T("ex.sites.spa"),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if siteName == "" {
				return errors.New(i18n.T("sites.deploy.need_site"))
			}
			s, err := settings()
			if err != nil {
				return err
			}
			project, err := requireProject(s)
			if err != nil {
				return err
			}
			ctx, cl, p := cmd.Context(), client(s), printer(cmd)
			steps := p.Steps()
			fail := func(err error) error { steps.Failed(); return err }

			dir, _ := filepath.Abs(args[0])
			p.Title(siteName, dir)
			steps.Start(i18n.T("sites.deploy.packing", dir))
			zipped, err := archive.Zip(dir)
			if err != nil {
				return fail(err)
			}
			defer os.Remove(zipped.Path)
			steps.Done()
			p.Hint("  " + i18n.T("sites.deploy.packed", zipped.Files, archive.HumanSize(zipped.Size)))

			// Site visé, créé si demandé : seulement une fois l'archive prête
			// (un dossier vide ou illisible ne laisse pas de site vide derrière lui).
			site, err := findSite(cmd, cl, project, siteName)
			if err != nil {
				return fail(err)
			}
			if site == nil {
				if !create {
					return errors.New(i18n.T("sites.deploy.not_found", siteName))
				}
				steps.Start(i18n.T("sites.deploy.creating", siteName))
				if site, err = cl.CreateSite(ctx, project, siteName, spa); err != nil {
					return fail(err)
				}
			}

			f, err := os.Open(zipped.Path)
			if err != nil {
				return err
			}
			defer f.Close()
			steps.Start(i18n.T("sites.deploy.uploading"))
			dep, err := cl.UploadDeployment(ctx, project, site.ID.String(), f)
			if err != nil {
				return fail(err)
			}
			if noWait {
				steps.Done()
				return nil
			}

			steps.Start(i18n.T("sites.deploy.processing"))
			deadline := time.Now().Add(deployTimeout)
			for dep.Status != sitesdomain.DeploymentStatusReady && dep.Status != sitesdomain.DeploymentStatusFailed {
				if time.Now().After(deadline) {
					return fail(errors.New(i18n.T("sites.deploy.timeout")))
				}
				select {
				case <-ctx.Done():
					return fail(ctx.Err())
				case <-time.After(deployPollEvery):
				}
				if dep, err = cl.FindDeployment(ctx, project, site.ID.String(), dep.ID.String()); err != nil {
					return fail(err)
				}
			}
			if dep.Status == sitesdomain.DeploymentStatusFailed {
				return fail(errors.New(i18n.T("sites.deploy.failed", dep.Error)))
			}
			steps.Done()
			if site, err = cl.GetSite(ctx, project, site.ID.String()); err != nil {
				return err
			}
			p.Success(i18n.T("sites.deploy.ok", p.Link(site.URL)))
			return nil
		},
	}
	c.Flags().StringVarP(&siteName, "site", "s", "", i18n.T("flag.site"))
	c.Flags().BoolVar(&create, "create", false, i18n.T("flag.create"))
	c.Flags().BoolVar(&spa, "spa", false, i18n.T("flag.spa"))
	c.Flags().BoolVar(&noWait, "no-wait", false, i18n.T("flag.no_wait"))
	return c
}

func findSite(cmd *cobra.Command, cl *api.Client, project, name string) (*api.Site, error) {
	sites, err := cl.ListSites(cmd.Context(), project)
	if err != nil {
		return nil, err
	}
	for i := range sites {
		if sites[i].Name == name {
			return &sites[i], nil
		}
	}
	return nil, nil
}
