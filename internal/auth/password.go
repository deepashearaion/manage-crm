package auth

import (
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

const (
	MinPasswordLength = 8
	// MaxPasswordBytes is bcrypt's input limit; longer passwords are rejected
	// rather than silently truncated.
	MaxPasswordBytes = 72
)

// ValidPassword reports whether password meets the length rules.
func ValidPassword(password string) bool {
	return utf8.RuneCountInString(password) >= MinPasswordLength && len(password) <= MaxPasswordBytes
}

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

func checkPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
