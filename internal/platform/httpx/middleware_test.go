package httpx

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestID(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if seen == "" || rec.Header().Get(RequestIDHeader) != seen {
		t.Errorf("generated id = %q, header = %q", seen, rec.Header().Get(RequestIDHeader))
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(RequestIDHeader, "abc-123")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "abc-123" {
		t.Errorf("incoming id not reused: %q", seen)
	}

	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set(RequestIDHeader, "bad id\nwith newline")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen == "bad id\nwith newline" {
		t.Error("malformed incoming id was trusted")
	}
}

func TestRecoverAndAccessLog(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }),
		RequestID, AccessLog(logger), Recover(logger))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	out := logs.String()
	for _, want := range []string{"panic serving request", "boom", "http request", "status=500", "level=ERROR"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
}

func TestMaxBodyBytes(t *testing.T) {
	var readErr error
	h := MaxBodyBytes(4)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/", strings.NewReader("12345")))
	if readErr == nil {
		t.Error("oversized body was read without error")
	}
}
