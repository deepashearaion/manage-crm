package apitest

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

var istZone = time.FixedZone("IST", 5*3600+1800)

// stageIDs returns the org's stage ids by name.
func (s *server) stageIDs(token string) map[string]any {
	s.t.Helper()
	ids := map[string]any{}
	for _, st := range wantJSON(s.t, s.do("GET", "/api/v1/deal-stages", nil, token), http.StatusOK)["stages"].([]any) {
		m := st.(map[string]any)
		ids[m["name"].(string)] = m["id"]
	}
	return ids
}

func (s *server) createDeal(token string, body map[string]any) map[string]any {
	s.t.Helper()
	return wantJSON(s.t, s.do("POST", "/api/v1/deals", body, token), http.StatusCreated)["deal"].(map[string]any)
}

func (s *server) moveDeal(token string, deal map[string]any, stage any, reason string) *httpResult {
	s.t.Helper()
	body := map[string]any{"stage_id": stage}
	if reason != "" {
		body["lost_reason"] = reason
	}
	return s.do("POST", fmt.Sprintf("/api/v1/deals/%v/stage", deal["id"]), body, token)
}

func (s *server) contactStage(token string, id any) string {
	s.t.Helper()
	rec := s.do("GET", fmt.Sprintf("/api/v1/contacts/%v", id), nil, token)
	return wantJSON(s.t, rec, http.StatusOK)["contact"].(map[string]any)["lifecycle_stage"].(string)
}

func TestDealStagesDefaults(t *testing.T) {
	s := newServer(t)
	_, owner := s.userToken()
	stages := wantJSON(t, s.do("GET", "/api/v1/deal-stages", nil, owner), http.StatusOK)["stages"].([]any)
	var got []string
	for _, x := range stages {
		st := x.(map[string]any)
		got = append(got, fmt.Sprintf("%s:%s:%v", st["name"], st["kind"], st["probability"]))
	}
	want := "Qualification:open:10 Needs Analysis:open:25 Proposal:open:50 Negotiation:open:75 Closed Won:won:100 Closed Lost:lost:0"
	if strings.Join(got, " ") != want {
		t.Errorf("stages = %s", strings.Join(got, " "))
	}
	if g := stages[2].(map[string]any); g["guidance"] == "" || fmt.Sprint(g["required_fields"]) != "[amount contact]" {
		t.Errorf("Proposal guidance = %v", g)
	}
}

