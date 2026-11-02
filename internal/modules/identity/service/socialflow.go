package services

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/audit"
	"github.com/thekrauss/kubemanager/internal/auth"
	"github.com/thekrauss/kubemanager/internal/auth/social"
	keycloakadmin "github.com/thekrauss/kubemanager/internal/modules/identity/keycloak"
	identitytypes "github.com/thekrauss/kubemanager/internal/modules/identity/types"
)

// Connexion avec Google ou GitHub (docs/architecture/auth.md §8) : betorun
// parle directement au fournisseur ; la page de retour est celle de la
// console (/auth/callback/<fournisseur>).

const (
	pageSocialCallback = "/auth/callback/"
	socialStateTTL     = 10 * time.Minute
)

var (
	ErrSocialUnavailable = errors.New("this sign-in method is not available")
	ErrLastSignInMethod  = errors.New("keep at least one way to sign in: set a password first")
)

type socialState struct {
	Provider string `json:"provider"`
	Verifier string `json:"verifier"`
	Nonce    string `json:"nonce"`
	Client   string `json:"client,omitempty"`
}

func (s *service) provider(name string) (social.Provider, error) {
	if err := s.authReady(); err != nil {
		return nil, err
	}
	p, ok := s.socialProviders[name]
	if !ok || s.identities == nil || s.frontendBaseURL == "" {
		return nil, ErrSocialUnavailable
	}
	return p, nil
}

func (s *service) socialRedirect(provider string) string {
	return s.consoleLink(pageSocialCallback+provider, "")
}

// SocialProviders : fournisseurs configurés (boutons affichés par la console).
func (s *service) SocialProviders() []string {
	out := make([]string, 0, len(s.socialProviders))
	for _, name := range []string{social.Google, social.GitHub} {
		if _, ok := s.socialProviders[name]; ok {
			out = append(out, name)
		}
	}
	return out
}

// SocialAuthorizeURL : page du fournisseur ; state, PKCE et nonce gardés
// côté serveur (usage unique, 10 min).
func (s *service) SocialAuthorizeURL(ctx context.Context, name string) (*identitytypes.URLResponse, error) {
	p, err := s.provider(name)
	if err != nil {
		return nil, err
	}
	state, err := social.NewState()
	if err != nil {
		return nil, err
	}
	nonce, err := social.NewState()
	if err != nil {
		return nil, err
	}
	verifier, challenge, err := social.NewPKCE()
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(socialState{Provider: name, Verifier: verifier, Nonce: nonce})
	if err := s.stateStore.SetValue(ctx, "auth:social:"+digest(state), string(raw), socialStateTTL); err != nil {
		return nil, ErrAuthUnavailable
	}
	return &identitytypes.URLResponse{URL: p.AuthCodeURL(state, challenge, nonce, s.socialRedirect(name))}, nil
}

// SocialCallback : code du fournisseur contre une session betorun (ou une
// demande de second facteur).
func (s *service) SocialCallback(ctx context.Context, name string, in identitytypes.SocialCallbackRequest, cl Client) (*identitytypes.AuthResponse, error) {
	p, err := s.provider(name)
	if err != nil {
		return nil, err
	}
	if err := s.limit(ctx, "login:ip:"+cl.IP, loginPerIP, loginWindow); err != nil {
		return nil, err
	}
	key := "auth:social:" + digest(in.State)
	raw, err := s.stateStore.GetValue(ctx, key)
	if err != nil || raw == "" {
		return nil, ErrMFAChallengeExpired
	}
	_ = s.stateStore.DeleteValue(ctx, key) // usage unique
	var st socialState
	if err := json.Unmarshal([]byte(raw), &st); err != nil || st.Provider != name {
		return nil, ErrMFAChallengeExpired
	}
	profile, err := p.Exchange(ctx, in.Code, st.Verifier, st.Nonce, s.socialRedirect(name))
	if err != nil {
		audit.Annotate(ctx, "social", "provider_refused")
		return nil, err
	}
	if profile.Email == "" || !profile.EmailVerified {
		return nil, social.ErrEmailUnverified
	}
	userID, subject, err := s.resolveSocialUser(ctx, profile)
	if err != nil {
		return nil, err
	}
	audit.Annotate(ctx, "user", "user:"+userID.String())
	return s.completeLogin(ctx, userID, subject, []string{name}, auth.KindWeb, cl)
}

