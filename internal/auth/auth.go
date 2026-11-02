// Package auth : authentification des personnes gérée par betorun
// (docs/architecture/auth.md). Keycloak reste l'annuaire (comptes, mots de
// passe) derrière le module identity ; ce paquet tient les sessions, le
// second facteur et les liens envoyés par email. Aucun jeton ni secret n'y
// est stocké en clair.
package auth

import (
	"errors"
	"time"
)

// Durées de vie.
const (
	// AccessTokenTTL : jeton d'accès (JWT signé par le STS).
	AccessTokenTTL = 10 * time.Minute
	// WebSessionTTL / CLISessionTTL : durée maximale d'une session, même active.
	WebSessionTTL = 30 * 24 * time.Hour
	CLISessionTTL = 90 * 24 * time.Hour
	// WebIdleTTL / CLIIdleTTL : session abandonnée (jeton de rafraîchissement inutilisé).
	WebIdleTTL = 7 * 24 * time.Hour
	CLIIdleTTL = 30 * 24 * time.Hour
	// RefreshGrace : un jeton de rafraîchissement tout juste remplacé reste
	// accepté ce temps-là (requêtes simultanées d'un même navigateur), sans
	// nouvelle rotation ; au-delà, sa réutilisation révoque la session.
	RefreshGrace = 20 * time.Second
	// RecentMFA : fraîcheur exigée pour gérer ses facteurs (comme le step-up).
	RecentMFA = 10 * time.Minute
)

// Types de session.
const (
	KindWeb = "web"
	KindCLI = "cli"
)

// Méthodes d'authentification (claim amr, RFC 8176).
const (
	AMRPassword = "pwd"
	AMROTP      = "otp"
)

// Niveaux d'authentification (claim acr), alignés sur iam.mfa_acr_values.
const (
	ACRSilver = "silver"
	ACRGold   = "gold"
)

// Préfixe des jetons de rafraîchissement (reconnaissables, jamais confondus
// avec ceux de l'ancien fournisseur pendant la transition).
const RefreshTokenPrefix = "btr_rt_"

var (
	ErrSessionNotFound  = errors.New("session not found")
	ErrRefreshInvalid   = errors.New("your session has expired, sign in again")
	ErrRefreshReused    = errors.New("this session was closed for your security, sign in again")
	ErrInvalidCode      = errors.New("invalid verification code")
	ErrNoFactor         = errors.New("two-factor authentication is not set up")
	ErrFactorExists     = errors.New("two-factor authentication is already set up")
	ErrRecentMFA        = errors.New("confirm your identity with a verification code first")
	ErrEmailLinkInvalid = errors.New("this link is invalid or has expired")
)
