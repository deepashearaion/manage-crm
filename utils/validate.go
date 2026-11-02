package utils

import (
	"net/mail"
	"strings"
	"unicode/utf8"
)

const (
	MinPasswordLength = 8
	// MaxPasswordBytes is bcrypt's input limit; longer passwords are rejected
	// rather than silently truncated.
	MaxPasswordBytes = 72

	PasswordLengthMessage = "Password must be 8 to 72 characters long"
	InvalidEmailMessage   = "Invalid email address"
)

// ValidPassword reports whether password meets the length rules.
func ValidPassword(password string) bool {
	return utf8.RuneCountInString(password) >= MinPasswordLength && len(password) <= MaxPasswordBytes
}

// NormalizeEmail trims and lowercases an email address for storage and lookup.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidEmail reports whether email is a bare address such as "a@b.co"
// (no display name or angle brackets).
func ValidEmail(email string) bool {
	if len(email) > 254 {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email && strings.Contains(email[strings.LastIndex(email, "@"):], ".")
}
