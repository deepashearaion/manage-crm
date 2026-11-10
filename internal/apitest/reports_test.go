package apitest

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// reportData builds, through the API, a small org with leads, deals in each
// outcome and an issued invoice. It returns the owner's and the rep's tokens
// and a period query covering today.
func reportData(t *testing.T, s *server) (owner, rep, period string, invoiceTotal float64) {
	t.Helper()
	ownerID, owner := s.userToken()
	_, rep = s.member(s.orgOf(ownerID), "rep", "rep@example.com")
	s.billingProfile(owner)
	stage := s.stageIDs(owner)
	lead := func(token string, body map[string]any) any {
		rec := s.do("POST", "/api/v1/leads", body, token)
		return wantJSON(t, rec, http.StatusCreated)["lead"].(map[string]any)["contact_id"]
	}
	c1 := lead(owner, map[string]any{"first_name": "Won", "email": "won@example.com", "source": "Website"})
	c2 := lead(owner, map[string]any{"first_name": "Lost", "email": "lost@example.com", "source": "Website"})
	lead(owner, map[string]any{"first_name": "Hot", "email": "hot@example.com", "mobile": "9800000003", "source": "Referral", "message": hotMessage})
	c4 := lead(rep, map[string]any{"first_name": "Open", "email": "open@example.com"})

	// c1 wins twice (one new customer), c2 loses, the rep's deal stays open.
	a := s.createDeal(owner, map[string]any{"title": "A", "contact_id": c1, "amount_paise": 100000})
	wantStatus(t, s.moveDeal(owner, a, stage["Closed Won"], ""), http.StatusOK)
	b := s.createDeal(owner, map[string]any{"title": "B", "contact_id": c1, "amount_paise": 5000,
		"billing_type": "recurring", "billing_period": "monthly"}) // ₹600 a year
	wantStatus(t, s.moveDeal(owner, b, stage["Closed Won"], ""), http.StatusOK)
	c := s.createDeal(owner, map[string]any{"title": "C", "contact_id": c2, "amount_paise": 30000})
	wantStatus(t, s.moveDeal(owner, c, stage["Closed Lost"], "Price"), http.StatusOK)
	s.createDeal(rep, map[string]any{"title": "D", "contact_id": c4, "amount_paise": 20000})

	// The won deal already has its automatic draft.
	wantProblem(t, s.do("POST", "/api/v1/invoices", map[string]any{"deal_id": a["id"]}, owner), http.StatusConflict, "deal_invoiced")
	list := wantJSON(t, s.do("GET", fmt.Sprintf("/api/v1/invoices?deal_id=%v", a["id"]), nil, owner), http.StatusOK)["invoices"].([]any)
	draft := list[0].(map[string]any)
	wantStatus(t, s.do("PATCH", fmt.Sprintf("/api/v1/invoices/%v", draft["id"]), map[string]any{"place_of_supply": "27"}, owner), http.StatusOK)
	issued := wantJSON(t, s.invoiceAction(owner, draft["id"], "issue", nil), http.StatusOK)["invoice"].(map[string]any)

	today := time.Now().In(istZone)
	period = fmt.Sprintf("?from=%s&to=%s", today.AddDate(0, 0, -1).Format("2006-01-02"), today.AddDate(0, 0, 1).Format("2006-01-02"))
	return owner, rep, period, issued["total_paise"].(float64)
}

