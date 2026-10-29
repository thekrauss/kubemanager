package keycloak

import (
	"encoding/base64"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, webClientID string) *Client {
	t.Helper()
	c, err := New(AdminConfig{
		BaseURL:      "http://kc",
		Realm:        "kubemanager",
		ClientID:     "kubemanager-admin",
		ClientSecret: "s3cret",
		WebClientID:  webClientID,
	}, "")
	require.NoError(t, err)
	return c
}

func fakeJWT(payload string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(payload)) + ".sig"
}

func TestGetAuthorizationURL_PKCEWithWebClient(t *testing.T) {
	c := newTestClient(t, "kubemanager-web")

	raw := c.GetAuthorizationURL("", "http://app/auth/callback", "st", "chal")
	u, err := url.Parse(raw)
	require.NoError(t, err)
	q := u.Query()

	assert.Equal(t, "/realms/kubemanager/protocol/openid-connect/auth", u.Path)
	assert.Equal(t, "kubemanager-web", q.Get("client_id"))
	assert.Equal(t, "chal", q.Get("code_challenge"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.Empty(t, q.Get("kc_idp_hint"), "login hébergé : pas d'IdP imposé")

	social, _ := url.Parse(c.GetAuthorizationURL("google", "http://app/auth/callback", "st", "chal"))
	assert.Equal(t, "google", social.Query().Get("kc_idp_hint"))
}

func TestSetClientCredentials_SecretOnlyForConfidentialClient(t *testing.T) {
	c := newTestClient(t, "kubemanager-web")

	pub := url.Values{}
	c.setClientCredentials(pub, c.authClientID())
	assert.Equal(t, "kubemanager-web", pub.Get("client_id"))
	assert.Empty(t, pub.Get("client_secret"))

	conf := url.Values{}
	c.setClientCredentials(conf, "kubemanager-admin")
	assert.Equal(t, "s3cret", conf.Get("client_secret"))
}

func TestClientForRefreshToken_FollowsAZP(t *testing.T) {
	c := newTestClient(t, "kubemanager-web")

	assert.Equal(t, "kubemanager-web", c.clientForRefreshToken(fakeJWT(`{"azp":"kubemanager-web"}`)))
	assert.Equal(t, "kubemanager-admin", c.clientForRefreshToken(fakeJWT(`{"azp":"kubemanager-admin"}`)))
	assert.Equal(t, "kubemanager-admin", c.clientForRefreshToken("not-a-jwt"))
}

func TestAuthClientID_FallsBackToConfidentialClient(t *testing.T) {
	c := newTestClient(t, "")
	assert.Equal(t, "kubemanager-admin", c.authClientID())
}
