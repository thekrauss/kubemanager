package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	betoerrors "github.com/thekrauss/beto-shared/pkg/errors"
	"github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/core/configs"
	"github.com/thekrauss/kubemanager/internal/middleware/security"

	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	"github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	"go.uber.org/zap"
)

type AuthService struct {
	AuthRepo repository.AuthRepository
	Cache    cache.CacheRedis
	JWT      security.JWTManager
	Logger   *zap.SugaredLogger
	Config   *configs.GlobalConfig
	Hasher   security.PasswordHasher
}

func NewAuthService(
	cfg *configs.GlobalConfig,
	repo repository.AuthRepository,
	jwtMgr security.JWTManager,
	cacheRepo cache.CacheRedis,
	logger *zap.SugaredLogger,
	hasher security.PasswordHasher,
) *AuthService {
	return &AuthService{
		AuthRepo: repo,
		Cache:    cacheRepo,
		JWT:      jwtMgr,
		Logger:   logger.With("service", "AuthService"),
		Config:   cfg,
		Hasher:   hasher,
	}
}

func (s *AuthService) Login(ctx context.Context, req *domain.LoginRequest) (*domain.LoginResponse, error) {

	blocked, err := s.Cache.IsUserBlocked(ctx, req.Identifier)
	if err != nil {
		s.Logger.Errorw("failed to check user block status", "email", req.Identifier, "error", err)
	}
	if blocked {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "Account temporarily blocked due to multiple failed attempts")
	}

	user, err := s.AuthRepo.GetUserByEmail(ctx, req.Identifier)
	if err != nil {
		s.Logger.Errorw("database error during login", "email", req.Identifier, "error", err)
		return nil, betoerrors.New(betoerrors.CodeInternal, "authentication failed")
	}

	if user == nil || !user.IsActive {
		s.handleFailedLogin(ctx, req.Identifier)
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "invalid credentials")
	}

	if !s.Hasher.CheckPasswordHash(req.Password, user.PasswordHash) {
		s.handleFailedLogin(ctx, req.Identifier)
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "invalid credentials")
	}

	projectRolesMap := make(map[string]string)

	memberships, err := s.AuthRepo.GetUserProjectMemberships(ctx, user.ID)
	if err != nil {
		s.Logger.Errorw("failed to fetch user memberships", "user_id", user.ID, "error", err)
	}

	for _, m := range memberships {
		projectRolesMap[m.ProjectID.String()] = m.Role.Name
	}

	accessToken, err := s.JWT.GenerateAccessToken(
		user.ID.String(),
		user.Role,
		s.Config.JWT.AccessExpiration,
	)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInternal, "failed to generate access token")
	}

	refreshToken := uuid.New().String()

	session := cache.SessionData{
		UserID:       user.ID.String(),
		Email:        user.Email,
		GlobalRole:   user.Role,
		ProjectRoles: projectRolesMap,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(s.Config.JWT.AccessExpiration),
	}

	if err := s.Cache.StoreSession(ctx, user.ID.String(), session, s.Config.JWT.AccessExpiration); err != nil {
		s.Logger.Errorw("failed to store session in redis", "user_id", user.ID, "error", err)
	}

	_ = s.AuthRepo.UpdateUser(ctx, user)

	return &domain.LoginResponse{
		Token:        accessToken,
		RefreshToken: refreshToken,
		UserID:       user.ID.String(),
		ExpiresAt:    session.ExpiresAt.Format(time.RFC3339),
	}, nil
}

func (s *AuthService) handleFailedLogin(ctx context.Context, email string) {
	attempts, _ := s.Cache.IncrementLoginAttempts(ctx, email, 15*time.Minute)
	if attempts >= 5 {
		_ = s.Cache.BlockUser(ctx, email, 15*time.Minute)
		s.Logger.Warnw("user blocked due to failed attempts", "email", email)
	}
}

