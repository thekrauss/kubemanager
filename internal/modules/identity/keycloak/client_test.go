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

func TestSetClientCredentials_SecretOnlyForConfidentialClient(t *testing.T) {
	c := newTestClient(t, "kubemanager-web")

	pub := url.Values{}
	c.setClientCredentials(pub, "kubemanager-web")
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