// resolveSocialUser : compte lié ; sinon compte existant de même adresse
// (attestée par le fournisseur) ; sinon nouveau compte.
func (s *service) resolveSocialUser(ctx context.Context, p *social.Profile) (uuid.UUID, string, error) {
	if id, err := s.identities.Find(ctx, p.Provider, p.Subject); err != nil {
		return uuid.Nil, "", err
	} else if id != uuid.Nil {
		user, err := s.users.GetUserByID(ctx, id)
		if err != nil || user == nil || !user.IsActive {
			return uuid.Nil, "", ErrInvalidCredentials
		}
		return id, user.KeycloakUserID, nil
	}

	kcUser, err := s.keycloak.FindUserByEmail(ctx, p.Email)
	switch {
	case err == nil:
		if !kcUser.Enabled {
			return uuid.Nil, "", ErrInvalidCredentials
		}
		if !kcUser.EmailVerified {
			// Compte jamais confirmé : peut-être créé par un tiers au nom de
			// la personne (pré-prise de contrôle). Son mot de passe et ses
			// sessions ne valent plus ; l'adresse est désormais prouvée.
			random, err := auth.RandomToken()
			if err != nil {
				return uuid.Nil, "", err
			}
			if err := s.keycloak.SetPassword(ctx, kcUser.ID, random+"-Aa1!"); err != nil {
				return uuid.Nil, "", err
			}
			if err := s.keycloak.MarkEmailVerified(ctx, kcUser.ID); err != nil {
				return uuid.Nil, "", err
			}
			audit.Annotate(ctx, "social", "unverified_account_secured")
		}
	case errors.Is(err, keycloakadmin.ErrUserNotFound):
		kcUser, err = s.keycloak.CreateUser(ctx, keycloakadmin.CreateUserInput{Email: p.Email, FullName: p.Name,
			Enabled: true, EmailVerified: true, WithoutPassword: true})
		if err != nil {
			return uuid.Nil, "", err
		}
		audit.Annotate(ctx, "social", "account_created")
	default:
		return uuid.Nil, "", err
	}
	user, err := s.localUser(ctx, kcUser.ID, p.Email, p.Name)
	if err != nil {
		return uuid.Nil, "", err
	}
	if !kcUser.EmailVerified {
		_ = s.sessions.RevokeAll(ctx, user.ID, uuid.Nil, "account_secured")
	}
	if err := s.identities.Link(ctx, user.ID, p.Provider, p.Subject, p.Email); err != nil {
		return uuid.Nil, "", err
	}
	audit.Annotate(ctx, "social", "linked")
	return user.ID, kcUser.ID, nil
}

// completeLogin : session ouverte, ou second facteur demandé s'il est actif.
func (s *service) completeLogin(ctx context.Context, userID uuid.UUID, subject string, amr []string, kind string, cl Client) (*identitytypes.AuthResponse, error) {
	hasFactor, err := s.factors.HasFactor(ctx, userID)
	if err != nil {
		return nil, err
	}
	if hasFactor {
		challenge, err := randomID()
		if err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(mfaChallenge{UserID: userID, Subject: subject, Kind: kind, AMR: amr})
		if err := s.stateStore.SetValue(ctx, "auth:mfa:"+challenge, string(raw), mfaChallengeTTL); err != nil {
			return nil, ErrAuthUnavailable
		}
		return &identitytypes.AuthResponse{Status: identitytypes.AuthStatusMFARequired, Challenge: challenge}, nil
	}
	t, err := s.sessions.Create(ctx, auth.NewSession{UserID: userID, Kind: kind, AMR: amr, IP: cl.IP, UserAgent: cl.UserAgent})
	if err != nil {
		return nil, err
	}
	if user, err := s.users.FindByKeycloakUserID(ctx, subject); err == nil && user != nil {
		s.touchLastLogin(ctx, user)
	}
	audit.Annotate(ctx, "login", "succeeded")
	return authResponse(t), nil
}

func (s *service) ListIdentities(ctx context.Context, userID uuid.UUID) ([]auth.LinkedIdentity, error) {
	if s.identities == nil {
		return []auth.LinkedIdentity{}, nil
	}
	return s.identities.List(ctx, userID)
}

// UnlinkIdentity : vérification récente exigée, et il doit rester un moyen
// de se connecter (mot de passe ou autre fournisseur).
func (s *service) UnlinkIdentity(ctx context.Context, userID, sessionID uuid.UUID, subject, provider string) error {
	if s.identities == nil {
		return auth.ErrIdentityNotFound
	}
	hasFactor, err := s.factors.HasFactor(ctx, userID)
	if err != nil {
		return err
	}
	if hasFactor {
		if err := s.requireRecentMFA(ctx, userID, sessionID); err != nil {
			return err
		}
	}
	linked, err := s.identities.List(ctx, userID)
	if err != nil {
		return err
	}
	others := 0
	for _, l := range linked {
		if l.Provider != provider {
			others++
		}
	}
	if others == 0 {
		hasPassword, err := s.keycloak.HasPassword(ctx, subject)
		if err != nil {
			return err
		}
		if !hasPassword {
			return ErrLastSignInMethod
		}
	}
	if err := s.identities.Unlink(ctx, userID, provider); err != nil {
		return err
	}
	audit.Annotate(ctx, "social", "unlinked")
	return nil
}
