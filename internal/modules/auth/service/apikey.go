package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	"github.com/thekrauss/kubemanager/internal/modules/utils"
)

type APIKeyService struct {
	Repo repository.AuthRepository
}

func NewAPIKeyService(repo repository.AuthRepository) *APIKeyService {
	return &APIKeyService{Repo: repo}
}

type CreateAPIKeyInput struct {
	UserID string   `json:"-"`
	Name   string   `json:"name"`   // "MacBook Pro CLI"
	Scopes []string `json:"scopes"` // ["read", "write"]
}

type APIKeyCreatedResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	RawAPIKey string    `json:"api_key"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *APIKeyService) CreateAPIKey(ctx context.Context, input CreateAPIKeyInput) (*APIKeyCreatedResponse, error) {
	uID, _ := uuid.Parse(input.UserID)

	var validScopes []domain.PermissionType

	for _, scopeStr := range input.Scopes {
		perm, err := domain.PermissionTypes.NewFromString(ctx, scopeStr)
		if err != nil {
			return nil, betoerrors.Wrap(err, betoerrors.CodeDBNotFound, fmt.Sprintf("invalid scope provided: %s", scopeStr))
		}
		validScopes = append(validScopes, perm)
	}

	rawKey, err := utils.GenerateSecureKey("k8m", 32)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInternal, "failed to generate key")
	}

	keyHash := utils.HashAPIKey(rawKey)
	dbPrefix := rawKey[:7]

	apiKey := &domain.APIKey{
		UserID:  uID,
		Name:    input.Name,
		Prefix:  dbPrefix,
		KeyHash: keyHash,
		Scopes:  validScopes,
	}

	if err := s.Repo.CreateAPIKey(ctx, apiKey); err != nil {
		return nil, betoerrors.Wrap(err, betoerrors.CodeInternal, "failed to save api key")
	}

	return &APIKeyCreatedResponse{
		ID:        apiKey.ID.String(),
		Name:      apiKey.Name,
		RawAPIKey: rawKey,
		CreatedAt: apiKey.CreatedAt,
	}, nil
}

type APIKeyDTO struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (s *APIKeyService) ListUserKeys(ctx context.Context, userIDStr string) ([]APIKeyDTO, error) {
	uID, _ := uuid.Parse(userIDStr)
	keys, err := s.Repo.ListUserAPIKeys(ctx, uID)
	if err != nil {
		return nil, err
	}

	var result []APIKeyDTO
	for _, k := range keys {
		result = append(result, APIKeyDTO{
			ID:         k.ID.String(),
			Name:       k.Name,
			Prefix:     k.Prefix + "...",
			LastUsedAt: k.LastUsedAt,
			CreatedAt:  k.CreatedAt,
		})
	}
	return result, nil
}

func (s *APIKeyService) RevokeKey(ctx context.Context, userIDStr, keyIDStr string) error {
	uID, _ := uuid.Parse(userIDStr)
	kID, err := uuid.Parse(keyIDStr)
	if err != nil {
		return betoerrors.New(betoerrors.CodeInvalidInput, "invalid key id")
	}

	return s.Repo.RevokeAPIKey(ctx, kID, uID)
}
