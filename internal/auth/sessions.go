package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SessionClaims : contenu du jeton d'accès d'une session (signé par le STS).
type SessionClaims struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	AuthTime  time.Time
	AMR       []string
	ACR       string
}

// TokenMinter : émetteur des jetons d'accès (le STS).
type TokenMinter interface {
	MintSession(c SessionClaims, ttl time.Duration) (string, time.Time, error)
}

// Tokens : ce que reçoit le client à l'ouverture ou au renouvellement.
type Tokens struct {
	AccessToken string
	ExpiresAt   time.Time
	// RefreshToken : vide quand le jeton présenté vient d'être remplacé
	// (fenêtre de grâce) : le client garde celui qu'il a déjà reçu.
	RefreshToken string
	SessionID    uuid.UUID
	ACR          string
}

type sessionRow struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID        uuid.UUID `gorm:"type:uuid"`
	Kind          string
	RefreshHash   []byte
	PreviousHash  []byte
	RotatedAt     time.Time
	AMR           string `gorm:"column:amr"`
	AuthTime      time.Time
	MFAAt         *time.Time `gorm:"column:mfa_at"`
	IP            string     `gorm:"column:ip"`
	UserAgent     string
	CreatedAt     time.Time
	LastUsedAt    time.Time
	IdleExpiresAt time.Time
	ExpiresAt     time.Time
	RevokedAt     *time.Time
	RevokedReason *string
}

func (sessionRow) TableName() string { return "auth_sessions" }

func (r *sessionRow) claims() SessionClaims {
	c := SessionClaims{UserID: r.UserID, SessionID: r.ID, AuthTime: r.AuthTime, ACR: ACRSilver}
	if r.AMR != "" {
		c.AMR = strings.Split(r.AMR, ",")
	}
	if r.MFAAt != nil {
		c.ACR = ACRGold
		if r.MFAAt.After(c.AuthTime) {
			c.AuthTime = *r.MFAAt
		}
	}
	return c
}

