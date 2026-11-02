package types

import "time"

type RegisterRequest struct {
	Email       string `json:"email" validate:"required,email"`
	Password    string `json:"password" validate:"required,min=8"`
	FullName    string `json:"full_name" validate:"required"`
	PhoneNumber string `json:"phone_number,omitempty"`
}

// RegisterResponse : identique que l'adresse ait déjà un compte ou non
// (son titulaire est prévenu par email) ; rien de l'annuaire n'est exposé.
type RegisterResponse struct {
	Email            string `json:"email"`
	VerificationSent bool   `json:"verification_sent"`
}

type BootstrapRequest struct {
	FullName           string `json:"full_name,omitempty"`
	PhoneNumber        string `json:"phone_number,omitempty"`
	Country            string `json:"country,omitempty"`
	ProjectName        string `json:"project_name,omitempty"`
	ProjectDescription string `json:"project_description,omitempty"`
	CpuLimit           string `json:"cpu_limit,omitempty"`
	MemoryLimit        string `json:"memory_limit,omitempty"`
	StorageLimit       string `json:"storage_limit,omitempty"`
}

type ProjectDTO struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	CpuLimit     string    `json:"cpu_limit"`
	MemoryLimit  string    `json:"memory_limit"`
	StorageLimit string    `json:"storage_limit"`
	Status       string    `json:"status"`
	CurrentPhase string    `json:"current_phase"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type BootstrapResponse struct {
	Created        bool   `json:"created"`
	InternalUserID string `json:"internal_user_id"`
	// KeycloakUserID : usage interne, jamais exposé (annuaire invisible).
	KeycloakUserID   string      `json:"-"`
	Email            string      `json:"email,omitempty"`
	FullName         string      `json:"full_name,omitempty"`
	PhoneNumber      string      `json:"phone_number,omitempty"`
	Country          string      `json:"country,omitempty"`
	Role             string      `json:"role"`
	MembershipStatus string      `json:"membership_status"`
	Project          *ProjectDTO `json:"project,omitempty"`
}

type BootstrapStatusResponse struct {
	Bootstrapped      bool    `json:"bootstrapped"`
	RequiresBootstrap bool    `json:"requires_bootstrap"`
	InternalUserID    *string `json:"internal_user_id,omitempty"`
	// KeycloakUserID : usage interne, jamais exposé (annuaire invisible).
	KeycloakUserID   string      `json:"-"`
	Email            string      `json:"email,omitempty"`
	FullName         string      `json:"full_name,omitempty"`
	PhoneNumber      string      `json:"phone_number,omitempty"`
	Country          string      `json:"country,omitempty"`
	Role             string      `json:"role,omitempty"`
	MembershipStatus string      `json:"membership_status,omitempty"`
	ProjectID        *string     `json:"project_id,omitempty"`
	Project          *ProjectDTO `json:"project,omitempty"`
}

// ── Login / Logout / Refresh ──────────────────────────────────────────────

type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type RefreshTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

type LogoutRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
	// AccessToken rempli par le handler depuis le header Authorization (pas du body).
	AccessToken string `json:"-"`
}

type LogoutResponse struct{}

type TokenRequest struct{}

type TokenResponse struct {
	Valid  bool   `json:"valid"`
	UserID string `json:"user_id,omitempty"`
	Email  string `json:"email,omitempty"`
}

// ── Password ──────────────────────────────────────────────────────────────

type ForgotPasswordRequest struct {
	Email string `json:"email" binding:"required,email"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required,min=8"`
}

// ── Erreur commune ────────────────────────────────────────────────────────

type ErrorResponse struct {
	Error   string      `json:"error"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

type NoBody struct{}
