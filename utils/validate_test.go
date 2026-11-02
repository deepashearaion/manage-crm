package utils

import (
	"strings"
	"testing"
)

func TestValidPassword(t *testing.T) {
	for pw, want := range map[string]bool{
		"":                      false,
		"1234567":               false,
		"12345678":              true,
		"pässwörd":              true, // 8 runes, 10 bytes
		strings.Repeat("a", 72): true,
		strings.Repeat("a", 73): false,
		strings.Repeat("é", 37): false, // 37 runes but 74 bytes
	} {
		if got := ValidPassword(pw); got != want {
			t.Errorf("ValidPassword(%q) = %v, want %v", pw, got, want)
		}
	}
}

func TestEmail(t *testing.T) {
	if got := NormalizeEmail("  Asha@Example.COM "); got != "asha@example.com" {
		t.Errorf("NormalizeEmail = %q", got)
	}
	for email, want := range map[string]bool{
		"asha@example.com":         true,
		"a.b+tag@sub.example.co":   true,
		"":                         false,
		"asha":                     false,
		"asha@localhost":           false,
		"Asha <asha@example.com>":  false,
		"asha@example.com\r\nBcc:": false,
	} {
		if got := ValidEmail(email); got != want {
			t.Errorf("ValidEmail(%q) = %v, want %v", email, got, want)
		}
	}
}
