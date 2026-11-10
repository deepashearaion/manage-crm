package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient(t *testing.T) {
	var got map[string]any
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&got)
		if got["to"] == "fail" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited","code":130429}}`))
			return
		}
		_, _ = w.Write([]byte(`{"messaging_product":"whatsapp","messages":[{"id":"wamid.OUT"}]}`))
	}))
	defer srv.Close()
	c := NewClient("tok", "v23.0")
	c.BaseURL = srv.URL

	id, err := c.SendText(context.Background(), "1111", "919876543210", "hello")
	if err != nil || id != "wamid.OUT" || path != "/v23.0/1111/messages" || auth != "Bearer tok" ||
		got["type"] != "text" || got["text"].(map[string]any)["body"] != "hello" {
		t.Errorf("text: id %q err %v path %q auth %q body %v", id, err, path, auth, got)
	}

	_, err = c.SendTemplate(context.Background(), "1111", "919876543210", "followup", "en", []string{"Priya"})
	tmpl := got["template"].(map[string]any)
	params := tmpl["components"].([]any)[0].(map[string]any)["parameters"].([]any)
	if err != nil || tmpl["name"] != "followup" || params[0].(map[string]any)["text"] != "Priya" {
		t.Errorf("template: err %v body %v", err, got)
	}

	_, err = c.SendText(context.Background(), "1111", "fail", "x")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.Retryable() || apiErr.Code != 130429 {
		t.Errorf("rate limit error = %v", err)
	}

	if _, err := NewClient("", "v23.0").SendText(context.Background(), "1", "2", "x"); err == nil {
		t.Error("sent without a token")
	}
}
