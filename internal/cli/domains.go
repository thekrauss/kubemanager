package cli

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
)

func newDomains() *cobra.Command {
	var siteName string
	c := &cobra.Command{Use: "domains", Short: i18n.T("dom.short")}
	c.PersistentFlags().StringVarP(&siteName, "site", "s", "", i18n.T("flag.site"))

	// siteFor : client, projet et site désigné par --site.
	siteFor := func(cmd *cobra.Command) (*api.Client, string, *api.Site, error) {
		if siteName == "" {
			return nil, "", nil, errors.New(i18n.T("sites.deploy.need_site"))
		}
		s, err := settings()
		if err != nil {
			return nil, "", nil, err
		}
		project, err := requireProject(s)
		if err != nil {
			return nil, "", nil, err
		}
		cl := client(s)
		site, err := findSite(cmd, cl, project, siteName)
		if err != nil {
			return nil, "", nil, err
		}
		if site == nil {
			return nil, "", nil, errors.New(i18n.T("sites.not_found", siteName))
		}
		return cl, project, site, nil
	}
	// domainFor : domaine du site désigné par son nom d'hôte.
	domainFor := func(cmd *cobra.Command, host string) (*api.Client, string, *api.Site, *api.Domain, error) {
		cl, project, site, err := siteFor(cmd)
		if err != nil {
			return nil, "", nil, nil, err
		}
		list, err := cl.ListDomains(cmd.Context(), project, site.ID.String())
		if err != nil {
			return nil, "", nil, nil, err
		}
		for i := range list {
			if strings.EqualFold(list[i].Hostname, host) {
				return cl, project, site, &list[i], nil
			}
		}
		return nil, "", nil, nil, errors.New(i18n.T("dom.not_found", host))
	}

	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: i18n.T("dom.list.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, project, site, err := siteFor(cmd)
			if err != nil {
				return err
			}
			list, err := cl.ListDomains(cmd.Context(), project, site.ID.String())
			if err != nil {
				return err
			}
			if len(list) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), i18n.T("dom.list.empty"))
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			for _, d := range list {
				state := i18n.T("dom.pending")
				if d.Verified {
					state = i18n.T("dom.verified")
				}
				fmt.Fprintf(tw, "%s\t%s\tCNAME → %s\n", d.Hostname, state, d.CNAMETarget)
			}
			return tw.Flush()
		},
	}, &cobra.Command{
		Use:   "add " + i18n.T("arg.host"),
		Short: i18n.T("dom.add.short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, project, site, err := siteFor(cmd)
			if err != nil {
				return err
			}
			d, err := cl.AddDomain(cmd.Context(), project, site.ID.String(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), i18n.T("dom.add.ok", d.Hostname, d.Hostname, d.CNAMETarget, siteName, d.Hostname))
			return nil
		},
	}, &cobra.Command{
		Use:   "verify " + i18n.T("arg.host"),
		Short: i18n.T("dom.verify.short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, project, site, d, err := domainFor(cmd, args[0])
			if err != nil {
				return err
			}
			if d, err = cl.VerifyDomain(cmd.Context(), project, site.ID.String(), d.ID.String()); err != nil {
				return err
			}
			if !d.Verified {
				return errors.New(i18n.T("dom.verify.pending", d.Hostname, d.CNAMETarget))
			}
			fmt.Fprintln(cmd.OutOrStdout(), i18n.T("dom.verify.ok", d.Hostname))
			return nil
		},
	}, &cobra.Command{
		Use:   "remove " + i18n.T("arg.host"),
		Short: i18n.T("dom.remove.short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, project, site, d, err := domainFor(cmd, args[0])
			if err != nil {
				return err
			}
			if err := cl.DeleteDomain(cmd.Context(), project, site.ID.String(), d.ID.String()); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), i18n.T("dom.remove.ok", d.Hostname))
			return nil
		},
	})
	return c
}
