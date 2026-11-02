package utils

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWT issues and validates HS256 access tokens signed with a secret from
// configuration.
type JWT struct {
	secret []byte
	ttl    time.Duration
}

func NewJWT(secret []byte, ttl time.Duration) *JWT {
	return &JWT{secret: secret, ttl: ttl}
}

func (j *JWT) Generate(userID int64) (string, error) {

	now := time.Now()

	claims := jwt.MapClaims{
		"user_id": userID,
		"exp":     now.Add(j.ttl).Unix(),
		"iat":     now.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return token.SignedString(j.secret)
}

// Validate accepts only HS256 tokens signed with the configured secret that
// carry an unexpired exp claim.
func (j *JWT) Validate(tokenString string) (*jwt.Token, error) {

	return jwt.Parse(
		tokenString,
		func(*jwt.Token) (interface{}, error) {
			return j.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
}
