package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
	"github.com/thekrauss/kubemanager/internal/cli/ui"
)

// Gestion des identités : groupes (organisation), rôles personnalisés et
// comptes de service (projet). Partout, une ressource se désigne par le nom
// choisi à sa création (equipe-paiement, api-runner, ciDeployer) ; les
// identifiants techniques restent acceptés.

// iamCtx : client, projet courant et (à la demande) son organisation.
type iamCtx struct {
	cl      *api.Client
	project string
	org     string
}

func newIAMCtx(cmd *cobra.Command) (*iamCtx, error) {
	s, err := settings()
	if err != nil {
		return nil, err
	}
	project, err := requireProject(s)
	if err != nil {
		return nil, err
	}
	return &iamCtx{cl: client(s), project: project}, nil
}

// organization : organisation du projet courant (groupes, rôles d'organisation).
func (x *iamCtx) organization(ctx context.Context) (string, error) {
	if x.org != "" {
		return x.org, nil
	}
	ancestors, err := x.cl.Ancestry(ctx, prefixProjects+x.project)
	if err != nil {
		return "", err
	}
	for _, a := range ancestors {
		if id, ok := strings.CutPrefix(a, prefixOrganizations); ok {
			x.org = id
			return id, nil
		}
	}
	return "", errors.New(i18n.T("iam.no_org"))
}

// group : groupe de l'organisation désigné par son nom, son UUID ou group:<UUID>.
func (x *iamCtx) group(ctx context.Context, ref string) (*api.Group, error) {
	org, err := x.organization(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := x.cl.ListGroups(ctx, org)
	if err != nil {
		return nil, err
	}
	ref = strings.TrimPrefix(strings.TrimSpace(ref), principalGroup)
	for i := range groups {
		if groups[i].GroupID == ref || groups[i].ID == ref {
			return &groups[i], nil
		}
	}
	return nil, errors.New(i18n.T("iam.group.not_found", ref))
}

// serviceAccount : compte du projet désigné par son identifiant de compte,
// son UUID ou serviceAccount:<UUID>.
func (x *iamCtx) serviceAccount(ctx context.Context, ref string) (*api.ServiceAccount, error) {
	list, err := x.cl.ListServiceAccounts(ctx, x.project)
	if err != nil {
		return nil, err
	}
	ref = strings.TrimPrefix(strings.TrimSpace(ref), principalServiceAccount)
	for i := range list {
		if list[i].AccountID == ref || list[i].ID == ref {
			return &list[i], nil
		}
	}
	return nil, errors.New(i18n.T("iam.sa.not_found", ref))
}

// member : principal à partir de ce que l'utilisateur tape :
//
//	awa@ex.sn                  → user:awa@ex.sn (résolu par l'API)
//	group:equipe-paiement      → group:<UUID>
//	serviceAccount:api-runner  → serviceAccount:<UUID>  (aussi sa:api-runner)
func (x *iamCtx) member(ctx context.Context, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	kind, name, ok := strings.Cut(raw, ":")
	switch {
	case !ok:
		return memberRef(raw), nil
	case kind == kindGroup:
		if _, err := uuid.Parse(name); err == nil {
			return raw, nil
		}
		g, err := x.group(ctx, name)
		if err != nil {
			return "", err
		}
		return g.Principal, nil
	case kind == kindServiceAccount || kind == kindServiceAccountShort:
		if _, err := uuid.Parse(name); err == nil {
			return principalServiceAccount + name, nil
		}
		sa, err := x.serviceAccount(ctx, name)
		if err != nil {
			return "", err
		}
		return sa.Principal, nil
	}
	return raw, nil
}

// role : rôle prédéfini (viewer → roles/viewer), sinon rôle personnalisé du
// projet puis de l'organisation (ciDeployer → projects/<id>/roles/ciDeployer) ;
// un nom complet est pris tel quel.
func (x *iamCtx) role(ctx context.Context, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "/") {
		return raw, nil
	}
	// Liste indisponible : le nom part tel quel, l'API le valide.
	predefined, _ := x.cl.ListRoles(ctx)
	for _, r := range predefined {
		if r.Name == prefixRoles+raw {
			return r.Name, nil
		}
	}
	if r, err := x.customRole(ctx, raw); err == nil {
		return r.Name, nil
	}
	return roleRef(raw), nil // l'API répondra « rôle inconnu »
}

