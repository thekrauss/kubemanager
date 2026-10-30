// Tests d'intégration : vrai Postgres, ignorés sans IAM_E2E.
//
//	bash scripts/iam_e2e_db.sh
//	IAM_E2E=1 go test -p 1 ./internal/auth/... -run E2E -v
package auth

import (
	"context"
	"encoding/base64"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const e2eDSN = "postgres://postgres:pw@127.0.0.1:55433/postgres?sslmode=disable"

func e2eDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("IAM_E2E") == "" {
		t.Skip("IAM_E2E not set")
	}
	db, err := gorm.Open(postgres.Open(e2eDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	return db
}

func e2eUser(t *testing.T, db *gorm.DB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	require.NoError(t, db.Exec(`INSERT INTO users (id, keycloak_user_id, email, full_name) VALUES (?, ?, ?, '')`,
		id, id.String(), id.String()[:8]+"@ex.sn").Error)
	return id
}

// fakeMinter : jeton lisible, pour vérifier ce qui serait signé.
type fakeMinter struct{ last SessionClaims }

func (f *fakeMinter) MintSession(c SessionClaims, ttl time.Duration) (string, time.Time, error) {
	f.last = c
	return "at:" + c.SessionID.String() + ":" + c.ACR, time.Now().Add(ttl), nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func TestE2E_SessionsRotationAndReuse(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	user := e2eUser(t, db)
	clk := &clock{t: time.Now()}
	minter := &fakeMinter{}
	s := &Sessions{DB: db, Minter: minter, Now: clk.now}

	tok, err := s.Create(ctx, NewSession{UserID: user, Kind: KindWeb, AMR: []string{AMRPassword}, IP: "41.82.1.2", UserAgent: "Firefox"})
	require.NoError(t, err)
	assert.Equal(t, ACRSilver, tok.ACR)
	assert.Equal(t, []string{AMRPassword}, minter.last.AMR)
	assert.Contains(t, tok.RefreshToken, RefreshTokenPrefix)

	// Rotation : nouveau jeton, l'ancien reste accepté pendant la grâce sans rotation.
	clk.t = clk.t.Add(time.Minute)
	next, err := s.Refresh(ctx, tok.RefreshToken)
	require.NoError(t, err)
	require.NotEmpty(t, next.RefreshToken)
	assert.NotEqual(t, tok.RefreshToken, next.RefreshToken)
	clk.t = clk.t.Add(5 * time.Second)
	grace, err := s.Refresh(ctx, tok.RefreshToken)
	require.NoError(t, err, "requêtes simultanées : acceptées")
	assert.Empty(t, grace.RefreshToken, "pas de nouvelle rotation pendant la grâce")

	// Au-delà de la grâce : réutilisation = vol présumé, session révoquée.
	clk.t = clk.t.Add(RefreshGrace + time.Second)
	_, err = s.Refresh(ctx, tok.RefreshToken)
	assert.ErrorIs(t, err, ErrRefreshReused)
	_, err = s.Refresh(ctx, next.RefreshToken)
	assert.ErrorIs(t, err, ErrRefreshInvalid, "le jeton légitime ne vaut plus non plus")
	active, err := s.Active(ctx, user, tok.SessionID)
	require.NoError(t, err)
	assert.False(t, active)

	_, err = s.Refresh(ctx, "btr_rt_inconnu")
	assert.ErrorIs(t, err, ErrRefreshInvalid)
	_, err = s.Refresh(ctx, "ancien-format")
	assert.ErrorIs(t, err, ErrRefreshInvalid)
}

func TestE2E_SessionsStepUpListAndRevoke(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	user, other := e2eUser(t, db), e2eUser(t, db)
	clk := &clock{t: time.Now()}
	s := &Sessions{DB: db, Minter: &fakeMinter{}, Now: clk.now}

	web, err := s.Create(ctx, NewSession{UserID: user, Kind: KindWeb, AMR: []string{AMRPassword}})
	require.NoError(t, err)
	cli, err := s.Create(ctx, NewSession{UserID: user, Kind: KindCLI, AMR: []string{AMRPassword}})
	require.NoError(t, err)

	gold, err := s.MarkMFA(ctx, user, web.SessionID)
	require.NoError(t, err)
	assert.Equal(t, ACRGold, gold.ACR)
	at, err := s.MFAAt(ctx, user, web.SessionID)
	require.NoError(t, err)
	assert.WithinDuration(t, clk.t, at, time.Second)
	_, err = s.MarkMFA(ctx, other, web.SessionID)
	assert.ErrorIs(t, err, ErrSessionNotFound, "session d'un autre compte")

	list, err := s.List(ctx, user, web.SessionID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	for _, x := range list {
		assert.Equal(t, x.ID == web.SessionID, x.Current)
	}
	assert.ErrorIs(t, s.Revoke(ctx, other, cli.SessionID, "user"), ErrSessionNotFound)
	require.NoError(t, s.RevokeAll(ctx, user, web.SessionID, "signout_others"))
	list, err = s.List(ctx, user, web.SessionID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, web.SessionID, list[0].ID)

	require.NoError(t, s.RevokeByRefreshToken(ctx, web.RefreshToken))
	_, err = s.Refresh(ctx, web.RefreshToken)
	assert.ErrorIs(t, err, ErrRefreshInvalid, "déconnecté")

	// Session expirée par inactivité.
	idle, err := s.Create(ctx, NewSession{UserID: user, Kind: KindWeb})
	require.NoError(t, err)
	clk.t = clk.t.Add(WebIdleTTL + time.Hour)
	_, err = s.Refresh(ctx, idle.RefreshToken)
	assert.ErrorIs(t, err, ErrRefreshInvalid)
}

func TestE2E_FactorsTOTPAndRecovery(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	user := e2eUser(t, db)
	sealer, err := NewSealer(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	require.NoError(t, err)
	clk := &clock{t: time.Now()}
	f := &Factors{DB: db, Sealer: sealer, Pepper: []byte("pepper-de-test-assez-long-pour-hmac"), Now: clk.now}

	_, err = f.Verify(ctx, user, "123456")
	assert.ErrorIs(t, err, ErrNoFactor)
	enr, err := f.StartTOTP(ctx, user, "awa@ex.sn")
	require.NoError(t, err)
	active, err := f.HasFactor(ctx, user)
	require.NoError(t, err)
	assert.False(t, active, "inactif avant le premier code")

	secret, err := totpEncoding.DecodeString(stripSpaces(enr.Secret))
	require.NoError(t, err)
	_, err = f.ConfirmTOTP(ctx, user, "000000")
	assert.ErrorIs(t, err, ErrInvalidCode)
	codes, err := f.ConfirmTOTP(ctx, user, totpCode(secret, totpStep(clk.t)))
	require.NoError(t, err)
	require.Len(t, codes, RecoveryCodeCount)
	_, err = f.StartTOTP(ctx, user, "awa@ex.sn")
	assert.ErrorIs(t, err, ErrFactorExists)

	// Même code rejoué : refusé ; code du pas suivant : accepté.
	_, err = f.Verify(ctx, user, totpCode(secret, totpStep(clk.t)))
	assert.ErrorIs(t, err, ErrInvalidCode, "code déjà utilisé")
	clk.t = clk.t.Add(30 * time.Second)
	m, err := f.Verify(ctx, user, totpCode(secret, totpStep(clk.t)))
	require.NoError(t, err)
	assert.Equal(t, MethodTOTP, m)

	// Code de secours : une fois, quelle que soit la casse.
	m, err = f.Verify(ctx, user, codes[0])
	require.NoError(t, err)
	assert.Equal(t, MethodRecovery, m)
	_, err = f.Verify(ctx, user, codes[0])
	assert.ErrorIs(t, err, ErrInvalidCode)
	st, err := f.Status(ctx, user)
	require.NoError(t, err)
	assert.True(t, st.TOTP)
	assert.Equal(t, RecoveryCodeCount-1, st.RecoveryRemaining)

	fresh, err := f.RegenerateRecovery(ctx, user)
	require.NoError(t, err)
	_, err = f.Verify(ctx, user, codes[1])
	assert.ErrorIs(t, err, ErrInvalidCode, "anciens codes révoqués")
	_, err = f.Verify(ctx, user, fresh[1])
	require.NoError(t, err)

	require.NoError(t, f.DisableTOTP(ctx, user))
	active, _ = f.HasFactor(ctx, user)
	assert.False(t, active)
	assert.ErrorIs(t, f.DisableTOTP(ctx, user), ErrNoFactor)
}

func TestE2E_EmailTokens(t *testing.T) {
	db := e2eDB(t)
	ctx := context.Background()
	clk := &clock{t: time.Now()}
	e := &EmailTokens{DB: db, Now: clk.now}
	subject := uuid.NewString()

	first, err := e.Issue(ctx, subject, "awa@ex.sn", PurposeResetPassword, ResetPasswordTTL)
	require.NoError(t, err)
	second, err := e.Issue(ctx, subject, "awa@ex.sn", PurposeResetPassword, ResetPasswordTTL)
	require.NoError(t, err)
	_, err = e.Consume(ctx, first, PurposeResetPassword)
	assert.ErrorIs(t, err, ErrEmailLinkInvalid, "seul le dernier lien envoyé vaut")
	_, err = e.Consume(ctx, second, PurposeVerifyEmail)
	assert.ErrorIs(t, err, ErrEmailLinkInvalid, "pas pour un autre usage")
	link, err := e.Consume(ctx, second, PurposeResetPassword)
	require.NoError(t, err)
	assert.Equal(t, subject, link.Subject)
	_, err = e.Consume(ctx, second, PurposeResetPassword)
	assert.ErrorIs(t, err, ErrEmailLinkInvalid, "usage unique")

	late, err := e.Issue(ctx, subject, "awa@ex.sn", PurposeVerifyEmail, VerifyEmailTTL)
	require.NoError(t, err)
	clk.t = clk.t.Add(VerifyEmailTTL + time.Minute)
	_, err = e.Consume(ctx, late, PurposeVerifyEmail)
	assert.ErrorIs(t, err, ErrEmailLinkInvalid, "expiré")
}

func stripSpaces(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r != ' ' {
			out = append(out, r)
		}
	}
	return string(out)
}
