package cli

import (
	"bufio"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
)

// États d'un conteneur après un déploiement.
var (
	workloadReady = map[string]bool{statusRunning: true, phaseDeployed: true, phaseStarted: true}
	// Échecs signalés par le workflow, ou par le watcher pendant le démarrage
	// (avant : la CLI attendait 10 minutes un conteneur en CrashLoopBackOff).
	workloadFailed = map[string]bool{statusFailed: true, phaseHelmError: true, phaseImageParseError: true, phaseSecretCreateError: true,
		statusCrashLoop: true, statusImagePullFailed: true, statusConfigError: true, statusOOMKilled: true}

	workloadPollEvery = 3 * time.Second
	workloadTimeout   = 10 * time.Minute
)

func newWorkloads() *cobra.Command {
	c := &cobra.Command{Use: "workloads", Aliases: []string{"wl", "containers"}, Short: i18n.T("wl.short"),
		Example: "# " + i18n.T("ex.wl.deploy") + "\n" +
			"betorun workloads deploy --name web --image nginx:1.27 --port 80\n" +
			"# " + i18n.T("ex.wl.follow") + "\n" +
			"betorun workloads logs web --follow"}
	c.AddCommand(newWorkloadsList(), newWorkloadsDeploy(), newWorkloadsUpdate(), newWorkloadsLogs(), newWorkloadsStatus(),
		newWorkloadAction("restart"), newWorkloadAction("stop"), newWorkloadAction("start"), newWorkloadsDelete(),
		newWorkloadsEnv(), newWorkloadsSecrets(), newWorkloadsDomains(), newWorkloadsGit())
	return c
}

// workloadContext : client, projet et conteneur désigné par son nom.
func workloadByName(cmd *cobra.Command, name string) (*api.Client, *api.Workload, error) {
	s, err := settings()
	if err != nil {
		return nil, nil, err
	}
	project, err := requireProject(s)
	if err != nil {
		return nil, nil, err
	}
	cl := client(s)
	list, err := cl.ListWorkloads(cmd.Context(), project)
	if err != nil {
		return nil, nil, err
	}
	for i := range list {
		if list[i].Name == name {
			return cl, &list[i], nil
		}
	}
	return nil, nil, errors.New(i18n.T("wl.not_found", name))
}

func newWorkloadsList() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: i18n.T("wl.list.short"),
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
			list, err := client(s).ListWorkloads(cmd.Context(), project)
			if err != nil {
				return err
			}
			if done, err := wantJSON(cmd, list); done {
				return err
			}
			p := printer(cmd)
			if len(list) == 0 {
				p.Hint(i18n.T("wl.list.empty"))
				return nil
			}
			t := p.Table(i18n.T("wl.col.name"), i18n.T("wl.col.status"), i18n.T("wl.col.replicas"), i18n.T("wl.col.image"), i18n.T("wl.col.url"))
			for _, w := range list {
				url := ""
				if w.ExternalURL != "" {
					url = p.Link(w.ExternalURL)
				}
				t.Row(p.Bold(w.Name), workloadStatus(p, w.Status), fmt.Sprint(w.Replicas), w.Image, url)
			}
			return t.Flush()
		},
	}
}

