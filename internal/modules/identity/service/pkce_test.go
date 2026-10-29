package services

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPKCEPair_ChallengeIsS256OfVerifier(t *testing.T) {
	verifier, challenge, err := newPKCEPair()
	require.NoError(t, err)

	// RFC 7636 : verifier 43–128 caractères base64url.
	assert.Len(t, verifier, 43)
	sum := sha256.Sum256([]byte(verifier))
	assert.Equal(t, base64.RawURLEncoding.EncodeToString(sum[:]), challenge)
}

func TestNewPKCEPair_Unique(t *testing.T) {
	v1, _, err := newPKCEPair()
	require.NoError(t, err)
	v2, _, err := newPKCEPair()
	require.NoError(t, err)
	assert.NotEqual(t, v1, v2)
}
