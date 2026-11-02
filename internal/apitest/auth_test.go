package apitest

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestRegister(t *testing.T) {
	s := newServer(t)

	rec := s.do("POST", "/api/auth/register", map[string]string{"name": "Asha", "email": "asha@example.com", "password": "secret-pass-1"}, "")
	body := wantJSON(t, rec, http.StatusCreated)
	if body["message"] != "User registered successfully" {
		t.Errorf("message = %v", body["message"])
	}
	if body["user_id"] != float64(1) {
		t.Errorf("user_id = %v, want 1", body["user_id"])
	}

	t.Run("duplicate email", func(t *testing.T) {
		rec := s.do("POST", "/api/auth/register", map[string]string{"name": "B", "email": "asha@example.com", "password": "secret-pass-1"}, "")
		wantText(t, rec, http.StatusConflict, "Email already registered")
	})
	t.Run("missing fields", func(t *testing.T) {
		for _, req := range []map[string]string{
			{"email": "x@example.com", "password": "secret-pass-1"},
			{"name": "X", "password": "secret-pass-1"},
			{"name": "X", "email": "x@example.com"},
		} {
			rec := s.do("POST", "/api/auth/register", req, "")
			wantText(t, rec, http.StatusBadRequest, "Name, email and password are required")
		}
	})
	t.Run("invalid body", func(t *testing.T) {
		rec := s.do("POST", "/api/auth/register", "{not json", "")
		wantText(t, rec, http.StatusBadRequest, "Invalid request body")
	})
}

func TestLogin(t *testing.T) {
	s := newServer(t)
	id := s.register("Asha", "asha@example.com", "secret-pass-1")

	rec := s.do("POST", "/api/auth/login", map[string]string{"email": "asha@example.com", "password": "secret-pass-1"}, "")
	body := wantJSON(t, rec, http.StatusOK)
	if body["message"] != "Login successful" {
		t.Errorf("message = %v", body["message"])
	}
	if body["user_id"] != float64(id) {
		t.Errorf("user_id = %v, want %d", body["user_id"], id)
	}
	if tok, _ := body["token"].(string); tok == "" {
		t.Error("token missing")
	}

	t.Run("wrong password", func(t *testing.T) {
		rec := s.do("POST", "/api/auth/login", map[string]string{"email": "asha@example.com", "password": "nope-nope-1"}, "")
		wantText(t, rec, http.StatusUnauthorized, "Invalid email or password")
	})
	t.Run("unknown email", func(t *testing.T) {
		rec := s.do("POST", "/api/auth/login", map[string]string{"email": "who@example.com", "password": "secret-pass-1"}, "")
		wantText(t, rec, http.StatusUnauthorized, "Invalid email or password")
	})
	t.Run("invalid body", func(t *testing.T) {
		rec := s.do("POST", "/api/auth/login", "{", "")
		wantText(t, rec, http.StatusBadRequest, "Invalid request body")
	})
	t.Run("wrong method", func(t *testing.T) {
		rec := s.do("GET", "/api/auth/login", nil, "")
		wantStatus(t, rec, http.StatusMethodNotAllowed)
	})
}

func TestLogout(t *testing.T) {
	s := newServer(t)
	body := wantJSON(t, s.do("POST", "/api/auth/logout", nil, ""), http.StatusOK)
	if body["message"] != "Logout successful" {
		t.Errorf("message = %v", body["message"])
	}
}

