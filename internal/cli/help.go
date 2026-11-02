package cli

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/thekrauss/kubemanager/internal/cli/i18n"
	"github.com/thekrauss/kubemanager/internal/cli/ui"
)

// Aide de la CLI : traduite, commandes rangées par usage, exemples commentés,
// couleurs dans un terminal (texte simple ailleurs). Les erreurs de saisie
// (commande inconnue, mauvais arguments, option inconnue) sont traduites et
// renvoient vers l'aide de la commande.

// Groupes de commandes de premier niveau.
const (
	groupStart  = "start"
	groupDeploy = "deploy"
	groupAccess = "access"
	groupOther  = "other"
)

// suggestDistance : écart de frappe toléré pour suggérer une commande.
const suggestDistance = 2

// docsPath : documentation de l'API, servie (ou redirigée vers la console)
// par l'API elle-même.
const docsPath = "/docs"

// setupHelp : aide et messages d'erreur de saisie pour root et toutes ses
// sous-commandes ; à appeler une fois les commandes ajoutées.
func setupHelp(root *cobra.Command) {
	root.AddGroup(
		&cobra.Group{ID: groupStart, Title: i18n.T("help.group.start")},
		&cobra.Group{ID: groupDeploy, Title: i18n.T("help.group.deploy")},
		&cobra.Group{ID: groupAccess, Title: i18n.T("help.group.access")},
		&cobra.Group{ID: groupOther, Title: i18n.T("help.group.other")},
	)
	root.SetHelpCommand(newHelpCommand())
	root.SetHelpCommandGroupID(groupOther)
	root.SetCompletionCommandGroupID(groupOther)
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	localizeCompletion(root)

	root.SetHelpFunc(func(c *cobra.Command, _ []string) { renderHelp(c) })
	root.SetUsageFunc(func(c *cobra.Command) error { renderHelp(c); return nil })
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return usageError(c, translateFlagError(err))
	})
	walk(root, func(c *cobra.Command) {
		c.SuggestionsMinimumDistance = suggestDistance
		c.InitDefaultHelpFlag()
		if f := c.Flags().Lookup("help"); f != nil {
			f.Usage = i18n.T("flag.help")
		}
		switch {
		case c.HasSubCommands() && !c.Runnable():
			// Groupe de commandes : seul, il affiche son aide ; suivi d'un
			// mot inconnu, il le signale avec des suggestions.
			c.Args = func(c *cobra.Command, args []string) error {
				if len(args) > 0 {
					return unknownCommand(c, args[0])
				}
				return nil
			}
			c.RunE = func(c *cobra.Command, _ []string) error { return c.Help() }
		case c.Args != nil:
			check := c.Args
			c.Args = func(c *cobra.Command, args []string) error {
				if check(c, args) != nil {
					return usageError(c, i18n.T("help.err.args", useLine(ui.Plain(io.Discard), c)))
				}
				return nil
			}
		}
	})
}

func walk(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, sub := range c.Commands() {
		walk(sub, fn)
	}
}

// newHelpCommand : « betorun help [commande] ».
func newHelpCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "help " + i18n.T("arg.command"),
		Short: i18n.T("help.short"),
		ValidArgsFunction: func(c *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			target, _, err := c.Root().Find(args)
			if err != nil || target == nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			var names []string
			for _, sub := range target.Commands() {
				if sub.IsAvailableCommand() {
					names = append(names, sub.Name())
				}
			}
			return names, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(c *cobra.Command, args []string) error {
			target, rest, err := c.Root().Find(args)
			if err != nil || target == nil || len(rest) > 0 {
				word := strings.Join(args, " ")
				if len(rest) > 0 {
					word = rest[0]
				}
				parent := c.Root()
				if target != nil {
					parent = target
				}
				return unknownCommand(parent, word)
			}
			return target.Help()
		},
	}
}

