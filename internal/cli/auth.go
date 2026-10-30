package cli

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/config"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
)

func newLogin() *cobra.Command {
	var noBrowser, mfa bool
	c := &cobra.Command{
		Use:   "login",
		Short: i18n.T("login.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := settings()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			cl := api.New(s.APIURL)
			if mfa {
				// Double authentification exigée : navigateur + serveur local.
				if _, err := browserLogin(ctx, cl, cmd.OutOrStdout(), true, !noBrowser); err != nil {
					return err
				}
			} else {
				dc, err := cl.StartDeviceLogin(ctx)
				if err != nil {
					return err
				}
				link := dc.VerificationURIComplete
				if link == "" {
					link = dc.VerificationURI
				}
				fmt.Fprintln(cmd.OutOrStdout(), i18n.T("login.open", link, dc.UserCode))
				if !noBrowser {
					openURL(link)
				}
				fmt.Fprintln(cmd.OutOrStdout(), i18n.T("login.waiting"))
				creds, err := cl.WaitDeviceLogin(ctx, dc)
				if err != nil {
					return err
				}
				if err := config.SaveCredentials(creds); err != nil {
					return err
				}
				cl.UseCredentials(creds)
			}

			// Mémorise l'API utilisée et, faute de mieux, le projet de l'utilisateur.
			status, err := cl.BootstrapStatus(ctx)
			if err != nil {
				return err
			}
			project := s.ProjectID
			if project == "" && status.ProjectID != nil {
				project = *status.ProjectID
			}
			if err := config.SaveSettings(func(st *config.Settings) {
				st.APIURL = s.APIURL
				if st.ProjectID == "" {
					st.ProjectID = project
				}
			}); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), i18n.T("login.ok", firstNonEmpty(status.Email, status.FullName)))
			if project != "" {
				fmt.Fprintln(cmd.OutOrStdout(), i18n.T("login.project", projectLabel(status, project)))
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), i18n.T("login.no_project"))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&noBrowser, "no-browser", false, i18n.T("flag.no_browser"))
	c.Flags().BoolVar(&mfa, "mfa", false, i18n.T("flag.mfa"))
	return c
}

func newLogout() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: i18n.T("logout.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			creds, err := config.LoadCredentials()
			if err != nil {
				return err
			}
			if creds == nil {
				fmt.Fprintln(cmd.OutOrStdout(), i18n.T("logout.none"))
				return nil
			}
			// Révocation côté serveur au mieux ; la session locale est effacée dans tous les cas.
			if s, err := settings(); err == nil {
				cl := client(s)
				cl.Source = nil
				_ = cl.Logout(cmd.Context(), creds.RefreshToken)
			}
			if err := config.DeleteCredentials(); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), i18n.T("logout.ok"))
			return nil
		},
	}
}

func newWhoami() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: i18n.T("whoami.short"),
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := settings()
			if err != nil {
				return err
			}
			cl := client(s)
			session, err := cl.Session(cmd.Context())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			// Un compte de service (BETORUN_TOKEN) n'a pas de profil personnel.
			if strings.HasPrefix(session.Principal, principalUser) {
				if status, err := cl.BootstrapStatus(cmd.Context()); err == nil {
					fmt.Fprintln(out, i18n.T("whoami.account", firstNonEmpty(status.Email, status.FullName)))
				}
			}
			fmt.Fprintln(out, i18n.T("whoami.principal", session.Principal))
			if cl.Source != nil {
				fmt.Fprintln(out, i18n.T("whoami.credentials", cl.Source.Describe()))
			}
			if s.ProjectID != "" {
				fmt.Fprintln(out, i18n.T("whoami.project", s.ProjectID))
			}
			if session.MFAAvailable && session.AuthMethod == "bearer" {
				if session.MFA {
					fmt.Fprintln(out, i18n.T("whoami.mfa_on"))
				} else {
					fmt.Fprintln(out, i18n.T("whoami.mfa_off"))
				}
			}
			fmt.Fprintln(out, i18n.T("whoami.api", s.APIURL))
			return nil
		},
	}
}

// newToken : jeton d'accès courant, quelle que soit sa source (session,
// fichier de clé, CI) ; pour curl et les outils tiers.
func newToken() *cobra.Command {
	return &cobra.Command{
		Use:     "token",
		Short:   i18n.T("token.short"),
		Example: "  curl -H \"Authorization: Bearer $(betorun token)\" $BETORUN_API_URL/iam/session",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := settings()
			if err != nil {
				return err
			}
			tok, err := client(s).AccessToken(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), tok)
			return nil
		},
	}
}

func newVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: i18n.T("version.short"),
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "betorun %s (%s/%s)\n", Version, runtime.GOOS, runtime.GOARCH)
		},
	}
}

// openBrowser : au mieux ; l'URL est de toute façon affichée.
func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		c = exec.Command("xdg-open", url)
	}
	_ = c.Start()
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func projectLabel(status *api.BootstrapStatus, id string) string {
	if status.Project != nil && status.Project.Name != "" && status.Project.ID == id {
		return status.Project.Name + " (" + id + ")"
	}
	return id
}
