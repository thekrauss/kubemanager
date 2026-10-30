package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
)

// Relations de confiance OIDC : une CI (GitHub Actions, GitLab) obtient des
// jetons courts pour un compte de service, sans aucun secret stocké. La
// commande crée la relation puis affiche le bloc de configuration à coller
// dans la CI.

const (
	issuerGitHub = "https://token.actions.githubusercontent.com"
	issuerGitLab = "https://gitlab.com"
	installCmd   = "curl -fsSL https://raw.githubusercontent.com/thekrauss/kubemanager/main/scripts/install.sh | sh"
)

func jsonIndent(v any) []byte {
	b, _ := json.MarshalIndent(v, "", "  ")
	return append(b, '\n')
}

func newSATrust() *cobra.Command {
	c := &cobra.Command{Use: "trust", Short: i18n.T("trust.short")}
	c.AddCommand(newTrustList(), newTrustGitHub(), newTrustGitLab(), newTrustDelete())
	return c
}

func newTrustList() *cobra.Command {
	return &cobra.Command{
		Use: "list " + i18n.T("arg.sa"), Short: i18n.T("trust.list.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			sa, err := x.serviceAccount(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			trusts, err := x.cl.ListTrusts(cmd.Context(), x.project, sa.ID)
			if err != nil {
				return err
			}
			if len(trusts) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), i18n.T("trust.list.empty", sa.AccountID))
				return nil
			}
			tw := newTable(cmd)
			for _, t := range trusts {
				used := i18n.T("sa.keys.never_used")
				if t.LastUsedAt != nil {
					used = i18n.T("sa.keys.used", t.LastUsedAt.Format("2006-01-02"))
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.ID, t.Issuer, t.SubjectPattern, used)
			}
			return tw.Flush()
		},
	}
}

// githubSubject : sujet du jeton GitHub Actions pour la cible choisie.
func githubSubject(repo, branch, tag, environment string, pullRequests bool) (string, error) {
	if !strings.Contains(repo, "/") || strings.ContainsAny(repo, " *:") {
		return "", errors.New(i18n.T("trust.github.repo"))
	}
	set := 0
	for _, v := range []bool{branch != "", tag != "", environment != "", pullRequests} {
		if v {
			set++
		}
	}
	if set > 1 {
		return "", errors.New(i18n.T("trust.one_target"))
	}
	base := "repo:" + repo + ":"
	switch {
	case tag != "":
		return base + "ref:refs/tags/" + tag, nil
	case environment != "":
		return base + "environment:" + environment, nil
	case pullRequests:
		return base + "pull_request", nil
	}
	return base + "ref:refs/heads/" + firstNonEmpty(branch, "main"), nil
}

func newTrustGitHub() *cobra.Command {
	var repo, branch, tag, environment, ownerID string
	var pullRequests bool
	c := &cobra.Command{
		Use: "github " + i18n.T("arg.sa"), Short: i18n.T("trust.github.short"), Args: cobra.ExactArgs(1),
		Example: "  betorun iam sa trust github ci-deployer --repo sunupay/api                # branche main\n" +
			"  betorun iam sa trust github ci-deployer --repo sunupay/api --environment production\n" +
			"  betorun iam sa trust github ci-deployer --repo sunupay/api --tag 'v*'",
		RunE: func(cmd *cobra.Command, args []string) error {
			subject, err := githubSubject(repo, branch, tag, environment, pullRequests)
			if err != nil {
				return err
			}
			in := api.FederationTrustInput{DisplayName: "GitHub " + repo, Issuer: issuerGitHub, SubjectPattern: subject}
			if ownerID != "" {
				// Protège d'un dépôt recréé sous le même nom par un autre compte.
				in.Conditions = map[string]string{"repository_owner_id": ownerID}
			}
			target := ciSetup{Branch: branch, Tag: tag, Environment: environment, PullRequests: pullRequests}
			return createTrust(cmd, args[0], in, target, githubSnippet)
		},
	}
	f := c.Flags()
	f.StringVar(&repo, "repo", "", i18n.T("flag.trust.repo"))
	f.StringVar(&branch, "branch", "", i18n.T("flag.trust.branch"))
	f.StringVar(&tag, "tag", "", i18n.T("flag.trust.tag"))
	f.StringVar(&environment, "environment", "", i18n.T("flag.trust.environment"))
	f.BoolVar(&pullRequests, "pull-requests", false, i18n.T("flag.trust.pr"))
	f.StringVar(&ownerID, "owner-id", "", i18n.T("flag.trust.owner_id"))
	_ = c.MarkFlagRequired("repo")
	return c
}

// gitlabSubject : sujet du jeton GitLab CI pour la cible choisie.
func gitlabSubject(project, branch, tag string) (string, error) {
	if !strings.Contains(project, "/") || strings.ContainsAny(project, " *:") {
		return "", errors.New(i18n.T("trust.gitlab.project"))
	}
	if branch != "" && tag != "" {
		return "", errors.New(i18n.T("trust.one_target"))
	}
	base := "project_path:" + project + ":"
	if tag != "" {
		return base + "ref_type:tag:ref:" + tag, nil
	}
	return base + "ref_type:branch:ref:" + firstNonEmpty(branch, "main"), nil
}