func TestForgotAndResetPassword(t *testing.T) {
	s := newServer(t)
	s.register("Asha", "asha@example.com", "secret-pass-1")

	rec := s.do("POST", "/api/auth/forgot-password", map[string]string{"email": "asha@example.com"}, "")
	body := wantJSON(t, rec, http.StatusOK)
	if body["message"] != "Password reset process started" {
		t.Errorf("message = %v", body["message"])
	}
	token, _ := body["reset_token"].(string)
	if token == "" {
		t.Fatal("reset_token missing")
	}

	rec = s.do("POST", "/api/auth/reset-password", map[string]string{"token": token, "new_password": "new-secret-2"}, "")
	body = wantJSON(t, rec, http.StatusOK)
	if body["message"] != "Password reset successful" {
		t.Errorf("message = %v", body["message"])
	}
	s.login("asha@example.com", "new-secret-2")
	wantText(t, s.do("POST", "/api/auth/login", map[string]string{"email": "asha@example.com", "password": "secret-pass-1"}, ""),
		http.StatusUnauthorized, "Invalid email or password")

	t.Run("token is single use", func(t *testing.T) {
		rec := s.do("POST", "/api/auth/reset-password", map[string]string{"token": token, "new_password": "again-secret-3"}, "")
		wantText(t, rec, http.StatusBadRequest, "Invalid or expired reset token")
	})
	t.Run("expired token", func(t *testing.T) {
		rec := s.do("POST", "/api/auth/forgot-password", map[string]string{"email": "asha@example.com"}, "")
		token := wantJSON(t, rec, http.StatusOK)["reset_token"].(string)
		s.exec(`UPDATE users SET reset_token_expires_at = CURRENT_TIMESTAMP - INTERVAL '1 minute'`)
		rec = s.do("POST", "/api/auth/reset-password", map[string]string{"token": token, "new_password": "again-secret-3"}, "")
		wantText(t, rec, http.StatusBadRequest, "Invalid or expired reset token")
	})
	t.Run("unknown email", func(t *testing.T) {
		rec := s.do("POST", "/api/auth/forgot-password", map[string]string{"email": "who@example.com"}, "")
		wantText(t, rec, http.StatusOK, "If the email exists, a reset process has been started")
	})
	t.Run("invalid bodies", func(t *testing.T) {
		wantText(t, s.do("POST", "/api/auth/forgot-password", "{", ""), http.StatusBadRequest, "Invalid request body")
		wantText(t, s.do("POST", "/api/auth/reset-password", "{", ""), http.StatusBadRequest, "Invalid request body")
	})
}

func TestProfile(t *testing.T) {
	s := newServer(t)
	_, token := s.userToken()

	body := wantJSON(t, s.do("GET", "/api/profile", nil, token), http.StatusOK)
	if body["message"] != "You accessed a protected API successfully" {
		t.Errorf("message = %v", body["message"])
	}
}

func TestAuthMiddleware(t *testing.T) {
	s := newServer(t)

	wantText(t, s.do("GET", "/api/profile", nil, ""), http.StatusUnauthorized, "Authorization token required")
	wantText(t, s.do("GET", "/api/profile", nil, "not-a-jwt"), http.StatusUnauthorized, "Invalid or expired token")

	req := httptest.NewRequest("GET", "/api/profile", nil)
	req.Header.Set("Authorization", "Token abc")
	wantText(t, s.serve(req), http.StatusUnauthorized, "Invalid authorization format")
}

func TestForgedTokensRejected(t *testing.T) {
	s := newServer(t)
	userID, _ := s.userToken()
	future := time.Now().Add(time.Hour).Unix()
	secret := s.cfg.JWTSecret

	sign := func(method jwt.SigningMethod, key any, claims jwt.MapClaims) string {
		t.Helper()
		tok, err := jwt.NewWithClaims(method, claims).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}

	cases := map[string]string{
		// The secret that used to be hardcoded in utils/jwt.go.
		"old hardcoded secret": sign(jwt.SigningMethodHS256, []byte("my-secret-key"), jwt.MapClaims{"user_id": userID, "exp": future}),
		"alg none":             sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, jwt.MapClaims{"user_id": userID, "exp": future}),
		"other HMAC algorithm": sign(jwt.SigningMethodHS512, secret, jwt.MapClaims{"user_id": userID, "exp": future}),
		"expired":              sign(jwt.SigningMethodHS256, secret, jwt.MapClaims{"user_id": userID, "exp": time.Now().Add(-time.Minute).Unix()}),
		"no expiry":            sign(jwt.SigningMethodHS256, secret, jwt.MapClaims{"user_id": userID}),
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			wantText(t, s.do("GET", "/api/profile", nil, tok), http.StatusUnauthorized, "Invalid or expired token")
		})
	}

	valid := sign(jwt.SigningMethodHS256, secret, jwt.MapClaims{"user_id": userID, "exp": future})
	wantStatus(t, s.do("GET", "/api/profile", nil, valid), http.StatusOK)
}
