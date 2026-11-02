package apitest

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"authentication-backend/internal/platform/config"
)

const forgotMessage = "If an account exists for that email, a password reset link has been sent"

var resetLink = regexp.MustCompile(`http://frontend\.test/reset-password\?token=(\S+)`)

// forgot requests a reset and returns the token from the emailed link
// ("" when no email was sent).
func (s *server) forgot(email string) string {
	s.t.Helper()
	before := len(s.mail.messages())
	body := wantJSON(s.t, s.do("POST", "/api/auth/forgot-password", map[string]string{"email": email}, ""), http.StatusOK)
	if body["message"] != forgotMessage {
		s.t.Errorf("message = %v", body["message"])
	}
	if _, ok := body["reset_token"]; ok {
		s.t.Error("reset_token returned although RETURN_RESET_TOKEN_IN_RESPONSE is off")
	}
	msgs := s.mail.messages()
	if len(msgs) == before {
		return ""
	}
	m := resetLink.FindStringSubmatch(msgs[len(msgs)-1].Text)
	if m == nil {
		s.t.Fatalf("no reset link in email:\n%s", msgs[len(msgs)-1].Text)
	}
	token, err := url.QueryUnescape(m[1])
	if err != nil {
		s.t.Fatal(err)
	}
	return token
}

func (s *server) reset(token, password string) *httpResult {
	s.t.Helper()
	return s.do("POST", "/api/auth/reset-password", map[string]string{"token": token, "new_password": password}, "")
}