// customRole : rôle personnalisé du projet, sinon de l'organisation.
func (x *iamCtx) customRole(ctx context.Context, raw string) (*api.CustomRole, error) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, segmentCustomRoles) {
		return x.cl.GetCustomRole(ctx, raw)
	}
	parents := []string{prefixProjects + x.project}
	if org, err := x.organization(ctx); err == nil {
		parents = append(parents, prefixOrganizations+org)
	}
	for _, parent := range parents {
		roles, err := x.cl.ListCustomRoles(ctx, parent)
		if err != nil {
			continue // pas le droit de lister à ce niveau : on essaie le suivant
		}
		for i := range roles {
			if roles[i].RoleID == raw {
				return &roles[i], nil
			}
		}
	}
	return nil, errors.New(i18n.T("iam.role.not_found", raw))
}

// newTable : tableau mis en forme (en-têtes, couleurs) ; les lignes s'y
// écrivent comme dans un tabwriter (colonnes séparées par des tabulations).
func newTable(cmd *cobra.Command, headers ...string) *rowWriter {
	return &rowWriter{t: printer(cmd).Table(headers...)}
}

type rowWriter struct {
	t   *ui.Table
	buf strings.Builder
}

func (r *rowWriter) Write(b []byte) (int, error) {
	r.buf.Write(b)
	for {
		s := r.buf.String()
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			return len(b), nil
		}
		r.t.Row(strings.Split(s[:i], "\t")...)
		r.buf.Reset()
		r.buf.WriteString(s[i+1:])
	}
}

func (r *rowWriter) Flush() error { return r.t.Flush() }

// ── Groupes ──────────────────────────────────────────────────────────────

func newGroups() *cobra.Command {
	c := &cobra.Command{Use: "groups", Aliases: []string{"group"}, Short: i18n.T("grp.short")}
	c.AddCommand(newGroupsList(), newGroupsCreate(), newGroupsDelete(), newGroupsMembers(),
		newGroupsAddMember(), newGroupsRemoveMember())
	return c
}

func newGroupsList() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: i18n.T("grp.list.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			org, err := x.organization(cmd.Context())
			if err != nil {
				return err
			}
			groups, err := x.cl.ListGroups(cmd.Context(), org)
			if err != nil {
				return err
			}
			if len(groups) == 0 {
				printer(cmd).Hint(i18n.T("grp.list.empty"))
				return nil
			}
			tw := newTable(cmd, i18n.T("col.group"), i18n.T("col.display_name"), i18n.T("col.members"))
			for _, g := range groups {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", g.GroupID, g.DisplayName, i18n.T("grp.members_count", g.MemberCount))
			}
			return tw.Flush()
		},
	}
}

func newGroupsCreate() *cobra.Command {
	var name, description string
	c := &cobra.Command{
		Use: "create " + i18n.T("arg.group"), Short: i18n.T("grp.create.short"), Args: cobra.ExactArgs(1),
		Example: "  betorun iam groups create equipe-paiement --name \"Équipe paiement\"",
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			org, err := x.organization(cmd.Context())
			if err != nil {
				return err
			}
			g, err := x.cl.CreateGroup(cmd.Context(), org, args[0], firstNonEmpty(name, args[0]), description)
			if err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("grp.create.ok", g.GroupID))
			printer(cmd).Hint(i18n.T("grp.create.next", g.GroupID))
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", i18n.T("flag.display_name"))
	c.Flags().StringVar(&description, "description", "", i18n.T("flag.description"))
	return c
}