func newWorkloadsDeploy() *cobra.Command {
	var (
		req    api.CreateWorkloadRequest
		env    []string
		noWait bool
		src    buildSource
		secret []string
	)
	c := &cobra.Command{
		Use:   "deploy",
		Short: i18n.T("wl.deploy.short"),
		Example: "# " + i18n.T("ex.wl.from_source") + "\n" +
			"betorun workloads deploy --name api --source .\n" +
			"# " + i18n.T("ex.wl.from_git") + "\n" +
			"betorun workloads deploy --name api --git https://github.com/acme/api --ref main\n" +
			"# " + i18n.T("ex.wl.from_binary") + "\n" +
			"betorun workloads deploy --name api --binary ./api --port 8080\n" +
			"# " + i18n.T("ex.wl.from_image") + "\n" +
			"betorun workloads deploy --name web --image nginx:1.27 --port 80\n" +
			"betorun workloads deploy -n api -i ghcr.io/acme/api:1.4 -e DATABASE_URL=postgres://… --tls\n" +
			"betorun workloads deploy -n worker -i acme/worker:2 --replicas 2 --cpu 500m --memory 512Mi",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if req.Name == "" || src.count(req.Image) != 1 {
				return errors.New(i18n.T("wl.deploy.need"))
			}
			vars, err := parseEnv(env)
			if err != nil {
				return err
			}
			s, err := settings()
			if err != nil {
				return err
			}
			project, err := requireProject(s)
			if err != nil {
				return err
			}
			req.ProjectID, req.EnvVars = project, vars
			if req.SecretData, err = secretsFromEnv(secret); err != nil {
				return err
			}
			// Compte transmis tel que tapé : l'API accepte son identifiant
			// (api-runner), son UUID ou serviceAccount:<UUID>.
			ctx, cl := cmd.Context(), client(s)
			if req.Image == "" {
				built, err := runBuild(cmd, cl, project, req.Name, src)
				if err != nil {
					return err
				}
				req.Image = built.Image
			}
			printer(cmd).Title(i18n.T("wl.deploy.sending", req.Name, req.Image), "")
			res, err := cl.CreateWorkload(ctx, req)
			if err != nil {
				return err
			}
			if noWait {
				printer(cmd).Success(i18n.T("wl.deploy.started", res.WorkloadID))
				return nil
			}
			return waitWorkload(cmd, cl, res.WorkloadID, req.Name)
		},
	}
	f := c.Flags()
	f.StringVarP(&req.Name, "name", "n", "", i18n.T("flag.wl.name"))
	f.StringVarP(&req.Image, "image", "i", "", i18n.T("flag.wl.image"))
	src.addFlags(f)
	f.IntVar(&req.TargetPort, "port", defaultTargetPort, i18n.T("flag.wl.port"))
	f.IntVar(&req.Replicas, "replicas", 1, i18n.T("flag.wl.replicas"))
	f.StringVar(&req.CPULimit, "cpu", defaultCPULimit, i18n.T("flag.wl.cpu"))
	f.StringVar(&req.MemoryLimit, "memory", defaultMemoryLimit, i18n.T("flag.wl.memory"))
	f.StringArrayVarP(&env, "env", "e", nil, i18n.T("flag.wl.env"))
	f.StringArrayVar(&secret, "secret", nil, i18n.T("flag.wl.secret"))
	f.StringVar(&req.ServiceAccount, "service-account", "", i18n.T("flag.wl.sa"))
	f.BoolVar(&req.TLSEnabled, "tls", false, i18n.T("flag.wl.tls"))
	f.BoolVar(&noWait, "no-wait", false, i18n.T("flag.no_wait"))
	return c
}

// waitWorkload : suit les étapes jusqu'au démarrage (ou l'échec) du conteneur.
func waitWorkload(cmd *cobra.Command, cl *api.Client, id, name string) error {
	ctx, p := cmd.Context(), printer(cmd)
	p.Hint(i18n.T("wl.deploy.waiting"))
	steps := p.Steps()
	deadline := time.Now().Add(workloadTimeout)
	for {
		w, err := cl.GetWorkload(ctx, id)
		if err != nil {
			steps.Failed()
			return err
		}
		if w.CurrentPhase != "" && !workloadReady[w.Status] {
			steps.Start(phaseLabel(w.CurrentPhase))
		}
		switch {
		case workloadReady[w.Status]:
			steps.Done()
			if w.ExternalURL != "" {
				p.Success(i18n.T("wl.deploy.ok_url", p.Link(w.ExternalURL)))
			} else {
				p.Success(i18n.T("wl.deploy.ok"))
			}
			return nil
		case workloadFailed[w.Status]:
			steps.Failed()
			return errors.New(i18n.T("wl.deploy.failed", w.Status, w.CurrentPhase, name))
		case time.Now().After(deadline):
			steps.Failed()
			return errors.New(i18n.T("wl.deploy.timeout", name))
		}
		select {
		case <-ctx.Done():
			steps.Failed()
			return ctx.Err()
		case <-time.After(workloadPollEvery):
		}
	}
}

// newWorkloadsUpdate : nouvelle version d'un conteneur existant (image,
// variables) ; le reste de sa configuration est conservé.
func newWorkloadsUpdate() *cobra.Command {
	var image string
	var env []string
	var noWait bool
	var src buildSource
	var secret []string
	c := &cobra.Command{
		Use: "update " + i18n.T("arg.name"),
		Example: "betorun workloads update api --source .  # " + i18n.T("ex.wl.update_source") + "\n" +
			"betorun workloads update web --image nginx:1.28  # " + i18n.T("ex.wl.update_image") + "\n" +
			"betorun workloads update api -e LOG_LEVEL=debug -e PORT=8080  # " + i18n.T("ex.wl.update_env"),
		Short: i18n.T("wl.update.short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if src.count(image) > 1 || (src.count(image) == 0 && len(env) == 0 && len(secret) == 0) {
				return errors.New(i18n.T("wl.update.need"))
			}
			var req api.UpdateWorkloadRequest
			req.Image = image
			if len(env) > 0 {
				vars, err := parseEnv(env)
				if err != nil {
					return err
				}
				req.EnvVars = vars // remplace les variables ; les secrets restent
			}
			var err error
			if req.SecretData, err = secretsFromEnv(secret); err != nil {
				return err
			}
			cl, w, err := workloadByName(cmd, args[0])
			if err != nil {
				return err
			}
			if image == "" && !src.empty() {
				built, err := runBuild(cmd, cl, w.ProjectID.String(), w.Name, src)
				if err != nil {
					return err
				}
				req.Image = built.Image
			}
			printer(cmd).Title(i18n.T("wl.update.sending", w.Name), req.Image)
			if err := cl.UpdateWorkload(cmd.Context(), w.ID.String(), req); err != nil {
				return err
			}
			if noWait {
				printer(cmd).Success(i18n.T("wl.deploy.started", w.ID))
				return nil
			}
			return waitWorkload(cmd, cl, w.ID.String(), w.Name)
		},
	}
	f := c.Flags()
	f.StringVarP(&image, "image", "i", "", i18n.T("flag.wl.image"))
	src.addFlags(f)
	f.StringArrayVarP(&env, "env", "e", nil, i18n.T("flag.wl.env_replace"))
	f.StringArrayVar(&secret, "secret", nil, i18n.T("flag.wl.secret"))
	f.BoolVar(&noWait, "no-wait", false, i18n.T("flag.no_wait"))
	return c
}

