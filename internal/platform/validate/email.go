// Package validate holds input checks shared by several modules.
package validate

import (
	"net/mail"
	"strings"
)

// NormalizeEmail trims and lowercases an email address for storage and lookup.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// Email reports whether email is a bare address such as "a@b.co" (no display
// name or angle brackets, and a dot in the domain).
func Email(email string) bool {
	if len(email) > 254 {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email && strings.Contains(email[strings.LastIndex(email, "@"):], ".")
}
