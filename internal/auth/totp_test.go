package auth

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Vecteurs de RFC 6238 annexe B (SHA1), tronqués à 6 chiffres.
func TestTOTP_RFC6238Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for unix, want := range map[int64]string{
		59: "287082", 1111111109: "081804", 1111111111: "050471", 1234567890: "005924", 2000000000: "279037",
	} {
		assert.Equal(t, want, totpCode(secret, totpStep(time.Unix(unix, 0))), "t=%d", unix)
	}
}

func TestTOTP_MatchSkewAndReplay(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111111, 0)
	cur := totpStep(now)
	assert.Equal(t, cur, totpMatch(secret, totpCode(secret, cur), now, 0))
	assert.Equal(t, cur-1, totpMatch(secret, totpCode(secret, cur-1), now, 0), "pas précédent toléré (horloge)")
	assert.Zero(t, totpMatch(secret, totpCode(secret, cur-2), now, 0), "trop ancien")
	assert.Zero(t, totpMatch(secret, totpCode(secret, cur), now, cur), "code déjà utilisé : refusé")
	assert.Equal(t, cur, totpMatch(secret, " "+totpCode(secret, cur)[:3]+" "+totpCode(secret, cur)[3:], now, 0), "espaces tolérés")
	assert.Zero(t, totpMatch(secret, "12345", now, 0))
}

func TestTOTPURI(t *testing.T) {
	uri := TOTPURI([]byte("12345678901234567890"), "awa@ex.sn")
	assert.True(t, strings.HasPrefix(uri, "otpauth://totp/betorun:awa@ex.sn?"), uri)
	assert.Contains(t, uri, "secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ")
	assert.Contains(t, uri, "issuer=betorun")
	assert.Equal(t, "GEZD GNBV GY3T QOJQ GEZD GNBV GY3T QOJQ", FormatTOTPSecret([]byte("12345678901234567890")))
}

func TestSealer_BoundToOwner(t *testing.T) {
	_, err := NewSealer("trop-court")
	assert.ErrorIs(t, err, ErrInvalidKEK)
	s, err := NewSealer(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	require.NoError(t, err)
	a, b := uuid.New(), uuid.New()
	sealed, err := s.Seal([]byte("secret"), associated(a))
	require.NoError(t, err)
	plain, err := s.Open(sealed, associated(a))
	require.NoError(t, err)
	assert.Equal(t, "secret", string(plain))
	_, err = s.Open(sealed, associated(b))
	assert.Error(t, err, "recopié sur un autre compte : indéchiffrable")
}

func TestRecoveryCodes(t *testing.T) {
	c, err := newRecoveryCode()
	require.NoError(t, err)
	assert.Len(t, c, recoveryLen)
	for _, r := range c {
		assert.Contains(t, recoveryAlphabet, string(r))
	}
	assert.Equal(t, "ABCDE23456", normalizeRecovery("abcde-23456"))
}

func TestWeakPassword(t *testing.T) {
	for _, pw := range []string{"password", "aaaaaaaa", "court-123", "aaaaaaaaaaaaaaaa", "Password1234",
		"kofi.mensah-2026!", strings.Repeat("x", MaxPasswordLength) + "y"} {
		assert.True(t, WeakPassword("kofi.mensah@ex.gh", pw), pw)
	}
	for _, pw := range []string{"Teranga-Dakar-2026", "correct horse battery staple", "mon chat s'appelle Rambo"} {
		assert.False(t, WeakPassword("kofi.mensah@ex.gh", pw), pw)
	}
}
