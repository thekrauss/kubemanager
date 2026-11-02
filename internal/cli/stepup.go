package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/thekrauss/kubemanager/internal/cli/api"
	"github.com/thekrauss/kubemanager/internal/cli/i18n"
	"github.com/thekrauss/kubemanager/internal/cli/ui"
)

// Confirmation d'identité (step-up) dans le terminal : code de
// l'application d'authentification ou code de secours, saisi sans écho,
// vérifié par l'API ; la requête refusée est ensuite rejouée.

// stepUpAttempts : essais avant abandon.
const stepUpAttempts = 3

// Codes d'erreur de l'API utiles ici.
const (
	apiCodeInvalidCode   = "INVALID_CODE"
	apiCodeMFANotEnabled = "MFA_NOT_ENROLLED"
)

// readCode : remplaçable dans les tests.
var readCode = func(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt+" ")
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return strings.TrimSpace(string(b)), err
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// stepUp : demande le code et le fait vérifier dans la session courante.
func stepUp(ctx context.Context, cl *api.Client, out io.Writer) error {
	p := ui.New(out)
	p.Warn(i18n.T("stepup.intro"))
	for attempt := 1; ; attempt++ {
		code, err := readCode(i18n.T("stepup.prompt"))
		if err != nil {
			return err
		}
		if code == "" {
			return errors.New(i18n.T("stepup.cancelled"))
		}
		err = cl.VerifySession(ctx, code)
		var apiErr *api.Error
		switch {
		case err == nil:
			p.Success(i18n.T("stepup.ok"))
			return nil
		case errors.As(err, &apiErr) && apiErr.Code == apiCodeMFANotEnabled:
			return errors.New(i18n.T("stepup.no_factor"))
		case errors.As(err, &apiErr) && apiErr.Code == apiCodeInvalidCode && attempt < stepUpAttempts:
			p.Fail(i18n.T("stepup.invalid"))
		default:
			return err
		}
	}
}
