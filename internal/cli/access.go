package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
)

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func newAccess() *cobra.Command {
	c := &cobra.Command{Use: "access", Aliases: []string{"iam"}, Short: i18n.T("acc.short")}
	c.AddCommand(newAccessList(), newAccessGrant(), newAccessRevoke(), newRoles(), newPermissions(),
		newGroups(), newServiceAccounts())
	return c
}

func projectResource() (*api.Client, string, error) {
	s, err := settings()
	if err != nil {
		return nil, "", err
	}
	project, err := requireProject(s)
	if err != nil {
		return nil, "", err
	}
	return client(s), "projects/" + project, nil
}

// memberRef : "awa@ex.sn" → "user:awa@ex.sn" ; principals complets inchangés.
func memberRef(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, ":") && strings.Contains(raw, "@") {
		return "user:" + raw
	}
	return raw
}

// roleRef : "viewer" → "roles/viewer" ; rôles personnalisés inchangés.
func roleRef(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "/") {
		return "roles/" + raw
	}
	return raw
}

// roleLabel : nom court d'un rôle pour l'affichage (roles/viewer → viewer,
// projects/<id>/roles/ciDeployer → ciDeployer, organizations/<id>/roles/x → x (organisation)).
func roleLabel(name string) string {
	if short, ok := strings.CutPrefix(name, "roles/"); ok {
		return short
	}
	if i := strings.Index(name, "/roles/"); i >= 0 {
		short := name[i+len("/roles/"):]
		if strings.HasPrefix(name, "organizations/") {
			return short + " (" + i18n.T("roles.scope.org") + ")"
		}
		return short
	}
	return name
}

// resolvePrincipal : principal de la policy qui correspond à ref (email compris).
func resolvePrincipal(view *api.PolicyView, ref string) string {
	email := strings.TrimPrefix(ref, "user:")
	for _, m := range view.Members {
		if strings.EqualFold(m.Email, email) {
			return m.Principal
		}
	}
	return ref
}

func newAccessList() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: i18n.T("acc.list.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, resource, err := projectResource()
			if err != nil {
				return err
			}
			view, err := cl.GetPolicy(cmd.Context(), resource)
			if err != nil {
				return err
			}
			names := map[string]string{}
			for _, m := range view.Members {
				names[m.Principal] = firstNonEmpty(m.Email, m.DisplayName)
			}
			type row struct{ member, role, until string }
			var rows []row
			for _, b := range view.Policy.Bindings {
				until := ""
				if len(b.Condition) > 0 {
					var c struct {
						Title string `json:"title"`
					}
					_ = json.Unmarshal(b.Condition, &c)
					until = c.Title
				}
				for _, m := range b.Members {
					label := m
					if n := names[m]; n != "" {
						label = n + " (" + m + ")"
					}
					rows = append(rows, row{label, roleLabel(b.Role), until})
				}
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].member < rows[j].member })
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			for _, r := range rows {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", r.member, r.role, r.until)
			}
			return tw.Flush()
		},
	}
}

func newAccessGrant() *cobra.Command {
	var until string
	c := &cobra.Command{
		Use:   "grant " + i18n.T("arg.member") + " " + i18n.T("arg.role"),
		Short: i18n.T("acc.grant.short"),
		Example: "  betorun iam grant awa@ex.sn developer\n" +
			"  betorun iam grant group:equipe-paiement viewer --until 2026-12-31\n" +
			"  betorun iam grant serviceAccount:ci-deployer ciDeployer",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if until != "" && !dateRe.MatchString(until) {
				return errors.New(i18n.T("acc.until.invalid"))
			}
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			cl, resource := x.cl, "projects/"+x.project
			member, err := x.member(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			role, err := x.role(cmd.Context(), args[1])
			if err != nil {
				return err
			}
			var condition json.RawMessage
			if until != "" {
				condition, _ = json.Marshal(map[string]string{
					"title":      i18n.T("acc.until.title", until),
					"expression": "request.time < timestamp('" + until + "T23:59:59Z')",
				})
			}
			_, err = cl.UpdatePolicy(cmd.Context(), resource, func(v *api.PolicyView) error {
				member = resolvePrincipal(v, member)
				for i, b := range v.Policy.Bindings {
					if b.Role == role && string(b.Condition) == string(condition) {
						for _, m := range b.Members {
							if m == member {
								return nil // déjà accordé
							}
						}
						v.Policy.Bindings[i].Members = append(b.Members, member)
						return nil
					}
				}
				v.Policy.Bindings = append(v.Policy.Bindings, api.PolicyBinding{Role: role, Members: []string{member}, Condition: condition})
				return nil
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), i18n.T("acc.grant.ok", roleLabel(role), args[0]))
			return nil
		},
	}
	c.Flags().StringVar(&until, "until", "", i18n.T("flag.until"))
	return c
}

func newAccessRevoke() *cobra.Command {
	var role string
	c := &cobra.Command{
		Use:   "revoke " + i18n.T("arg.member"),
		Short: i18n.T("acc.revoke.short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			cl, resource := x.cl, "projects/"+x.project
			member, err := x.member(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if role != "" {
				if role, err = x.role(cmd.Context(), role); err != nil {
					return err
				}
			}
			removed := 0
			_, err = cl.UpdatePolicy(cmd.Context(), resource, func(v *api.PolicyView) error {
				member = resolvePrincipal(v, member)
				removed = 0
				var kept []api.PolicyBinding
				for _, b := range v.Policy.Bindings {
					if role == "" || b.Role == role {
						var members []string
						for _, m := range b.Members {
							if m == member {
								removed++
								continue
							}
							members = append(members, m)
						}
						b.Members = members
					}
					if len(b.Members) > 0 {
						kept = append(kept, b)
					}
				}
				if removed == 0 {
					return errors.New(i18n.T("acc.revoke.none", args[0]))
				}
				v.Policy.Bindings = kept
				return nil
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), i18n.T("acc.revoke.ok", args[0], removed))
			return nil
		},
	}
	c.Flags().StringVar(&role, "role", "", i18n.T("flag.role"))
	return c
}
