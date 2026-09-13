package platform

import (
	"context"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"time"
)

type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type AuthToken struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

type PlatformClaims struct {
	OperatorID string `json:"operatorId"`
	IsPlatform bool   `json:"isPlatform"`
	jwt.RegisteredClaims
}

func (s *Service) Login(ctx context.Context, input LoginInput) (AuthToken, error) {
	var id, passwordHash, status string
	err := s.pool.QueryRow(ctx, "SELECT id::text, password_hash, status FROM platform_operators WHERE email = $1", input.Email).Scan(&id, &passwordHash, &status)
	if err != nil {
		return AuthToken{}, ErrUnauthorized
	}

	if status != "active" {
		return AuthToken{}, ErrUnauthorized
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(input.Password)); err != nil {
		return AuthToken{}, ErrUnauthorized
	}

	expiration := time.Now().Add(12 * time.Hour)
	claims := &PlatformClaims{
		OperatorID: id,
		IsPlatform: true,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiration),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "easyhr-platform",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(s.jwtSecret))
	if err != nil {
		return AuthToken{}, err
	}

	return AuthToken{
		Token:     tokenString,
		ExpiresAt: expiration.Format(time.RFC3339),
	}, nil
}
