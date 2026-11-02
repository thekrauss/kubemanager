package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
)

// Fichier de projet : ce qu'il faut pour redéployer, versionné avec le code.
// Il ne contient volontairement ni URL d'API (un dépôt cloné ne doit pas
// pouvoir détourner vos identifiants) ni secret : les variables peuvent
// référencer l'environnement avec ${NOM}.

// projectFileName : fichier lu par défaut.
const projectFileName = "betorun.yaml"

// dockerfileExposeRe : port indiqué par le Dockerfile, repris par « init ».
var dockerfileExposeRe = regexp.MustCompile(`(?im)^\s*EXPOSE\s+(\d+)`)

var envRefRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// invalidNameChars : remplacés pour déduire un nom du dossier.
var invalidNameChars = regexp.MustCompile(`[^a-z0-9-]+`)

type projectFile struct {
	Name           string            `yaml:"name"`
	Project        string            `yaml:"project,omitempty"`
	Image          string            `yaml:"image,omitempty"`
	Build          *fileBuild        `yaml:"build,omitempty"`
	Git            *fileGit          `yaml:"git,omitempty"`
	Port           int               `yaml:"port,omitempty"`
	Replicas       int               `yaml:"replicas,omitempty"`
	CPU            string            `yaml:"cpu,omitempty"`
	Memory         string            `yaml:"memory,omitempty"`
	TLS            *bool             `yaml:"tls,omitempty"`
	ServiceAccount string            `yaml:"service_account,omitempty"`
	Env            map[string]string `yaml:"env,omitempty"`
	// Secrets : noms ; chaque valeur est lue dans l'environnement au déploiement.
	Secrets []string `yaml:"secrets,omitempty"`
}

type fileBuild struct {
	Context    string `yaml:"context,omitempty"`
	Binary     string `yaml:"binary,omitempty"`
	Dockerfile string `yaml:"dockerfile,omitempty"`
}

type fileGit struct {
	URL        string `yaml:"url"`
	Ref        string `yaml:"ref,omitempty"`
	ContextDir string `yaml:"context_dir,omitempty"`
	Dockerfile string `yaml:"dockerfile,omitempty"`
}

// loadProjectFile : lecture stricte (une faute de frappe est une erreur, pas
// une option silencieusement ignorée) et validation.
func loadProjectFile(path string) (*projectFile, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New(i18n.T("file.missing", path))
		}
		return nil, err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var pf projectFile
	if err := dec.Decode(&pf); err != nil {
		return nil, fmt.Errorf("%s : %w", path, err)
	}
	if pf.Name == "" {
		return nil, errors.New(i18n.T("file.need_name", path))
	}
	origins := 0
	if pf.Image != "" {
		origins++
	}
	if pf.Build != nil {
		origins++
	}
	if pf.Git != nil {
		origins++
	}
	if origins != 1 {
		return nil, errors.New(i18n.T("file.need_origin", path))
	}
	for k, v := range pf.Env {
		expanded, err := expandEnv(v)
		if err != nil {
			return nil, err
		}
		pf.Env[k] = expanded
	}
	return &pf, nil
}

// expandEnv : ${NOM} remplacé par la variable d'environnement. Une variable
// absente est une erreur : déployer avec une valeur vide serait pire.
func expandEnv(v string) (string, error) {
	var missing error
	out := envRefRe.ReplaceAllStringFunc(v, func(ref string) string {
		name := envRefRe.FindStringSubmatch(ref)[1]
		val, ok := os.LookupEnv(name)
		if !ok && missing == nil {
			missing = errors.New(i18n.T("file.env_missing", name))
		}
		return val
	})
	return out, missing
}

func (pf *projectFile) source(base string) buildSource {
	switch {
	case pf.Build != nil && pf.Build.Binary != "":
		return buildSource{binary: filepath.Join(base, pf.Build.Binary)}
	case pf.Build != nil:
		return buildSource{dir: filepath.Join(base, firstNonEmpty(pf.Build.Context, ".")), dockerfile: pf.Build.Dockerfile}
	case pf.Git != nil:
		return buildSource{git: pf.Git.URL, ref: pf.Git.Ref, contextDir: pf.Git.ContextDir, dockerfile: pf.Git.Dockerfile}
	}
	return buildSource{}
}

