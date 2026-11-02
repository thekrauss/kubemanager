package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/thekrauss/kubemanager/internal/audit"
	"github.com/thekrauss/kubemanager/internal/auth"
	identitytypes "github.com/thekrauss/kubemanager/internal/modules/identity/types"
)

// Connexion de la CLI gérée par betorun (docs/architecture/auth.md §9) :
//
//   - navigateur (RFC 8252 + PKCE) : la CLI ouvre la page /cli/authorize de
//     la console ; la personne, connectée, confirme ; un code à usage unique,
//     lié au PKCE et à l'adresse de retour 127.0.0.1, est échangé par la CLI ;
//   - code d'appareil (RFC 8628) : pour une machine sans navigateur, code à
//     saisir sur la page /device de la console.
//
// Avec un second facteur actif, la confirmation exige une vérification
// récente dans la session web (STEP_UP_REQUIRED sinon) : la session CLI
// ouverte est alors de niveau « second facteur vérifié ».

const (
	pageCLIAuthorize = "/cli/authorize"
	pageDevice       = "/device"

	cliCodeTTL        = 5 * time.Minute
	deviceTTL         = 10 * time.Minute
	deviceInterval    = 5 * time.Second
	deviceSlowDown    = 5 * time.Second
	deviceStartsPerIP = 20
	userCodeLength    = 8
	// userCodeAlphabet : sans voyelles ni caractères ambigus (RFC 8628 §6.1).
	userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"

	deviceStatusPending  = "pending"
	deviceStatusApproved = "approved"
	deviceStatusDenied   = "denied"
)

// Erreurs OAuth du flux par code d'appareil (RFC 8628 §3.5).
const (
	DeviceAuthorizationPending = "authorization_pending"
	DeviceSlowDown             = "slow_down"
	DeviceExpired              = "expired_token"
	DeviceAccessDenied         = "access_denied"
)

var (
	ErrInvalidPKCE       = errors.New("invalid code challenge")
	ErrCLICodeInvalid    = errors.New("this authorization has expired, run betorun login again")
	ErrDeviceCodeUnknown = errors.New("unknown or expired code, check the code shown in your terminal")
)

// DeviceError : réponse d'attente ou de refus du flux par code d'appareil.
type DeviceError struct{ Code string }

func (e *DeviceError) Error() string { return e.Code }

// pkceChallengeRe : S256, 32 octets en base64url sans bourrage (RFC 7636).
var pkceChallengeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type cliCode struct {
	UserID      uuid.UUID `json:"user_id"`
	MFA         bool      `json:"mfa"`
	Challenge   string    `json:"challenge"`
	RedirectURI string    `json:"redirect_uri"`
}

