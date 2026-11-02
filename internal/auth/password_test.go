package auth

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

func TestPasswordHash(t *testing.T) {
	hash, err := hashPassword("secret-pass-1")
	if err != nil {
		t.Fatal(err)
	}
	if !checkPassword("secret-pass-1", hash) || checkPassword("secret-pass-2", hash) {
		t.Error("checkPassword does not match hashPassword")
	}
}