func TestDealLifecycle(t *testing.T) {
	s := newServer(t)
	ownerID, owner := s.userToken()
	stage := s.stageIDs(owner)
	rec := s.do("POST", "/api/v1/leads", map[string]any{"first_name": "Priya", "email": "priya@example.com"}, owner)
	contact := wantJSON(t, rec, http.StatusCreated)["lead"].(map[string]any)["contact_id"]

	// Created in the first stage; the contact becomes an opportunity.
	deal := s.createDeal(owner, map[string]any{"title": " Goa trip ", "contact_id": contact})
	if deal["title"] != "Goa trip" || deal["stage_name"] != "Qualification" || deal["status"] != "open" ||
		deal["owner_id"] != float64(ownerID) || deal["billing_type"] != "one_time" || deal["value_paise"] != float64(0) {
		t.Errorf("deal = %v", deal)
	}
	// No close date: 10% × 0.8.
	if deal["win_probability"] != float64(8) || len(deal["probability_notes"].([]any)) != 2 {
		t.Errorf("probability = %v %v", deal["win_probability"], deal["probability_notes"])
	}
	if got := s.contactStage(owner, contact); got != "opportunity" {
		t.Errorf("contact stage = %s", got)
	}
	path := fmt.Sprintf("/api/v1/deals/%v", deal["id"])

	// Guided stages: Proposal needs an amount.
	p := wantProblem(t, s.moveDeal(owner, deal, stage["Proposal"], ""), http.StatusUnprocessableEntity, "stage_requirements")
	if fmt.Sprint(p["errors"]) != "map[amount:required]" {
		t.Errorf("problem = %v", p)
	}
	closeDate := time.Now().In(istZone).AddDate(0, 0, 10).Format("2006-01-02")
	rec = s.do("PATCH", path, map[string]any{"amount_paise": 4500000, "expected_close_date": closeDate}, owner)
	wantStatus(t, rec, http.StatusOK)
	rec = s.moveDeal(owner, deal, stage["Proposal"], "")
	if d := wantJSON(t, rec, http.StatusOK)["deal"].(map[string]any); d["stage_name"] != "Proposal" || d["win_probability"] != float64(50) {
		t.Errorf("in Proposal = %v", d)
	}

	// Lost needs a reason; then the contact is lost and a re-contact task is due in 30 days.
	wantProblem(t, s.moveDeal(owner, deal, stage["Closed Lost"], ""), http.StatusUnprocessableEntity, "validation_failed")
	rec = s.moveDeal(owner, deal, stage["Closed Lost"], "Chose another agency")
	lost := wantJSON(t, rec, http.StatusOK)["deal"].(map[string]any)
	if lost["status"] != "lost" || lost["closed_at"] == nil || lost["lost_reason"] != "Chose another agency" || lost["win_probability"] != float64(0) {
		t.Errorf("lost deal = %v", lost)
	}
	if got := s.contactStage(owner, contact); got != "lost" {
		t.Errorf("contact stage after loss = %s", got)
	}
	tasks := s.tasks(owner, "?contact_id="+fmt.Sprint(contact))
	if len(tasks) != 1 || tasks[0]["title"] != "Re-contact: Goa trip" || tasks[0]["origin"] != "deal_lost" ||
		!strings.Contains(tasks[0]["description"].(string), "Chose another agency") {
		t.Fatalf("tasks = %v", tasks)
	}
	closed, _ := time.Parse(time.RFC3339, lost["closed_at"].(string))
	due, _ := time.Parse(time.RFC3339, tasks[0]["due_at"].(string))
	if d := due.Sub(closed); d < 30*24*time.Hour-time.Second || d > 30*24*time.Hour+time.Second {
		t.Errorf("re-contact due %v after closing", d)
	}

	// Reopening clears the outcome; winning makes them a customer.
	rec = s.moveDeal(owner, deal, stage["Negotiation"], "")
	if d := wantJSON(t, rec, http.StatusOK)["deal"].(map[string]any); d["status"] != "open" || d["closed_at"] != nil || d["lost_reason"] != "" {
		t.Errorf("reopened = %v", d)
	}
	if got := s.contactStage(owner, contact); got != "opportunity" {
		t.Errorf("contact stage after reopening = %s", got)
	}
	rec = s.moveDeal(owner, deal, stage["Closed Won"], "")
	if d := wantJSON(t, rec, http.StatusOK)["deal"].(map[string]any); d["status"] != "won" || d["closed_at"] == nil || d["win_probability"] != float64(100) {
		t.Errorf("won = %v", d)
	}
	if got := s.contactStage(owner, contact); got != "customer" {
		t.Errorf("contact stage after winning = %s", got)
	}

	// Losing another deal does not demote a customer.
	other := s.createDeal(owner, map[string]any{"title": "Kerala trip", "contact_id": contact})
	wantStatus(t, s.moveDeal(owner, other, stage["Closed Lost"], "Budget"), http.StatusOK)
	if got := s.contactStage(owner, contact); got != "customer" {
		t.Errorf("customer demoted to %s", got)
	}

	// History and timeline.
	hist := wantJSON(t, s.do("GET", path+"/history", nil, owner), http.StatusOK)["history"].([]any)
	var moves []string
	for _, h := range hist {
		moves = append(moves, h.(map[string]any)["to_stage"].(string))
	}
	if strings.Join(moves, ",") != "Qualification,Proposal,Closed Lost,Negotiation,Closed Won" {
		t.Errorf("history = %v", moves)
	}
	if got := kinds(s.timeline(owner, contact)); !strings.Contains(got, "deal_won") || !strings.Contains(got, "deal_lost") || !strings.Contains(got, "deal_created") {
		t.Errorf("timeline = %s", got)
	}
}

func TestDealValidation(t *testing.T) {
	s := newServer(t)
	_, owner := s.userToken()
	stage := s.stageIDs(owner)
	post := func(body map[string]any) *httpResult { return s.do("POST", "/api/v1/deals", body, owner) }

	wantProblem(t, post(map[string]any{"title": ""}), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, post(map[string]any{"title": "x", "billing_type": "recurring"}), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, post(map[string]any{"title": "x", "amount_paise": -1}), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, post(map[string]any{"title": "x", "expected_close_date": "10/11/2026"}), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, post(map[string]any{"title": "x", "stage_id": stage["Closed Won"]}), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, post(map[string]any{"title": "x", "stage_id": stage["Proposal"]}), http.StatusUnprocessableEntity, "stage_requirements")
	wantProblem(t, post(map[string]any{"title": "x", "contact_id": 999999}), http.StatusUnprocessableEntity, "validation_failed")

	// Recurring deals are valued at their first year.
	d := s.createDeal(owner, map[string]any{"title": "Retainer", "amount_paise": 250000, "billing_type": "recurring", "billing_period": "quarterly"})
	if d["value_paise"] != float64(1000000) || d["billing_period"] != "quarterly" {
		t.Errorf("recurring deal = %v", d)
	}
	rec := s.do("PATCH", fmt.Sprintf("/api/v1/deals/%v", d["id"]), map[string]any{"billing_type": "one_time"}, owner)
	if d := wantJSON(t, rec, http.StatusOK)["deal"].(map[string]any); d["billing_period"] != nil || d["value_paise"] != float64(250000) {
		t.Errorf("switched to one-time = %v", d)
	}
}

