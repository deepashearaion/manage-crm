package validate

import "testing"

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
		if got := Email(email); got != want {
			t.Errorf("Email(%q) = %v, want %v", email, got, want)
		}
	}
}