// newDeploy : déploie ce que décrit le fichier, en création ou en mise à jour.
func newDeploy() *cobra.Command {
	var file string
	var noWait bool
	c := &cobra.Command{
		Use: "deploy", Short: i18n.T("deploy.short"), Args: cobra.NoArgs,
		Example: "betorun init    # " + i18n.T("ex.deploy.init") + "\n" +
			"betorun deploy  # " + i18n.T("ex.deploy.run") + "\n" +
			"betorun deploy -f deploy/staging.yaml",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pf, err := loadProjectFile(file)
			if err != nil {
				return err
			}
			// Priorité : --project, puis environnement, puis fichier, puis login.
			if flagProject == "" && os.Getenv("BETORUN_PROJECT") == "" {
				flagProject = pf.Project
			}
			secrets, err := secretsFromEnv(pf.Secrets)
			if err != nil {
				return err
			}
			cl, project, _, err := projectClient()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			workloads, err := cl.ListWorkloads(ctx, project)
			if err != nil {
				return err
			}
			image := pf.Image
			if src := pf.source(filepath.Dir(file)); !src.empty() {
				built, err := runBuild(cmd, cl, project, pf.Name, src)
				if err != nil {
					return err
				}
				image = built.Image
			}
			for i := range workloads {
				if workloads[i].Name != pf.Name {
					continue
				}
				id := workloads[i].ID.String()
				req := api.UpdateWorkloadRequest{Image: image, EnvVars: pf.Env, SecretData: secrets, Replicas: pf.Replicas, TargetPort: pf.Port,
					CPULimit: pf.CPU, MemoryLimit: pf.Memory, TLSEnabled: pf.TLS}
				printer(cmd).Title(i18n.T("wl.update.sending", pf.Name), image)
				if err := cl.UpdateWorkload(ctx, id, req); err != nil {
					return err
				}
				return finishDeploy(cmd, cl, id, pf.Name, noWait)
			}
			req := api.CreateWorkloadRequest{ProjectID: project, Name: pf.Name, Image: image, EnvVars: pf.Env, SecretData: secrets, Replicas: firstPositive(pf.Replicas, 1),
				TargetPort: firstPositive(pf.Port, defaultTargetPort), CPULimit: firstNonEmpty(pf.CPU, defaultCPULimit),
				MemoryLimit: firstNonEmpty(pf.Memory, defaultMemoryLimit), ServiceAccount: pf.ServiceAccount}
			if pf.TLS != nil {
				req.TLSEnabled = *pf.TLS
			}
			printer(cmd).Title(i18n.T("wl.deploy.sending", pf.Name, image), "")
			res, err := cl.CreateWorkload(ctx, req)
			if err != nil {
				return err
			}
			return finishDeploy(cmd, cl, res.WorkloadID, pf.Name, noWait)
		},
	}
	c.Flags().StringVarP(&file, "file", "f", projectFileName, i18n.T("flag.deploy.file"))
	c.Flags().BoolVar(&noWait, "no-wait", false, i18n.T("flag.no_wait"))
	return c
}

func finishDeploy(cmd *cobra.Command, cl *api.Client, id, name string, noWait bool) error {
	if noWait {
		printer(cmd).Success(i18n.T("wl.deploy.started", id))
		return nil
	}
	return waitWorkload(cmd, cl, id, name)
}

func firstPositive(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// newInit : écrit un betorun.yaml de départ dans le dossier courant.
func newInit() *cobra.Command {
	var name string
	c := &cobra.Command{
		Use: "init", Short: i18n.T("init.short"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := os.Stat(projectFileName); err == nil {
				return errors.New(i18n.T("init.exists", projectFileName))
			}
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			if name == "" {
				name = defaultName(filepath.Base(wd))
			}
			port := defaultTargetPort
			origin := "build:\n  context: .\n"
			// Sans Dockerfile, le type de projet est détecté au build.
			if raw, err := os.ReadFile("Dockerfile"); err == nil {
				if m := dockerfileExposeRe.FindSubmatch(raw); m != nil {
					if _, err := fmt.Sscan(string(m[1]), &port); err != nil {
						port = defaultTargetPort
					}
				}
			}
			content := fmt.Sprintf("# %s\nname: %s\n%sport: %d\nreplicas: 1\n# cpu: %s\n# memory: %s\n# tls: true\n# env:\n#   LOG_LEVEL: info\n#   PUBLIC_URL: ${PUBLIC_URL}  # %s\n# secrets:\n#   - DATABASE_URL  # %s\n",
				i18n.T("init.header"), name, origin, port, defaultCPULimit, defaultMemoryLimit, i18n.T("init.env_hint"), i18n.T("init.secret_hint"))
			if err := os.WriteFile(projectFileName, []byte(content), 0o644); err != nil {
				return err
			}
			p := printer(cmd)
			p.Success(i18n.T("init.done", projectFileName))
			p.Hint(i18n.T("init.next"))
			return nil
		},
	}
	c.Flags().StringVarP(&name, "name", "n", "", i18n.T("flag.wl.name"))
	return c
}

// defaultName : nom de conteneur valide déduit du dossier.
func defaultName(dir string) string {
	n := strings.Trim(invalidNameChars.ReplaceAllString(strings.ToLower(dir), "-"), "-")
	if n == "" {
		return "app"
	}
	return n
}
