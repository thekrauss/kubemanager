package types

import "time"

type RegisterRequest struct {
	Email       string `json:"email" validate:"required,email"`
	Password    string `json:"password" validate:"required,min=8"`
	FullName    string `json:"full_name" validate:"required"`
	PhoneNumber string `json:"phone_number,omitempty"`
}

type RegisterResponse struct {
	KeycloakUserID    string    `json:"keycloak_user_id"`
	Email             string    `json:"email"`
	FullName          string    `json:"full_name,omitempty"`
	PhoneNumber       string    `json:"phone_number,omitempty"`
	RequiresBootstrap bool      `json:"requires_bootstrap"`
	CreatedAt         time.Time `json:"created_at"`
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
	Created          bool        `json:"created"`
	InternalUserID   string      `json:"internal_user_id"`
	KeycloakUserID   string      `json:"keycloak_user_id"`
	Email            string      `json:"email,omitempty"`
	FullName         string      `json:"full_name,omitempty"`
	PhoneNumber      string      `json:"phone_number,omitempty"`
	Country          string      `json:"country,omitempty"`
	Role             string      `json:"role"`
	MembershipStatus string      `json:"membership_status"`
	Project          *ProjectDTO `json:"project,omitempty"`
}

type BootstrapStatusResponse struct {
	Bootstrapped      bool        `json:"bootstrapped"`
	RequiresBootstrap bool        `json:"requires_bootstrap"`
	InternalUserID    *string     `json:"internal_user_id,omitempty"`
	KeycloakUserID    string      `json:"keycloak_user_id"`
	Email             string      `json:"email,omitempty"`
	FullName          string      `json:"full_name,omitempty"`
	PhoneNumber       string      `json:"phone_number,omitempty"`
	Country           string      `json:"country,omitempty"`
	Role              string      `json:"role,omitempty"`
	MembershipStatus  string      `json:"membership_status,omitempty"`
	ProjectID         *string     `json:"project_id,omitempty"`
	Project           *ProjectDTO `json:"project,omitempty"`
}

// ── Login / Logout / Refresh ──────────────────────────────────────────────

type LoginRequest struct {
	Email    string `json:"email"    binding:"required" description:"Email de l'utilisateur"`
	Password string `json:"password" binding:"required" description:"Mot de passe"`
}

type LoginResponse struct {
	AccessToken    string `json:"access_token"      description:"JWT d'accès Keycloak"`
	RefreshToken   string `json:"refresh_token"     description:"Jeton de rafraîchissement"`
	ExpiresIn      int64  `json:"expires_in"        description:"Durée de validité (secondes)"`
	KeycloakUserID string `json:"keycloak_user_id,omitempty"`
	Email          string `json:"email,omitempty"`
	Bootstrapped   bool   `json:"bootstrapped"      description:"True si le user local est provisionné"`
}

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
	Valid          bool   `json:"valid"`
	KeycloakUserID string `json:"keycloak_user_id,omitempty"`
	Email          string `json:"email,omitempty"`
}

// ── Password ──────────────────────────────────────────────────────────────

type ForgotPasswordRequest struct {
	Email string `json:"email" binding:"required,email"`
}

type ChangePasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

// ── Login hébergé Keycloak (Authorization Code + PKCE) ────────────────────

type LoginURLQuery struct {
	RedirectURI string `query:"redirect_uri" description:"Redirect URI absolue (optionnelle si fournie via config)"`
	StepUp      bool   `query:"step_up" description:"Ré-authentification avec double authentification (réponse à STEP_UP_REQUIRED)"`
	Client      string `query:"client" description:"cli : connexion de la CLI (redirect_uri http://127.0.0.1:<port>/callback)"`
}

type LoginURLResponse struct {
	URL string `json:"url" description:"URL Keycloak vers laquelle rediriger l'utilisateur"`
}

// ── Social login (Google, GitHub) ─────────────────────────────────────────

type SocialLoginURLQuery struct {
	Provider    string `path:"provider"     binding:"required" description:"google | github"`
	RedirectURI string `query:"redirect_uri" description:"Redirect URI absolue (optionnelle si fournie via config)"`
}

type SocialLoginURLResponse struct {
	URL      string `json:"url"      description:"URL Keycloak vers laquelle rediriger l'utilisateur"`
	Provider string `json:"provider" description:"Nom du provider (google, github)"`
}

type SocialCallbackQuery struct {
	Code        string `query:"code"         binding:"required" description:"Code OAuth2 retourné par Keycloak"`
	State       string `query:"state"        binding:"required" description:"State CSRF à valider"`
	RedirectURI string `query:"redirect_uri" binding:"required" description:"Redirect URI utilisée à l'étape précédente"`
}

// ── Erreur commune ────────────────────────────────────────────────────────

type ErrorResponse struct {
	Error   string      `json:"error"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

type NoBody struct{}
