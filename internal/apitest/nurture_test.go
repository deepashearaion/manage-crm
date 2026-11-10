package apitest

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// noonIST is a fixed time outside quiet hours.
var noonIST = time.Date(2026, 11, 10, 12, 0, 0, 0, istZone)

func newNurtureServer(t *testing.T) (*server, string) {
	s := newWhatsAppServer(t)
	s.clock.Set(noonIST)
	_, owner := s.userToken()
	wantStatus(t, s.do("POST", "/api/v1/whatsapp-accounts", map[string]any{"phone_number_id": "1111"}, owner), http.StatusCreated)
	return s, owner
}

func (s *server) createSequence(token string, body map[string]any) map[string]any {
	s.t.Helper()
	return wantJSON(s.t, s.do("POST", "/api/v1/nurture-sequences", body, token), http.StatusCreated)["sequence"].(map[string]any)
}

func (s *server) enrollments(token, query string) []map[string]any {
	s.t.Helper()
	var out []map[string]any
	for _, e := range wantJSON(s.t, s.do("GET", "/api/v1/nurture-enrollments"+query, nil, token), http.StatusOK)["enrollments"].([]any) {
		out = append(out, e.(map[string]any))
	}
	return out
}

func (s *server) mailTo(addr string) []string {
	var out []string
	for _, m := range s.mail.messages() {
		if m.To == addr {
			out = append(out, m.Subject+"\n"+m.Text)
		}
	}
	return out
}

var welcomeSequence = map[string]any{
	"name": "Welcome", "trigger": "new_lead", "active": true,
	"steps": []map[string]any{
		{"channel": "email", "delay_minutes": 0, "subject": "Hello {{first_name}}", "body": "Hi {{first_name}}, thanks for your interest."},
		{"channel": "whatsapp", "delay_minutes": 60, "body": "Hey {{first_name}}!", "whatsapp_template": "lead_followup",
			"whatsapp_template_params": []string{"{{first_name}}"}},
		{"channel": "email", "delay_minutes": 1440, "subject": "Still thinking?", "body": "Any questions, {{full_name}}?"},
	},
}

func TestNurtureNewLead(t *testing.T) {
	s, owner := newNurtureServer(t)
	s.createSequence(owner, welcomeSequence)
	key := s.formSource(owner, "Website")

	// A cold lead is enrolled; the first step is due at once.
	wantStatus(t, s.do("POST", "/api/v1/intake/forms/"+key, map[string]any{
		"name": "Priya Sharma", "email": "priya@example.com", "mobile": "98765 43210", "message": "hello",
	}, ""), http.StatusAccepted)
	contact := s.v1Contacts(owner, "")[0].(map[string]any)["id"]
	list := s.enrollments(owner, fmt.Sprintf("?contact_id=%v", contact))
	if len(list) != 1 || list[0]["status"] != "active" || list[0]["sequence_name"] != "Welcome" || list[0]["next_step"] != float64(0) {
		t.Fatalf("enrollments = %v", list)
	}

	// Step 1: email, rendered, with an unsubscribe link.
	s.runJob("nurture")
	mails := s.mailTo("priya@example.com")
	if len(mails) != 1 || !strings.HasPrefix(mails[0], "Hello Priya\nHi Priya, thanks for your interest.") ||
		!strings.Contains(mails[0], "http://frontend.test/unsubscribe?token=") {
		t.Fatalf("emails = %q", mails)
	}
	e := s.enrollments(owner, fmt.Sprintf("?contact_id=%v", contact))[0]
	due, _ := time.Parse(time.RFC3339, e["next_run_at"].(string))
	if e["next_step"] != float64(1) || !due.Equal(noonIST.Add(time.Hour)) {
		t.Errorf("after step 1 = %v", e)
	}

	// Not yet due: nothing sent.
	s.runJob("nurture")
	if n := len(s.wa.messages()); n != 0 {
		t.Errorf("sent early: %d WhatsApp messages", n)
	}

	// Step 2: WhatsApp, outside the 24-hour window, so the template.
	s.clock.Set(noonIST.Add(61 * time.Minute))
	s.runJob("nurture")
	wa := s.wa.messages()
	if len(wa) != 1 || wa[0].From != "1111" || wa[0].To != "919876543210" || wa[0].Template != "lead_followup" ||
		fmt.Sprint(wa[0].Params) != "[Priya]" {
		t.Fatalf("whatsapp = %+v", wa)
	}
	acts := s.timeline(owner, contact)
	if acts[0]["kind"] != "message" || acts[0]["direction"] != "outbound" || acts[0]["channel"] != "whatsapp" ||
		!strings.Contains(acts[0]["summary"].(string), "step 2 of 3") {
		t.Errorf("latest activity = %v", acts[0])
	}

	// A reply stops the sequence (and re-scores the lead).
	reply := waBody("wamid.r1", "919876543210", "Priya Sharma", "Not now, thanks")
	wantStatus(t, s.waPost(reply, waSign(reply)), http.StatusOK)
	e = s.enrollments(owner, fmt.Sprintf("?contact_id=%v", contact))[0]
	if e["status"] != "stopped" || e["stop_reason"] != "replied" || e["finished_at"] == nil {
		t.Errorf("after reply = %v", e)
	}
	s.clock.Set(noonIST.Add(48 * time.Hour))
	s.runJob("nurture")
	if n := len(s.mailTo("priya@example.com")); n != 1 {
		t.Errorf("sent after the reply: %d emails", n)
	}
	if got := kinds(s.timeline(owner, contact)); !strings.Contains(got, "nurture_stopped") || !strings.Contains(got, "nurture_enrolled") {
		t.Errorf("timeline = %s", got)
	}
}