func newGroupsDelete() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use: "delete " + i18n.T("arg.group"), Short: i18n.T("grp.delete.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			g, err := x.group(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !yes && !confirm(cmd, i18n.T("grp.delete.confirm", g.GroupID), g.GroupID) {
				return errors.New(i18n.T("op.aborted"))
			}
			if err := x.cl.DeleteGroup(cmd.Context(), x.org, g.ID); err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("grp.delete.ok", g.GroupID))
			return nil
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, i18n.T("flag.yes"))
	return c
}

func newGroupsMembers() *cobra.Command {
	return &cobra.Command{
		Use: "members " + i18n.T("arg.group"), Short: i18n.T("grp.members.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			g, err := x.group(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			members, err := x.cl.ListGroupMembers(cmd.Context(), x.org, g.ID)
			if err != nil {
				return err
			}
			if len(members) == 0 {
				printer(cmd).Hint(i18n.T("grp.members.empty", g.GroupID))
				return nil
			}
			tw := newTable(cmd, i18n.T("col.member"), i18n.T("col.identity"))
			for _, m := range members {
				fmt.Fprintf(tw, "%s\t%s\n", firstNonEmpty(m.Email, m.DisplayName, m.Principal), m.Principal)
			}
			return tw.Flush()
		},
	}
}

func newGroupsAddMember() *cobra.Command {
	return &cobra.Command{
		Use: "add-member " + i18n.T("arg.group") + " " + i18n.T("arg.member"), Aliases: []string{"add"},
		Short: i18n.T("grp.add.short"), Args: cobra.ExactArgs(2),
		Example: "  betorun iam groups add-member equipe-paiement moussa@ex.sn\n" +
			"  betorun iam groups add-member equipe-paiement serviceAccount:api-runner",
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			g, err := x.group(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			member, err := x.member(cmd.Context(), args[1])
			if err != nil {
				return err
			}
			if _, err := x.cl.AddGroupMember(cmd.Context(), x.org, g.ID, member); err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("grp.add.ok", args[1], g.GroupID))
			return nil
		},
	}
}

func newGroupsRemoveMember() *cobra.Command {
	return &cobra.Command{
		Use: "remove-member " + i18n.T("arg.group") + " " + i18n.T("arg.member"), Aliases: []string{"remove"},
		Short: i18n.T("grp.remove.short"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			g, err := x.group(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			member, err := x.member(cmd.Context(), args[1])
			if err != nil {
				return err
			}
			if err := x.cl.RemoveGroupMember(cmd.Context(), x.org, g.ID, member); err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("grp.remove.ok", args[1], g.GroupID))
			return nil
		},
	}
}

// ── Rôles ────────────────────────────────────────────────────────────────