func TestDealPermissions(t *testing.T) {
	s := newServer(t)
	ownerID, owner := s.userToken()
	org := s.orgOf(ownerID)
	repID, rep := s.member(org, "rep", "rep@example.com")
	_, rep2 := s.member(org, "rep", "rep2@example.com")
	_, viewer := s.member(org, "viewer", "viewer@example.com")

	d := s.createDeal(rep, map[string]any{"title": "Rep deal", "amount_paise": 100})
	if d["owner_id"] != float64(repID) {
		t.Errorf("rep deal owner = %v", d["owner_id"])
	}
	s.createDeal(owner, map[string]any{"title": "Owner deal"})
	path := fmt.Sprintf("/api/v1/deals/%v", d["id"])

	if list := wantJSON(t, s.do("GET", "/api/v1/deals", nil, rep), http.StatusOK)["deals"].([]any); len(list) != 1 {
		t.Errorf("rep sees %d deals", len(list))
	}
	if list := wantJSON(t, s.do("GET", "/api/v1/deals", nil, viewer), http.StatusOK)["deals"].([]any); len(list) != 2 {
		t.Errorf("viewer sees %d deals", len(list))
	}
	wantProblem(t, s.do("GET", path, nil, rep2), http.StatusNotFound, "not_found")
	wantProblem(t, s.do("POST", "/api/v1/deals", map[string]any{"title": "x", "owner_id": ownerID}, rep), http.StatusForbidden, "forbidden")
	wantProblem(t, s.do("POST", "/api/v1/deals", map[string]any{"title": "x"}, viewer), http.StatusForbidden, "forbidden")
	wantProblem(t, s.do("PATCH", path, map[string]any{"owner_id": ownerID}, rep), http.StatusForbidden, "forbidden")
	wantProblem(t, s.do("POST", "/api/v1/deal-stages", map[string]any{"name": "X"}, rep), http.StatusForbidden, "forbidden")

	// A rep's stage list counts only their deals.
	for _, x := range wantJSON(t, s.do("GET", "/api/v1/deal-stages", nil, rep), http.StatusOK)["stages"].([]any) {
		if st := x.(map[string]any); st["name"] == "Qualification" && (st["open_deals"] != float64(1) || st["open_value_paise"] != float64(100)) {
			t.Errorf("rep's Qualification = %v", st)
		}
	}

	s.register("Out", "out@example.com", "secret-pass-1")
	outsider := s.login("out@example.com", "secret-pass-1")
	wantProblem(t, s.do("GET", path, nil, outsider), http.StatusNotFound, "not_found")
	// Another org's stage cannot be used.
	other := s.stageIDs(outsider)
	wantProblem(t, s.do("POST", path+"/stage", map[string]any{"stage_id": other["Proposal"]}, owner), http.StatusUnprocessableEntity, "validation_failed")
}

func TestDealStageManagement(t *testing.T) {
	s := newServer(t)
	_, owner := s.userToken()
	stage := s.stageIDs(owner)

	rec := s.do("POST", "/api/v1/deal-stages", map[string]any{
		"name": "Site visit", "position": 3, "probability": 40, "required_fields": []string{"contact", "contact"},
	}, owner)
	visit := wantJSON(t, rec, http.StatusCreated)["stage"].(map[string]any)
	if visit["kind"] != "open" || fmt.Sprint(visit["required_fields"]) != "[contact]" {
		t.Errorf("new stage = %v", visit)
	}
	wantProblem(t, s.do("POST", "/api/v1/deal-stages", map[string]any{"name": "Site visit"}, owner), http.StatusConflict, "name_taken")
	wantProblem(t, s.do("POST", "/api/v1/deal-stages", map[string]any{"name": "Y", "required_fields": []string{"budget"}}, owner),
		http.StatusUnprocessableEntity, "validation_failed")

	// Outcome stages are certain; kinds are fixed.
	rec = s.do("PATCH", fmt.Sprintf("/api/v1/deal-stages/%v", stage["Closed Won"]), map[string]any{"probability": 60}, owner)
	if st := wantJSON(t, rec, http.StatusOK)["stage"].(map[string]any); st["probability"] != float64(100) {
		t.Errorf("won stage probability = %v", st["probability"])
	}
	wantProblem(t, s.do("PATCH", fmt.Sprintf("/api/v1/deal-stages/%v", visit["id"]), map[string]any{"kind": "won"}, owner),
		http.StatusUnprocessableEntity, "validation_failed")

	// Deletion: not while deals use it, never the last of its kind.
	s.createDeal(owner, map[string]any{"title": "In Qualification"})
	wantProblem(t, s.do("DELETE", fmt.Sprintf("/api/v1/deal-stages/%v", stage["Qualification"]), nil, owner), http.StatusConflict, "stage_in_use")
	wantProblem(t, s.do("DELETE", fmt.Sprintf("/api/v1/deal-stages/%v", stage["Closed Lost"]), nil, owner), http.StatusConflict, "last_stage_of_kind")
	wantStatus(t, s.do("DELETE", fmt.Sprintf("/api/v1/deal-stages/%v", visit["id"]), nil, owner), http.StatusNoContent)
}

