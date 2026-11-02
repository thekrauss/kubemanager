package types

import "time"

// Authentification gérée par betorun (docs/architecture/auth.md). Aucun
// type ci-dessous ne contient d'information de l'annuaire d'identité.

// Statut d'une tentative de connexion.
const (
	AuthStatusAuthenticated = "authenticated"
	AuthStatusMFARequired   = "mfa_required"
)

type PasswordLoginRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
	// Client : "cli" pour une session de la CLI (plus longue) ; défaut : navigateur.
	Client string `json:"client,omitempty"`
}

type MFALoginRequest struct {
	Challenge string `json:"challenge" binding:"required"`
	Code      string `json:"code" binding:"required" description:"Code de l'application d'authentification ou code de secours"`
}

// AuthResponse : session ouverte, ou second facteur demandé.
type AuthResponse struct {
	Status       string    `json:"status" description:"authenticated | mfa_required"`
	AccessToken  string    `json:"access_token,omitempty"`
	ExpiresIn    int64     `json:"expires_in,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	// MFA : session vérifiée avec un second facteur.
	MFA bool `json:"mfa"`
	// Challenge : à renvoyer avec le code (POST /identity/login/mfa), 5 min.
	Challenge string `json:"challenge,omitempty"`
}

type StepUpRequest struct {
	Code string `json:"code" binding:"required"`
}

type TOTPConfirmRequest struct {
	Code string `json:"code" binding:"required"`
}

// RecoveryCodesResponse : codes de secours, affichés une seule fois.
type RecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
	// Session passée au niveau « second facteur vérifié » (activation).
	AccessToken string `json:"access_token,omitempty"`
	ExpiresIn   int64  `json:"expires_in,omitempty"`
}

type VerifyEmailRequest struct {
	Token string `json:"token" binding:"required"`
}

type ResendVerificationRequest struct {
	Email string `json:"email" binding:"required"`
}

type ResetPasswordRequest struct {
	Token    string `json:"token" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type SessionPath struct {
	ID string `path:"id" binding:"required"`
}

// AcceptedResponse : demande prise en compte (réponse identique que le
// compte existe ou non : rien n'est révélé).
type AcceptedResponse struct {
	Accepted bool `json:"accepted"`
}

// ── Connexion de la CLI (docs/architecture/auth.md §9) ──

type URLResponse struct {
	URL string `json:"url"`
}

// CLIAuthorizeQuery : page de la console où autoriser la CLI. La CLI génère
// elle-même son PKCE : le vérificateur ne quitte jamais la machine.
type CLIAuthorizeQuery struct {
	RedirectURI   string `query:"redirect_uri" binding:"required" description:"http://127.0.0.1:<port>/callback"`
	CodeChallenge string `query:"code_challenge" binding:"required" description:"PKCE S256"`
	State         string `query:"state" binding:"required"`
}

type CLIAuthorizeRequest struct {
	RedirectURI   string `json:"redirect_uri" binding:"required"`
	CodeChallenge string `json:"code_challenge" binding:"required"`
	State         string `json:"state" binding:"required"`
}

type CLITokenRequest struct {
	Code         string `json:"code" binding:"required"`
	CodeVerifier string `json:"code_verifier" binding:"required"`
	RedirectURI  string `json:"redirect_uri" binding:"required"`
}

// DeviceCodeResponse : RFC 8628 §3.2.
type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type DeviceCodeQuery struct {
	UserCode string `query:"user_code" binding:"required"`
}

// DeviceInfo : origine de la demande, montrée avant validation (hameçonnage).
type DeviceInfo struct {
	UserCode  string    `json:"user_code"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	CreatedAt time.Time `json:"created_at"`
}

type DeviceApproveRequest struct {
	UserCode string `json:"user_code" binding:"required"`
	Approve  bool   `json:"approve"`
}

// ── Connexion Google / GitHub ──

type SocialProviderPath struct {
	Provider string `path:"provider" binding:"required" description:"google | github"`
}

type SocialCallbackRequest struct {
	Provider string `path:"provider" binding:"required"`
	Code     string `json:"code" binding:"required"`
	State    string `json:"state" binding:"required"`
}

type SocialProvidersResponse struct {
	Providers []string `json:"providers"`
}
