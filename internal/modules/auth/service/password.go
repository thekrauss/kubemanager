package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/thekrauss/kubemanager/internal/core/cache"
)

type ChangePasswordInput struct {
	UserID          string
	CurrentPassword string
	NewPassword     string
	UserEmail       string
}

type ResetPasswordInput struct {
	Token       string
	NewPassword string
}

func (s *AuthService) ForgotPassword(ctx context.Context, email string) (string, error) {
	user, err := s.AuthRepo.GetUserByEmail(ctx, email)

	if err != nil || user == nil {
		return "", nil
	}

	resetToken := uuid.New().String()

	err = s.Cache.StoreResetToken(ctx, resetToken, user.ID.String(), 15*time.Minute)
	if err != nil {
		return "", errors.New(errors.CodeInternal, "failed to generate reset token")
	}

	return resetToken, nil
}

func (s *AuthService) ResetPassword(ctx context.Context, input ResetPasswordInput) error {

	userIDStr, err := s.Cache.GetResetToken(ctx, input.Token)
	if err != nil {
		if err == cache.ErrCacheMiss {
			return errors.New(errors.CodeInvalidInput, "reset link is invalid or expired")
		}
		return errors.Wrap(err, errors.CodeInternal, "failed to validate token")
	}

	userUUID, err := uuid.Parse(userIDStr)
	if err != nil {
		return errors.New(errors.CodeInternal, "invalid user ID in cache")
	}

	hashedPwd, err := s.Hasher.HashPassword(input.NewPassword)
	if err != nil {
		return errors.New(errors.CodeInternal, "failed to hash password")
	}

	if err := s.AuthRepo.UpdatePassword(ctx, userUUID, hashedPwd); err != nil {
		return err
	}

	_ = s.Cache.DeleteResetToken(ctx, input.Token)

	_ = s.Cache.DeleteSession(ctx, userIDStr)

	_ = s.Cache.DeleteUser(ctx, userIDStr)

	return nil
}

func (s *AuthService) ChangePassword(ctx context.Context, input ChangePasswordInput) error {
	userUUID, err := uuid.Parse(input.UserID)
	if err != nil {
		return errors.New(errors.CodeInvalidInput, "invalid user ID format")
	}

	user, err := s.AuthRepo.GetUserByID(ctx, userUUID)
	if err != nil {
		return errors.New(errors.CodeNotFound, "user not found")
	}

	if !s.Hasher.CheckPasswordHash(input.CurrentPassword, user.PasswordHash) {
		return errors.New(errors.CodeInvalidInput, "current password is incorrect")
	}

	newHashedPwd, err := s.Hasher.HashPassword(input.NewPassword)
	if err != nil {
		return errors.New(errors.CodeInternal, "failed to hash new password")
	}

	if err := s.AuthRepo.UpdatePassword(ctx, userUUID, newHashedPwd); err != nil {
		return err
	}
	_ = s.Cache.DeleteUser(ctx, input.UserID)

	return nil
}
