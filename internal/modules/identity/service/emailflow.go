package services

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/thekrauss/kubemanager/internal/audit"
	"github.com/thekrauss/kubemanager/internal/auth"
	keycloakadmin "github.com/thekrauss/kubemanager/internal/modules/identity/keycloak"
	identitytypes "github.com/thekrauss/kubemanager/internal/modules/identity/types"
)

// Inscription, vérification d'adresse et mot de passe (docs/architecture/
// auth.md §7). Les réponses ne révèlent jamais si un compte existe : le
// titulaire de l'adresse, lui, reçoit l'email adapté.

func (s *service) mailReady() error {
	if s.mailer == nil || s.emailTokens == nil || s.frontendBaseURL == "" {
		return ErrAuthUnavailable
	}
	return nil
}

// send : envoi au mieux (une panne SMTP ne doit pas révéler l'existence du
// compte par une réponse différente) ; journalisé.
func (s *service) send(ctx context.Context, kind emailKind, to, name, link string) {
	msg, err := renderEmail(ctx, kind, to, name, link)
	if err == nil {
		err = s.mailer.Send(ctx, msg)
	}
	if err != nil && s.logger != nil {
		s.logger.Errorw("transactional email failed", "kind", kind, zap.Error(err))
	}
}

func (s *service) sendVerification(ctx context.Context, subject, email, name string) error {
	tok, err := s.emailTokens.Issue(ctx, subject, email, auth.PurposeVerifyEmail, auth.VerifyEmailTTL)
	if err != nil {
		return err
	}
	s.send(ctx, emailVerify, email, name, s.consoleLink(pageVerifyEmail, tok))
	return nil
}

// RegisterAccount : crée le compte (adresse à confirmer) ; si l'adresse a
// déjà un compte, son titulaire en est averti et la réponse est la même.
func (s *service) RegisterAccount(ctx context.Context, req identitytypes.RegisterRequest, cl Client) (*identitytypes.RegisterResponse, error) {
	if s == nil || s.keycloak == nil {
		return nil, ErrIdentityServiceUnavailable
	}
	if err := s.mailReady(); err != nil {
		return nil, err
	}
	email := normalizeEmail(req.Email)
	fullName := strings.TrimSpace(req.FullName)
	if email == "" || !strings.Contains(email, "@") || req.Password == "" || fullName == "" {
		return nil, ErrRegisterInputInvalid
	}
	if auth.WeakPassword(email, req.Password) {
		return nil, ErrWeakPassword
	}
	if err := s.limit(ctx, "register:ip:"+cl.IP, registerPerIP, mailWindow); err != nil {
		return nil, err
	}
	accepted := &identitytypes.RegisterResponse{Email: email, VerificationSent: true}
	if existing, err := s.keycloak.FindUserByEmail(ctx, email); err == nil {
		if s.limit(ctx, "mail:acct:"+emailKey(email), mailPerAccount, mailWindow) == nil {
			s.send(ctx, emailAccountExists, email, existing.FullName, s.consoleLink(pageSignIn, ""))
		}
		return accepted, nil
	} else if !errors.Is(err, keycloakadmin.ErrUserNotFound) {
		return nil, err
	}
	kcUser, err := s.keycloak.CreateUser(ctx, keycloakadmin.CreateUserInput{
		Email: email, Password: req.Password, FullName: fullName, PhoneNumber: strings.TrimSpace(req.PhoneNumber), Enabled: true,
	})
	if errors.Is(err, keycloakadmin.ErrPasswordRejected) {
		return nil, ErrWeakPassword
	}
	if err != nil {
		return nil, err
	}
	_ = s.limit(ctx, "mail:acct:"+emailKey(email), mailPerAccount, mailWindow)
	if err := s.sendVerification(ctx, kcUser.ID, email, fullName); err != nil {
		return nil, err
	}
	return accepted, nil
}

// VerifyEmail : lien de confirmation d'adresse.
func (s *service) VerifyEmail(ctx context.Context, token string) error {
	if err := s.mailReady(); err != nil {
		return err
	}
	link, err := s.emailTokens.Consume(ctx, token, auth.PurposeVerifyEmail)
	if err != nil {
		return err
	}
	if err := s.keycloak.MarkEmailVerified(ctx, link.Subject); err != nil {
		return err
	}
	if user, err := s.users.FindByKeycloakUserID(ctx, link.Subject); err == nil && user != nil && !user.IsVerified {
		user.IsVerified = true
		_ = s.users.UpdateUser(ctx, user)
	}
	audit.Annotate(ctx, "email", "verified")
	return nil
}

