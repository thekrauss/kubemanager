package security

import "golang.org/x/crypto/bcrypt"

type PasswordHasher interface {
	CheckPasswordHash(password, hash string) bool
	HashPassword(password string) (string, error)
}

type ConcretePasswordHasher struct{}

func (c *ConcretePasswordHasher) HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func (c *ConcretePasswordHasher) CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

var _ PasswordHasher = (*ConcretePasswordHasher)(nil)
