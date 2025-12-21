package security

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/thekrauss/kubemanager/internal/core/configs"
)

type JWTManager interface {
	GenerateAccessToken(userID string, globalRole string, duration time.Duration) (string, error)
	VerifyAccessToken(tokenString string) (*UserClaims, error)
}

var _ JWTManager = (*jwtManager)(nil)

type jwtManager struct {
	secretKey string
	issuer    string
}

type UserClaims struct {
	UserID     string `json:"user_id"`
	GlobalRole string `json:"global_role"`
	jwt.RegisteredClaims
}

func NewJWTManager(cfg *configs.JWTConfig, serviceName string) JWTManager {
	return &jwtManager{
		secretKey: cfg.Secret,
		issuer:    serviceName,
	}
}

func (m *jwtManager) GenerateAccessToken(userID string, globalRole string, duration time.Duration) (string, error) {
	claims := UserClaims{
		UserID:     userID,
		GlobalRole: globalRole,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(duration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    m.issuer,
			Subject:   userID,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(m.secretKey))
}

func (m *jwtManager) VerifyAccessToken(tokenString string) (*UserClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &UserClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(m.secretKey), nil
	})

	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(*UserClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}

	return claims, nil
}