func TestReportsDashboard(t *testing.T) {
	s := newServer(t)
	owner, rep, period, invoiced := reportData(t, s)

	d := wantJSON(t, s.do("GET", "/api/v1/reports/dashboard"+period, nil, owner), http.StatusOK)["dashboard"].(map[string]any)
	want := map[string]any{
		"open_deals":       map[string]any{"count": 1.0, "value_paise": 20000.0},
		"won":              map[string]any{"count": 2.0, "value_paise": 160000.0},
		"lost":             map[string]any{"count": 1.0, "value_paise": 30000.0},
		"win_rate_percent": 66.7, "avg_deal_size_paise": 80000.0, "new_customers": 1.0, "new_leads": 4.0,
		"hot_leads": 1.0, "invoiced_paise": invoiced, "arr_paise": 60000.0, "mrr_paise": 5000.0,
	}
	for k, v := range want {
		if fmt.Sprint(d[k]) != fmt.Sprint(v) {
			t.Errorf("owner %s = %v, want %v", k, d[k], v)
		}
	}

	// A rep's dashboard covers only their records.
	d = wantJSON(t, s.do("GET", "/api/v1/reports/dashboard"+period, nil, rep), http.StatusOK)["dashboard"].(map[string]any)
	if fmt.Sprint(d["open_deals"]) != "map[count:1 value_paise:20000]" || d["new_leads"] != 1.0 || d["new_customers"] != 0.0 ||
		fmt.Sprint(d["won"]) != "map[count:0 value_paise:0]" || d["invoiced_paise"] != 0.0 {
		t.Errorf("rep dashboard = %v", d)
	}

	// The default period is this month; bad periods are rejected.
	d = wantJSON(t, s.do("GET", "/api/v1/reports/dashboard", nil, owner), http.StatusOK)["dashboard"].(map[string]any)
	if p := d["period"].(map[string]any); !strings.HasSuffix(p["from"].(string), "-01") {
		t.Errorf("default period = %v", p)
	}
	for _, q := range []string{"?from=2026-11-10&to=2026-11-01", "?from=10/11/2026", "?from=2020-01-01&to=2026-01-01"} {
		wantProblem(t, s.do("GET", "/api/v1/reports/dashboard"+q, nil, owner), http.StatusUnprocessableEntity, "validation_failed")
	}
}

func TestReportsFunnelPipelineSources(t *testing.T) {
	s := newServer(t)
	owner, rep, period, _ := reportData(t, s)

	f := wantJSON(t, s.do("GET", "/api/v1/reports/funnel"+period, nil, owner), http.StatusOK)["funnel"].(map[string]any)
	var got []string
	for _, st := range f["stages"].([]any) {
		m := st.(map[string]any)
		got = append(got, fmt.Sprintf("%s:%v:%v", m["stage"], m["reached"], m["from_previous_percent"]))
	}
	// Won and Open passed through opportunity; Lost reached opportunity
	// before losing; Hot is still a lead.
	if strings.Join(got, " ") != "lead:4:100 mql:3:75 sql:3:100 opportunity:3:100 customer:1:33.3" || f["lost"] != 1.0 {
		t.Errorf("funnel = %v, lost %v", got, f["lost"])
	}

	stages := wantJSON(t, s.do("GET", "/api/v1/reports/pipeline", nil, owner), http.StatusOK)["stages"].([]any)
	q := stages[0].(map[string]any)
	if len(stages) != 4 || q["name"] != "Qualification" || q["deals"] != 1.0 || q["value_paise"] != 20000.0 || q["weighted_paise"] != 2000.0 {
		t.Errorf("pipeline = %v", stages)
	}

	sources := wantJSON(t, s.do("GET", "/api/v1/reports/sources"+period, nil, owner), http.StatusOK)["sources"].([]any)
	got = nil
	for _, x := range sources {
		m := x.(map[string]any)
		got = append(got, fmt.Sprintf("%s:%v/%v/%v/%v/%v", m["source"], m["leads"], m["hot_leads"], m["customers"], m["conversion_percent"], m["won_paise"]))
	}
	if strings.Join(got, " ") != "Website:2/0/1/50/160000 (none):1/0/0/0/0 Referral:1/1/0/0/0" {
		t.Errorf("sources = %v", got)
	}
	if list := wantJSON(t, s.do("GET", "/api/v1/reports/sources"+period, nil, rep), http.StatusOK)["sources"].([]any); len(list) != 1 {
		t.Errorf("rep sources = %v", list)
	}
}