type deviceState struct {
	UserCode  string    `json:"user_code"`
	Status    string    `json:"status"`
	UserID    uuid.UUID `json:"user_id,omitempty"`
	MFA       bool      `json:"mfa,omitempty"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	CreatedAt time.Time `json:"created_at"`
	LastPoll  time.Time `json:"last_poll"`
}

func digest(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

func (s *service) cliReady() error {
	if err := s.authReady(); err != nil {
		return err
	}
	if s.stateStore == nil || s.frontendBaseURL == "" {
		return ErrAuthUnavailable
	}
	return nil
}

func validCLIRedirect(redirectURI, challenge string) error {
	if !cliRedirectRe.MatchString(strings.TrimSpace(redirectURI)) {
		return ErrCLIRedirectURI
	}
	if !pkceChallengeRe.MatchString(challenge) {
		return ErrInvalidPKCE
	}
	return nil
}

// CLIAuthorizeURL : page de la console où la personne autorise la CLI.
func (s *service) CLIAuthorizeURL(ctx context.Context, q identitytypes.CLIAuthorizeQuery) (*identitytypes.URLResponse, error) {
	if err := s.cliReady(); err != nil {
		return nil, err
	}
	if err := validCLIRedirect(q.RedirectURI, q.CodeChallenge); err != nil {
		return nil, err
	}
	v := url.Values{"redirect_uri": {q.RedirectURI}, "code_challenge": {q.CodeChallenge}, "state": {q.State}}
	return &identitytypes.URLResponse{URL: s.consoleLink(pageCLIAuthorize, "") + "?" + v.Encode()}, nil
}

// requireCLIConsent : avec un second facteur actif, vérification récente exigée.
func (s *service) requireCLIConsent(ctx context.Context, userID, sessionID uuid.UUID) (bool, error) {
	hasFactor, err := s.factors.HasFactor(ctx, userID)
	if err != nil || !hasFactor {
		return false, err
	}
	if err := s.requireRecentMFA(ctx, userID, sessionID); err != nil {
		return false, err
	}
	return true, nil
}

// AuthorizeCLI : la personne connectée autorise la CLI ; renvoie l'adresse
// de retour (127.0.0.1) avec le code à usage unique.
func (s *service) AuthorizeCLI(ctx context.Context, userID, sessionID uuid.UUID, in identitytypes.CLIAuthorizeRequest) (*identitytypes.URLResponse, error) {
	if err := s.cliReady(); err != nil {
		return nil, err
	}
	if err := validCLIRedirect(in.RedirectURI, in.CodeChallenge); err != nil {
		return nil, err
	}
	mfa, err := s.requireCLIConsent(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	code, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(cliCode{UserID: userID, MFA: mfa, Challenge: in.CodeChallenge, RedirectURI: in.RedirectURI})
	if err := s.stateStore.SetValue(ctx, "auth:cli:code:"+digest(code), string(raw), cliCodeTTL); err != nil {
		return nil, ErrAuthUnavailable
	}
	audit.Annotate(ctx, "cli", "authorized")
	v := url.Values{"code": {code}, "state": {in.State}}
	return &identitytypes.URLResponse{URL: in.RedirectURI + "?" + v.Encode()}, nil
}

// ExchangeCLICode : la CLI échange le code (preuve PKCE) contre une session.
func (s *service) ExchangeCLICode(ctx context.Context, in identitytypes.CLITokenRequest, cl Client) (*identitytypes.AuthResponse, error) {
	if err := s.cliReady(); err != nil {
		return nil, err
	}
	key := "auth:cli:code:" + digest(strings.TrimSpace(in.Code))
	raw, err := s.stateStore.GetValue(ctx, key)
	if err != nil || raw == "" {
		return nil, ErrCLICodeInvalid
	}
	_ = s.stateStore.DeleteValue(ctx, key) // usage unique, même en cas d'échec
	var c cliCode
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, ErrCLICodeInvalid
	}
	sum := sha256.Sum256([]byte(in.CodeVerifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(got), []byte(c.Challenge)) != 1 || in.RedirectURI != c.RedirectURI {
		audit.Annotate(ctx, "cli", "pkce_mismatch")
		return nil, ErrCLICodeInvalid
	}
	return s.openCLISession(ctx, c.UserID, c.MFA, cl)
}

func (s *service) openCLISession(ctx context.Context, userID uuid.UUID, mfa bool, cl Client) (*identitytypes.AuthResponse, error) {
	amr := []string{auth.AMRPassword}
	if mfa {
		amr = append(amr, auth.AMROTP)
	}
	t, err := s.sessions.Create(ctx, auth.NewSession{UserID: userID, Kind: auth.KindCLI, AMR: amr, MFA: mfa,
		IP: cl.IP, UserAgent: cl.UserAgent})
	if err != nil {
		return nil, err
	}
	audit.Annotate(ctx, "user", "user:"+userID.String())
	audit.Annotate(ctx, "login", "cli")
	return authResponse(t), nil
}

func newUserCode() (string, error) {
	b := make([]byte, userCodeLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, userCodeLength)
	for i, v := range b {
		out[i] = userCodeAlphabet[int(v)%len(userCodeAlphabet)]
	}
	return string(out[:4]) + "-" + string(out[4:]), nil
}

func normalizeUserCode(code string) string {
	code = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(code))
	if len(code) != userCodeLength {
		return ""
	}
	return code[:4] + "-" + code[4:]
}

// StartDevice : codes d'appareil (RFC 8628 §3.2) ; la demande garde l'IP et
// l'appareil d'origine, montrés à la personne avant qu'elle ne valide.
func (s *service) StartDevice(ctx context.Context, cl Client) (*identitytypes.DeviceCodeResponse, error) {
	if err := s.cliReady(); err != nil {
		return nil, err
	}
	if err := s.limit(ctx, "device:ip:"+cl.IP, deviceStartsPerIP, time.Hour); err != nil {
		return nil, err
	}
	deviceCode, err := auth.RandomToken()
	if err != nil {
		return nil, err
	}
	userCode, err := newUserCode()
	if err != nil {
		return nil, err
	}
	st := deviceState{UserCode: userCode, Status: deviceStatusPending, IP: cl.IP, UserAgent: cl.UserAgent, CreatedAt: time.Now()}
	if err := s.saveDevice(ctx, digest(deviceCode), st); err != nil {
		return nil, err
	}
	if err := s.stateStore.SetValue(ctx, "auth:device:user:"+userCode, digest(deviceCode), deviceTTL); err != nil {
		return nil, ErrAuthUnavailable
	}
	page := s.consoleLink(pageDevice, "")
	return &identitytypes.DeviceCodeResponse{DeviceCode: deviceCode, UserCode: userCode, VerificationURI: page,
		VerificationURIComplete: page + "?" + url.Values{"user_code": {userCode}}.Encode(),
		ExpiresIn:               int(deviceTTL.Seconds()), Interval: int(deviceInterval.Seconds())}, nil
}

func (s *service) saveDevice(ctx context.Context, key string, st deviceState) error {
	raw, _ := json.Marshal(st)
	ttl := time.Until(st.CreatedAt.Add(deviceTTL))
	if ttl <= 0 {
		return &DeviceError{Code: DeviceExpired}
	}
	if err := s.stateStore.SetValue(ctx, "auth:device:"+key, string(raw), ttl); err != nil {
		return ErrAuthUnavailable
	}
	return nil
}

func (s *service) deviceByUserCode(ctx context.Context, userCode string) (string, *deviceState, error) {
	code := normalizeUserCode(userCode)
	if code == "" {
		return "", nil, ErrDeviceCodeUnknown
	}
	key, err := s.stateStore.GetValue(ctx, "auth:device:user:"+code)
	if err != nil || key == "" {
		return "", nil, ErrDeviceCodeUnknown
	}
	raw, err := s.stateStore.GetValue(ctx, "auth:device:"+key)
	if err != nil || raw == "" {
		return "", nil, ErrDeviceCodeUnknown
	}
	var st deviceState
	if err := json.Unmarshal([]byte(raw), &st); err != nil || st.Status != deviceStatusPending {
		return "", nil, ErrDeviceCodeUnknown
	}
	return key, &st, nil
}

// DeviceInfo : demande à valider (origine montrée à la personne).
func (s *service) DeviceInfo(ctx context.Context, userCode string) (*identitytypes.DeviceInfo, error) {
	if err := s.cliReady(); err != nil {
		return nil, err
	}
	_, st, err := s.deviceByUserCode(ctx, userCode)
	if err != nil {
		return nil, err
	}
	return &identitytypes.DeviceInfo{UserCode: st.UserCode, IP: st.IP, UserAgent: st.UserAgent, CreatedAt: st.CreatedAt}, nil
}

// ApproveDevice : la personne connectée valide (ou refuse) la demande.
func (s *service) ApproveDevice(ctx context.Context, userID, sessionID uuid.UUID, in identitytypes.DeviceApproveRequest) error {
	if err := s.cliReady(); err != nil {
		return err
	}
	if err := s.limit(ctx, "device:approve:"+userID.String(), deviceStartsPerIP, time.Hour); err != nil {
		return err
	}
	key, st, err := s.deviceByUserCode(ctx, in.UserCode)
	if err != nil {
		return err
	}
	if !in.Approve {
		st.Status = deviceStatusDenied
		audit.Annotate(ctx, "cli", "device_denied")
		return s.saveDevice(ctx, key, *st)
	}
	mfa, err := s.requireCLIConsent(ctx, userID, sessionID)
	if err != nil {
		return err
	}
	st.Status, st.UserID, st.MFA = deviceStatusApproved, userID, mfa
	_ = s.stateStore.DeleteValue(ctx, "auth:device:user:"+st.UserCode)
	audit.Annotate(ctx, "cli", "device_approved")
	return s.saveDevice(ctx, key, *st)
}

// PollDevice : la CLI sonde (RFC 8628 §3.4) ; session ouverte une seule fois.
func (s *service) PollDevice(ctx context.Context, deviceCode string, cl Client) (*identitytypes.AuthResponse, error) {
	if err := s.cliReady(); err != nil {
		return nil, err
	}
	key := digest(strings.TrimSpace(deviceCode))
	raw, err := s.stateStore.GetValue(ctx, "auth:device:"+key)
	if err != nil || raw == "" {
		return nil, &DeviceError{Code: DeviceExpired}
	}
	var st deviceState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return nil, &DeviceError{Code: DeviceExpired}
	}
	switch st.Status {
	case deviceStatusDenied:
		_ = s.stateStore.DeleteValue(ctx, "auth:device:"+key)
		return nil, &DeviceError{Code: DeviceAccessDenied}
	case deviceStatusApproved:
		_ = s.stateStore.DeleteValue(ctx, "auth:device:"+key)
		return s.openCLISession(ctx, st.UserID, st.MFA, cl)
	}
	now := time.Now()
	tooFast := now.Sub(st.LastPoll) < deviceInterval
	st.LastPoll = now
	if err := s.saveDevice(ctx, key, st); err != nil {
		return nil, err
	}
	if tooFast {
		return nil, &DeviceError{Code: DeviceSlowDown}
	}
	return nil, &DeviceError{Code: DeviceAuthorizationPending}
}
