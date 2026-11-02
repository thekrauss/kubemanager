package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/archive"
	"github.com/thekrauss/kubemanager/internal/cli/config"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
	"github.com/thekrauss/kubemanager/internal/cli/ui"
	buildsdomain "github.com/thekrauss/kubemanager/internal/modules/builds/domain"
)

// buildPollEvery : suivi de l'état une fois le journal terminé (réduit en test).
var buildPollEvery = 2 * time.Second

// buildSource : d'où vient le code d'un build (options communes à
// « builds create », « workloads deploy » et « workloads update »).
type buildSource struct {
	dir, git, binary, ref, contextDir, dockerfile string
}

func (b *buildSource) addFlags(f *pflag.FlagSet) {
	f.StringVar(&b.dir, "source", "", i18n.T("flag.build.source"))
	f.StringVar(&b.git, "git", "", i18n.T("flag.build.git"))
	f.StringVar(&b.binary, "binary", "", i18n.T("flag.build.binary"))
	f.StringVar(&b.ref, "ref", "", i18n.T("flag.build.ref"))
	f.StringVar(&b.contextDir, "context-dir", "", i18n.T("flag.build.context"))
	f.StringVar(&b.dockerfile, "dockerfile", "", i18n.T("flag.build.dockerfile"))
}

func (b buildSource) empty() bool { return b.dir == "" && b.git == "" && b.binary == "" }

// count : origines précisées, image comprise (une seule attendue).
func (b buildSource) count(image string) int {
	n := 0
	for _, v := range []string{b.dir, b.git, b.binary, image} {
		if v != "" {
			n++
		}
	}
	return n
}

// runBuild : lance le build, affiche son journal et attend le résultat.
func runBuild(cmd *cobra.Command, cl *api.Client, project, name string, src buildSource) (*api.Build, error) {
	ctx, p := cmd.Context(), printer(cmd)
	p.Title(i18n.T("builds.title", name), firstNonEmpty(src.git, src.binary, src.dir))
	build, err := startBuild(ctx, cl, p, project, name, src)
	if err != nil {
		return nil, err
	}
	var logs io.Writer = cmd.OutOrStdout()
	if flagJSON {
		logs = cmd.ErrOrStderr() // la sortie standard reste du JSON valide
	}
	if err := cl.StreamBuildLogs(ctx, project, build.ID.String(), true, logs); err != nil {
		return nil, err
	}
	return waitBuild(cmd, cl, project, build.ID.String())
}

