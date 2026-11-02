package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
	"github.com/thekrauss/kubemanager/internal/cli/ui"
)

func printDNSRecords(p *ui.Printer, d *api.WorkloadDomainView) error {
	p.Println(i18n.T("wl.dom.records"))
	t := p.Table(i18n.T("col.dns.type"), i18n.T("col.dns.name"), i18n.T("col.dns.value"))
	for _, r := range d.Records {
		t.Row(r.Type, r.Name, r.Value)
	}
	return t.Flush()
}

// findWorkloadDomain : domaine du conteneur désigné par son nom.
func findWorkloadDomain(cmd *cobra.Command, cl *api.Client, workloadID, host string) (*api.WorkloadDomainView, error) {
	list, err := cl.ListWorkloadDomains(cmd.Context(), workloadID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if strings.EqualFold(list[i].Hostname, strings.TrimSuffix(host, ".")) {
			return &list[i], nil
		}
	}
	return nil, errors.New(i18n.T("wl.dom.not_found", host))
}

// newWorkloadsDomains : « workloads domains ».
func newWorkloadsDomains() *cobra.Command {
	c := &cobra.Command{Use: "domains", Aliases: []string{"domain"}, Short: i18n.T("wl.dom.short"),
		Example: "betorun workloads domains add web www.exemple.sn\n" +
			"betorun workloads domains verify web www.exemple.sn\n" +
			"betorun workloads domains list web"}
	c.AddCommand(&cobra.Command{
		Use: "list " + i18n.T("arg.name"), Short: i18n.T("wl.dom.list.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, w, err := workloadByName(cmd, args[0])
			if err != nil {
				return err
			}
			list, err := cl.ListWorkloadDomains(cmd.Context(), w.ID.String())
			if err != nil {
				return err
			}
			if done, err := wantJSON(cmd, list); done {
				return err
			}
			p := printer(cmd)
			if len(list) == 0 {
				p.Hint(i18n.T("wl.dom.empty", args[0]))
				return nil
			}
			t := p.Table(i18n.T("col.dom.host"), i18n.T("col.dom.status"))
			for _, d := range list {
				st := p.Status(ui.ToneProgress, i18n.T("wl.dom.st.pending"))
				if d.Verified {
					st = p.Status(ui.ToneOK, i18n.T("wl.dom.st.verified"))
				}
				t.Row(d.Hostname, st)
			}
			return t.Flush()
		},
	}, &cobra.Command{
		Use: "add " + i18n.T("arg.name") + " " + i18n.T("arg.hostname"), Short: i18n.T("wl.dom.add.short"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, w, err := workloadByName(cmd, args[0])
			if err != nil {
				return err
			}
			d, err := cl.AddWorkloadDomain(cmd.Context(), w.ID.String(), args[1])
			if err != nil {
				return err
			}
			if done, err := wantJSON(cmd, d); done {
				return err
			}
			p := printer(cmd)
			if err := printDNSRecords(p, d); err != nil {
				return err
			}
			p.Hint(i18n.T("wl.dom.then", args[0], d.Hostname))
			return nil
		},
	}, &cobra.Command{
		Use: "verify " + i18n.T("arg.name") + " " + i18n.T("arg.hostname"), Short: i18n.T("wl.dom.verify.short"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, w, err := workloadByName(cmd, args[0])
			if err != nil {
				return err
			}
			found, err := findWorkloadDomain(cmd, cl, w.ID.String(), args[1])
			if err != nil {
				return err
			}
			d, err := cl.VerifyWorkloadDomain(cmd.Context(), w.ID.String(), found.ID.String())
			if err != nil {
				return err
			}
			if done, err := wantJSON(cmd, d); done {
				return err
			}
			p := printer(cmd)
			if !d.Verified {
				p.Warn(i18n.T("wl.dom.pending", d.Hostname))
				return printDNSRecords(p, d)
			}
			p.Success(i18n.T("wl.dom.ok", d.Hostname))
			if !d.Pointing {
				p.Warn(i18n.T("wl.dom.not_pointing", d.Hostname))
			}
			return nil
		},
	}, &cobra.Command{
		Use: "remove " + i18n.T("arg.name") + " " + i18n.T("arg.hostname"), Short: i18n.T("wl.dom.remove.short"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, w, err := workloadByName(cmd, args[0])
			if err != nil {
				return err
			}
			found, err := findWorkloadDomain(cmd, cl, w.ID.String(), args[1])
			if err != nil {
				return err
			}
			if err := cl.RemoveWorkloadDomain(cmd.Context(), w.ID.String(), found.ID.String()); err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("wl.dom.removed", found.Hostname))
			return nil
		},
	})
	return c
}
