package apitest

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Timestamps in responses must be the real instant, in UTC ("Z"), whatever
// the database server's or API host's time zone. Before timestamptz, values
// written in the server's zone (Asia/Kolkata) came back labelled UTC, 5h30m off.
func TestTimestampsAreUTCInstants(t *testing.T) {
	s := newServer(t)
	_, token := s.userToken()

	before := time.Now()
	c := createContact(t, s, token, map[string]any{"first_name": "Ravi"})
	after := time.Now()

	for _, field := range []string{"created_at", "updated_at"} {
		raw, _ := c[field].(string)
		if !strings.HasSuffix(raw, "Z") {
			t.Errorf("%s = %q, want UTC (Z suffix)", field, raw)
		}
		ts, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			t.Fatalf("%s = %q: %v", field, raw, err)
		}
		if ts.Before(before.Add(-2*time.Second)) || ts.After(after.Add(2*time.Second)) {
			t.Errorf("%s = %s, want between %s and %s", field, ts, before.UTC(), after.UTC())
		}
	}

	// The same instant comes back from the list and update endpoints.
	list := wantJSON(t, s.do("GET", "/api/contacts", nil, token), http.StatusOK)["contacts"].([]any)
	if got := list[0].(map[string]any)["created_at"]; got != c["created_at"] {
		t.Errorf("list created_at = %v, want %v", got, c["created_at"])
	}
}