// ResendVerification : nouvel email de confirmation (réponse identique dans tous les cas).
func (s *service) ResendVerification(ctx context.Context, email string, cl Client) error {
	if err := s.mailReady(); err != nil {
		return err
	}
	email = normalizeEmail(email)
	if err := s.limit(ctx, "mail:ip:"+cl.IP, mailPerIP, mailWindow); err != nil {
		return err
	}
	if s.limit(ctx, "mail:acct:"+emailKey(email), mailPerAccount, mailWindow) != nil {
		return nil
	}
	kcUser, err := s.keycloak.FindUserByEmail(ctx, email)
	if err != nil || kcUser.EmailVerified {
		return nil
	}
	return s.sendVerification(ctx, kcUser.ID, email, kcUser.FullName)
}

// RequestPasswordReset : lien de réinitialisation (réponse identique dans tous les cas).
func (s *service) RequestPasswordReset(ctx context.Context, email string, cl Client) error {
	if err := s.mailReady(); err != nil {
		return err
	}
	email = normalizeEmail(email)
	if err := s.limit(ctx, "mail:ip:"+cl.IP, mailPerIP, mailWindow); err != nil {
		return err
	}
	if s.limit(ctx, "mail:acct:"+emailKey(email), mailPerAccount, mailWindow) != nil {
		return nil
	}
	kcUser, err := s.keycloak.FindUserByEmail(ctx, email)
	if err != nil {
		return nil
	}
	tok, err := s.emailTokens.Issue(ctx, kcUser.ID, email, auth.PurposeResetPassword, auth.ResetPasswordTTL)
	if err != nil {
		return err
	}
	s.send(ctx, emailReset, email, kcUser.FullName, s.consoleLink(pageResetPassword, tok))
	return nil
}

// CompletePasswordReset : nouveau mot de passe depuis le lien reçu ; toutes
// les sessions sont fermées et l'adresse est confirmée (le lien le prouve).
func (s *service) CompletePasswordReset(ctx context.Context, token, password string) error {
	if err := s.mailReady(); err != nil {
		return err
	}
	if len([]rune(password)) < auth.MinPasswordLength || len([]rune(password)) > auth.MaxPasswordLength {
		return ErrWeakPassword // avant de consommer le lien : l'utilisateur peut réessayer
	}
	link, err := s.emailTokens.Consume(ctx, token, auth.PurposeResetPassword)
	if err != nil {
		return err
	}
	if auth.WeakPassword(link.Email, password) {
		return ErrWeakPassword
	}
	if err := s.keycloak.SetPassword(ctx, link.Subject, password); err != nil {
		if errors.Is(err, keycloakadmin.ErrPasswordRejected) {
			return ErrWeakPassword
		}
		return err
	}
	_ = s.keycloak.MarkEmailVerified(ctx, link.Subject)
	if user, err := s.users.FindByKeycloakUserID(ctx, link.Subject); err == nil && user != nil {
		_ = s.sessions.RevokeAll(ctx, user.ID, uuid.Nil, "password_reset")
	}
	s.send(ctx, emailPasswordChanged, link.Email, "", s.consoleLink(pageSignIn, ""))
	audit.Annotate(ctx, "password", "reset")
	return nil
}

// ChangePassword : mot de passe actuel exigé (un jeton volé ne suffit pas à
// prendre le compte) ; les autres sessions sont fermées.
func (s *service) ChangePassword(ctx context.Context, subject string, sessionID uuid.UUID, current, next string) error {
	if err := s.authReady(); err != nil {
		return err
	}
	if err := s.limit(ctx, "pwchange:"+subject, loginPerAccount, loginWindow); err != nil {
		return err
	}
	kcUser, err := s.keycloak.GetUserByID(ctx, subject)
	if err != nil {
		return err
	}
	if auth.WeakPassword(kcUser.Email, next) {
		return ErrWeakPassword
	}
	if _, err := s.keycloak.VerifyPassword(ctx, kcUser.Email, current); err != nil {
		if errors.Is(err, keycloakadmin.ErrInvalidCredentials) {
			return ErrCurrentPassword
		}
		return err
	}
	if err := s.keycloak.SetPassword(ctx, subject, next); err != nil {
		if errors.Is(err, keycloakadmin.ErrPasswordRejected) {
			return ErrWeakPassword
		}
		return err
	}
	if user, err := s.users.FindByKeycloakUserID(ctx, subject); err == nil && user != nil {
		_ = s.sessions.RevokeAll(ctx, user.ID, sessionID, "password_changed")
	}
	if s.mailer != nil {
		s.send(ctx, emailPasswordChanged, kcUser.Email, kcUser.FullName, s.consoleLink(pageForgot, ""))
	}
	audit.Annotate(ctx, "password", "changed")
	return nil
}