// Session : vue d'une session pour son propriétaire (page Sécurité).
type Session struct {
	ID         uuid.UUID `json:"id"`
	Kind       string    `json:"kind"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	MFA        bool      `json:"mfa"`
	Current    bool      `json:"current"`
}

// NewSession : ouverture après une authentification réussie.
type NewSession struct {
	UserID    uuid.UUID
	Kind      string
	AMR       []string
	MFA       bool
	IP        string
	UserAgent string
}

// Sessions : ouverture, renouvellement, révocation.
type Sessions struct {
	DB     *gorm.DB
	Minter TokenMinter
	Now    func() time.Time
}

func (s *Sessions) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func lifetimes(kind string) (idle, absolute time.Duration) {
	if kind == KindCLI {
		return CLIIdleTTL, CLISessionTTL
	}
	return WebIdleTTL, WebSessionTTL
}

func newRefreshToken() (string, []byte, error) {
	raw, err := randomToken()
	if err != nil {
		return "", nil, err
	}
	tok := RefreshTokenPrefix + raw
	return tok, hashToken(tok), nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (s *Sessions) Create(ctx context.Context, in NewSession) (*Tokens, error) {
	if in.Kind != KindCLI {
		in.Kind = KindWeb
	}
	refresh, hash, err := newRefreshToken()
	if err != nil {
		return nil, err
	}
	now := s.now()
	idle, absolute := lifetimes(in.Kind)
	row := sessionRow{UserID: in.UserID, Kind: in.Kind, RefreshHash: hash, RotatedAt: now,
		AMR: strings.Join(in.AMR, ","), AuthTime: now, IP: truncate(in.IP, 64), UserAgent: truncate(in.UserAgent, 512),
		CreatedAt: now, LastUsedAt: now, IdleExpiresAt: now.Add(idle), ExpiresAt: now.Add(absolute)}
	if in.MFA {
		row.MFAAt = &now
	}
	if err := s.DB.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, err
	}
	return s.issue(&row, refresh)
}

func (s *Sessions) issue(row *sessionRow, refresh string) (*Tokens, error) {
	c := row.claims()
	access, exp, err := s.Minter.MintSession(c, AccessTokenTTL)
	if err != nil {
		return nil, err
	}
	return &Tokens{AccessToken: access, ExpiresAt: exp, RefreshToken: refresh, SessionID: row.ID, ACR: c.ACR}, nil
}

// Refresh : rotation du jeton de rafraîchissement. Un jeton remplacé depuis
// plus de RefreshGrace qui revient signale un vol : la session est révoquée.
func (s *Sessions) Refresh(ctx context.Context, refreshToken string) (*Tokens, error) {
	if !strings.HasPrefix(refreshToken, RefreshTokenPrefix) {
		return nil, ErrRefreshInvalid
	}
	hash := hashToken(refreshToken)
	now := s.now()
	db := s.DB.WithContext(ctx)

	next, nextHash, err := newRefreshToken()
	if err != nil {
		return nil, err
	}
	var rows []sessionRow
	// Rotation atomique : une seule requête concurrente gagne.
	err = db.Raw(`UPDATE auth_sessions SET previous_hash = refresh_hash, refresh_hash = ?, rotated_at = ?,
			last_used_at = ?, idle_expires_at = CASE WHEN kind = ? THEN ?::timestamptz ELSE ?::timestamptz END
		WHERE refresh_hash = ? AND revoked_at IS NULL AND expires_at > ? AND idle_expires_at > ?
		RETURNING *`,
		nextHash, now, now, KindCLI, now.Add(CLIIdleTTL), now.Add(WebIdleTTL),
		hash, now, now).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 1 {
		return s.issue(&rows[0], next)
	}

	// Jeton déjà remplacé ?
	var prev sessionRow
	err = db.Where("previous_hash = ?", hash).First(&prev).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRefreshInvalid
	}
	if err != nil {
		return nil, err
	}
	if prev.RevokedAt != nil || !now.Before(prev.ExpiresAt) {
		return nil, ErrRefreshInvalid
	}
	if now.Sub(prev.RotatedAt) <= RefreshGrace {
		// Requêtes simultanées : nouveau jeton d'accès, pas de rotation.
		return s.issue(&prev, "")
	}
	if err := s.revoke(ctx, "id = ?", []any{prev.ID}, "refresh_reuse"); err != nil {
		return nil, err
	}
	return nil, ErrRefreshReused
}

// Revoke : session de l'utilisateur (sans effet sur celles des autres).
func (s *Sessions) Revoke(ctx context.Context, userID, sessionID uuid.UUID, reason string) error {
	res := s.DB.WithContext(ctx).Model(&sessionRow{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", sessionID, userID).
		Updates(map[string]any{"revoked_at": s.now(), "revoked_reason": reason})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// RevokeByRefreshToken : déconnexion (jeton inconnu : rien à faire).
func (s *Sessions) RevokeByRefreshToken(ctx context.Context, refreshToken string) error {
	return s.revoke(ctx, "refresh_hash = ?", []any{hashToken(refreshToken)}, "logout")
}

// RevokeAll : toutes les sessions de l'utilisateur, sauf except (uuid.Nil : aucune).
func (s *Sessions) RevokeAll(ctx context.Context, userID, except uuid.UUID, reason string) error {
	return s.revoke(ctx, "user_id = ? AND id <> ?", []any{userID, except}, reason)
}

func (s *Sessions) revoke(ctx context.Context, where string, args []any, reason string) error {
	return s.DB.WithContext(ctx).Model(&sessionRow{}).Where(where+" AND revoked_at IS NULL", args...).
		Updates(map[string]any{"revoked_at": s.now(), "revoked_reason": reason}).Error
}

// List : sessions actives de l'utilisateur, la plus récente d'abord.
func (s *Sessions) List(ctx context.Context, userID, current uuid.UUID) ([]Session, error) {
	now := s.now()
	var rows []sessionRow
	err := s.DB.WithContext(ctx).
		Where("user_id = ? AND revoked_at IS NULL AND expires_at > ? AND idle_expires_at > ?", userID, now, now).
		Order("last_used_at DESC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		out = append(out, Session{ID: r.ID, Kind: r.Kind, IP: r.IP, UserAgent: r.UserAgent, CreatedAt: r.CreatedAt,
			LastUsedAt: r.LastUsedAt, MFA: r.MFAAt != nil, Current: r.ID == current})
	}
	return out, nil
}

// MarkMFA : second facteur vérifié dans la session (step-up) ; nouveau jeton
// d'accès de niveau gold.
func (s *Sessions) MarkMFA(ctx context.Context, userID, sessionID uuid.UUID) (*Tokens, error) {
	now := s.now()
	var rows []sessionRow
	err := s.DB.WithContext(ctx).Raw(`UPDATE auth_sessions SET mfa_at = ?, last_used_at = ?,
			amr = CASE WHEN amr LIKE '%' || ? || '%' THEN amr WHEN amr = '' THEN ? ELSE amr || ',' || ? END
		WHERE id = ? AND user_id = ? AND revoked_at IS NULL AND expires_at > ? RETURNING *`,
		now, now, AMROTP, AMROTP, AMROTP, sessionID, userID, now).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrSessionNotFound
	}
	return s.issue(&rows[0], "")
}

// MFAAt : dernière vérification du second facteur dans la session (zéro : jamais).
func (s *Sessions) MFAAt(ctx context.Context, userID, sessionID uuid.UUID) (time.Time, error) {
	var row sessionRow
	err := s.DB.WithContext(ctx).Select("mfa_at").
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", sessionID, userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return time.Time{}, ErrSessionNotFound
	}
	if err != nil || row.MFAAt == nil {
		return time.Time{}, err
	}
	return *row.MFAAt, nil
}

// Active : session utilisable (vérifiée par le STS pour chaque jeton d'accès).
func (s *Sessions) Active(ctx context.Context, userID, sessionID uuid.UUID) (bool, error) {
	var n int64
	now := s.now()
	err := s.DB.WithContext(ctx).Model(&sessionRow{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL AND expires_at > ? AND idle_expires_at > ?",
			sessionID, userID, now, now).Count(&n).Error
	return n == 1, err
}