func (s *AuthService) ValidateToken(ctx context.Context, req *domain.TokenRequest) (*domain.TokenResponse, error) {
	s.Logger.Debugw("Manual token validation request")

	if req.Token == "" {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "token is required")
	}

	claims, err := s.JWT.VerifyAccessToken(req.Token)
	if err != nil {
		s.Logger.Warnw("Manual token validation failed", "error", err)
		return &domain.TokenResponse{Valid: false}, nil
	}

	blacklisted, _ := s.Cache.IsTokenBlacklisted(ctx, claims.ID)
	if blacklisted {
		return &domain.TokenResponse{Valid: false}, nil
	}

	return &domain.TokenResponse{Valid: true}, nil
}
func (s *AuthService) RefreshToken(ctx context.Context, req *domain.RefreshTokenRequest) (*domain.RefreshTokenResponse, error) {
	refreshTokenID, err := uuid.Parse(req.RefreshToken)
	if err != nil {
		return nil, betoerrors.New(betoerrors.CodeInvalidInput, "invalid refresh token format")
	}

	refreshToken, err := s.AuthRepo.FindRefreshTokenByJTI(ctx, refreshTokenID)
	if err != nil || refreshToken == nil {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "invalid or expired refresh token")
	}

	if time.Now().After(refreshToken.ExpiresAt) {
		_ = s.AuthRepo.DeleteRefreshTokenByJTI(ctx, refreshTokenID)
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "refresh token expired")
	}

	user, err := s.AuthRepo.GetUserByID(ctx, refreshToken.UserID)
	if err != nil || user == nil || !user.IsActive {
		return nil, betoerrors.New(betoerrors.CodeUnauthorized, "user account is no longer active or not found")
	}

	projectRolesMap := make(map[string]string)
	memberships, err := s.AuthRepo.GetUserProjectMemberships(ctx, user.ID)
	if err == nil {
		for _, m := range memberships {
			projectRolesMap[m.ProjectID.String()] = m.Role.Name
		}
	}

	newAccessToken, err := s.JWT.GenerateAccessToken(
		user.ID.String(),
		user.Role,
		s.Config.JWT.AccessExpiration,
	)
	if err != nil {
		s.Logger.Errorw("failed to generate access token during refresh", "error", err)
		return nil, betoerrors.New(betoerrors.CodeInternal, "failed to generate access token")
	}

	session := cache.SessionData{
		UserID:       user.ID.String(),
		Email:        user.Email,
		GlobalRole:   user.Role,
		ProjectRoles: projectRolesMap,
		AccessToken:  newAccessToken,
		RefreshToken: req.RefreshToken,
		ExpiresAt:    time.Now().Add(s.Config.JWT.AccessExpiration),
	}

	if err := s.Cache.StoreSession(ctx, user.ID.String(), session, s.Config.JWT.AccessExpiration); err != nil {
		s.Logger.Errorw("failed to update cache session during refresh", "error", err)
	}

	return &domain.RefreshTokenResponse{
		Token:           newAccessToken,
		RefreshToken:    req.RefreshToken,
		AccessExpiresAt: session.ExpiresAt.Format(time.RFC3339),
	}, nil
}
func (s *AuthService) Logout(ctx context.Context, req *domain.LogoutRequest) error {
	s.Logger.Infow("User logout requested", "user_id", req.UserID)

	if err := s.Cache.DeleteSession(ctx, req.UserID); err != nil {
		s.Logger.Warnw("failed to delete session", "user_id", req.UserID, "error", err)
	}

	if req.RefreshToken != "" {
		tokenUUID, err := uuid.Parse(req.RefreshToken)
		if err == nil {
			_ = s.AuthRepo.DeleteRefreshTokenByJTI(ctx, tokenUUID)
		}
	}

	if req.AccessToken != "" {
		claims, err := s.JWT.VerifyAccessToken(req.AccessToken)
		if err == nil {
			ttl := time.Until(claims.ExpiresAt.Time)
			if ttl > 0 {
				_ = s.Cache.BlacklistToken(ctx, claims.ID, ttl)
			}
		}
	}

	return nil
}
