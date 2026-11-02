package apitest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/deepashearaion/manage-crm/handlers"
)

// loginSession logs in and returns the access token and refresh cookie.
func (s *server) loginSession(email, password string) (string, *http.Cookie) {
	s.t.Helper()
	rec := s.do("POST", "/api/auth/login", map[string]string{"email": email, "password": password}, "")
	body := wantJSON(s.t, rec, http.StatusOK)
	return body["token"].(string), refreshCookie(s.t, rec)
}

// refreshCookie returns the refresh cookie set by a response, failing if absent.
func refreshCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() { //nolint:bodyclose // recorder body
		if c.Name == handlers.RefreshCookieName {
			return c
		}
	}
	t.Fatalf("no %s cookie in response; Set-Cookie: %q", handlers.RefreshCookieName, rec.Header().Values("Set-Cookie"))
	return nil
}

// postWithCookie sends a body-less POST carrying the refresh cookie, if any.
func (s *server) postWithCookie(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	s.t.Helper()
	req := httptest.NewRequest("POST", path, nil)
	if cookie != nil {
		req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	return s.serve(req)
}

func TestLoginSetsRefreshCookie(t *testing.T) {
	s := newServer(t)
	s.register("Asha", "asha@example.com", "secret-pass-1")

	rec := s.do("POST", "/api/auth/login", map[string]string{"email": "asha@example.com", "password": "secret-pass-1"}, "")
	body := wantJSON(t, rec, http.StatusOK)
	if body["expires_in"] != s.cfg.AccessTokenTTL.Seconds() {
		t.Errorf("expires_in = %v, want %v", body["expires_in"], s.cfg.AccessTokenTTL.Seconds())
	}

	c := refreshCookie(t, rec)
	if !c.HttpOnly || c.Path != "/api/auth" || c.SameSite != http.SameSiteLaxMode || c.Secure != s.cfg.RefreshCookieSecure {
		t.Errorf("cookie attributes: %+v", c)
	}
	if want := s.cfg.RefreshTokenTTL.Seconds(); float64(c.MaxAge) < want-5 || float64(c.MaxAge) > want {
		t.Errorf("Max-Age = %d, want about %v", c.MaxAge, want)
	}
	if len(c.Value) < 43 {
		t.Errorf("refresh token %q too short", c.Value)
	}

	// Only the hash is stored.
	var n int
	if err := s.db.QueryRow(context.Background(), `SELECT count(*) FROM refresh_tokens WHERE token_hash = sha256($1::bytea)`, c.Value).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("stored hashes matching the cookie = %d, want 1", n)
	}
}

func TestRefreshRotates(t *testing.T) {
	s := newServer(t)
	userID := s.register("Asha", "asha@example.com", "secret-pass-1")
	_, first := s.loginSession("asha@example.com", "secret-pass-1")

	rec := s.postWithCookie("/api/auth/refresh", first)
	body := wantJSON(t, rec, http.StatusOK)
	if body["user_id"] != float64(userID) || body["expires_in"] != s.cfg.AccessTokenTTL.Seconds() {
		t.Errorf("refresh body = %v", body)
	}
	access, _ := body["token"].(string)
	wantStatus(t, s.do("GET", "/api/profile", nil, access), http.StatusOK)

	second := refreshCookie(t, rec)
	if second.Value == first.Value {
		t.Fatal("refresh token was not rotated")
	}
	wantStatus(t, s.postWithCookie("/api/auth/refresh", second), http.StatusOK)
}

func TestRefreshReuseRevokesSession(t *testing.T) {
	s := newServer(t)
	s.register("Asha", "asha@example.com", "secret-pass-1")
	_, stolen := s.loginSession("asha@example.com", "secret-pass-1")
	_, other := s.loginSession("asha@example.com", "secret-pass-1") // a separate session

	// The legitimate client refreshes; then the stolen (rotated) token is replayed.
	current := refreshCookie(t, s.postWithCookie("/api/auth/refresh", stolen))

	rec := s.postWithCookie("/api/auth/refresh", stolen)
	wantText(t, rec, http.StatusUnauthorized, "Invalid or expired refresh token")
	if c := refreshCookie(t, rec); c.MaxAge >= 0 {
		t.Errorf("cookie not cleared on reuse: %+v", c)
	}

	// The whole session is gone, including the token issued after rotation...
	wantText(t, s.postWithCookie("/api/auth/refresh", current), http.StatusUnauthorized, "Invalid or expired refresh token")
	// ...but other sessions of the same user are unaffected.
	wantStatus(t, s.postWithCookie("/api/auth/refresh", other), http.StatusOK)
}