func TestReportsRepsAndRevenue(t *testing.T) {
	s := newServer(t)
	owner, rep, period, invoiced := reportData(t, s)

	reps := wantJSON(t, s.do("GET", "/api/v1/reports/reps"+period, nil, owner), http.StatusOK)["reps"].([]any)
	byName := map[string]map[string]any{}
	for _, x := range reps {
		m := x.(map[string]any)
		byName[m["role"].(string)] = m
	}
	// Leads the owner entered were routed (no rules: unowned), so none are theirs.
	if o := byName["owner"]; o["leads_assigned"] != 0.0 || fmt.Sprint(o["won"]) != "map[count:2 value_paise:160000]" || o["win_rate_percent"] != 66.7 {
		t.Errorf("owner row = %v", o)
	}
	if r := byName["rep"]; r["leads_assigned"] != 1.0 || fmt.Sprint(r["open_deals"]) != "map[count:1 value_paise:20000]" {
		t.Errorf("rep row = %v", r)
	}
	wantProblem(t, s.do("GET", "/api/v1/reports/reps", nil, rep), http.StatusForbidden, "forbidden")

	months := wantJSON(t, s.do("GET", "/api/v1/reports/revenue?months=2", nil, owner), http.StatusOK)["months"].([]any)
	cur := months[1].(map[string]any)
	if len(months) != 2 || cur["month"] != time.Now().In(istZone).Format("2006-01") || cur["won_deals"] != 2.0 ||
		cur["won_paise"] != 160000.0 || cur["new_arr_paise"] != 60000.0 || cur["new_customers"] != 1.0 || cur["invoiced_paise"] != invoiced {
		t.Errorf("revenue = %v", months)
	}
	if prev := months[0].(map[string]any); prev["won_deals"] != 0.0 {
		t.Errorf("previous month = %v", prev)
	}
	wantProblem(t, s.do("GET", "/api/v1/reports/revenue?months=99", nil, owner), http.StatusUnprocessableEntity, "validation_failed")
}

func TestWeeklyReportEmail(t *testing.T) {
	s := newServer(t)
	owner, rep, _, _ := reportData(t, s)
	_, _ = s.member(s.orgOf(s.idOf("asha@example.com")), "manager", "manager@example.com")
	_ = rep

	// A quiet org with the weekly email off gets nothing.
	s.register("Quiet", "quiet@example.com", "secret-pass-1")
	quiet := s.login("quiet@example.com", "secret-pass-1")
	wantProblem(t, s.do("PUT", "/api/v1/reports/settings", map[string]any{"weekly_email": false}, rep), http.StatusForbidden, "forbidden")
	rec := s.do("PUT", "/api/v1/reports/settings", map[string]any{"weekly_email": false}, quiet)
	if st := wantJSON(t, rec, http.StatusOK)["settings"].(map[string]any); st["weekly_email"] != false {
		t.Errorf("settings = %v", st)
	}
	if st := wantJSON(t, s.do("GET", "/api/v1/reports/settings", nil, owner), http.StatusOK)["settings"].(map[string]any); st["weekly_email"] != true {
		t.Errorf("default settings = %v", st)
	}

	// Next Monday 08:00 IST is too early; 10:00 sends this week's report.
	now := time.Now().In(istZone)
	monday := time.Date(now.Year(), now.Month(), now.Day()-(int(now.Weekday())+6)%7+7, 0, 0, 0, 0, istZone)
	weekStart := monday.AddDate(0, 0, -7).Format("2006-01-02")
	s.clock.Set(monday.Add(8 * time.Hour))
	s.runJob("weekly reports")
	before := len(s.mail.messages())
	s.clock.Set(monday.Add(10 * time.Hour))
	s.runJob("weekly reports")
	s.runJob("weekly reports") // once per week

	var to []string
	for _, m := range s.mail.messages()[before:] {
		to = append(to, m.To)
		if !strings.Contains(m.Subject, "weekly report, "+weekStart) || !strings.Contains(m.Text, "Deals won: 2, worth ₹1,600.00") ||
			!strings.Contains(m.Text, "New customers: 1") || !strings.Contains(m.Text, "http://frontend.test/reports") {
			t.Errorf("report email = %s\n%s", m.Subject, m.Text)
		}
	}
	if strings.Join(to, ",") != "asha@example.com,manager@example.com" {
		t.Errorf("sent to %v", to)
	}
}