func TestNurtureSkipsHotLeadsAndStopsWhenHot(t *testing.T) {
	s, owner := newNurtureServer(t)
	s.createSequence(owner, welcomeSequence)
	key := s.formSource(owner, "Website")

	wantStatus(t, s.do("POST", "/api/v1/intake/forms/"+key, map[string]any{
		"first_name": "Hot", "email": "hot@example.com", "mobile": "9800000001", "message": hotMessage,
	}, ""), http.StatusAccepted)
	if list := s.enrollments(owner, ""); len(list) != 0 {
		t.Errorf("hot lead enrolled: %v", list)
	}

	wantStatus(t, s.do("POST", "/api/v1/intake/forms/"+key, map[string]any{"first_name": "Cool", "email": "cool@example.com"}, ""), http.StatusAccepted)
	cool := s.v1Contacts(owner, "")[0].(map[string]any)["id"]
	// Lower the bar so Cool is hot on rescore: nurture stops, a rep takes over.
	wantStatus(t, s.do("PATCH", "/api/v1/lead-settings", map[string]any{"hot_threshold": 10, "warm_threshold": 5}, owner), http.StatusOK)
	wantStatus(t, s.do("POST", fmt.Sprintf("/api/v1/leads/%v/rescore", cool), nil, owner), http.StatusOK)
	if e := s.enrollments(owner, fmt.Sprintf("?contact_id=%v", cool)); len(e) != 1 || e[0]["stop_reason"] != "hot" {
		t.Errorf("enrollment after turning hot = %v", e)
	}
}

