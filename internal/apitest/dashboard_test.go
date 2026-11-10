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
	// The test user registered the first org (id 1), which got the default stages.
	s.exec(`INSERT INTO deals (org_id, title, amount_paise, billing_type, billing_period, stage_id, status, closed_at)
		SELECT 1, v.title, v.paise, v.btype, v.period, ds.id, ds.kind, CASE WHEN ds.kind <> 'open' THEN now() END
		FROM (VALUES
			('open-1', 10050, 'one_time', NULL, 'Proposal'),
			('open-2', 20000, 'one_time', NULL, 'Qualification'),
			('won', 100000, 'one_time', NULL, 'Closed Won'),
			('won-subscription', 5000, 'recurring', 'monthly', 'Closed Won'), -- ₹50 a month = ₹600 a year
			('lost', 30000, 'one_time', NULL, 'Closed Lost'),
			('lost-subscription', 9900, 'recurring', 'yearly', 'Closed Lost')
		) AS v(title, paise, btype, period, stage)
		JOIN deal_stages ds ON ds.name = v.stage AND ds.org_id = 1`)

	got := wantJSON(t, s.do("GET", "/api/dashboard/sales", nil, token), http.StatusOK)
	// Deal values are one-time amounts or first-year values; ARR counts only
	// won recurring deals, and MRR is ARR / 12.
	want := map[string]float64{
		"total_open_deals": 2, "total_open_deal_value": 300.5, "total_won": 1600, "total_loss": 399,
		"total_new_customers": 1, "total_arr": 600, "total_mrr": 50,
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
	if got := res.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Allow-Credentials = %q, want true (refresh cookie)", got)
	}

	res = preflight("http://evil.example")
	if res.Code != http.StatusNoContent {
		t.Errorf("other-origin preflight status = %d, want 204", res.Code)
	}
	if got := res.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Allow-Origin for unknown origin = %q, want none", got)
	}
}

// "New customers" counts contacts created since midnight IST, independent of
// the session (UTC) and server time zones.
func TestSalesDashboardTodayIsIST(t *testing.T) {
	s := newServer(t)
	_, token := s.userToken()

	s.exec(`INSERT INTO contacts (org_id, first_name, created_at) VALUES
		(1, 'just after IST midnight',  date_trunc('day', now() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata' + interval '1 minute'),
		(1, 'just before IST midnight', date_trunc('day', now() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata' - interval '1 minute')`)

	got := wantJSON(t, s.do("GET", "/api/dashboard/sales", nil, token), http.StatusOK)
	if got["total_new_customers"] != float64(1) {
		t.Errorf("total_new_customers = %v, want 1", got["total_new_customers"])
	}
}
