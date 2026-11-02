package services

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/audit"
	"github.com/thekrauss/kubemanager/internal/auth"
	identitytypes "github.com/thekrauss/kubemanager/internal/modules/identity/types"
)

// Second facteur géré par betorun (docs/architecture/auth.md §6).

func (s *service) MFAStatus(ctx context.Context, userID uuid.UUID) (*auth.FactorStatus, error) {
	if err := s.authReady(); err != nil {
		return nil, err
	}
	return s.factors.Status(ctx, userID)
}

// StartTOTP : secret à enregistrer dans l'application d'authentification.
func (s *service) StartTOTP(ctx context.Context, userID uuid.UUID, account string) (*auth.TOTPEnrollment, error) {
	if err := s.authReady(); err != nil {
		return nil, err
	}
	return s.factors.StartTOTP(ctx, userID, account)
}

// ConfirmTOTP : active le TOTP ; la session courante passe au niveau
// « second facteur vérifié ».
func (s *service) ConfirmTOTP(ctx context.Context, userID, sessionID uuid.UUID, code string) (*identitytypes.RecoveryCodesResponse, error) {
	if err := s.authReady(); err != nil {
		return nil, err
	}
	codes, err := s.factors.ConfirmTOTP(ctx, userID, code)
	if err != nil {
		return nil, err
	}
	audit.Annotate(ctx, "mfa", "totp_enabled")
	out := &identitytypes.RecoveryCodesResponse{RecoveryCodes: codes}
	if t, err := s.sessions.MarkMFA(ctx, userID, sessionID); err == nil {
		out.AccessToken = t.AccessToken
		out.ExpiresIn = int64(time.Until(t.ExpiresAt).Round(time.Second).Seconds())
	}
	return out, nil
}

// DisableTOTP : exige un second facteur vérifié récemment dans la session.
func (s *service) DisableTOTP(ctx context.Context, userID, sessionID uuid.UUID) error {
	if err := s.requireRecentMFA(ctx, userID, sessionID); err != nil {
		return err
	}
	if err := s.factors.DisableTOTP(ctx, userID); err != nil {
		return err
	}
	audit.Annotate(ctx, "mfa", "totp_disabled")
	return nil
}

// RegenerateRecoveryCodes : exige un second facteur vérifié récemment.
func (s *service) RegenerateRecoveryCodes(ctx context.Context, userID, sessionID uuid.UUID) (*identitytypes.RecoveryCodesResponse, error) {
	if err := s.requireRecentMFA(ctx, userID, sessionID); err != nil {
		return nil, err
	}
	codes, err := s.factors.RegenerateRecovery(ctx, userID)
	if err != nil {
		return nil, err
	}
	audit.Annotate(ctx, "mfa", "recovery_codes_regenerated")
	return &identitytypes.RecoveryCodesResponse{RecoveryCodes: codes}, nil
}

func (s *service) requireRecentMFA(ctx context.Context, userID, sessionID uuid.UUID) error {
	if err := s.authReady(); err != nil {
		return err
	}
	at, err := s.sessions.MFAAt(ctx, userID, sessionID)
	if err != nil {
		return err
	}
	if at.IsZero() || time.Since(at) > auth.RecentMFA {
		return auth.ErrRecentMFA
	}
	return nil
}