func newTrustGitLab() *cobra.Command {
	var project, branch, tag, issuer string
	c := &cobra.Command{
		Use: "gitlab " + i18n.T("arg.sa"), Short: i18n.T("trust.gitlab.short"), Args: cobra.ExactArgs(1),
		Example: "  betorun iam sa trust gitlab ci-deployer --project sunupay/api\n" +
			"  betorun iam sa trust gitlab ci-deployer --project infra/api --issuer https://gitlab.sunupay.sn",
		RunE: func(cmd *cobra.Command, args []string) error {
			subject, err := gitlabSubject(project, branch, tag)
			if err != nil {
				return err
			}
			in := api.FederationTrustInput{DisplayName: "GitLab " + project,
				Issuer: strings.TrimRight(firstNonEmpty(issuer, issuerGitLab), "/"), SubjectPattern: subject}
			return createTrust(cmd, args[0], in, ciSetup{Branch: branch, Tag: tag}, gitlabSnippet)
		},
	}
	f := c.Flags()
	f.StringVar(&project, "project", "", i18n.T("flag.trust.gl_project"))
	f.StringVar(&branch, "branch", "", i18n.T("flag.trust.branch"))
	f.StringVar(&tag, "tag", "", i18n.T("flag.trust.tag"))
	f.StringVar(&issuer, "issuer", "", i18n.T("flag.trust.issuer"))
	_ = c.MarkFlagRequired("project")
	return c
}

// ciSetup : ce qu'il faut à la CI pour obtenir ses jetons, et la cible
// autorisée (le bloc affiché doit produire un jeton qui correspond).
type ciSetup struct {
	APIURL, Project, ServiceAccount, Audience string
	Branch, Tag, Environment                  string
	PullRequests                              bool
}

type snippetFunc func(ciSetup) string

func createTrust(cmd *cobra.Command, saRef string, in api.FederationTrustInput, target ciSetup, snippet snippetFunc) error {
	s, err := settings()
	if err != nil {
		return err
	}
	x, err := newIAMCtx(cmd)
	if err != nil {
		return err
	}
	sa, err := x.serviceAccount(cmd.Context(), saRef)
	if err != nil {
		return err
	}
	t, err := x.cl.CreateTrust(cmd.Context(), x.project, sa.ID, in)
	if err != nil {
		return err
	}
	audience, err := x.cl.STSIssuer(cmd.Context())
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, i18n.T("trust.create.ok", sa.AccountID, t.SubjectPattern))
	fmt.Fprintln(out, i18n.T("trust.create.roles", sa.AccountID))
	fmt.Fprintln(out, "\n"+i18n.T("trust.create.snippet"))
	target.APIURL, target.Project, target.ServiceAccount, target.Audience = s.APIURL, x.project, sa.Principal, audience
	fmt.Fprintln(out, snippet(target))
	return nil
}

func githubSnippet(c ciSetup) string {
	trigger := "on:\n  push:\n    branches: [" + firstNonEmpty(c.Branch, "main") + "]\n"
	switch {
	case c.Tag != "":
		trigger = "on:\n  push:\n    tags: ['" + c.Tag + "']\n"
	case c.PullRequests:
		trigger = "on:\n  pull_request:\n"
	case c.Environment != "":
		trigger = "on:\n  push:\n    branches: [main]\n"
	}
	environment := ""
	if c.Environment != "" {
		// Le jeton porte l'environnement du job : il doit être déclaré.
		environment = "    environment: " + c.Environment + "\n"
	}
	return trigger + `
permissions:
  id-token: write   # jeton OIDC du job, échangé contre un jeton betorun
  contents: read

jobs:
  deploy:
    runs-on: ubuntu-latest
` + environment + `    env:
      BETORUN_API_URL: ` + c.APIURL + `
      BETORUN_PROJECT: ` + c.Project + `
      BETORUN_SERVICE_ACCOUNT: ` + c.ServiceAccount + `
    steps:
      - uses: actions/checkout@v4
      - run: ` + installCmd + `
      - run: betorun workloads update api --image ghcr.io/${{ github.repository }}:${{ github.sha }}`
}

func gitlabSnippet(c ciSetup) string {
	rule := `$CI_COMMIT_BRANCH == "` + firstNonEmpty(c.Branch, "main") + `"`
	if c.Tag != "" {
		rule = `$CI_COMMIT_TAG == "` + c.Tag + `"`
	}
	return `deploy:
  rules:
    - if: '` + rule + `'
  id_tokens:
    BETORUN_ID_TOKEN:
      aud: ` + c.Audience + `
  variables:
    BETORUN_API_URL: ` + c.APIURL + `
    BETORUN_PROJECT: ` + c.Project + `
    BETORUN_SERVICE_ACCOUNT: ` + c.ServiceAccount + `
  script:
    - ` + installCmd + `
    - betorun workloads update api --image $CI_REGISTRY_IMAGE:$CI_COMMIT_SHA`
}

func newTrustDelete() *cobra.Command {
	return &cobra.Command{
		Use: "delete " + i18n.T("arg.sa") + " " + i18n.T("arg.trust_id"), Short: i18n.T("trust.delete.short"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			sa, err := x.serviceAccount(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := x.cl.DeleteTrust(cmd.Context(), x.project, sa.ID, args[1]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), i18n.T("trust.delete.ok", args[1]))
			return nil
		},
	}
}
