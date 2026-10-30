// Package cli : ligne de commande de betorun. Deux points d'entrée :
//
//	cmd/betorun          binaire dédié, distribué aux utilisateurs
//	kubemanager betocli  même CLI depuis le binaire du serveur (opérations)
//
// La CLI ne parle qu'à l'API publique ; elle réutilise les types de l'API
// (sites/domain, identity/types…) mais jamais le code interne du serveur.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/archive"
	"github.com/thekrauss/kubemanager/internal/cli/config"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
)

// Version et DefaultAPIURL : posés à la compilation
// (-ldflags "-X github.com/thekrauss/kubemanager/internal/cli.Version=… -X …DefaultAPIURL=…").
var (
	Version       = "dev"
	DefaultAPIURL = ""
)

var (
	flagAPI     string
	flagProject string
)

func newRoot(name string) *cobra.Command {
	root := &cobra.Command{
		Use:           name,
		Short:         i18n.T("root.short"),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&flagAPI, "api", "", i18n.T("flag.api"))
	root.PersistentFlags().StringVarP(&flagProject, "project", "p", "", i18n.T("flag.project"))
	root.AddCommand(newLogin(), newLogout(), newWhoami(), newSites(), newWorkloads(), newAccess(), newVersion())
	return root
}

// Execute lance la commande décrite par args (sans le nom du programme) ;
// name est le nom affiché dans l'aide. Renvoie le code de sortie.
func Execute(name string, args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// « kubemanager betocli » : la CLI devient une sous-commande, pour que
	// l'aide affiche le chemin complet à taper.
	parent, sub, nested := strings.Cut(name, " ")
	root := newRoot(name)
	if nested {
		root.Use = sub
		top := &cobra.Command{Use: parent, SilenceUsage: true, SilenceErrors: true}
		top.AddCommand(root)
		args = append([]string{sub}, args...)
		root = top
	}
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "betorun: "+describe(err))
		return 1
	}
	return 0
}

// describe : erreurs connues dans la langue de l'utilisateur.
func describe(err error) string {
	switch {
	case errors.Is(err, api.ErrNotLoggedIn):
		return i18n.T("err.not_logged_in")
	case errors.Is(err, api.ErrSessionExpired):
		return i18n.T("err.session_expired")
	case errors.Is(err, api.ErrDeviceExpired):
		return i18n.T("login.expired")
	case errors.Is(err, api.ErrDeviceDenied):
		return i18n.T("login.denied")
	case errors.Is(err, api.ErrDeviceDisabled):
		return i18n.T("login.disabled")
	case errors.Is(err, archive.ErrEmpty):
		return i18n.T("sites.deploy.empty")
	}
	return err.Error()
}

// settings : réglages, avec --api et --project prioritaires.
func settings() (config.Settings, error) {
	s, err := config.LoadSettings()
	if err != nil {
		return s, err
	}
	if flagAPI != "" {
		s.APIURL = flagAPI
	}
	if s.APIURL == "" {
		s.APIURL = DefaultAPIURL
	}
	if flagProject != "" {
		s.ProjectID = flagProject
	}
	if s.APIURL == "" {
		return s, errors.New(i18n.T("err.no_api"))
	}
	return s, nil
}

// client : client de l'API ; BETORUN_TOKEN remplace la session (CI).
func client(s config.Settings) *api.Client {
	c := api.New(s.APIURL)
	c.StaticToken = os.Getenv("BETORUN_TOKEN")
	return c
}

func requireProject(s config.Settings) (string, error) {
	if s.ProjectID == "" {
		return "", errors.New(i18n.T("err.no_project"))
	}
	return s.ProjectID, nil
}
