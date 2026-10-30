package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/archive"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
)

// Attente de la publication (extraction et mise en ligne côté serveur).
var (
	deployPollEvery = 2 * time.Second
	deployTimeout   = 5 * time.Minute
)

func newSites() *cobra.Command {
	c := &cobra.Command{Use: "sites", Short: i18n.T("sites.short")}
	c.AddCommand(newSitesList(), newSitesDeploy())
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
			if len(sites) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), i18n.T("sites.list.empty"))
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			for _, site := range sites {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", site.Name, site.Status, site.URL)
			}
			return tw.Flush()
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
		Args:  cobra.ExactArgs(1),
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
			ctx, out, cl := cmd.Context(), cmd.OutOrStdout(), client(s)

			dir, _ := filepath.Abs(args[0])
			fmt.Fprintln(out, i18n.T("sites.deploy.packing", dir))
			zipped, err := archive.Zip(dir)
			if err != nil {
				return err
			}
			defer os.Remove(zipped.Path)
			fmt.Fprintln(out, i18n.T("sites.deploy.packed", zipped.Files, archive.HumanSize(zipped.Size)))

			// Site visé, créé si demandé : seulement une fois l'archive prête
			// (un dossier vide ou illisible ne laisse pas de site vide derrière lui).
			site, err := findSite(cmd, cl, project, siteName)
			if err != nil {
				return err
			}
			if site == nil {
				if !create {
					return errors.New(i18n.T("sites.deploy.not_found", siteName))
				}
				fmt.Fprintln(out, i18n.T("sites.deploy.creating", siteName))
				if site, err = cl.CreateSite(ctx, project, siteName, spa); err != nil {
					return err
				}
			}

			f, err := os.Open(zipped.Path)
			if err != nil {
				return err
			}
			defer f.Close()
			fmt.Fprintln(out, i18n.T("sites.deploy.uploading"))
			dep, err := cl.UploadDeployment(ctx, project, site.ID.String(), f)
			if err != nil {
				return err
			}
			if noWait {
				return nil
			}

			fmt.Fprintln(out, i18n.T("sites.deploy.processing"))
			deadline := time.Now().Add(deployTimeout)
			for dep.Status != "READY" && dep.Status != "FAILED" {
				if time.Now().After(deadline) {
					return errors.New(i18n.T("sites.deploy.timeout"))
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(deployPollEvery):
				}
				if dep, err = cl.FindDeployment(ctx, project, site.ID.String(), dep.ID.String()); err != nil {
					return err
				}
			}
			if dep.Status == "FAILED" {
				return errors.New(i18n.T("sites.deploy.failed", dep.Error))
			}
			if site, err = cl.GetSite(ctx, project, site.ID.String()); err != nil {
				return err
			}
			fmt.Fprintln(out, i18n.T("sites.deploy.ok", site.URL))
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