func startBuild(ctx context.Context, cl *api.Client, p *ui.Printer, project, name string, src buildSource) (*api.Build, error) {
	if src.git != "" {
		return cl.CreateGitBuild(ctx, project, api.CreateGitBuildRequest{
			Name: name, GitURL: src.git, GitRef: src.ref, ContextDir: src.contextDir, Dockerfile: src.dockerfile,
		})
	}
	if src.binary != "" {
		return uploadBinary(ctx, cl, p, project, name, src.binary)
	}
	// Sans Dockerfile, le type de projet est détecté au build. Un chemin
	// explicite doit exister : c'est une erreur de saisie.
	dockerfile := firstNonEmpty(src.dockerfile, buildsdomain.DefaultDockerfile)
	if src.dockerfile != "" {
		if _, err := os.Stat(filepath.Join(src.dir, filepath.FromSlash(dockerfile))); err != nil {
			return nil, errors.New(i18n.T("builds.no_dockerfile", dockerfile, src.dir))
		}
	}
	source, err := archive.TarGz(src.dir)
	if err != nil {
		return nil, err
	}
	defer os.Remove(source.Path)
	p.Hint("  " + i18n.T("builds.packed", source.Files, archive.HumanSize(source.Size)))
	if len(source.Sensitive) > 0 {
		p.Warn(i18n.T("builds.sensitive", strings.Join(source.Sensitive, ", ")))
	}
	f, err := os.Open(source.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return cl.UploadBuild(ctx, project, name, dockerfile, f)
}

// uploadBinary : le binaire est emballé dans une image minimale (voir archive).
func uploadBinary(ctx context.Context, cl *api.Client, p *ui.Printer, project, name, path string) (*api.Build, error) {
	source, err := archive.BinaryTarGz(path)
	if err != nil {
		switch {
		case errors.Is(err, archive.ErrNotLinuxBinary):
			return nil, errors.New(i18n.T("builds.bin.not_linux", path))
		case errors.Is(err, archive.ErrWrongArch):
			return nil, errors.New(i18n.T("builds.bin.arch", path, strings.TrimPrefix(err.Error(), archive.ErrWrongArch.Error()+": ")))
		}
		return nil, err
	}
	defer os.Remove(source.Path)
	p.Hint("  " + archive.HumanSize(source.Size))
	f, err := os.Open(source.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return cl.UploadBuild(ctx, project, name, buildsdomain.DefaultDockerfile, f)
}

// waitBuild : le journal s'arrête à la fin du pod ; l'état final suit de peu.
// Sur Ctrl-C, le build continue côté serveur : on dit comment l'arrêter.
func waitBuild(cmd *cobra.Command, cl *api.Client, project, id string) (*api.Build, error) {
	ctx, p := cmd.Context(), printer(cmd)
	for {
		if ctx.Err() != nil {
			p.Hint(i18n.T("builds.background", id))
			return nil, ctx.Err()
		}
		b, err := cl.GetBuild(ctx, project, id)
		if err != nil {
			return nil, err
		}
		if b.Finished() {
			if b.Status == buildsdomain.StatusSucceeded {
				if b.Detected != "" {
					p.Info(i18n.T("builds.detected", b.Detected))
				}
				p.Success(i18n.T("builds.ok", b.Image))
				return b, nil
			}
			return nil, errors.New(buildFailure(b))
		}
		select {
		case <-ctx.Done():
		case <-time.After(buildPollEvery):
		}
	}
}

func buildFailure(b *api.Build) string {
	id := b.ID.String()
	switch {
	case b.Status == buildsdomain.StatusCancelled:
		return i18n.T("builds.failed.cancelled", id)
	case b.Failure == buildsdomain.FailureBuild:
		return i18n.T("builds.failed.build", id)
	case b.Failure == buildsdomain.FailureTimeout:
		return i18n.T("builds.failed.timeout", id)
	case b.Failure == buildsdomain.FailureDisk:
		return i18n.T("builds.failed.disk", id)
	case b.Failure == buildsdomain.FailureNoPlan:
		return i18n.T("builds.failed.no_plan")
	case b.Failure == buildsdomain.FailureGitAccess:
		return i18n.T("builds.failed.git_access")
	case b.Failure == buildsdomain.FailureTooMany:
		return i18n.T("builds.failed.too_many")
	}
	return i18n.T("builds.failed.internal", id)
}

func buildStatus(p *ui.Printer, status string) string {
	switch status {
	case buildsdomain.StatusQueued:
		return p.Status(ui.ToneProgress, i18n.T("st.queued"))
	case buildsdomain.StatusBuilding:
		return p.Status(ui.ToneProgress, i18n.T("st.building"))
	case buildsdomain.StatusSucceeded:
		return p.Status(ui.ToneOK, i18n.T("st.succeeded"))
	case buildsdomain.StatusFailed:
		return p.Status(ui.ToneError, i18n.T("st.build_failed"))
	case buildsdomain.StatusCancelled:
		return p.Status(ui.ToneNeutral, i18n.T("st.cancelled"))
	}
	return p.Status(ui.ToneNeutral, status)
}

// projectClient : client et projet courant.
func projectClient() (*api.Client, string, config.Settings, error) {
	s, err := settings()
	if err != nil {
		return nil, "", s, err
	}
	project, err := requireProject(s)
	if err != nil {
		return nil, "", s, err
	}
	return client(s), project, s, nil
}

func newBuilds() *cobra.Command {
	c := &cobra.Command{Use: "builds", Aliases: []string{"build"}, Short: i18n.T("builds.short"),
		Example: "betorun builds create --name api --source .\n" +
			"betorun builds list\n" +
			"betorun builds logs <id> --follow"}
	c.AddCommand(newBuildsList(), newBuildsCreate(), newBuildsLogs(), newBuildsCancel())
	return c
}

func newBuildsList() *cobra.Command {
	var name string
	c := &cobra.Command{
		Use: "list", Short: i18n.T("builds.list.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, project, _, err := projectClient()
			if err != nil {
				return err
			}
			builds, err := cl.ListBuilds(cmd.Context(), project, name)
			if err != nil {
				return err
			}
			if done, err := wantJSON(cmd, builds); done {
				return err
			}
			p := printer(cmd)
			if len(builds) == 0 {
				p.Hint(i18n.T("builds.list.empty"))
				return nil
			}
			t := p.Table(i18n.T("col.build.id"), i18n.T("col.build.name"), i18n.T("col.build.status"), i18n.T("col.build.source"), i18n.T("col.build.created"))
			for _, b := range builds {
				t.Row(b.ID.String(), b.Name, buildStatus(p, b.Status), firstNonEmpty(b.GitURL, b.SourceType), b.CreatedAt.Local().Format("2006-01-02 15:04"))
			}
			return t.Flush()
		},
	}
	c.Flags().StringVar(&name, "name", "", i18n.T("flag.build.filter"))
	return c
}

func newBuildsCreate() *cobra.Command {
	var name string
	var src buildSource
	c := &cobra.Command{
		Use: "create", Short: i18n.T("builds.create.short"), Args: cobra.NoArgs,
		Example: "betorun builds create --name api --source .\n" +
			"betorun builds create --name api --source . --dockerfile docker/Dockerfile.prod\n" +
			"betorun builds create --name api --git https://github.com/acme/api --ref v1.2 --context-dir services/api",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" || src.count("") != 1 {
				return errors.New(i18n.T("builds.create.need2"))
			}
			cl, project, _, err := projectClient()
			if err != nil {
				return err
			}
			b, err := runBuild(cmd, cl, project, name, src)
			if err != nil {
				return err
			}
			if flagJSON {
				return writeJSON(cmd.OutOrStdout(), b)
			}
			return nil
		},
	}
	c.Flags().StringVarP(&name, "name", "n", "", i18n.T("flag.build.name"))
	src.addFlags(c.Flags())
	return c
}

func newBuildsLogs() *cobra.Command {
	var follow bool
	c := &cobra.Command{
		Use: "logs " + i18n.T("arg.build_id"), Short: i18n.T("builds.logs.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, project, _, err := projectClient()
			if err != nil {
				return err
			}
			return cl.StreamBuildLogs(cmd.Context(), project, args[0], follow, cmd.OutOrStdout())
		},
	}
	c.Flags().BoolVarP(&follow, "follow", "f", false, i18n.T("flag.build.follow"))
	return c
}

func newBuildsCancel() *cobra.Command {
	return &cobra.Command{
		Use: "cancel " + i18n.T("arg.build_id"), Short: i18n.T("builds.cancel.short"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, project, _, err := projectClient()
			if err != nil {
				return err
			}
			if err := cl.CancelBuild(cmd.Context(), project, args[0]); err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("builds.cancelled"))
			return nil
		},
	}
}