func TestRefreshRejects(t *testing.T) {
	s := newServer(t)
	s.register("Asha", "asha@example.com", "secret-pass-1")

	wantText(t, s.postWithCookie("/api/auth/refresh", nil), http.StatusUnauthorized, "Invalid or expired refresh token")
	wantText(t, s.postWithCookie("/api/auth/refresh", &http.Cookie{Name: handlers.RefreshCookieName, Value: "made-up"}),
		http.StatusUnauthorized, "Invalid or expired refresh token")

	_, c := s.loginSession("asha@example.com", "secret-pass-1")
	s.exec(`UPDATE refresh_tokens SET expires_at = now() - interval '1 second'`)
	wantText(t, s.postWithCookie("/api/auth/refresh", c), http.StatusUnauthorized, "Invalid or expired refresh token")
}

func TestLogoutRevokesSession(t *testing.T) {
	s := newServer(t)
	s.register("Asha", "asha@example.com", "secret-pass-1")
	_, c := s.loginSession("asha@example.com", "secret-pass-1")

	rec := s.postWithCookie("/api/auth/logout", c)
	body := wantJSON(t, rec, http.StatusOK)
	if body["message"] != "Logout successful" {
		t.Errorf("message = %v", body["message"])
	}
	if cleared := refreshCookie(t, rec); cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Errorf("cookie not cleared: %+v", cleared)
	}
	wantText(t, s.postWithCookie("/api/auth/refresh", c), http.StatusUnauthorized, "Invalid or expired refresh token")

	// Logging out again, or without a cookie, still succeeds.
	wantStatus(t, s.postWithCookie("/api/auth/logout", c), http.StatusOK)
	wantStatus(t, s.postWithCookie("/api/auth/logout", nil), http.StatusOK)
}

func TestPasswordResetRevokesSessions(t *testing.T) {
	s := newServer(t)
	s.register("Asha", "asha@example.com", "secret-pass-1")
	_, c := s.loginSession("asha@example.com", "secret-pass-1")

	token := s.forgot("asha@example.com")
	wantStatus(t, s.reset(token, "new-secret-2"), http.StatusOK)

	wantText(t, s.postWithCookie("/api/auth/refresh", c), http.StatusUnauthorized, "Invalid or expired refresh token")
}

func TestAccessTokenLifetime(t *testing.T) {
	env := testEnv()
	env["ACCESS_TOKEN_TTL"] = "1m"
	s := newServerWith(t, env)
	s.register("Asha", "asha@example.com", "secret-pass-1")

	access, _ := s.loginSession("asha@example.com", "secret-pass-1")
	var exp float64
	claims := decodeJWTClaims(t, access)
	exp, _ = claims["exp"].(float64)
	if left := time.Until(time.Unix(int64(exp), 0)); left > time.Minute || left < 50*time.Second {
		t.Errorf("access token expires in %s, want about 1m", left)
	}
}

// Concurrent refreshes with the same token: one wins, and the other is
// treated as reuse, so the session ends. Clients must serialize refreshes.
func TestConcurrentRefreshOfOneToken(t *testing.T) {
	s := newServer(t)
	s.register("Asha", "asha@example.com", "secret-pass-1")
	_, c := s.loginSession("asha@example.com", "secret-pass-1")

	codes := make(chan int, 2)
	for range 2 {
		go func() { codes <- s.postWithCookie("/api/auth/refresh", c).Code }()
	}
	got := []int{<-codes, <-codes}
	slices.Sort(got)
	if got[0] != http.StatusOK || got[1] != http.StatusUnauthorized {
		t.Errorf("codes = %v; want one 200 and one 401", got)
	}
}
