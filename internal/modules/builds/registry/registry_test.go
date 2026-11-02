package registry

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var creds = Credentials{Secret: []byte("0123456789abcdef0123456789abcdef")}

func TestCredentials(t *testing.T) {
	project := uuid.New()
	now := time.Now()

	user, pass := creds.ForBuild(project, now.Add(time.Hour))
	got, actions, err := creds.Verify(user, pass, now)
	require.NoError(t, err)
	assert.Equal(t, project, got)
	assert.ElementsMatch(t, []string{ActionPull, ActionPush}, actions)

	_, _, err = creds.Verify(user, pass, now.Add(2*time.Hour))
	assert.ErrorIs(t, err, ErrBadCredentials, "identifiant de build expiré")

	_, _, err = creds.Verify(user, pass+"x", now)
	assert.ErrorIs(t, err, ErrBadCredentials)

	other := uuid.New()
	_, _, err = creds.Verify("build."+other.String()+"."+user[len("build."+project.String()+"."):], pass, now)
	assert.ErrorIs(t, err, ErrBadCredentials, "signature liée au projet")

	pullUser, pullPass := creds.ForPull(project)
	_, actions, err = creds.Verify(pullUser, pullPass, now.Add(1000*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, []string{ActionPull}, actions, "lecture seule")

	_, _, err = (Credentials{Secret: []byte("autre")}).Verify(pullUser, pullPass, now)
	assert.ErrorIs(t, err, ErrBadCredentials)
}

// Le cœur de l'isolation : un projet n'obtient rien sur les dépôts des autres.
func TestGrant_OnlyOwnProject(t *testing.T) {
	project, other := uuid.New(), uuid.New()
	own := project.String() + "/web"
	got := Grant(project, []string{ActionPull}, []string{
		"repository:" + own + ":pull,push",
		"repository:" + other.String() + "/web:pull",
		"repository:" + project.String() + "/../" + other.String() + "/web:pull",
		"repository:" + project.String() + "x/web:pull",
		"registry:catalog:*",
		"nimporte",
	})
	require.Len(t, got, 1)
	assert.Equal(t, own, got[0].Name)
	assert.Equal(t, []string{ActionPull}, got[0].Actions, "push non accordé à un identifiant de lecture")

	assert.Empty(t, Grant(project, []string{ActionPull, ActionPush}, []string{"repository:" + other.String() + "/x:push"}))
}

func TestImageOwnership(t *testing.T) {
	const host = "registry.betorun.app"
	project := uuid.New()
	own := host + "/" + project.String() + "/web:b-1"
	foreign := host + "/" + uuid.NewString() + "/web:b-1"

	assert.True(t, IsPlatformImage(host, own))
	assert.True(t, IsPlatformImage(host, "REGISTRY.betorun.app/x/y:1"), "insensible à la casse")
	assert.False(t, IsPlatformImage(host, "nginx:1.27"))
	assert.False(t, IsPlatformImage(host, "registry.betorun.app.evil.com/x:1"))
	assert.False(t, IsPlatformImage("", "nginx"))
	assert.True(t, OwnedBy(host, own, project))
	assert.False(t, OwnedBy(host, foreign, project))
}

func TestSourceSignature(t *testing.T) {
	build, now := uuid.New(), time.Now()
	exp := now.Add(time.Minute).Unix()
	sig := creds.SourceSignature(build, exp)
	assert.True(t, creds.VerifySource(build, exp, sig, now))
	assert.False(t, creds.VerifySource(build, exp, sig, now.Add(time.Hour)), "expirée")
	assert.False(t, creds.VerifySource(uuid.New(), exp, sig, now), "autre build")
	assert.False(t, creds.VerifySource(build, exp+1, sig, now), "expiration modifiée")
}
