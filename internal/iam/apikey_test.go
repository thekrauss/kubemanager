package iam

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyFormat_RoundTrip(t *testing.T) {
	full, keyID, secret, err := generateAPIKey(APIKeyEnvLive)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(full, "btr_live_"+keyID+"_"))
	assert.True(t, LooksLikeAPIKeyV2(full))

	env, gotID, gotSecret, err := parseAPIKey(full)
	require.NoError(t, err)
	assert.Equal(t, APIKeyEnvLive, env)
	assert.Equal(t, keyID, gotID)
	assert.Equal(t, secret, gotSecret)

	other, _, _, _ := generateAPIKey(APIKeyEnvLive)
	assert.NotEqual(t, full, other)
}

func TestAPIKeyFormat_ChecksumCatchesTypos(t *testing.T) {
	full, _, _, err := generateAPIKey(APIKeyEnvTest)
	require.NoError(t, err)
	// Une seule lettre changée dans le secret : refusée sans accès à la base.
	i := len("btr_test_") + apiKeyIDLen + 1 + 5
	typo := []byte(full)
	if typo[i] == 'a' {
		typo[i] = 'b'
	} else {
		typo[i] = 'a'
	}
	_, _, _, err = parseAPIKey(string(typo))
	assert.ErrorIs(t, err, ErrAPIKeyInvalid)

	for _, bad := range []string{"", "btr_live_x", "km_prod_abc_def", strings.Replace(full, "btr_test", "btr_prod", 1)} {
		_, _, _, err := parseAPIKey(bad)
		assert.ErrorIs(t, err, ErrAPIKeyInvalid, bad)
	}
}

func TestHashSecret_DependsOnPepper(t *testing.T) {
	a := hashSecret([]byte(strings.Repeat("a", 32)), "secret")
	b := hashSecret([]byte(strings.Repeat("b", 32)), "secret")
	assert.NotEqual(t, a, b)
	assert.Len(t, a, 64)
}

func TestKeyRestrictions_Allows(t *testing.T) {
	p, site := uuid.New(), uuid.New()
	siteName := ProjectName(p).Child("sites", site)

	assert.True(t, KeyRestrictions{}.Allows(WorkloadsDelete, ProjectName(p)), "sans restriction")

	r := KeyRestrictions{Permissions: []Permission{SiteDeploymentsCreate}, Resources: []ResourceName{siteName}}
	assert.True(t, r.Allows(SiteDeploymentsCreate, siteName))
	assert.True(t, r.Allows(SiteDeploymentsCreate, siteName.Child("domains", uuid.New())), "descendant autorisé")
	assert.False(t, r.Allows(SitesDelete, siteName), "permission hors liste")
	assert.False(t, r.Allows(SiteDeploymentsCreate, ProjectName(p).Child("sites", uuid.New())), "autre site")
	assert.False(t, r.Allows(SiteDeploymentsCreate, ResourceName(string(siteName)+"x")), "préfixe sans séparateur")

	ips := KeyRestrictions{AllowedCIDRs: []string{"41.82.0.0/16"}}
	assert.True(t, ips.allowsIP("41.82.10.4"))
	assert.False(t, ips.allowsIP("102.0.0.1"))
	assert.False(t, ips.allowsIP("not-an-ip"))
}

func TestKeyRestrictions_Validate(t *testing.T) {
	assert.NoError(t, KeyRestrictions{Permissions: []Permission{SitesGet}, AllowedCIDRs: []string{"10.0.0.0/8"}}.validate())
	assert.ErrorIs(t, KeyRestrictions{Permissions: []Permission{"x.y.z"}}.validate(), ErrInvalidKeyRestrictions)
	assert.ErrorIs(t, KeyRestrictions{Resources: []ResourceName{"nope"}}.validate(), ErrInvalidKeyRestrictions)
	assert.ErrorIs(t, KeyRestrictions{AllowedCIDRs: []string{"300.1.1.1/8"}}.validate(), ErrInvalidKeyRestrictions)
}