func parseEnv(list []string) (map[string]string, error) {
	out := map[string]string{}
	for _, kv := range list {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, errors.New(i18n.T("wl.env.invalid", kv))
		}
		out[strings.TrimSpace(k)] = v
	}
	return out, nil
}

func newWorkloadsLogs() *cobra.Command {
	var o api.LogsOptions
	var since time.Duration
	c := &cobra.Command{
		Use: "logs " + i18n.T("arg.name"),
		Example: "betorun workloads logs web --follow\n" +
			"betorun workloads logs web --since 15m --timestamps\n" +
			"betorun workloads logs web --previous  # " + i18n.T("ex.wl.previous"),
		Short: i18n.T("wl.logs.short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, w, err := workloadByName(cmd, args[0])
			if err != nil {
				return err
			}
			o.Since = int64(since.Seconds())
			return cl.StreamLogs(cmd.Context(), w.ID.String(), o, cmd.OutOrStdout())
		},
	}
	f := c.Flags()
	f.BoolVarP(&o.Follow, "follow", "f", false, i18n.T("flag.logs.follow"))
	f.Int64Var(&o.Tail, "tail", defaultLogTail, i18n.T("flag.logs.tail"))
	f.DurationVar(&since, "since", 0, i18n.T("flag.logs.since"))
	f.BoolVar(&o.Previous, "previous", false, i18n.T("flag.logs.previous"))
	f.BoolVar(&o.Timestamps, "timestamps", false, i18n.T("flag.logs.timestamps"))
	return c
}

func newWorkloadsStatus() *cobra.Command {
	return &cobra.Command{
		Use:     "status " + i18n.T("arg.name"),
		Example: "betorun workloads status web\nbetorun workloads status web --json",
		Short:   i18n.T("wl.status.short"),
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, w, err := workloadByName(cmd, args[0])
			if err != nil {
				return err
			}
			if done, err := wantJSON(cmd, w); done {
				return err
			}
			p := printer(cmd)
			identity, url := "", ""
			if w.ServiceAccountID != nil {
				identity = "serviceAccount:" + w.ServiceAccountID.String()
			}
			if w.ExternalURL != "" {
				url = p.Link(w.ExternalURL)
			}
			p.Fields(
				[2]string{i18n.T("wl.col.name"), p.Bold(w.Name)},
				[2]string{i18n.T("wl.col.status"), workloadStatus(p, w.Status) + " " + p.Dim("("+phaseLabel(w.CurrentPhase)+")")},
				[2]string{i18n.T("wl.col.image"), w.Image},
				[2]string{i18n.T("wl.col.replicas"), fmt.Sprint(w.Replicas)},
				[2]string{i18n.T("wl.col.url"), url},
				[2]string{i18n.T("wl.col.identity"), identity},
			)
			return nil
		},
	}
}

// newWorkloadAction : restart, stop, start.
func newWorkloadAction(action string) *cobra.Command {
	return &cobra.Command{
		Use:   action + " " + i18n.T("arg.name"),
		Short: i18n.T("wl." + action + ".short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, w, err := workloadByName(cmd, args[0])
			if err != nil {
				return err
			}
			if err := cl.WorkloadAction(cmd.Context(), w.ID.String(), action); err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("wl."+action+".ok", w.Name))
			return nil
		},
	}
}

func newWorkloadsDelete() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "delete " + i18n.T("arg.name"),
		Short: i18n.T("wl.delete.short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, w, err := workloadByName(cmd, args[0])
			if err != nil {
				return err
			}
			if !yes && !confirm(cmd, i18n.T("wl.delete.confirm", w.Name), w.Name) {
				return errors.New(i18n.T("wl.delete.aborted"))
			}
			if err := cl.DeleteWorkload(cmd.Context(), w.ID.String()); err != nil {
				return err
			}
			printer(cmd).Success(i18n.T("wl.delete.ok", w.Name))
			return nil
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, i18n.T("flag.yes"))
	return c
}

// confirm : l'utilisateur retape expected (action irréversible).
func confirm(cmd *cobra.Command, prompt, expected string) bool {
	fmt.Fprint(cmd.OutOrStdout(), prompt+" ")
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	return strings.TrimSpace(line) == expected
}