// newRoles : sans sous-commande, liste les rôles attribuables (prédéfinis et
// personnalisés du projet et de son organisation).
func newRoles() *cobra.Command {
	c := &cobra.Command{
		Use: "roles", Short: i18n.T("acc.roles.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := settings()
			if err != nil {
				return err
			}
			cl := client(s)
			roles, err := cl.ListRoles(cmd.Context())
			if err != nil {
				return err
			}
			tw := newTable(cmd, i18n.T("col.role"), i18n.T("col.description"))
			for _, r := range roles {
				fmt.Fprintf(tw, "%s\t%s\n", strings.TrimPrefix(r.Name, prefixRoles), r.Description)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if s.ProjectID == "" {
				return nil
			}
			x := &iamCtx{cl: cl, project: s.ProjectID}
			parents := []string{prefixProjects + s.ProjectID}
			if org, err := x.organization(cmd.Context()); err == nil {
				parents = append(parents, prefixOrganizations+org)
			}
			var custom []api.CustomRole
			for _, parent := range parents {
				if list, err := cl.ListCustomRoles(cmd.Context(), parent); err == nil {
					custom = append(custom, list...)
				}
			}
			if len(custom) == 0 {
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "\n"+i18n.T("roles.custom_header"))
			tw = newTable(cmd, i18n.T("col.role"), i18n.T("col.title"), i18n.T("col.scope"))
			for _, r := range custom {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", r.RoleID, r.Title, scopeOf(r.Parent))
			}
			return tw.Flush()
		},
	}
	c.AddCommand(newRolesDescribe(), newRolesCreate(), newRolesUpdate(), newRolesDelete())
	return c
}

func scopeOf(parent string) string {
	if strings.HasPrefix(parent, prefixOrganizations) {
		return i18n.T("roles.scope.org")
	}
	return i18n.T("roles.scope.project")
}

// splitList : --permission a,b --permission c → [a b c].
func splitList(values []string) []string {
	var out []string
	for _, v := range values {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func newRolesDescribe() *cobra.Command {
	return &cobra.Command{
		Use: "describe " + i18n.T("arg.role"), Short: i18n.T("roles.describe.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			r, err := x.customRole(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			tw := newTable(cmd)
			fmt.Fprintf(tw, "%s\t%s\n", i18n.T("roles.col.name"), r.Name)
			fmt.Fprintf(tw, "%s\t%s\n", i18n.T("roles.col.title"), r.Title)
			if r.Description != "" {
				fmt.Fprintf(tw, "%s\t%s\n", i18n.T("roles.col.description"), r.Description)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			fmt.Fprintln(out, i18n.T("roles.col.permissions"))
			for _, p := range r.Permissions {
				fmt.Fprintln(out, "  "+p)
			}
			return nil
		},
	}
}

func newRolesCreate() *cobra.Command {
	var in api.CustomRoleInput
	var perms []string
	var onOrg bool
	c := &cobra.Command{
		Use: "create " + i18n.T("arg.role_id"), Short: i18n.T("roles.create.short"), Args: cobra.ExactArgs(1),
		Example: "  betorun iam roles create ciDeployer --title \"Déploiement CI\" \\\n" +
			"      --permission workloads.workloads.update,sites.deployments.create\n" +
			"  betorun iam permissions --filter workloads  # " + i18n.T("ex.perms.available"),
		RunE: func(cmd *cobra.Command, args []string) error {
			in.Permissions = splitList(perms)
			if len(in.Permissions) == 0 {
				return errors.New(i18n.T("roles.create.need"))
			}
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			parent := prefixProjects + x.project
			if onOrg {
				org, err := x.organization(cmd.Context())
				if err != nil {
					return err
				}
				parent = prefixOrganizations + org
			}
			in.Title = firstNonEmpty(in.Title, args[0])
			r, err := x.cl.CreateCustomRole(cmd.Context(), parent, args[0], in)
			if err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("roles.create.ok", r.RoleID, len(r.Permissions)))
			printer(cmd).Hint(i18n.T("roles.create.next", r.RoleID))
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&in.Title, "title", "", i18n.T("flag.role.title"))
	f.StringVar(&in.Description, "description", "", i18n.T("flag.description"))
	f.StringArrayVar(&perms, "permission", nil, i18n.T("flag.role.permission"))
	f.BoolVar(&onOrg, "org", false, i18n.T("flag.role.org"))
	return c
}

func newRolesUpdate() *cobra.Command {
	var title, description string
	var add, remove []string
	c := &cobra.Command{
		Use: "update " + i18n.T("arg.role"), Short: i18n.T("roles.update.short"), Args: cobra.ExactArgs(1),
		Example: "  betorun iam roles update ciDeployer --add-permission workloads.workloads.restart",
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			r, err := x.customRole(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			set := map[string]bool{}
			for _, p := range r.Permissions {
				set[p] = true
			}
			for _, p := range splitList(add) {
				set[p] = true
			}
			for _, p := range splitList(remove) {
				delete(set, p)
			}
			in := api.CustomRoleInput{Title: firstNonEmpty(title, r.Title), Description: r.Description}
			if cmd.Flags().Changed("description") {
				in.Description = description
			}
			for p := range set {
				in.Permissions = append(in.Permissions, p)
			}
			sort.Strings(in.Permissions)
			updated, err := x.cl.UpdateCustomRole(cmd.Context(), r.Name, r.Etag, in)
			if err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("roles.update.ok", updated.RoleID, len(updated.Permissions)))
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&title, "title", "", i18n.T("flag.role.title"))
	f.StringVar(&description, "description", "", i18n.T("flag.description"))
	f.StringArrayVar(&add, "add-permission", nil, i18n.T("flag.role.add"))
	f.StringArrayVar(&remove, "remove-permission", nil, i18n.T("flag.role.remove"))
	return c
}

func newRolesDelete() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use: "delete " + i18n.T("arg.role"), Short: i18n.T("roles.delete.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			r, err := x.customRole(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !yes && !confirm(cmd, i18n.T("roles.delete.confirm", r.RoleID), r.RoleID) {
				return errors.New(i18n.T("op.aborted"))
			}
			if err := x.cl.DeleteCustomRole(cmd.Context(), r.Name); err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("roles.delete.ok", r.RoleID))
			return nil
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, i18n.T("flag.yes"))
	return c
}

func newPermissions() *cobra.Command {
	var filter string
	var all bool
	c := &cobra.Command{
		Use: "permissions", Short: i18n.T("perms.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := settings()
			if err != nil {
				return err
			}
			perms, err := client(s).ListPermissions(cmd.Context())
			if err != nil {
				return err
			}
			tw := newTable(cmd, i18n.T("col.permission"), i18n.T("col.description"))
			for _, p := range perms {
				if (!all && !p.CustomRoles) || (filter != "" && !strings.Contains(p.Name, filter)) {
					continue
				}
				fmt.Fprintf(tw, "%s\t%s\n", p.Name, p.Title)
			}
			return tw.Flush()
		},
	}
	c.Flags().StringVar(&filter, "filter", "", i18n.T("flag.perms.filter"))
	c.Flags().BoolVar(&all, "all", false, i18n.T("flag.perms.all"))
	return c
}

// ── Comptes de service ───────────────────────────────────────────────────

func newServiceAccounts() *cobra.Command {
	c := &cobra.Command{Use: "service-accounts", Aliases: []string{"sa", "service-account"}, Short: i18n.T("sa.short")}
	c.AddCommand(newSAList(), newSACreate(), newSADelete(), newSAKeys(), newSATrust())
	return c
}

func newSAList() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: i18n.T("sa.list.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			list, err := x.cl.ListServiceAccounts(cmd.Context(), x.project)
			if err != nil {
				return err
			}
			if len(list) == 0 {
				printer(cmd).Hint(i18n.T("sa.list.empty"))
				return nil
			}
			tw := newTable(cmd, i18n.T("col.account"), i18n.T("col.display_name"), i18n.T("col.identity"), i18n.T("wl.col.status"))
			for _, sa := range list {
				state := ""
				if sa.Disabled {
					state = i18n.T("sa.disabled")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", sa.AccountID, sa.DisplayName, sa.Principal, state)
			}
			return tw.Flush()
		},
	}
}

