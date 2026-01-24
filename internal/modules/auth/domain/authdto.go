package domain

import (
	"time"
)

type AssignRoleRequest struct {
	UserID    string `json:"user_id" validate:"required,uuid"`
	ProjectID string `json:"project_id" validate:"required,uuid"`
	RoleName  string `json:"role_name" validate:"required"`
}

type CheckPermissionRequest struct {
	UserID     string `json:"user_id" validate:"required,uuid"`
	ProjectID  string `json:"project_id" validate:"required,uuid"`
	Permission string `json:"permission" validate:"required"`
}

type PermissionsResponse struct {
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

type MessageResponse struct {
	Message string `json:"message"`
}

type LoginRequest struct {
	Identifier string `json:"identifier" binding:"required"`
	Password   string `json:"password" binding:"required"`
}

type LogoutRequest struct {
	UserID       string `json:"-"`
	RefreshToken string `json:"refresh_token" binding:"required"`
	AccessToken  string `json:"-"`
}

type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type LoginResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	UserID       string `json:"user_id"`
	ExpiresAt    string `json:"expires_at"`
	Message      string `json:"message"`
}

type RefreshTokenResponse struct {
	Token           string `json:"token"`
	RefreshToken    string `json:"refresh_token"`
	AccessExpiresAt string `json:"access_expires_at"`
}

type TokenRequest struct {
	Token string `json:"token" binding:"required"`
}

type TokenResponse struct {
	Valid bool `json:"valid"`
}

type RoleDTO struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

type ProjectMemberDTO struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	FullName  string `json:"full_name"`
	AvatarURL string `json:"avatar_url"`
	RoleName  string `json:"role_name"`
	JoinedAt  string `json:"joined_at"`
}

type RegisterRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
	FullName string `json:"full_name" binding:"required"`
}

type RegisterResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	FullName  string    `json:"full_name"`
	CreatedAt time.Time `json:"created_at"`
}
