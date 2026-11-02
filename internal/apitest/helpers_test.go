// Package apitest holds black-box HTTP tests for the legacy /api/* routes.
// They pin the current observable behavior (status codes, JSON shapes and
// plain-text error bodies) so refactors can be checked against them.
package apitest

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepashearaion/manage-crm/internal/app"
	"github.com/deepashearaion/manage-crm/internal/platform/config"
	"github.com/deepashearaion/manage-crm/internal/platform/logging"
	"github.com/deepashearaion/manage-crm/internal/platform/mail"
	"github.com/deepashearaion/manage-crm/internal/testutil"
)

type server struct {
	t    *testing.T
	h    http.Handler
	db   *pgxpool.Pool
	cfg  *config.Config
	mail *captureMailer
}

// testEnv is the configuration the API tests run with; tests may override
// entries before calling newServerWith.
func testEnv() map[string]string {
	return map[string]string{
		"APP_ENV":      config.EnvTest,
		"DB_HOST":      "unused",
		"DB_USER":      "unused",
		"DB_NAME":      "unused",
		"JWT_SECRET":   strings.Repeat("t", 32),
		"FRONTEND_URL": "http://frontend.test",
	}
}

// newServer builds the full HTTP stack, as cmd/api does, on an empty test database.
func newServer(t *testing.T) *server {
	return newServerWith(t, testEnv())
}

func newServerWith(t *testing.T, env map[string]string) *server {
	t.Helper()
	cfg, err := config.FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	db := testutil.FreshDB(t)
	mailer := &captureMailer{}
	h := app.NewHandler(app.Deps{
		Config: cfg,
		DB:     db,
		Logger: logging.New(testWriter{t}, "text", "warn"),
		Mailer: mailer,
	})
	return &server{t: t, h: h, db: db, cfg: cfg, mail: mailer}
}

// testWriter sends log output to the test log, shown only for failing tests.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

// do sends a request. body may be nil, a string (sent verbatim) or any value
// (sent as JSON). token, when non-empty, is sent as a Bearer token.
func (s *server) do(method, path string, body any, token string) *httptest.ResponseRecorder {
	s.t.Helper()
	var reader *bytes.Reader
	switch b := body.(type) {
	case nil:
		reader = bytes.NewReader(nil)
	case string:
		reader = bytes.NewReader([]byte(b))
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			s.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return s.serve(req)
}

func newRequest(method, path string) *http.Request {
	return httptest.NewRequest(method, path, nil)
}

func (s *server) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	return rec
}

func (s *server) exec(sql string, args ...any) {
	s.t.Helper()
	if _, err := s.db.Exec(context.Background(), sql, args...); err != nil {
		s.t.Fatalf("exec %q: %v", sql, err)
	}
}

// register creates a user and returns its id.
func (s *server) register(name, email, password string) int64 {
	s.t.Helper()
	rec := s.do("POST", "/api/auth/register", map[string]string{"name": name, "email": email, "password": password}, "")
	wantStatus(s.t, rec, http.StatusCreated)
	return int64(decode(s.t, rec)["user_id"].(float64))
}

// login returns a bearer token for the user.
func (s *server) login(email, password string) string {
	s.t.Helper()
	rec := s.do("POST", "/api/auth/login", map[string]string{"email": email, "password": password}, "")
	wantStatus(s.t, rec, http.StatusOK)
	return decode(s.t, rec)["token"].(string)
}

// userToken registers a default user and returns its id and token.
func (s *server) userToken() (int64, string) {
	s.t.Helper()
	id := s.register("Asha", "asha@example.com", "secret-pass-1")
	return id, s.login("asha@example.com", "secret-pass-1")
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, want, rec.Body.String())
	}
}

// wantText asserts a legacy plain-text error response from http.Error.
func wantText(t *testing.T, rec *httptest.ResponseRecorder, status int, msg string) {
	t.Helper()
	wantStatus(t, rec, status)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if got := strings.TrimSuffix(rec.Body.String(), "\n"); got != msg {
		t.Errorf("body = %q, want %q", got, msg)
	}
}

// wantJSON asserts a JSON response and returns the decoded object.
func wantJSON(t *testing.T, rec *httptest.ResponseRecorder, status int) map[string]any {
	t.Helper()
	wantStatus(t, rec, status)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	return decode(t, rec)
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode JSON %q: %v", rec.Body.String(), err)
	}
	return out
}

// captureMailer records emails instead of sending them.
type captureMailer struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (m *captureMailer) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *captureMailer) messages() []mail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mail.Message(nil), m.sent...)
}

type httpResult = httptest.ResponseRecorder
