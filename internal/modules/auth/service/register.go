package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/thekrauss/beto-shared/pkg/errors"
	"github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
)

func (s *AuthService) CreateUser(ctx context.Context, d *domain.User) (*domain.User, error) {
	existing, _ := s.AuthRepo.GetUserByEmail(ctx, d.Email)
	if existing != nil {
		return nil, errors.New(errors.CodeConflict, " User with this email already exists")

	}

	newUser := d
	if err := s.AuthRepo.CreateUser(ctx, newUser); err != nil {
		return nil, err
	}

	cacheKey := fmt.Sprintf("%s%s%s", cache.ServiceKeyPrefix, cache.KeyUser, newUser.ID)

	_ = s.Cache.SetUser(ctx, cacheKey, newUser, time.Hour)

	return newUser, nil
}

type UpdateUserInput struct {
	ID uuid.UUID `json:"-" path:"id"`

	Email     *string `json:"email"`
	FullName  *string `json:"full_name"`
	AvatarURL *string `json:"avatar_url"`
	IsActive  *bool   `json:"is_active"`
	Role      *string `json:"role"`
}

func (s *AuthService) UpdateUser(ctx context.Context, input UpdateUserInput) (*domain.User, error) {
	existing, err := s.AuthRepo.GetUserByID(ctx, input.ID)
	if err != nil {
		return nil, errors.Wrap(err, errors.CodeNotFound, "user not found")
	}

	if input.Email != nil && *input.Email != existing.Email {
		emailCheck, _ := s.AuthRepo.GetUserByEmail(ctx, *input.Email)
		if emailCheck != nil {
			return nil, errors.New(errors.CodeConflict, "email already in use")
		}
		existing.Email = *input.Email
	}

	if input.FullName != nil {
		existing.FullName = *input.FullName
	}
	if input.AvatarURL != nil {
		existing.AvatarURL = *input.AvatarURL
	}
	if input.IsActive != nil {
		existing.IsActive = *input.IsActive
	}

	existing.UpdatedAt = time.Now()

	if err := s.AuthRepo.UpdateUser(ctx, existing); err != nil {
		return nil, err
	}

	err = s.Cache.SetUser(ctx, existing.ID.String(), existing, time.Hour)
	if err != nil {
		_ = s.Cache.DeleteUser(ctx, existing.ID.String())
	}

	return existing, nil
}
