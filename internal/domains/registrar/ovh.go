package registrar

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // imposé par la signature de l'API OVH
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// OVHConfig : identifiants de l'API OVH (https://api.ovh.com). Secrets, hors
// du code. La clé client (ConsumerKey) doit être limitée aux chemins /order et
// /domain.
type OVHConfig struct {
	Endpoint          string // défaut : https://eu.api.ovh.com/1.0
	ApplicationKey    string
	ApplicationSecret string
	ConsumerKey       string
	// Subsidiary : filiale OVH de la commande (ex. FR), qui fixe la
	// devise et les extensions disponibles.
	Subsidiary string
}

const (
	ovhDefaultEndpoint = "https://eu.api.ovh.com/1.0"
	ovhTimeout         = 30 * time.Second
	ovhMaxResponse     = 4 << 20
	ovhPriceTotal      = "TOTAL"
	ovhCentsPerUnit    = 100
)

// OVH : API OVH. État : signature testée, mais aucune de ces routes n'a encore
// été appelée sur un vrai compte (voir ovh_test.go, API simulée). À valider
// sur un compte de test avant toute mise en service.
type OVH struct {
	cfg  OVHConfig
	http *http.Client
	now  func() time.Time
}

// NewOVH : client ; erreur si un identifiant manque.
func NewOVH(cfg OVHConfig) (*OVH, error) {
	if cfg.ApplicationKey == "" || cfg.ApplicationSecret == "" || cfg.ConsumerKey == "" || cfg.Subsidiary == "" {
		return nil, errors.New("registrar: incomplete OVH configuration")
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = ovhDefaultEndpoint
	}
	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")
	return &OVH{cfg: cfg, http: &http.Client{Timeout: ovhTimeout}, now: time.Now}, nil
}

// sign : "$1$" + SHA1(secret+clé client+méthode+URL+corps+horodatage).
func (o *OVH) sign(method, fullURL, body string, ts int64) string {
	h := sha1.New() //nolint:gosec
	h.Write([]byte(strings.Join([]string{o.cfg.ApplicationSecret, o.cfg.ConsumerKey, method, fullURL, body, strconv.FormatInt(ts, 10)}, "+")))
	return "$1$" + hex.EncodeToString(h.Sum(nil))
}

func (o *OVH) call(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return err
		}
	}
	full := o.cfg.Endpoint + path
	req, err := http.NewRequestWithContext(ctx, method, full, bytes.NewReader(body))
	if err != nil {
		return err
	}
	ts := o.now().Unix()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Ovh-Application", o.cfg.ApplicationKey)
	req.Header.Set("X-Ovh-Consumer", o.cfg.ConsumerKey)
	req.Header.Set("X-Ovh-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-Ovh-Signature", o.sign(method, full, string(body), ts))
	resp, err := o.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, ovhMaxResponse))
	if err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("registrar: OVH %s %s: %d %s", method, path, resp.StatusCode, e.Message)
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

type ovhCart struct {
	CartID string `json:"cartId"`
}

type ovhPrice struct {
	Label string `json:"label"`
	Price struct {
		Value        float64 `json:"value"`
		CurrencyCode string  `json:"currencyCode"`
	} `json:"price"`
}

type ovhOffer struct {
	Orderable bool       `json:"orderable"`
	Action    string     `json:"action"`
	Offer     string     `json:"offer"`
	Prices    []ovhPrice `json:"prices"`
}

func (o *OVH) newCart(ctx context.Context) (string, error) {
	var cart ovhCart
	err := o.call(ctx, http.MethodPost, "/order/cart", map[string]string{"ovhSubsidiary": o.cfg.Subsidiary}, &cart)
	return cart.CartID, err
}

// Check : disponibilité et prix, via un panier jetable (aucune commande).
func (o *OVH) Check(ctx context.Context, domain string) (*Availability, error) {
	cart, err := o.newCart(ctx)
	if err != nil {
		return nil, err
	}
	var offers []ovhOffer
	path := "/order/cart/" + url.PathEscape(cart) + "/domain?domain=" + url.QueryEscape(domain)
	if err := o.call(ctx, http.MethodGet, path, nil, &offers); err != nil {
		return nil, err
	}
	out := &Availability{Domain: domain}
	for _, offer := range offers {
		if !offer.Orderable || offer.Action != "create" {
			continue
		}
		out.Available = true
		out.Premium = offer.Offer == "premium"
		for _, p := range offer.Prices {
			if p.Label == ovhPriceTotal {
				out.PriceCents = int64(p.Price.Value*ovhCentsPerUnit + 0.5)
				out.Currency = p.Price.CurrencyCode
			}
		}
		break
	}
	return out, nil
}

// Register : commande réelle, débitée sur le compte OVH. Volontairement sans
// nouvel essai automatique. Le titulaire et la validation de la commande
// (configuration du panier, moyen de paiement) restent à finaliser avec un
// compte réel : tant que ce n'est pas fait, la méthode refuse de commander.
func (o *OVH) Register(context.Context, Order) (*OrderResult, error) {
	return nil, errors.New("registrar: OVH ordering is not enabled yet")
}

// SetRecords : crée les enregistrements, puis applique la zone.
func (o *OVH) SetRecords(ctx context.Context, zone string, records []Record) error {
	base := "/domain/zone/" + url.PathEscape(zone)
	for _, r := range records {
		sub := strings.TrimSuffix(strings.TrimSuffix(r.Name, zone), ".")
		in := map[string]any{"fieldType": r.Type, "subDomain": sub, "target": r.Value}
		if r.TTL > 0 {
			in["ttl"] = r.TTL
		}
		if err := o.call(ctx, http.MethodPost, base+"/record", in, nil); err != nil {
			return err
		}
	}
	return o.call(ctx, http.MethodPost, base+"/refresh", nil, nil)
}

var _ Registrar = (*OVH)(nil)
