package apitest

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// formSource creates a web-form lead source and returns its public key.
func (s *server) formSource(token, name string) string {
	s.t.Helper()
	rec := s.do("POST", "/api/v1/lead-sources", map[string]string{"name": name}, token)
	return wantJSON(s.t, rec, http.StatusCreated)["source"].(map[string]any)["public_key"].(string)
}

func (s *server) v1Contacts(token, query string) []any {
	s.t.Helper()
	return wantJSON(s.t, s.do("GET", "/api/v1/contacts"+query, nil, token), http.StatusOK)["contacts"].([]any)
}

func (s *server) timeline(token string, id any) []map[string]any {
	s.t.Helper()
	rec := s.do("GET", fmt.Sprintf("/api/v1/contacts/%v/timeline", id), nil, token)
	var out []map[string]any
	for _, a := range wantJSON(s.t, rec, http.StatusOK)["activities"].([]any) {
		out = append(out, a.(map[string]any))
	}
	return out
}

func kinds(acts []map[string]any) string {
	k := make([]string, 0, len(acts))
	for _, a := range acts {
		k = append(k, a["kind"].(string))
	}
	return strings.Join(k, ",")
}

func TestWebFormIntake(t *testing.T) {
	s := newServer(t)
	_, owner := s.userToken()
	key := s.formSource(owner, "Website")

	rec := s.do("POST", "/api/v1/intake/forms/"+key, map[string]any{
		"name": "Priya Sharma", "email": "Priya@Example.com", "phone": "98765 43210",
		"destination": "Goa", "message": "What is the price for 4 nights?", "utm_campaign": "ignored",
	}, "")
	if got := wantJSON(t, rec, http.StatusAccepted); got["status"] != "received" || len(got) != 1 {
		t.Errorf("response = %v (must not reveal the contact)", got)
	}

	list := s.v1Contacts(owner, "")
	if len(list) != 1 {
		t.Fatalf("contacts = %v", list)
	}
	c := list[0].(map[string]any)
	score := c["score"].(map[string]any)
	if c["first_name"] != "Priya" || c["last_name"] != "Sharma" || c["email"] != "priya@example.com" ||
		c["source"] != "Website" || c["lifecycle_stage"] != "lead" {
		t.Errorf("contact = %v", c)
	}
	// Default rules: email 10 + mobile 15 + "price" 15.
	if score["score"] != float64(40) || score["temperature"] != "warm" || score["method"] != "rules" {
		t.Errorf("score = %v", score)
	}
	if got := kinds(s.timeline(owner, c["id"])); got != "score_changed,message,captured" && got != "score_changed,captured,message" {
		t.Errorf("timeline kinds = %s", got)
	}

	// The same person again (different email case, phone format): no new
	// contact, the submission is on their timeline.
	wantStatus(t, s.do("POST", "/api/v1/intake/forms/"+key, map[string]any{
		"first_name": "Priya", "email": " PRIYA@example.COM ", "mobile": "+91-98765-43210",
	}, ""), http.StatusAccepted)
	if list := s.v1Contacts(owner, ""); len(list) != 1 {
		t.Fatalf("duplicate created: %v", list)
	}
	if got := kinds(s.timeline(owner, c["id"])); !strings.HasPrefix(got, "duplicate_captured,") {
		t.Errorf("timeline after repeat = %s", got)
	}

	// An HTML form post works too.
	form := url.Values{"first_name": {"Arjun"}, "mobile": {"09123456789"}}
	req := httptest.NewRequest("POST", "/api/v1/intake/forms/"+key, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wantStatus(t, s.serve(req), http.StatusAccepted)
	if list := s.v1Contacts(owner, ""); len(list) != 2 || list[0].(map[string]any)["first_name"] != "Arjun" {
		t.Errorf("form post contacts = %v", list)
	}
}

func TestWebFormIntakeRejects(t *testing.T) {
	s := newServer(t)
	_, owner := s.userToken()
	key := s.formSource(owner, "Website")
	post := func(k string, body map[string]any) *httpResult {
		return s.do("POST", "/api/v1/intake/forms/"+k, body, "")
	}

	wantStatus(t, s.do("POST", "/api/v1/intake/forms/"+key, "first_name=A", ""), http.StatusBadRequest) // malformed JSON
	wantProblem(t, post("lsk_nope", map[string]any{"first_name": "A", "email": "a@b.co"}), http.StatusNotFound, "not_found")
	p := wantProblem(t, post(key, map[string]any{"first_name": "A"}), http.StatusUnprocessableEntity, "validation_failed")
	if p["errors"].(map[string]any)["email"] != "required" {
		t.Errorf("problem = %v", p)
	}
	wantProblem(t, post(key, map[string]any{"first_name": "A", "email": "not-an-email"}), http.StatusUnprocessableEntity, "validation_failed")

	// Honeypot: accepted, nothing stored.
	wantStatus(t, post(key, map[string]any{"first_name": "Bot", "email": "bot@spam.co", "website": "http://spam"}), http.StatusAccepted)
	if list := s.v1Contacts(owner, ""); len(list) != 0 {
		t.Errorf("honeypot submission stored: %v", list)
	}

	// A deactivated source stops accepting submissions.
	src := wantJSON(t, s.do("GET", "/api/v1/lead-sources", nil, owner), http.StatusOK)["sources"].([]any)[0].(map[string]any)
	wantStatus(t, s.do("PATCH", fmt.Sprintf("/api/v1/lead-sources/%v", src["id"]), map[string]any{"active": false}, owner), http.StatusOK)
	wantProblem(t, post(key, map[string]any{"first_name": "A", "email": "a@b.co"}), http.StatusNotFound, "not_found")

	// Rotating the key retires the old one.
	wantStatus(t, s.do("PATCH", fmt.Sprintf("/api/v1/lead-sources/%v", src["id"]), map[string]any{"active": true}, owner), http.StatusOK)
	rec := s.do("POST", fmt.Sprintf("/api/v1/lead-sources/%v/rotate-key", src["id"]), nil, owner)
	newKey := wantJSON(t, rec, http.StatusOK)["source"].(map[string]any)["public_key"].(string)
	wantProblem(t, post(key, map[string]any{"first_name": "A", "email": "a@b.co"}), http.StatusNotFound, "not_found")
	wantStatus(t, post(newKey, map[string]any{"first_name": "A", "email": "a@b.co"}), http.StatusAccepted)
}

func TestLeadRouting(t *testing.T) {
	s := newServer(t)
	ownerID, owner := s.userToken()
	org := s.orgOf(ownerID)
	rep1, _ := s.member(org, "rep", "rep1@example.com")
	rep2, _ := s.member(org, "rep", "rep2@example.com")
	senior, _ := s.member(org, "rep", "senior@example.com")
	key := s.formSource(owner, "Website")

	// Hot leads go to the senior rep; everything else round robin.
	wantStatus(t, s.do("POST", "/api/v1/assignment-rules", map[string]any{
		"name": "Hot", "position": 1, "criterion": "temperature_equals", "value": "hot", "user_ids": []int64{senior},
	}, owner), http.StatusCreated)
	wantStatus(t, s.do("POST", "/api/v1/assignment-rules", map[string]any{
		"name": "Everyone", "position": 2, "criterion": "any", "user_ids": []int64{rep2, rep1},
	}, owner), http.StatusCreated)

	var owners []any
	for i, msg := range []string{"hi", "hello", "hey", "Urgent: price and quote please, budget ready"} {
		wantStatus(t, s.do("POST", "/api/v1/intake/forms/"+key, map[string]any{
			"first_name": fmt.Sprint("Lead", i), "email": fmt.Sprintf("lead%d@example.com", i),
			"mobile": fmt.Sprintf("98000000%02d", i), "message": msg,
		}, ""), http.StatusAccepted)
		c := s.v1Contacts(owner, "")[0].(map[string]any)
		owners = append(owners, c["lead_owner_id"])
	}
	want := []any{float64(rep1), float64(rep2), float64(rep1), float64(senior)}
	if fmt.Sprint(owners) != fmt.Sprint(want) {
		t.Errorf("owners = %v, want %v", owners, want)
	}
	hot := s.v1Contacts(owner, "?temperature=hot")
	if len(hot) != 1 || hot[0].(map[string]any)["first_name"] != "Lead3" {
		t.Errorf("hot leads = %v", hot)
	}

	// Members must be in the org.
	other := s.register("Other", "other@example.com", "secret-pass-1")
	wantProblem(t, s.do("POST", "/api/v1/assignment-rules", map[string]any{
		"name": "X", "criterion": "any", "user_ids": []int64{other},
	}, owner), http.StatusUnprocessableEntity, "invalid_owner")
}

func TestLeadsPermissionsAndTenancy(t *testing.T) {
	s := newServer(t)
	ownerID, owner := s.userToken()
	org := s.orgOf(ownerID)
	repID, rep := s.member(org, "rep", "rep@example.com")
	_, viewer := s.member(org, "viewer", "viewer@example.com")

	for _, path := range []string{"/api/v1/scoring-rules", "/api/v1/lead-sources", "/api/v1/assignment-rules", "/api/v1/lead-settings", "/api/v1/whatsapp-accounts"} {
		wantProblem(t, s.do("GET", path, nil, rep), http.StatusForbidden, "forbidden")
		wantStatus(t, s.do("GET", path, nil, owner), http.StatusOK)
	}
	wantProblem(t, s.do("GET", "/api/v1/scoring-rules", nil, ""), http.StatusUnauthorized, "unauthorized")
	wantProblem(t, s.do("POST", "/api/v1/leads", map[string]any{"first_name": "V", "email": "v@x.co"}, viewer), http.StatusForbidden, "forbidden")

	// New orgs get the default scoring rules.
	if rules := wantJSON(t, s.do("GET", "/api/v1/scoring-rules", nil, owner), http.StatusOK)["rules"].([]any); len(rules) != 8 {
		t.Errorf("default rules = %d, want 8", len(rules))
	}

	// A rep owns the leads they enter.
	rec := s.do("POST", "/api/v1/leads", map[string]any{"first_name": "Ravi", "email": "ravi@example.com", "channel": "phone"}, rep)
	lead := wantJSON(t, rec, http.StatusCreated)["lead"].(map[string]any)
	if lead["owner_id"] != float64(repID) || lead["created"] != true || lead["temperature"] != "cold" {
		t.Errorf("rep lead = %v", lead)
	}
	// The owner entering the same person gets the existing contact.
	rec = s.do("POST", "/api/v1/leads", map[string]any{"first_name": "Ravi", "email": "RAVI@example.com"}, owner)
	if again := wantJSON(t, rec, http.StatusOK)["lead"].(map[string]any); again["contact_id"] != lead["contact_id"] || again["created"] != false {
		t.Errorf("owner re-entry = %v", again)
	}

	// A rep entering someone owned by another user learns only that it exists.
	wantStatus(t, s.do("POST", "/api/v1/leads", map[string]any{"first_name": "Mina", "email": "mina@example.com"}, owner), http.StatusCreated)
	wantProblem(t, s.do("POST", "/api/v1/leads", map[string]any{"first_name": "Mina", "email": "mina@example.com"}, rep), http.StatusConflict, "duplicate_contact")

	// Reps see only their own contacts in /api/v1.
	if list := s.v1Contacts(rep, ""); len(list) != 1 {
		t.Errorf("rep sees %d contacts, want 1", len(list))
	}

	// Another org cannot see, score or log on this org's contacts.
	_, outsider := func() (int64, string) {
		s.register("Out", "out@example.com", "secret-pass-1")
		return 0, s.login("out@example.com", "secret-pass-1")
	}()
	id := lead["contact_id"]
	wantProblem(t, s.do("GET", fmt.Sprintf("/api/v1/contacts/%v", id), nil, outsider), http.StatusNotFound, "not_found")
	wantProblem(t, s.do("GET", fmt.Sprintf("/api/v1/contacts/%v/timeline", id), nil, outsider), http.StatusNotFound, "not_found")
	wantProblem(t, s.do("POST", fmt.Sprintf("/api/v1/leads/%v/rescore", id), nil, outsider), http.StatusNotFound, "not_found")
	if list := s.v1Contacts(outsider, ""); len(list) != 0 {
		t.Errorf("outsider sees %v", list)
	}

	// Dedupe is per org: the outsider's org gets its own contact.
	rec = s.do("POST", "/api/v1/leads", map[string]any{"first_name": "Ravi", "email": "ravi@example.com"}, outsider)
	if l := wantJSON(t, rec, http.StatusCreated)["lead"].(map[string]any); l["contact_id"] == id {
		t.Error("dedupe matched a contact in another org")
	}
}

func TestContactTimelineAndStage(t *testing.T) {
	s := newServer(t)
	ownerID, owner := s.userToken()
	_, viewer := s.member(s.orgOf(ownerID), "viewer", "viewer@example.com")
	rec := s.do("POST", "/api/v1/leads", map[string]any{"first_name": "Ravi", "mobile": "9876543210"}, owner)
	id := wantJSON(t, rec, http.StatusCreated)["lead"].(map[string]any)["contact_id"]
	path := fmt.Sprintf("/api/v1/contacts/%v", id)

	rec = s.do("POST", path+"/activities", map[string]any{"kind": "call", "direction": "outbound", "body": "Discussed dates"}, owner)
	if a := wantJSON(t, rec, http.StatusCreated)["activity"].(map[string]any); a["kind"] != "call" || a["channel"] != "phone" || a["actor_user_id"] != float64(ownerID) {
		t.Errorf("activity = %v", a)
	}
	wantProblem(t, s.do("POST", path+"/activities", map[string]any{"kind": "call", "body": "x"}, owner), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, s.do("POST", path+"/activities", map[string]any{"kind": "captured", "body": "x"}, owner), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, s.do("POST", path+"/activities", map[string]any{"kind": "note", "body": "x"}, viewer), http.StatusForbidden, "forbidden")

	rec = s.do("PUT", path+"/lifecycle-stage", map[string]any{"lifecycle_stage": "sql"}, owner)
	if c := wantJSON(t, rec, http.StatusOK)["contact"].(map[string]any); c["lifecycle_stage"] != "sql" {
		t.Errorf("contact = %v", c)
	}
	wantProblem(t, s.do("PUT", path+"/lifecycle-stage", map[string]any{"lifecycle_stage": "vip"}, owner), http.StatusUnprocessableEntity, "validation_failed")
	if got := s.v1Contacts(owner, "?lifecycle_stage=sql"); len(got) != 1 {
		t.Errorf("sql contacts = %v", got)
	}

	acts := s.timeline(owner, id)
	if acts[0]["kind"] != "lifecycle_changed" || acts[1]["kind"] != "call" {
		t.Errorf("timeline = %s", kinds(acts))
	}
	// Paging.
	page := wantJSON(t, s.do("GET", fmt.Sprintf("%s/timeline?limit=1&before_id=%v", path, acts[0]["id"]), nil, owner), http.StatusOK)["activities"].([]any)
	if len(page) != 1 || page[0].(map[string]any)["id"] == acts[0]["id"] {
		t.Errorf("page = %v", page)
	}
	wantProblem(t, s.do("GET", path+"/timeline?limit=-1", nil, owner), http.StatusBadRequest, "invalid_query")
}

func TestScoringRulesAndRescore(t *testing.T) {
	s := newServer(t)
	_, owner := s.userToken()
	rec := s.do("POST", "/api/v1/leads", map[string]any{"first_name": "Ravi", "email": "ravi@example.com", "destination": "Goa"}, owner)
	lead := wantJSON(t, rec, http.StatusCreated)["lead"].(map[string]any)
	if lead["score"] != float64(10) {
		t.Fatalf("lead = %v", lead)
	}

	wantProblem(t, s.do("POST", "/api/v1/scoring-rules", map[string]any{"name": "x", "criterion": "is_vip", "points": 5}, owner),
		http.StatusUnprocessableEntity, "validation_failed")
	rec = s.do("POST", "/api/v1/scoring-rules", map[string]any{"name": "Goa", "criterion": "destination_equals", "value": "goa", "points": 60}, owner)
	rule := wantJSON(t, rec, http.StatusCreated)["rule"].(map[string]any)

	rec = s.do("POST", fmt.Sprintf("/api/v1/leads/%v/rescore", lead["contact_id"]), nil, owner)
	if got := wantJSON(t, rec, http.StatusOK)["lead"].(map[string]any); got["score"] != float64(70) || got["temperature"] != "hot" {
		t.Errorf("rescored = %v", got)
	}

	// Thresholds are per org.
	wantProblem(t, s.do("PATCH", "/api/v1/lead-settings", map[string]any{"warm_threshold": 90}, owner), http.StatusUnprocessableEntity, "validation_failed")
	wantStatus(t, s.do("PATCH", "/api/v1/lead-settings", map[string]any{"hot_threshold": 80}, owner), http.StatusOK)
	rec = s.do("POST", fmt.Sprintf("/api/v1/leads/%v/rescore", lead["contact_id"]), nil, owner)
	if got := wantJSON(t, rec, http.StatusOK)["lead"].(map[string]any); got["temperature"] != "warm" {
		t.Errorf("after raising hot threshold = %v", got)
	}

	wantStatus(t, s.do("PATCH", fmt.Sprintf("/api/v1/scoring-rules/%v", rule["id"]), map[string]any{"active": false}, owner), http.StatusOK)
	wantStatus(t, s.do("DELETE", fmt.Sprintf("/api/v1/scoring-rules/%v", rule["id"]), nil, owner), http.StatusNoContent)
	wantProblem(t, s.do("DELETE", fmt.Sprintf("/api/v1/scoring-rules/%v", rule["id"]), nil, owner), http.StatusNotFound, "not_found")
}

const waSecret = "wa-app-secret"

func newWhatsAppServer(t *testing.T) *server {
	env := testEnv()
	env["WHATSAPP_APP_SECRET"] = waSecret
	env["WHATSAPP_VERIFY_TOKEN"] = "wa-verify"
	return newServerWith(t, env)
}

func waBody(msgID, from, name, text string) string {
	return fmt.Sprintf(`{"object":"whatsapp_business_account","entry":[{"id":"W","changes":[{"field":"messages","value":{
		"messaging_product":"whatsapp","metadata":{"display_phone_number":"15550001111","phone_number_id":"1111"},
		"contacts":[{"profile":{"name":%q},"wa_id":%q}],
		"messages":[{"from":%q,"id":%q,"timestamp":"1762761600","type":"text","text":{"body":%q}}]}}]}]}`,
		name, from, from, msgID, text)
}

func (s *server) waPost(body, signature string) *httpResult {
	s.t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/webhooks/whatsapp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if signature != "" {
		req.Header.Set("X-Hub-Signature-256", signature)
	}
	return s.serve(req)
}

func waSign(body string) string {
	mac := hmac.New(sha256.New, []byte(waSecret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestWhatsAppWebhook(t *testing.T) {
	s := newWhatsAppServer(t)
	_, owner := s.userToken()

	// Meta's subscription handshake.
	rec := s.serve(newRequest("GET", "/api/v1/webhooks/whatsapp?hub.mode=subscribe&hub.verify_token=wa-verify&hub.challenge=12345"))
	if rec.Code != http.StatusOK || rec.Body.String() != "12345" {
		t.Errorf("handshake = %d %q", rec.Code, rec.Body.String())
	}
	wantProblem(t, s.serve(newRequest("GET", "/api/v1/webhooks/whatsapp?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=1")),
		http.StatusForbidden, "forbidden")

	wantStatus(t, s.do("POST", "/api/v1/whatsapp-accounts", map[string]any{"phone_number_id": "1111", "display_phone": "+1 555 000 1111"}, owner), http.StatusCreated)
	wantProblem(t, s.do("POST", "/api/v1/whatsapp-accounts", map[string]any{"phone_number_id": "1111"}, owner), http.StatusConflict, "phone_number_taken")

	// Someone already in the CRM from a form, with a local number format.
	wantStatus(t, s.do("POST", "/api/v1/leads", map[string]any{"first_name": "Priya", "mobile": "098765 43210"}, owner), http.StatusCreated)

	body := waBody("wamid.1", "919876543210", "Priya Sharma", "Hi, what is the price?")
	wantProblem(t, s.waPost(body, ""), http.StatusUnauthorized, "invalid_signature")
	wantProblem(t, s.waPost(body, waSign(body+" ")), http.StatusUnauthorized, "invalid_signature")
	wantStatus(t, s.waPost(body, waSign(body)), http.StatusOK)
	wantStatus(t, s.waPost(body, waSign(body)), http.StatusOK) // Meta retry

	list := s.v1Contacts(owner, "")
	if len(list) != 1 {
		t.Fatalf("WhatsApp message did not match the existing contact: %v", list)
	}
	c := list[0].(map[string]any)
	score := c["score"].(map[string]any)
	// Mobile 15 + WhatsApp 15 + "price" 15.
	if score["score"] != float64(45) || score["temperature"] != "warm" {
		t.Errorf("score after first message = %v", score)
	}
	acts := s.timeline(owner, c["id"])
	messages := 0
	for _, a := range acts {
		if a["kind"] == "message" {
			messages++
			if a["channel"] != "whatsapp" || a["direction"] != "inbound" || a["body"] != "Hi, what is the price?" {
				t.Errorf("message activity = %v", a)
			}
		}
	}
	if messages != 1 {
		t.Errorf("%d message activities after a retry, want 1", messages)
	}

	// A reply re-scores: replied at least twice (+20) and "quote" (+15) make the lead hot.
	reply := waBody("wamid.2", "919876543210", "Priya Sharma", "Please send a quote")
	wantStatus(t, s.waPost(reply, waSign(reply)), http.StatusOK)
	c = s.v1Contacts(owner, "")[0].(map[string]any)
	if score := c["score"].(map[string]any); score["score"] != float64(80) || score["temperature"] != "hot" {
		t.Errorf("score after reply = %v", score)
	}

	// A new number becomes a new lead named from the WhatsApp profile.
	other := waBody("wamid.3", "14155550100", "John Doe", "hello")
	wantStatus(t, s.waPost(other, waSign(other)), http.StatusOK)
	c = s.v1Contacts(owner, "")[0].(map[string]any)
	if c["first_name"] != "John" || c["last_name"] != "Doe" || c["mobile"] != "+14155550100" || c["source"] != "WhatsApp" {
		t.Errorf("new WhatsApp lead = %v", c)
	}

	// Messages to numbers no org registered are acknowledged and dropped.
	stray := strings.Replace(waBody("wamid.4", "14155550199", "X", "hi"), `"phone_number_id":"1111"`, `"phone_number_id":"9999"`, 1)
	wantStatus(t, s.waPost(stray, waSign(stray)), http.StatusOK)
	if n := len(s.v1Contacts(owner, "")); n != 2 {
		t.Errorf("stray message created a contact (%d contacts)", n)
	}
}

func TestWhatsAppWebhookDisabled(t *testing.T) {
	s := newServer(t)
	wantProblem(t, s.waPost(`{}`, "sha256=00"), http.StatusServiceUnavailable, "whatsapp_disabled")
}

// Simultaneous submissions of the same person create one contact.
func TestConcurrentIntakeDedupes(t *testing.T) {
	s := newServer(t)
	_, owner := s.userToken()
	key := s.formSource(owner, "Website")

	const n = 8
	codes := make(chan int, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			body := fmt.Sprintf(`{"first_name":"Asha","email":"asha.lead@example.com","mobile":"98765 0000%d"}`, i%2)
			req := httptest.NewRequest("POST", "/api/v1/intake/forms/"+key, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			codes <- s.serve(req).Code
		}(i)
	}
	for i := 0; i < n; i++ {
		if code := <-codes; code != http.StatusAccepted {
			t.Errorf("status %d", code)
		}
	}
	if list := s.v1Contacts(owner, ""); len(list) != 1 {
		t.Errorf("%d contacts, want 1", len(list))
	}
}