func TestNurtureQuietHoursAndWindow(t *testing.T) {
	s, owner := newNurtureServer(t)
	seq := s.createSequence(owner, map[string]any{
		"name": "Chat", "trigger": "manual", "active": true, "stop_on_reply": false,
		"steps": []map[string]any{{"channel": "whatsapp", "body": "Hi {{first_name}}, here are the details."}},
	})
	// The contact wrote on WhatsApp an hour ago: free text is allowed.
	msg := waBody("wamid.in", "919876543210", "Priya", "Send details")
	wantStatus(t, s.waPost(msg, waSign(msg)), http.StatusOK)
	contact := s.v1Contacts(owner, "")[0].(map[string]any)["id"]
	s.exec(`UPDATE contact_activities SET occurred_at = $1 WHERE external_id = 'wamid.in'`, noonIST.Add(-time.Hour))

	// 22:00 IST is quiet: rescheduled to 09:00, nothing sent.
	s.clock.Set(time.Date(2026, 11, 10, 22, 0, 0, 0, istZone))
	rec := s.do("POST", fmt.Sprintf("/api/v1/nurture-sequences/%v/enrollments", seq["id"]), map[string]any{"contact_id": contact}, owner)
	wantStatus(t, rec, http.StatusCreated)
	s.runJob("nurture")
	e := s.enrollments(owner, fmt.Sprintf("?contact_id=%v", contact))[0]
	due, _ := time.Parse(time.RFC3339, e["next_run_at"].(string))
	if len(s.wa.messages()) != 0 || !due.Equal(time.Date(2026, 11, 11, 9, 0, 0, 0, istZone)) || e["attempts"] != float64(0) {
		t.Errorf("quiet hours: sent %v, enrollment %v", s.wa.messages(), e)
	}

	// At 09:00 the message was 22 hours ago: within the window, free text.
	s.clock.Set(time.Date(2026, 11, 11, 9, 0, 0, 0, istZone))
	s.runJob("nurture")
	wa := s.wa.messages()
	if len(wa) != 1 || wa[0].Text != "Hi Priya, here are the details." || wa[0].Template != "" {
		t.Errorf("in-window message = %+v", wa)
	}
	if e := s.enrollments(owner, fmt.Sprintf("?contact_id=%v", contact))[0]; e["status"] != "completed" {
		t.Errorf("enrollment = %v", e)
	}

	// Outside the window and without a template, the step is skipped.
	wantStatus(t, s.do("POST", fmt.Sprintf("/api/v1/nurture-sequences/%v/enrollments", seq["id"]), map[string]any{"contact_id": contact}, owner), http.StatusCreated)
	s.clock.Set(time.Date(2026, 11, 13, 12, 0, 0, 0, istZone))
	s.runJob("nurture")
	e = s.enrollments(owner, fmt.Sprintf("?contact_id=%v&status=completed", contact))[0]
	if len(s.wa.messages()) != 1 || !strings.Contains(e["last_error"].(string), "24-hour window") {
		t.Errorf("skipped step: sent %d, enrollment %v", len(s.wa.messages()), e)
	}
}

func TestNurtureRetries(t *testing.T) {
	s, owner := newNurtureServer(t)
	seq := s.createSequence(owner, map[string]any{
		"name": "One", "trigger": "manual", "active": true,
		"steps": []map[string]any{{"channel": "whatsapp", "whatsapp_template": "hello"}},
	})
	rec := s.do("POST", "/api/v1/leads", map[string]any{"first_name": "Ravi", "mobile": "9876543210"}, owner)
	contact := wantJSON(t, rec, http.StatusCreated)["lead"].(map[string]any)["contact_id"]
	wantStatus(t, s.do("POST", fmt.Sprintf("/api/v1/nurture-sequences/%v/enrollments", seq["id"]), map[string]any{"contact_id": contact}, owner), http.StatusCreated)
	wantProblem(t, s.do("POST", fmt.Sprintf("/api/v1/nurture-sequences/%v/enrollments", seq["id"]), map[string]any{"contact_id": contact}, owner),
		http.StatusConflict, "already_enrolled")

	s.wa.fail = errors.New("network down")
	s.runJob("nurture")
	e := s.enrollments(owner, "")[0]
	due, _ := time.Parse(time.RFC3339, e["next_run_at"].(string))
	if e["attempts"] != float64(1) || e["last_error"] != "network down" || !due.Equal(noonIST.Add(5*time.Minute)) {
		t.Fatalf("after a failure = %v", e)
	}
	s.wa.fail = nil
	s.clock.Set(noonIST.Add(5 * time.Minute))
	s.runJob("nurture")
	if e := s.enrollments(owner, "")[0]; e["status"] != "completed" || len(s.wa.messages()) != 1 {
		t.Errorf("after recovery = %v", e)
	}
}

var unsubscribeLink = regexp.MustCompile(`unsubscribe\?token=(\S+)`)

