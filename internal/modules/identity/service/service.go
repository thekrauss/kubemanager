package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/thekrauss/kubemanager/internal/auth"
	"github.com/thekrauss/kubemanager/internal/infrastructure/mail"

	"github.com/thekrauss/kubemanager/internal/core/cache"
	"github.com/thekrauss/kubemanager/internal/modules/auth/domain"
	authrepos "github.com/thekrauss/kubemanager/internal/modules/auth/repository"
	keycloakadmin "github.com/thekrauss/kubemanager/internal/modules/identity/keycloak"
	identitytypes "github.com/thekrauss/kubemanager/internal/modules/identity/types"
	projectsTypes "github.com/thekrauss/kubemanager/internal/modules/projects/domain"
	projectsSvc "github.com/thekrauss/kubemanager/internal/modules/projects/service"
)

var _ = cache.ErrCacheMiss // évite la suppression auto de l'import si plus utilisé directement

var (
	ErrIdentityServiceUnavailable = errors.New("identity service unavailable")
	ErrRegisterInputInvalid       = errors.New("invalid register payload")
	ErrBootstrapInputInvalid      = errors.New("invalid bootstrap payload")
	ErrBootstrapProjectRequired   = errors.New("project_name is required for initial bootstrap")
	ErrEmailNotVerified           = errors.New("email address not verified — check your inbox")
	ErrInvalidState               = errors.New("invalid or expired oauth state token")
	ErrUnknownProvider            = errors.New("unknown social provider — accepted: google, github")
	ErrSubjectInvalid             = errors.New("keycloak user id (sub) is missing or invalid")
	ErrMFANotConfigured           = errors.New("two-factor authentication is not configured on this platform")
	ErrDeviceLoginDisabled        = errors.New("CLI login (device flow) is not enabled on this platform")
	ErrCLIRedirectURI             = errors.New("CLI redirect_uri must be http://127.0.0.1:<port>/callback")
)

// validProviders : nom exposé → alias IDP configuré dans Keycloak (voir bootstrap_platform.sh).
var validProviders = map[string]string{
	"google": "google",
	"github": "github",
}

type BootstrapPrincipal struct {
	KeycloakUserID string
	Email          string
}

// LoginResult combine la token pair Keycloak + état de provisioning local.
type LoginResult struct {
	AccessToken    string
	RefreshToken   string
	ExpiresIn      int64
	KeycloakUserID string
	Email          string
	AvatarURL      string
	Bootstrapped   bool
}

type Service interface {
	// Register / bootstrap
	Bootstrap(ctx context.Context, principal BootstrapPrincipal, req identitytypes.BootstrapRequest) (*identitytypes.BootstrapResponse, error)
	BootstrapStatus(ctx context.Context, principal BootstrapPrincipal) (*identitytypes.BootstrapStatusResponse, error)

	// Auth classique
	Logout(ctx context.Context, refreshToken string) error
	RefreshToken(ctx context.Context, refreshToken string) (*keycloakadmin.TokenPair, error)

	// Authorization Code + PKCE : login hébergé Keycloak, ou social (kc_idp_hint).
	// Les deux reviennent sur HandleSocialCallback.
	// StartDeviceLogin / PollDeviceLogin : connexion de la CLI (RFC 8628).
	StartDeviceLogin(ctx context.Context) (*keycloakadmin.DeviceAuthorization, error)
	PollDeviceLogin(ctx context.Context, deviceCode string) (*keycloakadmin.TokenPair, error)
	// LoginURL : URL de la page de connexion Keycloak (voir LoginOptions).
	LoginURL(ctx context.Context, redirectURI string, opts LoginOptions) (string, error)
	SocialLoginURL(ctx context.Context, provider, redirectURI string) (string, error)
	HandleSocialCallback(ctx context.Context, code, state, redirectURI string) (*LoginResult, error)

	// Authentification gérée par betorun (docs/architecture/auth.md).
	PasswordLogin(ctx context.Context, in identitytypes.PasswordLoginRequest, cl Client) (*identitytypes.AuthResponse, error)
	MFALogin(ctx context.Context, in identitytypes.MFALoginRequest, cl Client) (*identitytypes.AuthResponse, error)
	RefreshSession(ctx context.Context, refreshToken string) (*identitytypes.RefreshTokenResponse, error)
	EndSession(ctx context.Context, refreshToken string) error
	StepUp(ctx context.Context, userID, sessionID uuid.UUID, code string) (*identitytypes.AuthResponse, error)
	ListSessions(ctx context.Context, userID, current uuid.UUID) ([]auth.Session, error)
	RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error
	RevokeOtherSessions(ctx context.Context, userID, current uuid.UUID) error

	MFAStatus(ctx context.Context, userID uuid.UUID) (*auth.FactorStatus, error)
	StartTOTP(ctx context.Context, userID uuid.UUID, account string) (*auth.TOTPEnrollment, error)
	ConfirmTOTP(ctx context.Context, userID, sessionID uuid.UUID, code string) (*identitytypes.RecoveryCodesResponse, error)
	DisableTOTP(ctx context.Context, userID, sessionID uuid.UUID) error
	RegenerateRecoveryCodes(ctx context.Context, userID, sessionID uuid.UUID) (*identitytypes.RecoveryCodesResponse, error)

	RegisterAccount(ctx context.Context, req identitytypes.RegisterRequest, cl Client) (*identitytypes.RegisterResponse, error)
	VerifyEmail(ctx context.Context, token string) error
	ResendVerification(ctx context.Context, email string, cl Client) error
	RequestPasswordReset(ctx context.Context, email string, cl Client) error
	CompletePasswordReset(ctx context.Context, token, password string) error
	ChangePassword(ctx context.Context, subject string, sessionID uuid.UUID, current, next string) error
}

