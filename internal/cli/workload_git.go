package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
	"github.com/thekrauss/kubemanager/internal/cli/ui"
	githubdomain "github.com/thekrauss/kubemanager/internal/modules/github/domain"
)

const shortCommit = 7

func shortSHA(sha string) string {
	if len(sha) > shortCommit {
		return sha[:shortCommit]
	}
	return sha
}

func gitAccessLabel(reason string) string {
	switch reason {
	case "":
		return i18n.T("wl.git.access.ok")
	case githubdomain.DisabledInstallationDeleted:
		return i18n.T("wl.git.access.deleted")
	case githubdomain.DisabledInstallationSuspended:
		return i18n.T("wl.git.access.suspended")
	case githubdomain.DisabledRepositoryRemoved:
		return i18n.T("wl.git.access.removed")
	}
	return reason
}

func onOff(v bool) string {
	if v {
		return i18n.T("wl.git.on")
	}
	return i18n.T("wl.git.off")
}

func printGitLink(p *ui.Printer, name string, v *api.GitLinkView) {
	if !v.Enabled {
		p.Hint(i18n.T("wl.git.disabled"))
		return
	}
	if !v.Linked || v.Link == nil {
		p.Hint(i18n.T("wl.git.none", name))
		return
	}
	fields := [][2]string{
		{i18n.T("wl.git.col.repo"), p.Bold(v.Link.RepoFullName)},
		{i18n.T("wl.git.col.branch"), v.Link.Branch},
		{i18n.T("wl.git.col.auto"), onOff(v.Link.AutoDeploy)},
		{i18n.T("wl.git.col.access"), gitAccessLabel(v.Link.DisabledReason)},
		{i18n.T("wl.git.col.deployed"), shortSHA(v.DeployedCommit)},
	}
	if b := v.LastBuild; b != nil {
		fields = append(fields, [2]string{i18n.T("wl.git.col.last"), fmt.Sprintf("%s %s", buildStatus(p, b.Status), shortSHA(b.GitCommit))})
	}
	p.Fields(fields...)
}

// newWorkloadsGit : « workloads git ». La connexion à GitHub se fait dans la
// console : elle passe par le navigateur.
func newWorkloadsGit() *cobra.Command {
	c := &cobra.Command{Use: "git", Short: i18n.T("wl.git.short"),
		Example: "betorun workloads git status web\nbetorun workloads git deploy web"}
	c.AddCommand(&cobra.Command{
		Use: "status " + i18n.T("arg.name"), Short: i18n.T("wl.git.status.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, w, err := workloadByName(cmd, args[0])
			if err != nil {
				return err
			}
			v, err := cl.GetGitLink(cmd.Context(), w.ID.String())
			if err != nil {
				return err
			}
			if done, err := wantJSON(cmd, v); done {
				return err
			}
			printGitLink(printer(cmd), args[0], v)
			return nil
		},
	})
	c.AddCommand(&cobra.Command{
		Use: "deploy " + i18n.T("arg.name"), Short: i18n.T("wl.git.deploy.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, w, err := workloadByName(cmd, args[0])
			if err != nil {
				return err
			}
			b, err := cl.DeployGitLink(cmd.Context(), w.ID.String())
			if err != nil {
				return err
			}
			if done, err := wantJSON(cmd, b); done {
				return err
			}
			p := printer(cmd)
			p.Success(i18n.T("wl.git.deploy.ok", args[0], shortSHA(b.GitCommit)))
			s, err := settings()
			if err != nil {
				return err
			}
			project, err := requireProject(s)
			if err != nil {
				return err
			}
			done, err := waitBuild(cmd, cl, project, b.ID.String())
			if err != nil {
				return err
			}
			if done.Status != "SUCCEEDED" {
				return errors.New(buildFailure(done))
			}
			p.Hint(i18n.T("wl.git.deploy.next", args[0]))
			return nil
		},
	})
	return c
}