// localizeCompletion : la commande d'autocomplétion de cobra, traduite.
func localizeCompletion(root *cobra.Command) {
	activate := map[string]string{
		"bash":       "source <(%s completion bash)",
		"zsh":        "source <(%s completion zsh)",
		"fish":       "%s completion fish | source",
		"powershell": "%s completion powershell | Out-String | Invoke-Expression",
	}
	for _, c := range root.Commands() {
		if c.Name() != "completion" {
			continue
		}
		c.Short, c.Long = i18n.T("completion.short"), i18n.T("completion.long")
		for _, sh := range c.Commands() {
			sh.Short, sh.Long = i18n.T("completion.shell.short", sh.Name()), ""
			if a, ok := activate[sh.Name()]; ok {
				sh.Example = "# " + i18n.T("completion.shell.example") + "\n" + fmt.Sprintf(a, root.Name())
			}
			if f := sh.Flags().Lookup("no-descriptions"); f != nil {
				f.Usage = i18n.T("completion.flag.no_desc")
			}
		}
	}
}

// renderHelp : aide d'une commande.
func renderHelp(c *cobra.Command) {
	p := ui.New(c.OutOrStdout())
	section := func(title string) { p.Println("\n" + p.Bold(strings.ToUpper(title))) }

	if c.HasParent() {
		p.Println(p.Bold(c.CommandPath()) + "  " + p.Dim(c.Short))
	} else {
		p.Println(p.Brand(p.Bold(c.Name())) + "  " + c.Short)
	}
	if long := strings.TrimSpace(c.Long); long != "" {
		p.Println()
		for _, line := range strings.Split(long, "\n") {
			if cmd, comment, ok := strings.Cut(line, "  # "); ok {
				line = cmd + "  " + p.Dim("# "+comment)
			}
			p.Println(indent(line, "  "))
		}
	}

	section(i18n.T("help.usage"))
	if c.Runnable() && !c.HasAvailableSubCommands() {
		p.Println("  " + useLine(p, c))
	}
	if c.HasAvailableSubCommands() {
		p.Println("  " + c.CommandPath() + " " + p.Cyan(i18n.T("arg.command")) + " " + p.Dim(i18n.T("help.options_arg")))
	}
	if len(c.Aliases) > 0 {
		p.Println("  " + p.Dim(i18n.T("help.aliases")+" : "+strings.Join(c.Aliases, ", ")))
	}

	if c.HasAvailableSubCommands() {
		renderCommands(p, c, section)
	}

	if c.Example != "" {
		section(i18n.T("help.examples"))
		renderExamples(p, c.Example)
	}

	if rows := flagRows(c.LocalFlags()); len(rows) > 0 {
		section(i18n.T("help.flags"))
		renderRows(p, rows)
	}
	if rows := flagRows(c.InheritedFlags()); len(rows) > 0 {
		section(i18n.T("help.global_flags"))
		renderRows(p, rows)
	}

	p.Println()
	switch {
	case c.HasAvailableSubCommands():
		p.Hint(i18n.T("help.more", c.CommandPath()))
	case c.HasParent():
		p.Hint(i18n.T("help.parent", c.Parent().CommandPath()))
	}
	if !c.HasParent() {
		if docs := docsURL(); docs != "" {
			p.Hint(i18n.T("help.docs", docs))
		}
	}
}

// useLine : « betorun workloads logs <nom> [options] ».
func useLine(p *ui.Printer, c *cobra.Command) string {
	line := c.CommandPath()
	if _, args, ok := strings.Cut(c.Use, " "); ok {
		line += " " + p.Cyan(args)
	}
	if c.HasAvailableFlags() {
		line += " " + p.Dim(i18n.T("help.options_arg"))
	}
	return line
}

// renderCommands : sous-commandes, par groupe s'il y en a.
func renderCommands(p *ui.Printer, c *cobra.Command, section func(string)) {
	byGroup := map[string][][2]string{}
	for _, sub := range c.Commands() {
		if !sub.IsAvailableCommand() && sub.Name() != "help" {
			continue
		}
		name := sub.Name()
		if len(sub.Aliases) > 0 {
			name += p.Dim(" (" + strings.Join(sub.Aliases, ", ") + ")")
		}
		byGroup[sub.GroupID] = append(byGroup[sub.GroupID], [2]string{p.Cyan(name), sub.Short})
	}
	if len(c.Groups()) == 0 {
		section(i18n.T("help.commands"))
		renderRows(p, byGroup[""])
		return
	}
	for _, g := range c.Groups() {
		if rows := byGroup[g.ID]; len(rows) > 0 {
			section(g.Title)
			renderRows(p, rows)
		}
	}
	if rows := byGroup[""]; len(rows) > 0 {
		section(i18n.T("help.group.other"))
		renderRows(p, rows)
	}
}