// StateStore : store distribué pour le state CSRF OAuth (fix H9).
//
// Doit être Redis en prod : le flow social login commence sur pod A
// (/social/google → set state) et revient sur pod B (/social/callback
// → get state). Ristretto in-memory ne peut pas partager entre pods.
//
// Interface minimale pour permettre le mock en tests.
type StateStore interface {
	SetValue(ctx context.Context, key, value string, ttl time.Duration) error
	GetValue(ctx context.Context, key string) (string, error)
	DeleteValue(ctx context.Context, key string) error
}

type Deps struct {
	Keycloak            keycloakadmin.KeycloakClient
	Users               authrepos.AuthRepository
	Projects            projectsSvc.IProjectService
	ProjectMembers      authrepos.AuthRepository
	OIDCClientID        string
	ActionEmailClientID string // client_id public pour les liens de reset/verify email
	FrontendBaseURL     string
	SocialRedirectURI   string          // redirect URI par défaut pour les flux sociaux
	Cache               cache.AuthCache // legacy — conservé pour compat (non utilisé pour state)
	StateStore          StateStore      // store distribué du state CSRF (fix H9 : Redis)
	StepUpACR           string          // acr_values demandé pour un step-up (ex. "gold") ; vide : MFA non configuré
	CLIClientID         string          // client Keycloak public de la CLI (flux device) ; vide : désactivé

	// Authentification gérée par betorun (docs/architecture/auth.md).
	Sessions    *auth.Sessions
	Factors     *auth.Factors
	EmailTokens *auth.EmailTokens
	Mailer      mail.Sender
	Limiter     Limiter
	Logger      *zap.SugaredLogger
}

type service struct {
	keycloak            keycloakadmin.KeycloakClient
	users               authrepos.AuthRepository
	projects            projectsSvc.IProjectService
	projectMembers      authrepos.AuthRepository
	oidcClientID        string
	actionEmailClientID string
	frontendBaseURL     string
	socialRedirectURI   string
	cache               cache.AuthCache
	stateStore          StateStore
	stepUpACR           string
	cliClientID         string
	sessions            *auth.Sessions
	factors             *auth.Factors
	emailTokens         *auth.EmailTokens
	mailer              mail.Sender
	limiter             Limiter
	logger              *zap.SugaredLogger
}

func NewService(deps Deps) Service {
	return &service{
		keycloak:            deps.Keycloak,
		users:               deps.Users,
		projects:            deps.Projects,
		projectMembers:      deps.ProjectMembers,
		oidcClientID:        strings.TrimSpace(deps.OIDCClientID),
		actionEmailClientID: strings.TrimSpace(deps.ActionEmailClientID),
		frontendBaseURL:     strings.TrimSpace(deps.FrontendBaseURL),
		socialRedirectURI:   strings.TrimSpace(deps.SocialRedirectURI),
		cache:               deps.Cache,
		stateStore:          deps.StateStore,
		stepUpACR:           strings.TrimSpace(deps.StepUpACR),
		cliClientID:         strings.TrimSpace(deps.CLIClientID),
		sessions:            deps.Sessions,
		factors:             deps.Factors,
		emailTokens:         deps.EmailTokens,
		mailer:              deps.Mailer,
		limiter:             deps.Limiter,
		logger:              deps.Logger,
	}
}