func TestForgotPasswordEmailsLink(t *testing.T) {
	s := newServer(t)
	s.register("Asha", "asha@example.com", "secret-pass-1")

	token := s.forgot("  ASHA@example.com ")
	if len(token) < 43 { // 32 random bytes, base64url
		t.Fatalf("token %q too short", token)
	}
	msg := s.mail.messages()[0]
	if msg.To != "asha@example.com" || msg.Subject != "Reset your password" {
		t.Errorf("email to %q subject %q", msg.To, msg.Subject)
	}

	// Only the hash is stored.
	var stored []byte
	if err := s.db.QueryRow(context.Background(), `SELECT token_hash FROM password_reset_tokens`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256([]byte(token)); string(stored) != string(sum[:]) {
		t.Error("stored value is not the SHA-256 of the token")
	}

	body := wantJSON(t, s.reset(token, "new-secret-2"), http.StatusOK)
	if body["message"] != "Password reset successful" {
		t.Errorf("message = %v", body["message"])
	}
	s.login("asha@example.com", "new-secret-2")
	wantText(t, s.do("POST", "/api/auth/login", map[string]string{"email": "asha@example.com", "password": "secret-pass-1"}, ""),
		http.StatusUnauthorized, "Invalid email or password")
}

func TestForgotPasswordDoesNotRevealAccounts(t *testing.T) {
	s := newServer(t)
	s.register("Asha", "asha@example.com", "secret-pass-1")

	known := s.do("POST", "/api/auth/forgot-password", map[string]string{"email": "asha@example.com"}, "")
	unknown := s.do("POST", "/api/auth/forgot-password", map[string]string{"email": "who@example.com"}, "")
	invalid := s.do("POST", "/api/auth/forgot-password", map[string]string{"email": "not-an-email"}, "")

	for name, rec := range map[string]*httpResult{"unknown": unknown, "invalid": invalid} {
		if rec.Code != known.Code || rec.Body.String() != known.Body.String() || rec.Header().Get("Content-Type") != known.Header().Get("Content-Type") {
			t.Errorf("%s email response differs: %d %q vs %d %q", name, rec.Code, rec.Body.String(), known.Code, known.Body.String())
		}
	}
	if n := len(s.mail.messages()); n != 1 {
		t.Errorf("emails sent = %d, want 1 (only for the existing account)", n)
	}
}

func TestResetTokenRules(t *testing.T) {
	s := newServer(t)
	s.register("Asha", "asha@example.com", "secret-pass-1")
	invalid := func(t *testing.T, token string) {
		t.Helper()
		wantText(t, s.reset(token, "another-pass-9"), http.StatusBadRequest, "Invalid or expired reset token")
	}

	t.Run("single use", func(t *testing.T) {
		token := s.forgot("asha@example.com")
		wantStatus(t, s.reset(token, "new-secret-2"), http.StatusOK)
		invalid(t, token)
	})
	t.Run("expires", func(t *testing.T) {
		token := s.forgot("asha@example.com")
		s.exec(`UPDATE password_reset_tokens SET expires_at = now() - interval '1 second' WHERE used_at IS NULL`)
		invalid(t, token)
	})
	t.Run("expiry is 30 minutes", func(t *testing.T) {
		s.forgot("asha@example.com")
		var minutes float64
		if err := s.db.QueryRow(context.Background(),
			`SELECT EXTRACT(EPOCH FROM expires_at - created_at) / 60 FROM password_reset_tokens WHERE used_at IS NULL`,
		).Scan(&minutes); err != nil {
			t.Fatal(err)
		}
		if minutes < 29.9 || minutes > 30.1 {
			t.Errorf("token lifetime = %.2f minutes, want 30", minutes)
		}
	})
	t.Run("newer request invalidates older token", func(t *testing.T) {
		older := s.forgot("asha@example.com")
		newer := s.forgot("asha@example.com")
		invalid(t, older)
		wantStatus(t, s.reset(newer, "new-secret-3"), http.StatusOK)
	})
	t.Run("password change invalidates outstanding tokens", func(t *testing.T) {
		token := s.forgot("asha@example.com")
		// A second outstanding token, as if issued concurrently.
		other := sha256.Sum256([]byte("other-outstanding-token"))
		s.exec(`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
			SELECT id, $1, now() + interval '10 minutes' FROM users`, other[:])
		wantStatus(t, s.reset(token, "new-secret-4"), http.StatusOK)
		invalid(t, "other-outstanding-token")
	})
	t.Run("unknown and empty tokens", func(t *testing.T) {
		invalid(t, "made-up")
		invalid(t, "")
	})
	t.Run("password length enforced", func(t *testing.T) {
		token := s.forgot("asha@example.com")
		for _, pw := range []string{"", "short", strings.Repeat("x", 73)} {
			wantText(t, s.reset(token, pw), http.StatusBadRequest, "Password must be 8 to 72 characters long")
		}
		wantStatus(t, s.reset(token, "long-enough"), http.StatusOK) // token was not consumed by the rejected attempts
	})
	t.Run("invalid bodies", func(t *testing.T) {
		wantText(t, s.do("POST", "/api/auth/forgot-password", "{", ""), http.StatusBadRequest, "Invalid request body")
		wantText(t, s.do("POST", "/api/auth/reset-password", "{", ""), http.StatusBadRequest, "Invalid request body")
	})
}

func TestResetTokenInResponseFlag(t *testing.T) {
	env := testEnv()
	env["APP_ENV"] = config.EnvDevelopment
	env["RETURN_RESET_TOKEN_IN_RESPONSE"] = "true"
	s := newServerWith(t, env)
	s.register("Asha", "asha@example.com", "secret-pass-1")

	body := wantJSON(t, s.do("POST", "/api/auth/forgot-password", map[string]string{"email": "asha@example.com"}, ""), http.StatusOK)
	token, _ := body["reset_token"].(string)
	if token == "" || body["message"] != forgotMessage {
		t.Fatalf("legacy response = %v", body)
	}
	wantStatus(t, s.reset(token, "new-secret-2"), http.StatusOK)

	body = wantJSON(t, s.do("POST", "/api/auth/forgot-password", map[string]string{"email": "who@example.com"}, ""), http.StatusOK)
	if _, ok := body["reset_token"]; ok {
		t.Error("reset_token returned for an unknown email")
	}
}
