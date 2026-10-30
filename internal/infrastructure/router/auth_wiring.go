package router

import (
	"fmt"

	"github.com/thekrauss/kubemanager/internal/auth"
	"github.com/thekrauss/kubemanager/internal/infrastructure/mail"
	identitySvc "github.com/thekrauss/kubemanager/internal/modules/identity/service"
)

// authDeps : authentification gérée par betorun (docs/architecture/auth.md).
// Sessions et second facteur exigent le STS (jetons) et sa clé de
// chiffrement ; l'envoi d'emails n'est actif que s'il est configuré.
func (a *App) authDeps(deps *identitySvc.Deps) error {
	if a.STS == nil {
		return fmt.Errorf("auth: STS required for sessions")
	}
	sealer, err := auth.NewSealer(a.Config.IAM.STSKeyEncryptionKey)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	deps.Sessions = &auth.Sessions{DB: a.DB, Minter: a.STS}
	deps.Factors = &auth.Factors{DB: a.DB, Sealer: sealer, Pepper: []byte(a.Config.IAM.APIKeyPepper)}
	deps.EmailTokens = &auth.EmailTokens{DB: a.DB}
	deps.Limiter = a.Cache
	deps.Logger = a.Logger

	m := a.Config.Mail
	if !m.Enabled {
		a.Logger.Warnw("auth: mail disabled — registration, email verification and password reset are unavailable")
		return nil
	}
	sender, err := mail.NewSMTP(mail.Config{Host: m.SMTPHost, Port: m.SMTPPort, User: m.SMTPUser, Password: m.SMTPPassword,
		FromEmail: m.FromEmail, FromName: m.FromName})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	deps.Mailer = sender
	return nil
}