func (s *service) Bootstrap(ctx context.Context, principal BootstrapPrincipal, req identitytypes.BootstrapRequest) (*identitytypes.BootstrapResponse, error) {
	if s == nil || s.keycloak == nil {
		return nil, ErrIdentityServiceUnavailable
	}
	keycloakUserID := strings.TrimSpace(principal.KeycloakUserID)
	if keycloakUserID == "" {
		return nil, ErrBootstrapInputInvalid
	}
	keycloakUser, err := s.keycloak.GetUserByID(ctx, keycloakUserID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	email := firstNonEmpty(normalizeEmail(principal.Email), normalizeEmail(keycloakUser.Email))
	fullName := firstNonEmpty(strings.TrimSpace(req.FullName), strings.TrimSpace(keycloakUser.FullName), deriveDisplayName(email))
	phoneNumber := firstNonEmpty(strings.TrimSpace(req.PhoneNumber), strings.TrimSpace(keycloakUser.PhoneNumber))
	country := normalizeCountry(req.Country)
	user, err := s.users.FindByKeycloakUserID(ctx, keycloakUserID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	created := false
	if user == nil {
		user, err = s.createBootstrapUser(ctx, keycloakUserID, email, fullName, phoneNumber, country, now)
		if err != nil {
			return nil, err
		}
		created = true
	} else {
		mutated, err := s.repairBootstrapUser(ctx, user, email, fullName, phoneNumber, country, now)
		if err != nil {
			return nil, err
		}
		created = created || mutated
	}
	member, project, memberCreated, err := s.ensureProject(ctx, user, req, now)
	if err != nil {
		return nil, err
	}
	created = created || memberCreated
	_ = s.ensureKeycloakRole(ctx, keycloakUserID)
	roleName, membershipStatus := "", ""
	if member != nil {
		roleName = member.Role.Name
		membershipStatus = member.Status
	}
	return &identitytypes.BootstrapResponse{
		Created: created, InternalUserID: user.ID.String(), KeycloakUserID: keycloakUserID,
		Email: user.Email, FullName: user.FullName, PhoneNumber: derefString(user.PhoneNumber),
		Country: user.Country, Role: roleName, MembershipStatus: membershipStatus, Project: project,
	}, nil
}

func (s *service) BootstrapStatus(ctx context.Context, principal BootstrapPrincipal) (*identitytypes.BootstrapStatusResponse, error) {
	if s == nil || s.keycloak == nil {
		return nil, ErrIdentityServiceUnavailable
	}
	keycloakUserID := strings.TrimSpace(principal.KeycloakUserID)
	if keycloakUserID == "" {
		return nil, ErrBootstrapInputInvalid
	}
	keycloakUser, err := s.keycloak.GetUserByID(ctx, keycloakUserID)
	if err != nil {
		return nil, err
	}
	email := firstNonEmpty(normalizeEmail(principal.Email), normalizeEmail(keycloakUser.Email))
	status := &identitytypes.BootstrapStatusResponse{
		Bootstrapped: false, RequiresBootstrap: true, KeycloakUserID: keycloakUserID, Email: email,
		FullName: strings.TrimSpace(keycloakUser.FullName), PhoneNumber: strings.TrimSpace(keycloakUser.PhoneNumber),
	}
	user, err := s.users.FindByKeycloakUserID(ctx, keycloakUserID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	// Pas encore de profil local (première visite) : le dépôt renvoie nil, nil.
	if user == nil {
		return status, nil
	}
	internalUserID := user.ID.String()
	status.InternalUserID = &internalUserID
	status.Email = firstNonEmpty(user.Email, status.Email)
	status.FullName = firstNonEmpty(strings.TrimSpace(user.FullName), status.FullName)
	status.PhoneNumber = firstNonEmpty(derefString(user.PhoneNumber), status.PhoneNumber)
	status.Country = user.Country
	status.Role = user.Role.String()
	member, err := s.projectMembers.FindFirstActiveByUser(ctx, user.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return status, nil
		}
		return nil, err
	}
	projectID := member.ProjectID.String()
	status.ProjectID = &projectID
	status.MembershipStatus = member.Status
	status.Role = member.Role.Name
	if member.Status == domain.ProjectMemberStatusActive || member.Status == domain.ProjectMemberStatusApproved {
		status.Bootstrapped = true
		status.RequiresBootstrap = false
	}
	project, err := s.projects.GetProjectStatus(ctx, member.ProjectID.String())
	if err == nil {
		status.Project = mapProjectStatusToDTO(project)
	}
	return status, nil
}

func (s *service) createBootstrapUser(ctx context.Context, keycloakUserID, email, fullName, phoneNumber, country string, now time.Time) (*domain.User, error) {
	user := &domain.User{ID: uuid.New(), KeycloakUserID: keycloakUserID, Email: email, FullName: fullName,
		PhoneNumber: optionalString(phoneNumber), Country: country, Role: domain.RoleTypes.User,
		Status: "active", IsActive: true, IsVerified: true, CreatedAt: now, UpdatedAt: now}
	if err := s.users.CreateUser(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

func (s *service) repairBootstrapUser(ctx context.Context, user *domain.User, email, fullName, phoneNumber, country string, now time.Time) (bool, error) {
	if user == nil {
		return false, errors.New("user is required")
	}
	mutated := false
	if email != "" && !strings.EqualFold(user.Email, email) {
		user.Email = email
		mutated = true
	}
	if fullName != "" && strings.TrimSpace(user.FullName) != fullName {
		user.FullName = fullName
		mutated = true
	}
	if phoneNumber != "" && derefString(user.PhoneNumber) != phoneNumber {
		user.PhoneNumber = optionalString(phoneNumber)
		mutated = true
	}
	if country != "" && user.Country != country {
		user.Country = country
		mutated = true
	}
	if user.Role.IsZero() {
		user.Role = domain.RoleTypes.User
		mutated = true
	}
	if user.Status != "active" {
		user.Status = "active"
		mutated = true
	}
	if !user.IsActive {
		user.IsActive = true
		mutated = true
	}
	user.LastLoginAt = &now
	if mutated {
		user.UpdatedAt = now
		if err := s.users.UpdateUser(ctx, user); err != nil {
			return false, err
		}
	}
	return mutated, nil
}

func (s *service) ensureProject(ctx context.Context, user *domain.User, req identitytypes.BootstrapRequest, now time.Time) (*domain.ProjectMember, *identitytypes.ProjectDTO, bool, error) {
	member, err := s.projectMembers.FindFirstActiveByUser(ctx, user.ID)
	if err == nil {
		ps, err := s.projects.GetProjectStatus(ctx, member.ProjectID.String())
		if err != nil {
			return member, nil, false, err
		}
		return member, mapProjectStatusToDTO(ps), false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, false, err
	}
	projectName := strings.TrimSpace(req.ProjectName)
	if projectName == "" {
		return nil, nil, false, ErrBootstrapProjectRequired
	}
	projectResp, err := s.projects.CreateProject(ctx, projectsTypes.CreateProjectRequest{
		Name: projectName, Description: strings.TrimSpace(req.ProjectDescription),
		CpuLimit: strings.TrimSpace(req.CpuLimit), MemoryLimit: strings.TrimSpace(req.MemoryLimit),
	}, user.ID.String())
	if err != nil {
		return nil, nil, false, err
	}
	projectID, err := uuid.Parse(strings.TrimSpace(projectResp.ProjectID))
	if err != nil {
		return nil, nil, false, err
	}
	member = &domain.ProjectMember{ProjectID: projectID, UserID: user.ID,
		Status: domain.ProjectMemberStatusActive, JoinedAt: now, CreatedAt: now, UpdatedAt: now,
		Role: domain.Role{Name: domain.RoleTypes.Owner.String()}}
	if err := s.projectMembers.Upsert(ctx, member); err != nil {
		return nil, nil, false, err
	}
	dto := &identitytypes.ProjectDTO{ID: projectResp.ProjectID, Name: projectName,
		Description: strings.TrimSpace(req.ProjectDescription), CpuLimit: strings.TrimSpace(req.CpuLimit),
		MemoryLimit: strings.TrimSpace(req.MemoryLimit), Status: projectResp.Status, CreatedAt: now, UpdatedAt: now}
	return member, dto, true, nil
}

func (s *service) ensureKeycloakRole(ctx context.Context, keycloakUserID string) error {
	if s.oidcClientID == "" {
		return nil
	}
	return s.keycloak.AssignClientRole(ctx, keycloakUserID, s.oidcClientID, domain.RoleTypes.Owner.String())
}

func mapProjectStatusToDTO(p *projectsTypes.ProjectStatusResponse) *identitytypes.ProjectDTO {
	if p == nil {
		return nil
	}
	return &identitytypes.ProjectDTO{ID: p.ProjectID, Status: p.Status, CurrentPhase: p.Phase, CreatedAt: p.CreatedAt}
}

func optionalString(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}
func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(*v)
}
func normalizeEmail(v string) string   { return strings.ToLower(strings.TrimSpace(v)) }
func normalizeCountry(v string) string { return strings.ToUpper(strings.TrimSpace(v)) }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func deriveDisplayName(email string) string {
	email = normalizeEmail(email)
	if email == "" {
		return "User"
	}
	if idx := strings.Index(email, "@"); idx > 0 {
		email = email[:idx]
	}
	email = strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(email)
	parts := strings.Fields(email)
	if len(parts) == 0 {
		return "User"
	}
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		}
	}
	return strings.Join(parts, " ")
}
