package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrInvalidKEK : clé de chiffrement absente ou mal formée.
var ErrInvalidKEK = errors.New("auth: key encryption key must be 32 bytes, base64-encoded")

// Sealer : chiffrement des secrets en base (AES-256-GCM), avec une clé hors
// base (même KEK que le STS, remplaçable par un KMS). Les données associées
// lient un secret à son propriétaire : recopié sur un autre compte, il ne se
// déchiffre plus.
type Sealer struct {
	aead cipher.AEAD
}

func NewSealer(kekBase64 string) (*Sealer, error) {
	kek, err := base64.StdEncoding.DecodeString(kekBase64)
	if err != nil || len(kek) != 32 {
		return nil, ErrInvalidKEK
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal : nonce || texte chiffré.
func (s *Sealer) Seal(plain []byte, associated string) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plain, []byte(associated)), nil
}

func (s *Sealer) Open(sealed []byte, associated string) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("auth: sealed value too short")
	}
	plain, err := s.aead.Open(nil, sealed[:n], sealed[n:], []byte(associated))
	if err != nil {
		return nil, fmt.Errorf("auth: open sealed value: %w", err)
	}
	return plain, nil
}

// hashToken : empreinte d'un jeton aléatoire de 256 bits (aucun pepper
// nécessaire : impossible à deviner).
func hashToken(raw string) []byte {
	h := sha256.Sum256([]byte(raw))
	return h[:]
}

// RandomToken : 32 octets aléatoires, base64url (identifiants imprévisibles).
func RandomToken() (string, error) { return randomToken() }

// randomToken : 32 octets aléatoires, base64url.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// keyedHash : HMAC-SHA256 avec le pepper serveur (codes de secours : courts,
// donc protégés contre une attaque hors ligne si la base fuit).
func keyedHash(pepper []byte, value string) []byte {
	m := hmac.New(sha256.New, pepper)
	m.Write([]byte(value))
	return m.Sum(nil)
}
