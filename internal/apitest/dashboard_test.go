package apitest

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSalesDashboard(t *testing.T) {
	s := newServer(t)
	_, token := s.userToken()

	zero := wantJSON(t, s.do("GET", "/api/dashboard/sales", nil, token), http.StatusOK)
	for _, k := range []string{"total_open_deals", "total_open_deal_value", "total_won", "total_loss", "total_new_customers", "total_arr", "total_mrr"} {
		if zero[k] != float64(0) {
			t.Errorf("empty %s = %v, want 0", k, zero[k])
		}
	}

	createContact(t, s, token, map[string]any{"first_name": "Today"})
	s.exec(`INSERT INTO deals (title, amount, stage_id, monthly_recurring_revenue, annual_recurring_revenue)
		SELECT v.title, v.amount, ds.id, v.mrr, v.arr
		FROM (VALUES
			('open-1', 100.50, 'Proposal', 10, 120),
			('open-2', 200, 'Qualification', 0, 0),
			('won', 1000, 'Closed Won', 50, 600),
			('lost', 300, 'Closed Lost', 5, 60)
		) AS v(title, amount, stage, mrr, arr)
		JOIN deal_stages ds ON ds.name = v.stage`)

	got := wantJSON(t, s.do("GET", "/api/dashboard/sales", nil, token), http.StatusOK)
	// Current behavior: ARR/MRR sum every deal, including open and lost ones.
	want := map[string]float64{
		"total_open_deals": 2, "total_open_deal_value": 300.5, "total_won": 1000, "total_loss": 300,
		"total_new_customers": 1, "total_arr": 780, "total_mrr": 65,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}

	wantText(t, s.do("GET", "/api/dashboard/sales", nil, ""), http.StatusUnauthorized, "Authorization token required")
}

func TestCORS(t *testing.T) {
	s := newServer(t)

	preflight := func(origin string) *httptest.ResponseRecorder {
		req := newRequest("OPTIONS", "/api/contacts")
		req.Header.Set("Origin", origin)
		req.Header.Set("Access-Control-Request-Method", "PATCH")
		return s.serve(req)
	}

	res := preflight("http://localhost:5173")
	if res.Code != http.StatusNoContent {
		t.Errorf("allowed preflight status = %d, want 204", res.Code)
	}
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("Allow-Origin = %q", got)
	}
	if got := res.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, Authorization" {
		t.Errorf("Allow-Headers = %q", got)
	}

	res = preflight("http://evil.example")
	if res.Code != http.StatusNoContent {
		t.Errorf("other-origin preflight status = %d, want 204", res.Code)
	}
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin for unknown origin = %q, want none", got)
	}
}
