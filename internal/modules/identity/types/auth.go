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
	Code      string `json:"code" binding:"required" desc:"Code de l'application d'authentification ou code de secours"`
}

// AuthResponse : session ouverte, ou second facteur demandé.
type AuthResponse struct {
	Status       string    `json:"status" desc:"authenticated | mfa_required"`
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
