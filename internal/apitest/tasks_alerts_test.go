package apitest

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// hotMessage scores 75 with the default rules when sent with an email and
// mobile: email 10 + mobile 15 + price 15 + quote 15 + budget 10 + urgent 10.
const hotMessage = "Urgent: need a price and a quote, budget is ready"

func (s *server) notifications(token, query string) ([]map[string]any, float64) {
	s.t.Helper()
	body := wantJSON(s.t, s.do("GET", "/api/v1/notifications"+query, nil, token), http.StatusOK)
	var out []map[string]any
	for _, n := range body["notifications"].([]any) {
		out = append(out, n.(map[string]any))
	}
	return out, body["unread_count"].(float64)
}

func (s *server) tasks(token, query string) []map[string]any {
	s.t.Helper()
	var out []map[string]any
	for _, x := range wantJSON(s.t, s.do("GET", "/api/v1/tasks"+query, nil, token), http.StatusOK)["tasks"].([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}

func TestHotLeadAlertsOwner(t *testing.T) {
	s := newServer(t)
	ownerID, owner := s.userToken()
	repID, rep := s.member(s.orgOf(ownerID), "rep", "rep@example.com")
	wantStatus(t, s.do("POST", "/api/v1/assignment-rules", map[string]any{
		"name": "All", "criterion": "any", "user_ids": []int64{repID},
	}, owner), http.StatusCreated)
	key := s.formSource(owner, "Website")

	before := time.Now()
	wantStatus(t, s.do("POST", "/api/v1/intake/forms/"+key, map[string]any{
		"name": "Priya Sharma", "email": "priya@example.com", "mobile": "9876543210",
		"destination": "Goa", "message": hotMessage,
	}, ""), http.StatusAccepted)

	// A follow-up task with the summary, due in 15 minutes (the default).
	list := s.tasks(rep, "")
	if len(list) != 1 {
		t.Fatalf("rep tasks = %v", list)
	}
	task := list[0]
	if task["title"] != "Follow up hot lead: Priya Sharma" || task["type"] != "follow_up" || task["priority"] != "urgent" ||
		task["origin"] != "hot_lead" || task["assignee_id"] != float64(repID) || task["status"] != "open" {
		t.Errorf("task = %v", task)
	}
	desc := task["description"].(string)
	for _, want := range []string{"Priya Sharma: hot lead, score 75/100.", "Source: Website", "Destination: Goa",
		"Asked for a quote (+15)", hotMessage, "Next step: Call within 15 minutes"} {
		if !strings.Contains(desc, want) {
			t.Errorf("summary lacks %q:\n%s", want, desc)
		}
	}
	due, err := time.Parse(time.RFC3339, task["due_at"].(string))
	if err != nil || due.Before(before.Add(14*time.Minute)) || due.After(time.Now().Add(16*time.Minute)) {
		t.Errorf("due_at = %v", task["due_at"])
	}

	// An in-app notification and an email, only for the rep.
	notes, unread := s.notifications(rep, "")
	if len(notes) != 1 || unread != 1 {
		t.Fatalf("rep notifications = %v (unread %v)", notes, unread)
	}
	n := notes[0]
	if n["kind"] != "hot_lead" || n["title"] != "Hot lead: Priya Sharma (score 75)" ||
		n["task_id"] != task["id"] || n["link"] != fmt.Sprintf("/contacts/%v", task["contact_id"]) {
		t.Errorf("notification = %v", n)
	}
	var mail []string
	for _, m := range s.mail.messages() {
		if m.To == "rep@example.com" {
			mail = append(mail, m.Subject+"\n"+m.Text)
		}
	}
	if len(mail) != 1 || !strings.Contains(mail[0], "Hot lead: Priya Sharma") ||
		!strings.Contains(mail[0], fmt.Sprintf("http://frontend.test/contacts/%v", task["contact_id"])) {
		t.Errorf("rep emails = %q", mail)
	}
	if notes, _ := s.notifications(owner, ""); len(notes) != 0 {
		t.Errorf("owner notified about a lead routed to the rep: %v", notes)
	}

	// The task is on the contact's timeline.
	if got := kinds(s.timeline(owner, task["contact_id"])); !strings.Contains(got, "task_created") {
		t.Errorf("timeline = %s", got)
	}

	// Cooling down and turning hot again alerts again but does not add a
	// second open follow-up.
	id := task["contact_id"]
	wantStatus(t, s.do("PATCH", "/api/v1/lead-settings", map[string]any{"hot_threshold": 90}, owner), http.StatusOK)
	wantStatus(t, s.do("POST", fmt.Sprintf("/api/v1/leads/%v/rescore", id), nil, owner), http.StatusOK)
	wantStatus(t, s.do("PATCH", "/api/v1/lead-settings", map[string]any{"hot_threshold": 70}, owner), http.StatusOK)
	wantStatus(t, s.do("POST", fmt.Sprintf("/api/v1/leads/%v/rescore", id), nil, owner), http.StatusOK)
	if list := s.tasks(rep, ""); len(list) != 1 {
		t.Errorf("tasks after re-heating = %d, want 1", len(list))
	}
	if notes, _ := s.notifications(rep, ""); len(notes) != 2 {
		t.Errorf("notifications after re-heating = %d, want 2", len(notes))
	}
	// Rescoring a lead that stays hot alerts no one.
	wantStatus(t, s.do("POST", fmt.Sprintf("/api/v1/leads/%v/rescore", id), nil, owner), http.StatusOK)
	if notes, _ := s.notifications(rep, ""); len(notes) != 2 {
		t.Errorf("still-hot rescore notified: %d", len(notes))
	}
}

func TestUnassignedHotLeadAlertsManagers(t *testing.T) {
	s := newServer(t)
	ownerID, owner := s.userToken()
	org := s.orgOf(ownerID)
	_, manager := s.member(org, "manager", "manager@example.com")
	_, rep := s.member(org, "rep", "rep@example.com")
	key := s.formSource(owner, "Website")

	wantStatus(t, s.do("POST", "/api/v1/intake/forms/"+key, map[string]any{
		"first_name": "Priya", "email": "priya@example.com", "mobile": "9876543210", "message": hotMessage,
	}, ""), http.StatusAccepted)

	for name, token := range map[string]string{"owner": owner, "manager": manager} {
		notes, _ := s.notifications(token, "")
		if len(notes) != 1 || !strings.HasPrefix(notes[0]["title"].(string), "Unassigned hot lead: Priya") {
			t.Errorf("%s notifications = %v", name, notes)
		}
	}
	if notes, _ := s.notifications(rep, ""); len(notes) != 0 {
		t.Errorf("rep notified: %v", notes)
	}
	if list := s.tasks(owner, ""); len(list) != 0 {
		t.Errorf("task created without an owner: %v", list)
	}
}

func TestRoutedLeadNotifiesOwner(t *testing.T) {
	s := newServer(t)
	ownerID, owner := s.userToken()
	repID, rep := s.member(s.orgOf(ownerID), "rep", "rep@example.com")
	wantStatus(t, s.do("PATCH", "/api/v1/lead-settings", map[string]any{"default_owner_id": repID}, owner), http.StatusOK)
	key := s.formSource(owner, "Website")
	mails := len(s.mail.messages())

	wantStatus(t, s.do("POST", "/api/v1/intake/forms/"+key, map[string]any{"first_name": "Arjun", "email": "arjun@example.com"}, ""), http.StatusAccepted)
	notes, _ := s.notifications(rep, "")
	if len(notes) != 1 || notes[0]["kind"] != "lead_assigned" || notes[0]["title"] != "New lead assigned: Arjun" || notes[0]["body"] != "Default owner" {
		t.Errorf("notifications = %v", notes)
	}
	if len(s.mail.messages()) != mails {
		t.Error("a routine assignment sent an email")
	}
	if list := s.tasks(rep, ""); len(list) != 0 {
		t.Errorf("cold lead got a task: %v", list)
	}
}

func TestTasksAPI(t *testing.T) {
	s := newServer(t)
	ownerID, owner := s.userToken()
	org := s.orgOf(ownerID)
	repID, rep := s.member(org, "rep", "rep@example.com")
	_, rep2 := s.member(org, "rep", "rep2@example.com")
	_, viewer := s.member(org, "viewer", "viewer@example.com")

	rec := s.do("POST", "/api/v1/leads", map[string]any{"first_name": "Ravi", "email": "ravi@example.com"}, rep)
	contactID := wantJSON(t, rec, http.StatusCreated)["lead"].(map[string]any)["contact_id"]

	// A rep creates their own task about their contact.
	due := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	rec = s.do("POST", "/api/v1/tasks", map[string]any{"title": " Call Ravi ", "type": "call", "contact_id": contactID, "due_at": due}, rep)
	task := wantJSON(t, rec, http.StatusCreated)["task"].(map[string]any)
	if task["title"] != "Call Ravi" || task["assignee_id"] != float64(repID) || task["priority"] != "normal" || task["origin"] != "manual" {
		t.Errorf("task = %v", task)
	}
	path := fmt.Sprintf("/api/v1/tasks/%v", task["id"])

	// Validation and permissions.
	wantProblem(t, s.do("POST", "/api/v1/tasks", map[string]any{"title": ""}, rep), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, s.do("POST", "/api/v1/tasks", map[string]any{"title": "x", "type": "party"}, rep), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, s.do("POST", "/api/v1/tasks", map[string]any{"title": "x", "assignee_id": ownerID}, rep), http.StatusForbidden, "forbidden")
	wantProblem(t, s.do("POST", "/api/v1/tasks", map[string]any{"title": "x"}, viewer), http.StatusForbidden, "forbidden")
	wantProblem(t, s.do("GET", path, nil, rep2), http.StatusNotFound, "not_found")
	wantProblem(t, s.do("PATCH", path, map[string]any{"status": "done"}, rep2), http.StatusNotFound, "not_found")
	wantProblem(t, s.do("PATCH", path, map[string]any{"status": "finished"}, rep), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, s.do("PATCH", path, map[string]any{"assignee_id": ownerID}, rep), http.StatusForbidden, "forbidden")
	// rep2 cannot attach a task to rep's contact.
	wantProblem(t, s.do("POST", "/api/v1/tasks", map[string]any{"title": "x", "contact_id": contactID}, rep2), http.StatusUnprocessableEntity, "validation_failed")

	// Lists: reps see only theirs; overdue filter.
	if got := s.tasks(rep2, ""); len(got) != 0 {
		t.Errorf("rep2 sees %v", got)
	}
	if got := s.tasks(owner, "?overdue=true"); len(got) != 1 {
		t.Errorf("overdue tasks = %v", got)
	}
	if got := s.tasks(owner, fmt.Sprintf("?contact_id=%v", contactID)); len(got) != 1 {
		t.Errorf("tasks for contact = %v", got)
	}

	// Completing records completed_at and the timeline.
	rec = s.do("PATCH", path, map[string]any{"status": "done"}, rep)
	done := wantJSON(t, rec, http.StatusOK)["task"].(map[string]any)
	if done["status"] != "done" || done["completed_at"] == nil {
		t.Errorf("done task = %v", done)
	}
	if got := kinds(s.timeline(rep, contactID)); !strings.HasPrefix(got, "task_completed,") || !strings.Contains(got, "task_created") {
		t.Errorf("timeline = %s", got)
	}
	rec = s.do("PATCH", path, map[string]any{"status": "open"}, rep)
	if reopened := wantJSON(t, rec, http.StatusOK)["task"].(map[string]any); reopened["completed_at"] != nil {
		t.Errorf("reopened task = %v", reopened)
	}

	// A manager reassigning notifies the new assignee.
	_, rep2Notes := s.notifications(rep2, "")
	wantStatus(t, s.do("PATCH", path, map[string]any{"assignee_id": s.idOf("rep2@example.com")}, owner), http.StatusOK)
	notes, unread := s.notifications(rep2, "?unread=true")
	if unread != rep2Notes+1 || notes[0]["kind"] != "task_assigned" || notes[0]["title"] != "New task: Call Ravi" {
		t.Errorf("rep2 notifications = %v", notes)
	}
	wantProblem(t, s.do("PATCH", path, map[string]any{"assignee_id": 999999}, owner), http.StatusUnprocessableEntity, "validation_failed")

	// Another org cannot see it.
	s.register("Out", "out@example.com", "secret-pass-1")
	outsider := s.login("out@example.com", "secret-pass-1")
	wantProblem(t, s.do("GET", path, nil, outsider), http.StatusNotFound, "not_found")

	wantStatus(t, s.do("DELETE", path, nil, owner), http.StatusNoContent)
	wantProblem(t, s.do("GET", path, nil, owner), http.StatusNotFound, "not_found")
}

func (s *server) idOf(email string) int64 {
	s.t.Helper()
	var id int64
	if err := s.db.QueryRow(s.t.Context(), `SELECT id FROM users WHERE email = $1`, email).Scan(&id); err != nil {
		s.t.Fatal(err)
	}
	return id
}

func TestNotificationsReadState(t *testing.T) {
	s := newServer(t)
	ownerID, owner := s.userToken()
	repID, rep := s.member(s.orgOf(ownerID), "rep", "rep@example.com")
	for i := range 2 {
		wantStatus(t, s.do("POST", "/api/v1/tasks", map[string]any{"title": fmt.Sprint("Task ", i), "assignee_id": repID}, owner), http.StatusCreated)
	}
	notes, unread := s.notifications(rep, "")
	if len(notes) != 2 || unread != 2 {
		t.Fatalf("notifications = %v, unread %v", notes, unread)
	}

	wantStatus(t, s.do("POST", fmt.Sprintf("/api/v1/notifications/%v/read", notes[0]["id"]), nil, rep), http.StatusNoContent)
	if list, unread := s.notifications(rep, "?unread=true"); len(list) != 1 || unread != 1 {
		t.Errorf("after reading one: %v, unread %v", list, unread)
	}
	// Someone else's notification is not found.
	wantProblem(t, s.do("POST", fmt.Sprintf("/api/v1/notifications/%v/read", notes[1]["id"]), nil, owner), http.StatusNotFound, "not_found")

	wantStatus(t, s.do("POST", "/api/v1/notifications/read-all", nil, rep), http.StatusNoContent)
	if _, unread := s.notifications(rep, ""); unread != 0 {
		t.Errorf("unread after read-all = %v", unread)
	}
	wantProblem(t, s.do("GET", "/api/v1/notifications", nil, ""), http.StatusUnauthorized, "unauthorized")
}
