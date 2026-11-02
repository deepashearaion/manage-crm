package apitest

import (
	"fmt"
	"net/http"
	"testing"
)

func createContact(t *testing.T, s *server, token string, fields map[string]any) map[string]any {
	t.Helper()
	body := wantJSON(t, s.do("POST", "/api/contacts", fields, token), http.StatusCreated)
	return body["contact"].(map[string]any)
}

func TestCreateContact(t *testing.T) {
	s := newServer(t)
	userID, token := s.userToken()

	rec := s.do("POST", "/api/contacts", map[string]any{
		"first_name":    "Ravi",
		"last_name":     "Kumar",
		"email":         "ravi@example.com",
		"mobile":        "+91 9876543210",
		"company_id":    5,
		"source":        "Website",
		"notes":         "met at expo",
		"lead_owner_id": 999, // ignored: the caller becomes the owner
	}, token)
	body := wantJSON(t, rec, http.StatusCreated)
	if body["message"] != "Contact created successfully" {
		t.Errorf("message = %v", body["message"])
	}
	c := body["contact"].(map[string]any)
	want := map[string]any{
		"id": float64(1), "first_name": "Ravi", "last_name": "Kumar", "email": "ravi@example.com",
		"mobile": "+91 9876543210", "alternate_mobile": "", "company_id": float64(5), "lead_status_id": nil,
		"lead_owner_id": float64(userID), "destination": "", "source": "Website", "notes": "met at expo",
	}
	for k, v := range want {
		if c[k] != v {
			t.Errorf("contact[%q] = %#v, want %#v", k, c[k], v)
		}
	}
	for _, k := range []string{"created_at", "updated_at"} {
		if s, _ := c[k].(string); s == "" {
			t.Errorf("contact[%q] missing", k)
		}
	}

	t.Run("first name required", func(t *testing.T) {
		rec := s.do("POST", "/api/contacts", map[string]any{"first_name": "   "}, token)
		wantText(t, rec, http.StatusBadRequest, "First name is required")
	})
	t.Run("invalid body", func(t *testing.T) {
		wantText(t, s.do("POST", "/api/contacts", "{", token), http.StatusBadRequest, "Invalid request body")
	})
	t.Run("requires auth", func(t *testing.T) {
		wantText(t, s.do("POST", "/api/contacts", map[string]any{"first_name": "X"}, ""), http.StatusUnauthorized, "Authorization token required")
	})
}

func TestListContacts(t *testing.T) {
	s := newServer(t)
	_, token := s.userToken()

	body := wantJSON(t, s.do("GET", "/api/contacts", nil, token), http.StatusOK)
	if list, ok := body["contacts"].([]any); !ok || len(list) != 0 {
		t.Fatalf("contacts = %#v, want empty array", body["contacts"])
	}

	createContact(t, s, token, map[string]any{"first_name": "First"})
	createContact(t, s, token, map[string]any{"first_name": "Second"})

	body = wantJSON(t, s.do("GET", "/api/contacts", nil, token), http.StatusOK)
	list := body["contacts"].([]any)
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	if got := list[0].(map[string]any)["first_name"]; got != "Second" {
		t.Errorf("first item = %v, want newest first", got)
	}
	if got := list[0].(map[string]any)["last_name"]; got != "" {
		t.Errorf("NULL last_name rendered as %#v, want empty string", got)
	}
}

func TestUpdateContact(t *testing.T) {
	s := newServer(t)
	_, token := s.userToken()
	c := createContact(t, s, token, map[string]any{"first_name": "Ravi", "notes": "old", "source": "Website"})
	path := fmt.Sprintf("/api/contacts/%v", c["id"])

	rec := s.do("PATCH", path, map[string]any{"notes": "new", "lead_status_id": 3}, token)
	body := wantJSON(t, rec, http.StatusOK)
	if body["message"] != "Contact updated successfully" {
		t.Errorf("message = %v", body["message"])
	}
	got := body["contact"].(map[string]any)
	if got["notes"] != "new" || got["lead_status_id"] != float64(3) {
		t.Errorf("updated fields = %v / %v", got["notes"], got["lead_status_id"])
	}
	if got["first_name"] != "Ravi" || got["source"] != "Website" {
		t.Errorf("untouched fields changed: %v / %v", got["first_name"], got["source"])
	}

	t.Run("not found", func(t *testing.T) {
		wantText(t, s.do("PATCH", "/api/contacts/999", map[string]any{"notes": "x"}, token), http.StatusNotFound, "Contact not found")
	})
	t.Run("invalid id", func(t *testing.T) {
		wantText(t, s.do("PATCH", "/api/contacts/abc", map[string]any{"notes": "x"}, token), http.StatusBadRequest, "Invalid contact ID")
		wantText(t, s.do("PATCH", "/api/contacts/0", map[string]any{"notes": "x"}, token), http.StatusBadRequest, "Invalid contact ID")
	})
	t.Run("invalid body", func(t *testing.T) {
		wantText(t, s.do("PATCH", path, "{", token), http.StatusBadRequest, "Invalid request body")
	})
}

func TestDeleteContact(t *testing.T) {
	s := newServer(t)
	_, token := s.userToken()
	c := createContact(t, s, token, map[string]any{"first_name": "Ravi"})
	path := fmt.Sprintf("/api/contacts/%v", c["id"])

	body := wantJSON(t, s.do("DELETE", path, nil, token), http.StatusOK)
	if body["message"] != "Contact deleted successfully" {
		t.Errorf("message = %v", body["message"])
	}
	wantText(t, s.do("DELETE", path, nil, token), http.StatusNotFound, "Contact not found")
	wantText(t, s.do("DELETE", "/api/contacts/x", nil, token), http.StatusBadRequest, "Invalid contact ID")
}

func TestGetContactByID(t *testing.T) {
	s := newServer(t)
	_, token := s.userToken()
	c := createContact(t, s, token, map[string]any{"first_name": "Ravi", "email": "ravi@example.com"})

	body := wantJSON(t, s.do("GET", fmt.Sprintf("/api/contacts/%v", c["id"]), nil, token), http.StatusOK)
	got := body["contact"].(map[string]any)
	if got["id"] != c["id"] || got["first_name"] != "Ravi" || got["email"] != "ravi@example.com" || got["last_name"] != "" {
		t.Errorf("contact = %v", got)
	}

	wantText(t, s.do("GET", "/api/contacts/999", nil, token), http.StatusNotFound, "Contact not found")
	wantText(t, s.do("GET", "/api/contacts/abc", nil, token), http.StatusBadRequest, "Invalid contact ID")
	wantText(t, s.do("GET", "/api/contacts/1", nil, ""), http.StatusUnauthorized, "Authorization token required")
}
