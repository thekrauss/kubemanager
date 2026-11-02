package sts_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thekrauss/kubemanager/internal/auth"
	"github.com/thekrauss/kubemanager/internal/iam"
	"github.com/thekrauss/kubemanager/internal/iam/sts"
)

// Jeton d'accès d'une session : signé par le STS, avec les claims de la
// session ; refusé dès que la session est révoquée ou le compte désactivé.
func TestE2E_SessionTokens(t *testing.T) {
	d := db(t)
	ctx := context.Background()
	ring, err := sts.NewKeyRing(d, kek)
	require.NoError(t, err)
	require.NoError(t, ring.Rotate(ctx))
	svc := sts.NewService("http://127.0.0.1/kmanager/v1/iam", ring, d)
	sessions := &auth.Sessions{DB: d, Minter: svc}

	user := uuid.New()
	require.NoError(t, d.Exec(`INSERT INTO users (id, keycloak_user_id, email, full_name) VALUES (?, ?, ?, '')`,
		user, user.String(), user.String()[:8]+"@ex.sn").Error)

	tok, err := sessions.Create(ctx, auth.NewSession{UserID: user, AMR: []string{auth.AMRPassword}})
	require.NoError(t, err)
	v, err := svc.Verify(ctx, tok.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, iam.UserPrincipal(user), v.Principal)
	assert.Equal(t, sts.MethodSession, v.Method)
	assert.Equal(t, tok.SessionID.String(), v.SessionID)
	assert.Equal(t, auth.ACRSilver, v.ACR)
	assert.Equal(t, []string{auth.AMRPassword}, v.AMR)
	assert.WithinDuration(t, time.Now(), v.AuthTime, 5*time.Second)

	gold, err := sessions.MarkMFA(ctx, user, tok.SessionID)
	require.NoError(t, err)
	v, err = svc.Verify(ctx, gold.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, auth.ACRGold, v.ACR, "second facteur vérifié")
	assert.Contains(t, v.AMR, auth.AMROTP)

	// Session révoquée : jeton refusé (après expiration du cache d'état).
	require.NoError(t, sessions.Revoke(ctx, user, tok.SessionID, "test"))
	svc.ForgetStatus()
	_, err = svc.Verify(ctx, gold.AccessToken)
	assert.ErrorIs(t, err, sts.ErrInvalidToken)

	// Compte désactivé : toutes ses sessions cessent de valoir.
	other, err := sessions.Create(ctx, auth.NewSession{UserID: user})
	require.NoError(t, err)
	require.NoError(t, d.Exec(`UPDATE users SET is_active = false WHERE id = ?`, user).Error)
	svc.ForgetStatus()
	_, err = svc.Verify(ctx, other.AccessToken)
	assert.ErrorIs(t, err, sts.ErrInvalidToken)
}