func TestDealForecast(t *testing.T) {
	s := newServer(t)
	ownerID, owner := s.userToken()
	_, rep := s.member(s.orgOf(ownerID), "rep", "rep@example.com")
	stage := s.stageIDs(owner)
	rec := s.do("POST", "/api/v1/leads", map[string]any{"first_name": "C", "email": "c@example.com"}, owner)
	contact := wantJSON(t, rec, http.StatusCreated)["lead"].(map[string]any)["contact_id"]

	now := time.Now().In(istZone)
	thisMonth := now.Format("2006-01")
	firstNext := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, istZone)
	nextMonth := firstNext.Format("2006-01")
	lastDay := firstNext.AddDate(0, 0, -1).Format("2006-01-02") // end of this month: never overdue

	// This month: ₹10,000 in Negotiation (75%: commit) and a won ₹5,000.
	neg := s.createDeal(owner, map[string]any{"title": "A", "contact_id": contact, "amount_paise": 1000000, "expected_close_date": lastDay})
	wantStatus(t, s.moveDeal(owner, neg, stage["Negotiation"], ""), http.StatusOK)
	won := s.createDeal(owner, map[string]any{"title": "B", "contact_id": contact, "amount_paise": 500000})
	wantStatus(t, s.moveDeal(owner, won, stage["Closed Won"], ""), http.StatusOK)
	// Next month: the rep's ₹2,000/month subscription (₹24,000 a year) in Qualification.
	s.createDeal(rep, map[string]any{"title": "C", "amount_paise": 200000, "billing_type": "recurring",
		"billing_period": "monthly", "expected_close_date": firstNext.AddDate(0, 0, 4).Format("2006-01-02")})
	// Undated.
	s.createDeal(owner, map[string]any{"title": "D", "amount_paise": 300000})

	f := wantJSON(t, s.do("GET", "/api/v1/deals/forecast?months=2", nil, owner), http.StatusOK)["forecast"].(map[string]any)
	months := f["months"].([]any)
	m0, m1 := months[0].(map[string]any), months[1].(map[string]any)
	if m0["month"] != thisMonth || m0["won_paise"] != float64(500000) || m0["won_deals"] != float64(1) ||
		m0["best_case_paise"] != float64(1000000) || m0["weighted_paise"] != float64(750000) ||
		m0["forecast_paise"] != float64(750000) || m0["commit_paise"] != float64(1000000) {
		t.Errorf("this month = %v", m0)
	}
	if m1["month"] != nextMonth || m1["best_case_paise"] != float64(2400000) || m1["weighted_paise"] != float64(240000) || m1["commit_paise"] != float64(0) {
		t.Errorf("next month = %v", m1)
	}
	if u := f["undated"].(map[string]any); u["deals"] != float64(1) || u["value_paise"] != float64(300000) {
		t.Errorf("undated = %v", u)
	}

	// A rep's forecast covers only their deals.
	f = wantJSON(t, s.do("GET", "/api/v1/deals/forecast?months=2", nil, rep), http.StatusOK)["forecast"].(map[string]any)
	if m0 := f["months"].([]any)[0].(map[string]any); m0["won_paise"] != float64(0) || m0["best_case_paise"] != float64(0) {
		t.Errorf("rep this month = %v", m0)
	}
	wantProblem(t, s.do("GET", "/api/v1/deals/forecast?from=2026-13", nil, owner), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, s.do("GET", "/api/v1/deals/forecast?months=24", nil, owner), http.StatusUnprocessableEntity, "validation_failed")
}
