package registrar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOVH_RequiresCredentials(t *testing.T) {
	_, err := NewOVH(OVHConfig{ApplicationKey: "a"})
	assert.Error(t, err)
}

// Signature documentée par OVH : "$1$" + SHA1 des champs joints par "+".
// Valeur attendue calculée indépendamment :
//
//	printf 'as+ck+GET+https://eu.api.ovh.com/1.0/me++1700000000' | sha1sum
func TestOVH_Signature(t *testing.T) {
	o, err := NewOVH(OVHConfig{ApplicationKey: "ak", ApplicationSecret: "as", ConsumerKey: "ck", Subsidiary: "FR"})
	require.NoError(t, err)
	assert.Equal(t, "$1$"+expectedSHA1, o.sign(http.MethodGet, "https://eu.api.ovh.com/1.0/me", "", 1700000000))
}

func fakeOVH(t *testing.T, offers string) (*OVH, *[]string) {
	t.Helper()
	var calls []string
	var client *OVH
	mux := http.NewServeMux()
	verify := func(r *http.Request, body string) {
		assert.Equal(t, "ak", r.Header.Get("X-Ovh-Application"))
		assert.Equal(t, "ck", r.Header.Get("X-Ovh-Consumer"))
		ts := r.Header.Get("X-Ovh-Timestamp")
		assert.NotEmpty(t, ts)
		assert.Equal(t, client.sign(r.Method, client.cfg.Endpoint+r.URL.RequestURI(), body, client.now().Unix()), r.Header.Get("X-Ovh-Signature"))
	}
	handle := func(pattern string, h func(http.ResponseWriter, *http.Request)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			verify(r, string(raw))
			calls = append(calls, r.Method+" "+r.URL.Path+" "+string(raw))
			h(w, r)
		})
	}
	handle("POST /order/cart", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"cartId":"c1"}`) })
	handle("GET /order/cart/c1/domain", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "exemple.sn", r.URL.Query().Get("domain"))
		_, _ = io.WriteString(w, offers)
	})
	handle("POST /domain/zone/exemple.sn/record", func(http.ResponseWriter, *http.Request) {})
	handle("POST /domain/zone/exemple.sn/refresh", func(http.ResponseWriter, *http.Request) {})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	var err error
	client, err = NewOVH(OVHConfig{Endpoint: srv.URL, ApplicationKey: "ak", ApplicationSecret: "as", ConsumerKey: "ck", Subsidiary: "FR"})
	require.NoError(t, err)
	fixed := time.Unix(1700000000, 0)
	client.now = func() time.Time { return fixed }
	return client, &calls
}

func TestOVH_Check(t *testing.T) {
	o, _ := fakeOVH(t, `[{"orderable":true,"action":"create","offer":"gold","prices":[{"label":"RENEW","price":{"value":1,"currencyCode":"EUR"}},{"label":"TOTAL","price":{"value":12.99,"currencyCode":"EUR"}}]}]`)
	a, err := o.Check(context.Background(), "exemple.sn")
	require.NoError(t, err)
	assert.Equal(t, &Availability{Domain: "exemple.sn", Available: true, PriceCents: 1299, Currency: "EUR"}, a)

	o, _ = fakeOVH(t, `[{"orderable":false,"action":"create","prices":[]}]`)
	a, err = o.Check(context.Background(), "exemple.sn")
	require.NoError(t, err)
	assert.False(t, a.Available)
}

// Aucune commande réelle ne peut partir tant que le parcours n'est pas validé.
func TestOVH_RegisterDisabled(t *testing.T) {
	o, calls := fakeOVH(t, `[]`)
	_, err := o.Register(context.Background(), Order{Domain: "exemple.sn", Years: 1})
	assert.Error(t, err)
	assert.Empty(t, *calls)
}

func TestOVH_SetRecords(t *testing.T) {
	o, calls := fakeOVH(t, `[]`)
	require.NoError(t, o.SetRecords(context.Background(), "exemple.sn", []Record{
		{Type: "CNAME", Name: "www.exemple.sn", Value: "ingress.betorun.app.", TTL: 300},
		{Type: "A", Name: "exemple.sn", Value: "203.0.113.10"},
	}))
	require.Len(t, *calls, 3)
	var first map[string]any
	_, raw, _ := cutBody((*calls)[0])
	require.NoError(t, json.Unmarshal([]byte(raw), &first))
	assert.Equal(t, "www", first["subDomain"])
	assert.EqualValues(t, 300, first["ttl"])
	_, raw, _ = cutBody((*calls)[1])
	var second map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &second))
	assert.Equal(t, "", second["subDomain"], "racine du domaine")
	assert.Contains(t, (*calls)[2], "/refresh", "zone appliquée à la fin")
}

func cutBody(call string) (string, string, bool) {
	for i := len(call) - 1; i >= 0; i-- {
		if call[i] == '{' && (i == 0 || call[i-1] == ' ') {
			return call[:i], call[i:], true
		}
	}
	return call, "", false
}

// expectedSHA1 : voir TestOVH_Signature.
const expectedSHA1 = "212ae9d571c1dbc085f3630b6c2b68d5b3fdc482"
