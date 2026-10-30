package iam

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"math/big"
	"strings"
)

// Format d'une clé API v2 (iam.md §8.3) :
//
//	btr_live_k7f3q9xz2m_<40 caractères base62>_<checksum>
//	│    │    │          │                      └ CRC32 en base62 (6) : faute de frappe détectée hors ligne
//	│    │    │          └ secret ≈ 238 bits
//	│    │    └ identifiant public (lookup, affichage, journaux)
//	│    └ environnement : live | test
//	└ préfixe produit : détection de fuite (secret scanning GitHub)
const (
	apiKeyPrefix   = "btr"
	apiKeyIDLen    = 10
	apiKeySecretLn = 40
	apiKeyCRCLen   = 6
	// apiKeySep : séparateur des segments ; apiKeyParts : préfixe, env, id,
	// secret, checksum.
	apiKeySep   = "_"
	apiKeyParts = 5
	// MinAPIKeyPepperLen : longueur minimale du pepper serveur.
	MinAPIKeyPepperLen = 32
)

const (
	APIKeyEnvLive = "live"
	APIKeyEnvTest = "test"
)

const (
	base62   = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	keyIDSet = "abcdefghijklmnopqrstuvwxyz0123456789"
	// base62Radix : base de l'alphabet base62.
	base62Radix = 62
)

// ErrAPIKeyInvalid : réponse unique à toute clé refusée (format, inconnue,
// révoquée, expirée…) : ne renseigne pas un attaquant sur la cause.
var ErrAPIKeyInvalid = errors.New("invalid API key")

// LooksLikeAPIKeyV2 : la chaîne a le préfixe des clés v2.
func LooksLikeAPIKeyV2(raw string) bool {
	return strings.HasPrefix(raw, apiKeyPrefix+"_")
}

// generateAPIKey : clé complète, identifiant public et secret.
func generateAPIKey(env string) (full, keyID, secret string, err error) {
	if keyID, err = randomString(keyIDSet, apiKeyIDLen); err != nil {
		return "", "", "", err
	}
	if secret, err = randomString(base62, apiKeySecretLn); err != nil {
		return "", "", "", err
	}
	body := apiKeyPrefix + apiKeySep + env + apiKeySep + keyID + apiKeySep + secret
	return body + apiKeySep + checksum(body), keyID, secret, nil
}

// parseAPIKey : découpe et vérifie le checksum, sans accès à la base.
func parseAPIKey(raw string) (env, keyID, secret string, err error) {
	parts := strings.Split(strings.TrimSpace(raw), apiKeySep)
	if len(parts) != apiKeyParts || parts[0] != apiKeyPrefix {
		return "", "", "", ErrAPIKeyInvalid
	}
	env, keyID, secret, crc := parts[1], parts[2], parts[3], parts[4]
	if env != APIKeyEnvLive && env != APIKeyEnvTest || len(keyID) != apiKeyIDLen ||
		len(secret) != apiKeySecretLn || len(crc) != apiKeyCRCLen {
		return "", "", "", ErrAPIKeyInvalid
	}
	body := strings.Join(parts[:apiKeyParts-1], apiKeySep)
	if !hmac.Equal([]byte(checksum(body)), []byte(crc)) {
		return "", "", "", ErrAPIKeyInvalid
	}
	return env, keyID, secret, nil
}

// hashSecret : HMAC-SHA256(pepper, secret) en hexadécimal. Sans le pepper
// (hors base), une fuite de la base ne permet pas de tester des clés.
func hashSecret(pepper []byte, secret string) string {
	m := hmac.New(sha256.New, pepper)
	m.Write([]byte(secret))
	return hex.EncodeToString(m.Sum(nil))
}

func checksum(body string) string {
	n := new(big.Int).SetUint64(uint64(crc32.ChecksumIEEE([]byte(body))))
	out := make([]byte, apiKeyCRCLen)
	b62 := big.NewInt(base62Radix)
	mod := new(big.Int)
	for i := apiKeyCRCLen - 1; i >= 0; i-- {
		n.DivMod(n, b62, mod)
		out[i] = base62[mod.Int64()]
	}
	return string(out)
}

func randomString(alphabet string, n int) (string, error) {
	max := big.NewInt(int64(len(alphabet)))
	out := make([]byte, n)
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[v.Int64()]
	}
	return string(out), nil
}
