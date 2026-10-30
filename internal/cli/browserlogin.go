package cli

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/config"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
	"github.com/thekrauss/kubemanager/internal/core/httpheader"
)

// Connexion par le navigateur avec retour vers un serveur local éphémère
// (RFC 8252 : http://127.0.0.1:<port libre>/callback). Sert au step-up :
// le flux device ne permet pas d'exiger une double authentification récente.

// browserLoginTimeout : temps laissé pour se connecter dans le navigateur.
var browserLoginTimeout = 5 * time.Minute

// openURL : ouverture du navigateur (remplacée dans les tests).
var openURL = openBrowser

func browserLogin(ctx context.Context, cl *api.Client, out io.Writer, stepUp, open bool) (*config.Credentials, error) {
	ln, err := net.Listen("tcp", loopbackListenAddr)
	if err != nil {
		return nil, err
	}
	defer ln.Close()
	redirect := fmt.Sprintf(loopbackRedirectFormat, ln.Addr().(*net.TCPAddr).Port)

	link, err := cl.BrowserLoginURL(ctx, redirect, stepUp)
	if err != nil {
		return nil, err
	}
	expectedState := ""
	if u, err := url.Parse(link); err == nil {
		expectedState = u.Query().Get("state")
	}

	type result struct {
		code, state string
		err         error
	}
	done := make(chan result, 1)
	srv := &http.Server{ReadHeaderTimeout: loopbackReadHeaderTimeout, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != loopbackCallbackPath {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		res := result{code: q.Get("code"), state: q.Get("state")}
		switch {
		case q.Get("error") != "":
			res.err = errors.New(i18n.T("login.browser_error", q.Get("error_description"), q.Get("error")))
		case res.code == "" || res.state != expectedState:
			// Un autre onglet ou une autre application ne peut pas injecter un code.
			res.err = errors.New(i18n.T("login.browser_state"))
		}
		w.Header().Set(httpheader.ContentType, httpheader.MIMETextHTMLUTF8)
		msg := i18n.T("login.browser_done")
		if res.err != nil {
			msg = res.err.Error()
		}
		fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>betorun</title>"+
			"<body style=\"font-family:system-ui;margin:3rem\"><h1>betorun</h1><p>%s</p></body>", html.EscapeString(msg))
		select {
		case done <- res:
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	fmt.Fprintln(out, i18n.T("login.browser_open", link))
	if open {
		openURL(link)
	}
	ctx, cancel := context.WithTimeout(ctx, browserLoginTimeout)
	defer cancel()
	var res result
	select {
	case <-ctx.Done():
		return nil, errors.New(i18n.T("login.expired"))
	case res = <-done:
	}
	if res.err != nil {
		return nil, res.err
	}
	creds, err := cl.BrowserLoginCallback(ctx, res.code, res.state, redirect)
	if err != nil {
		return nil, err
	}
	if err := config.SaveCredentials(creds); err != nil {
		return nil, err
	}
	cl.UseCredentials(creds)
	return creds, nil
}

// interactive : un humain peut répondre (terminal), pas une CI ni un script.
func interactive() bool {
	if os.Getenv(config.EnvCI) != "" {
		return false
	}
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
