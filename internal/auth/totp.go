package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // HMAC-SHA1 : algorithme de RFC 6238, seul compris par toutes les applications.
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238) : 6 chiffres, pas de 30 s, HMAC-SHA1 — les réglages que
// toutes les applications d'authentification comprennent.
const (
	totpDigits     = 6
	totpPeriod     = 30
	totpSecretSize = 20 // 160 bits (RFC 4226 §4)
	// totpSkew : pas tolérés de part et d'autre (horloge du téléphone).
	totpSkew = 1
	// TOTPIssuer : nom affiché dans l'application d'authentification.
	TOTPIssuer = "betorun"
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() ([]byte, error) {
	b := make([]byte, totpSecretSize)
	_, err := rand.Read(b)
	return b, err
}

// totpStep : numéro du pas de 30 s à l'instant t.
func totpStep(t time.Time) int64 { return t.Unix() / totpPeriod }

func totpCode(secret []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, v%1_000_000)
}

// totpMatch : pas accepté pour code (0 : refusé). Les pas déjà utilisés
// (≤ lastUsed) sont refusés : un code ne sert qu'une fois.
func totpMatch(secret []byte, code string, now time.Time, lastUsed int64) int64 {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0
	}
	current := totpStep(now)
	for d := -totpSkew; d <= totpSkew; d++ {
		step := current + int64(d)
		if step <= lastUsed {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, step)), []byte(code)) == 1 {
			return step
		}
	}
	return 0
}

// TOTPURI : URI otpauth:// à afficher en QR code (format Google Authenticator).
func TOTPURI(secret []byte, account string) string {
	label := url.PathEscape(TOTPIssuer + ":" + account)
	q := url.Values{}
	q.Set("secret", totpEncoding.EncodeToString(secret))
	q.Set("issuer", TOTPIssuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// FormatTOTPSecret : secret en base32 par groupes de 4, pour une saisie manuelle.
func FormatTOTPSecret(secret []byte) string {
	s := totpEncoding.EncodeToString(secret)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}