// renderExamples : commentaires (« # … ») atténués, commandes telles quelles.
func renderExamples(p *ui.Printer, example string) {
	for _, line := range strings.Split(strings.TrimRight(example, "\n"), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			p.Println()
		case strings.HasPrefix(line, "#"):
			p.Println("  " + p.Dim(line))
		default:
			cmd, comment, ok := strings.Cut(line, "  # ")
			if ok {
				p.Println("  " + cmd + "  " + p.Dim("# "+comment))
			} else {
				p.Println("  " + line)
			}
		}
	}
}

// flagRows : options visibles (hors --help), triées.
func flagRows(fs *pflag.FlagSet) [][2]string {
	var rows [][2]string
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		varname, usage := pflag.UnquoteUsage(f)
		name := "--" + f.Name
		if f.Shorthand != "" {
			name = "-" + f.Shorthand + ", " + name
		} else {
			name = "    " + name
		}
		if varname != "" {
			name += " " + typeName(varname)
		}
		if d := defaultValue(f); d != "" {
			usage += " (" + i18n.T("help.default", d) + ")"
		}
		rows = append(rows, [2]string{name, usage})
	})
	sort.SliceStable(rows, func(i, j int) bool {
		return strings.TrimLeft(rows[i][0], " -") < strings.TrimLeft(rows[j][0], " -")
	})
	return rows
}

// typeName : type de valeur d'une option, traduit s'il est connu.
func typeName(varname string) string {
	if t := i18n.T("type." + varname); t != "type."+varname {
		return t
	}
	return varname
}

func defaultValue(f *pflag.Flag) string {
	switch f.DefValue {
	case "", "false", "0", "0s", "[]":
		return ""
	}
	return f.DefValue
}

// renderRows : deux colonnes alignées sur la largeur affichée.
func renderRows(p *ui.Printer, rows [][2]string) {
	width := 0
	for _, r := range rows {
		width = max(width, ui.Width(r[0]))
	}
	for _, r := range rows {
		p.Println("  " + r[0] + strings.Repeat(" ", width-ui.Width(r[0])+3) + r[1])
	}
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

// docsURL : documentation de l'API (vide si l'API n'est pas connue).
func docsURL() string {
	s, err := settings()
	if err != nil {
		return ""
	}
	u, err := url.Parse(s.APIURL)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + docsPath
}

// unknownCommand : mot inconnu après c, avec les commandes proches.
func unknownCommand(c *cobra.Command, word string) error {
	msg := i18n.T("help.err.unknown", word, c.CommandPath())
	if s := c.SuggestionsFor(word); len(s) > 0 {
		msg += "\n  " + i18n.T("help.err.suggest", strings.Join(s, ", "))
	}
	return usageError(c, msg)
}

// usageError : message, puis où trouver l'aide de la commande.
func usageError(c *cobra.Command, msg string) error {
	return errors.New(msg + "\n  " + i18n.T("help.err.see", c.CommandPath()))
}

// translateFlagError : erreurs d'options de pflag, traduites.
func translateFlagError(err error) string {
	msg := err.Error()
	for _, t := range []struct{ prefix, key string }{
		{"unknown flag: ", "help.err.unknown_flag"},
		{"unknown shorthand flag: ", "help.err.unknown_flag"},
		{"flag needs an argument: ", "help.err.flag_value"},
		{"invalid argument ", "help.err.flag_invalid"},
	} {
		if rest, ok := strings.CutPrefix(msg, t.prefix); ok {
			return i18n.T(t.key, rest)
		}
	}
	return msg
}