func TestNurtureOptOut(t *testing.T) {
	s, owner := newNurtureServer(t)
	s.createSequence(owner, welcomeSequence)
	key := s.formSource(owner, "Website")
	wantStatus(t, s.do("POST", "/api/v1/intake/forms/"+key, map[string]any{"first_name": "Priya", "email": "priya@example.com"}, ""), http.StatusAccepted)
	contact := s.v1Contacts(owner, "")[0].(map[string]any)["id"]
	s.runJob("nurture")

	m := unsubscribeLink.FindStringSubmatch(s.mailTo("priya@example.com")[0])
	token, _ := url.QueryUnescape(m[1])
	wantProblem(t, s.do("POST", "/api/v1/unsubscribe", map[string]any{"token": token + "x"}, ""), http.StatusBadRequest, "invalid_token")
	wantStatus(t, s.do("POST", "/api/v1/unsubscribe", map[string]any{"token": token}, ""), http.StatusOK)
	wantStatus(t, s.do("POST", "/api/v1/unsubscribe", map[string]any{"token": token}, ""), http.StatusOK) // idempotent

	c := wantJSON(t, s.do("GET", fmt.Sprintf("/api/v1/contacts/%v", contact), nil, owner), http.StatusOK)["contact"].(map[string]any)
	if c["marketing_opt_out_at"] == nil {
		t.Errorf("contact not opted out: %v", c)
	}
	if e := s.enrollments(owner, ""); e[0]["status"] != "stopped" || e[0]["stop_reason"] != "opted_out" {
		t.Errorf("enrollment = %v", e)
	}
	seqs := wantJSON(t, s.do("GET", "/api/v1/nurture-sequences", nil, owner), http.StatusOK)["sequences"].([]any)
	wantProblem(t, s.do("POST", fmt.Sprintf("/api/v1/nurture-sequences/%v/enrollments", seqs[0].(map[string]any)["id"]),
		map[string]any{"contact_id": contact}, owner), http.StatusUnprocessableEntity, "validation_failed")

	// WhatsApp STOP opts out too.
	stop := waBody("wamid.stop", "14155550100", "John", " stop ")
	wantStatus(t, s.waPost(stop, waSign(stop)), http.StatusOK)
	john := s.v1Contacts(owner, "")[0].(map[string]any)
	if john["first_name"] != "John" || john["marketing_opt_out_at"] == nil {
		t.Errorf("STOP did not opt out: %v", john)
	}
}

func TestNurtureDeals(t *testing.T) {
	s, owner := newNurtureServer(t)
	s.createSequence(owner, map[string]any{
		"name": "Win back", "trigger": "deal_lost", "active": true,
		"steps": []map[string]any{{"channel": "email", "delay_minutes": 43200, "subject": "Plans?", "body": "Hi {{first_name}}"}},
	})
	stage := s.stageIDs(owner)
	rec := s.do("POST", "/api/v1/leads", map[string]any{"first_name": "Priya", "email": "priya@example.com"}, owner)
	contact := wantJSON(t, rec, http.StatusCreated)["lead"].(map[string]any)["contact_id"]

	// Lost: back to nurture, first message 30 days later; no re-contact task.
	deal := s.createDeal(owner, map[string]any{"title": "Goa", "contact_id": contact})
	wantStatus(t, s.moveDeal(owner, deal, stage["Closed Lost"], "Too expensive"), http.StatusOK)
	e := s.enrollments(owner, fmt.Sprintf("?contact_id=%v", contact))
	if len(e) != 1 || e[0]["sequence_name"] != "Win back" || e[0]["status"] != "active" {
		t.Fatalf("enrollments = %v", e)
	}
	due, _ := time.Parse(time.RFC3339, e[0]["next_run_at"].(string))
	if !due.Equal(noonIST.Add(30 * 24 * time.Hour)) {
		t.Errorf("first step due %v", due)
	}
	if tasks := s.tasks(owner, fmt.Sprintf("?contact_id=%v", contact)); len(tasks) != 0 {
		t.Errorf("re-contact task created as well: %v", tasks)
	}

	// Winning a deal ends nurture.
	other := s.createDeal(owner, map[string]any{"title": "Kerala", "contact_id": contact, "amount_paise": 100})
	wantStatus(t, s.moveDeal(owner, other, stage["Closed Won"], ""), http.StatusOK)
	if e := s.enrollments(owner, fmt.Sprintf("?contact_id=%v", contact)); e[0]["stop_reason"] != "won" {
		t.Errorf("after winning = %v", e)
	}
}