func newSACreate() *cobra.Command {
	var name, description string
	c := &cobra.Command{
		Use: "create " + i18n.T("arg.sa"), Short: i18n.T("sa.create.short"), Args: cobra.ExactArgs(1),
		Example: "  betorun iam sa create ci-deployer --name \"Déploiement CI\"",
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			sa, err := x.cl.CreateServiceAccount(cmd.Context(), x.project, args[0], firstNonEmpty(name, args[0]), description)
			if err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("sa.create.ok", sa.AccountID, sa.Principal))
			printer(cmd).Hint(i18n.T("sa.create.next", sa.AccountID))
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", i18n.T("flag.display_name"))
	c.Flags().StringVar(&description, "description", "", i18n.T("flag.description"))
	return c
}

func newSADelete() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use: "delete " + i18n.T("arg.sa"), Short: i18n.T("sa.delete.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			sa, err := x.serviceAccount(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if !yes && !confirm(cmd, i18n.T("sa.delete.confirm", sa.AccountID), sa.AccountID) {
				return errors.New(i18n.T("op.aborted"))
			}
			if err := x.cl.DeleteServiceAccount(cmd.Context(), x.project, sa.ID); err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("sa.delete.ok", sa.AccountID))
			return nil
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, i18n.T("flag.yes"))
	return c
}