func TestNurtureSequenceAPI(t *testing.T) {
	s, owner := newNurtureServer(t)
	_, rep := s.member(s.orgOf(s.idOf("asha@example.com")), "rep", "rep@example.com")

	wantProblem(t, s.do("POST", "/api/v1/nurture-sequences", map[string]any{"name": "X", "steps": []any{}}, owner),
		http.StatusUnprocessableEntity, "validation_failed")
	p := wantProblem(t, s.do("POST", "/api/v1/nurture-sequences", map[string]any{
		"name": "X", "steps": []map[string]any{{"channel": "email", "subject": "s", "body": "b"}, {"channel": "sms", "body": "b"}},
	}, owner), http.StatusUnprocessableEntity, "validation_failed")
	if _, ok := p["errors"].(map[string]any)["steps[1].channel"]; !ok {
		t.Errorf("problem = %v", p)
	}
	wantProblem(t, s.do("POST", "/api/v1/nurture-sequences", welcomeSequence, rep), http.StatusForbidden, "forbidden")

	first := s.createSequence(owner, welcomeSequence)
	second := map[string]any{}
	for k, v := range welcomeSequence {
		second[k] = v
	}
	second["name"] = "Welcome 2"
	wantProblem(t, s.do("POST", "/api/v1/nurture-sequences", second, owner), http.StatusConflict, "trigger_taken")
	second["active"] = false
	s.createSequence(owner, second)
	wantProblem(t, s.do("POST", "/api/v1/nurture-sequences", second, owner), http.StatusConflict, "name_taken")

	rec := s.do("PATCH", fmt.Sprintf("/api/v1/nurture-sequences/%v", first["id"]), map[string]any{"active": false}, owner)
	if seq := wantJSON(t, rec, http.StatusOK)["sequence"].(map[string]any); seq["active"] != false || len(seq["steps"].([]any)) != 3 {
		t.Errorf("deactivated = %v", seq)
	}
	wantProblem(t, s.do("GET", "/api/v1/nurture-enrollments", nil, rep), http.StatusUnprocessableEntity, "validation_failed")
	wantStatus(t, s.do("DELETE", fmt.Sprintf("/api/v1/nurture-sequences/%v", first["id"]), nil, owner), http.StatusNoContent)
}

func TestOverdueTaskReminders(t *testing.T) {
	s := newServer(t)
	_, owner := s.userToken()
	s.clock.Set(noonIST)
	past := noonIST.Add(-time.Hour).UTC().Format(time.RFC3339)
	rec := s.do("POST", "/api/v1/tasks", map[string]any{"title": "Call back", "due_at": past}, owner)
	task := wantJSON(t, rec, http.StatusCreated)["task"].(map[string]any)
	s.do("POST", "/api/v1/tasks", map[string]any{"title": "Later", "due_at": noonIST.Add(time.Hour).UTC().Format(time.RFC3339)}, owner)

	s.runJob("overdue task reminders")
	s.runJob("overdue task reminders") // once per due date
	notes, _ := s.notifications(owner, "")
	if len(notes) != 1 || notes[0]["kind"] != "task_overdue" || notes[0]["title"] != "Overdue: Call back" || notes[0]["task_id"] != task["id"] {
		t.Fatalf("notifications = %v", notes)
	}

	// A new due date earns a new reminder once it passes.
	wantStatus(t, s.do("PATCH", fmt.Sprintf("/api/v1/tasks/%v", task["id"]),
		map[string]any{"due_at": noonIST.Add(30 * time.Minute).UTC().Format(time.RFC3339)}, owner), http.StatusOK)
	s.clock.Set(noonIST.Add(2 * time.Hour))
	s.runJob("overdue task reminders")
	if notes, _ := s.notifications(owner, ""); len(notes) != 3 { // Call back again, and Later
		t.Errorf("notifications after rescheduling = %d", len(notes))
	}
}