func newSAKeys() *cobra.Command {
	c := &cobra.Command{Use: "keys", Short: i18n.T("sa.keys.short")}
	c.AddCommand(newSAKeysList(), newSAKeysCreate(), newSAKeysDisable())
	return c
}

func newSAKeysList() *cobra.Command {
	return &cobra.Command{
		Use: "list " + i18n.T("arg.sa"), Short: i18n.T("sa.keys.list.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			sa, err := x.serviceAccount(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			keys, err := x.cl.ListServiceAccountKeys(cmd.Context(), x.project, sa.ID)
			if err != nil {
				return err
			}
			if len(keys) == 0 {
				printer(cmd).Hint(i18n.T("sa.keys.empty", sa.AccountID))
				return nil
			}
			tw := newTable(cmd, i18n.T("col.key"), i18n.T("col.algorithm"), i18n.T("col.validity"), i18n.T("col.usage"), i18n.T("col.display_name"))
			for _, k := range keys {
				state := i18n.T("sa.keys.until", k.ValidBefore.Format("2006-01-02"))
				if k.DisabledAt != nil {
					state = i18n.T("sa.keys.disabled")
				}
				used := i18n.T("sa.keys.never_used")
				if k.LastUsedAt != nil {
					used = i18n.T("sa.keys.used", k.LastUsedAt.Format("2006-01-02"))
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", k.KeyID, k.Algorithm, state, used, k.DisplayName)
			}
			return tw.Flush()
		},
	}
}

func newSAKeysCreate() *cobra.Command {
	var output, name string
	var days int
	c := &cobra.Command{
		Use: "create " + i18n.T("arg.sa"), Short: i18n.T("sa.keys.create.short"), Args: cobra.ExactArgs(1),
		Example: "  betorun iam sa keys create ci-deployer --output ci-deployer.json\n" +
			"  BETORUN_KEY_FILE=ci-deployer.json betorun workloads list",
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			sa, err := x.serviceAccount(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if output == "" {
				output = sa.AccountID + ".json"
			}
			// Vérifié avant la création : la clé privée n'est remise qu'une fois.
			f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return errors.New(i18n.T("sa.keys.output_exists", output))
			}
			created, err := x.cl.CreateServiceAccountKey(cmd.Context(), x.project, sa.ID, name, days)
			if err == nil && created.KeyFile == nil {
				err = errors.New(i18n.T("sa.keys.no_file"))
			}
			if err != nil {
				f.Close()
				os.Remove(output)
				return err
			}
			enc := jsonIndent(created.KeyFile)
			if _, err := f.Write(enc); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("sa.keys.create.ok", created.KeyID, output, created.ValidBefore.Format("2006-01-02")))
			printer(cmd).Warn(i18n.T("sa.keys.create.warn"))
			printer(cmd).Hint(i18n.T("sa.keys.create.use", output))
			return nil
		},
	}
	f := c.Flags()
	f.StringVarP(&output, "output", "o", "", i18n.T("flag.sa.output"))
	f.StringVar(&name, "name", "", i18n.T("flag.display_name"))
	f.IntVar(&days, "expires-in-days", defaultSAKeyDays, i18n.T("flag.sa.days"))
	return c
}

func newSAKeysDisable() *cobra.Command {
	return &cobra.Command{
		Use: "disable " + i18n.T("arg.sa") + " " + i18n.T("arg.key_id"), Aliases: []string{"delete", "revoke"},
		Short: i18n.T("sa.keys.disable.short"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := newIAMCtx(cmd)
			if err != nil {
				return err
			}
			sa, err := x.serviceAccount(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := x.cl.DisableServiceAccountKey(cmd.Context(), x.project, sa.ID, args[1]); err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("sa.keys.disable.ok", args[1], sa.AccountID))
			return nil
		},
	}
}
