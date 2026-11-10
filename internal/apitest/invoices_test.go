package apitest

import (
	"bytes"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
)

// A valid Maharashtra GSTIN (state 27) for the seller.
const sellerGSTIN = "27AAPFU0939F1ZV"

func (s *server) billingProfile(token string) {
	s.t.Helper()
	wantStatus(s.t, s.do("PUT", "/api/v1/billing-profile", map[string]any{
		"legal_name": "Shearaion Travels Pvt Ltd", "gstin": sellerGSTIN, "address": "12 MG Road, Pune 411001",
		"default_sac": "998552", "default_gst_rate_bp": 500, "bank_details": "HDFC Bank, A/c 123",
	}, token), http.StatusOK)
}

func (s *server) invoice(token string, id any) map[string]any {
	s.t.Helper()
	return wantJSON(s.t, s.do("GET", fmt.Sprintf("/api/v1/invoices/%v", id), nil, token), http.StatusOK)["invoice"].(map[string]any)
}

func (s *server) invoiceAction(token string, id any, action string, body any) *httpResult {
	s.t.Helper()
	return s.do("POST", fmt.Sprintf("/api/v1/invoices/%v/%s", id, action), body, token)
}

func TestBillingProfile(t *testing.T) {
	s := newServer(t)
	ownerID, owner := s.userToken()
	_, rep := s.member(s.orgOf(ownerID), "rep", "rep@example.com")

	p := wantJSON(t, s.do("GET", "/api/v1/billing-profile", nil, owner), http.StatusOK)["billing_profile"].(map[string]any)
	if p["invoice_prefix"] != "INV" || p["default_gst_rate_bp"] != float64(1800) || p["payment_terms_days"] != float64(7) {
		t.Errorf("defaults = %v", p)
	}
	put := func(body map[string]any, token string) *httpResult {
		return s.do("PUT", "/api/v1/billing-profile", body, token)
	}
	wantProblem(t, put(map[string]any{"gstin": "27AAPFU0939F1ZW"}, owner), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, put(map[string]any{"gstin": sellerGSTIN, "state_code": "29"}, owner), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, put(map[string]any{"invoice_prefix": "INVOICE"}, owner), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, put(map[string]any{"default_sac": "99"}, owner), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, put(map[string]any{"legal_name": "X"}, rep), http.StatusForbidden, "forbidden")

	rec := put(map[string]any{"gstin": strings.ToLower(sellerGSTIN), "legal_name": "Shearaion"}, owner)
	if p := wantJSON(t, rec, http.StatusOK)["billing_profile"].(map[string]any); p["gstin"] != sellerGSTIN || p["state_code"] != "27" {
		t.Errorf("saved = %v", p)
	}
}

func TestWonDealOnboarding(t *testing.T) {
	s, owner := newNurtureServer(t) // clock at 10 Nov 2026, noon IST
	s.billingProfile(owner)
	s.createSequence(owner, welcomeSequence) // new_lead nurture
	s.createSequence(owner, map[string]any{
		"name": "Welcome aboard", "trigger": "deal_won", "active": true, "stop_on_reply": false,
		"steps": []map[string]any{{"channel": "email", "subject": "Welcome, {{first_name}}!", "body": "Your trip is confirmed."}},
	})
	key := s.formSource(owner, "Website")
	wantStatus(t, s.do("POST", "/api/v1/intake/forms/"+key, map[string]any{
		"name": "Priya Sharma", "email": "priya@example.com", "mobile": "9876543210",
	}, ""), http.StatusAccepted)
	contact := s.v1Contacts(owner, "")[0].(map[string]any)["id"]
	// She opted out of marketing: onboarding email still goes (transactional).
	s.exec(`UPDATE contacts SET marketing_opt_out_at = now() WHERE id = $1`, contact)

	stage := s.stageIDs(owner)
	deal := s.createDeal(owner, map[string]any{"title": "Goa holiday", "contact_id": contact, "amount_paise": 4500000})
	wantStatus(t, s.moveDeal(owner, deal, stage["Closed Won"], ""), http.StatusOK)

	// Nurture swapped for onboarding.
	var got []string
	for _, e := range s.enrollments(owner, fmt.Sprintf("?contact_id=%v", contact)) {
		got = append(got, fmt.Sprintf("%s:%s:%s", e["sequence_name"], e["status"], e["stop_reason"]))
	}
	sort.Strings(got)
	if strings.Join(got, " ") != "Welcome aboard:active: Welcome:stopped:won" {
		t.Errorf("enrollments = %v", got)
	}
	s.runJob("nurture")
	mails := s.mailTo("priya@example.com")
	if len(mails) != 1 || !strings.HasPrefix(mails[0], "Welcome, Priya!\nYour trip is confirmed.") || strings.Contains(mails[0], "unsubscribe") {
		t.Errorf("welcome emails = %q", mails)
	}

	// A draft invoice from the deal, and a review task for the owner.
	list := wantJSON(t, s.do("GET", fmt.Sprintf("/api/v1/invoices?deal_id=%v", deal["id"]), nil, owner), http.StatusOK)["invoices"].([]any)
	if len(list) != 1 {
		t.Fatalf("invoices = %v", list)
	}
	inv := s.invoice(owner, list[0].(map[string]any)["id"])
	line := inv["lines"].([]any)[0].(map[string]any)
	if inv["status"] != "draft" || inv["number"] != nil || inv["bill_to_name"] != "Priya Sharma" || inv["bill_to_email"] != "priya@example.com" ||
		line["description"] != "Goa holiday" || line["sac"] != "998552" || line["gst_rate_bp"] != float64(500) || line["unit_price_paise"] != float64(4500000) {
		t.Errorf("draft = %v", inv)
	}
	tasks := s.tasks(owner, fmt.Sprintf("?contact_id=%v", contact))
	if len(tasks) != 1 || tasks[0]["origin"] != "invoice_review" || !strings.HasPrefix(tasks[0]["title"].(string), "Review and issue invoice") {
		t.Errorf("tasks = %v", tasks)
	}

	// Issuing needs a place of supply.
	p := wantProblem(t, s.invoiceAction(owner, inv["id"], "issue", nil), http.StatusUnprocessableEntity, "invoice_incomplete")
	if fmt.Sprint(p["errors"]) != "map[place_of_supply:required]" {
		t.Errorf("incomplete = %v", p)
	}
	// Karnataka customer, Maharashtra seller: IGST.
	rec := s.do("PATCH", fmt.Sprintf("/api/v1/invoices/%v", inv["id"]), map[string]any{
		"place_of_supply": "29", "bill_to_address": "4 Lake View, Bengaluru",
	}, owner)
	if d := wantJSON(t, rec, http.StatusOK)["invoice"].(map[string]any); d["igst_paise"] != float64(225000) || d["total_paise"] != float64(4725000) {
		t.Errorf("draft totals = %v", d)
	}
	rec = s.invoiceAction(owner, inv["id"], "issue", nil)
	issued := wantJSON(t, rec, http.StatusOK)["invoice"].(map[string]any)
	if issued["status"] != "issued" || issued["number"] != "INV/26-27/0001" || issued["financial_year"] != "2026-27" ||
		issued["issue_date"] != "2026-11-10" || issued["due_date"] != "2026-11-17" || issued["seller_gstin"] != sellerGSTIN ||
		issued["seller_name"] != "Shearaion Travels Pvt Ltd" {
		t.Errorf("issued = %v", issued)
	}
	wantProblem(t, s.do("PATCH", fmt.Sprintf("/api/v1/invoices/%v", inv["id"]), map[string]any{"notes": "x"}, owner), http.StatusConflict, "not_draft")
	wantProblem(t, s.invoiceAction(owner, inv["id"], "issue", nil), http.StatusConflict, "not_draft")

	// The PDF.
	rec = s.do("GET", fmt.Sprintf("/api/v1/invoices/%v/pdf", inv["id"]), nil, owner)
	wantStatus(t, rec, http.StatusOK)
	if rec.Header().Get("Content-Type") != "application/pdf" || !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")) ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), "INV-26-27-0001.pdf") {
		t.Errorf("pdf: %v %q", rec.Header(), rec.Body.Bytes()[:min(10, rec.Body.Len())])
	}

	// Emailing attaches it.
	before := len(s.mail.messages())
	rec = s.invoiceAction(owner, inv["id"], "send", nil)
	if sent := wantJSON(t, rec, http.StatusOK)["invoice"].(map[string]any); sent["sent_at"] == nil {
		t.Errorf("sent = %v", sent)
	}
	m := s.mail.messages()[before]
	if m.To != "priya@example.com" || m.Subject != "Invoice INV/26-27/0001 from Shearaion Travels Pvt Ltd" ||
		len(m.Attachments) != 1 || m.Attachments[0].Name != "INV-26-27-0001.pdf" || !bytes.HasPrefix(m.Attachments[0].Data, []byte("%PDF-")) {
		t.Errorf("invoice email = %+v", m)
	}
	if got := kinds(s.timeline(owner, contact)); !strings.Contains(got, "invoice_sent,invoice_issued") {
		t.Errorf("timeline = %s", got)
	}
}

func TestInvoiceTaxAndNumbering(t *testing.T) {
	s, owner := newNurtureServer(t)
	s.billingProfile(owner)
	create := func(body map[string]any) map[string]any {
		return wantJSON(t, s.do("POST", "/api/v1/invoices", body, owner), http.StatusCreated)["invoice"].(map[string]any)
	}

	// Same state (27): CGST + SGST; inclusive prices; rounding to the rupee.
	inv := create(map[string]any{
		"bill_to_name": "Acme Corp", "bill_to_gstin": "27AAPFU0939F1ZV", "prices_include_gst": true,
		"lines": []map[string]any{
			{"description": "Corporate offsite", "quantity": 1, "unit_price_paise": 11800000, "gst_rate_bp": 1800},
			{"description": "Visa fees", "sac": "998599", "quantity": 3, "unit_price_paise": 33333, "gst_rate_bp": 0},
		},
	})
	if inv["place_of_supply"] != "27" || inv["taxable_paise"] != float64(10000000+99999) || inv["cgst_paise"] != float64(900000) ||
		inv["sgst_paise"] != float64(900000) || inv["igst_paise"] != float64(0) || inv["round_off_paise"] != float64(1) ||
		inv["total_paise"] != float64(11900000) {
		t.Errorf("intra-state inclusive = %v", inv)
	}
	wantProblem(t, s.do("POST", "/api/v1/invoices", map[string]any{"bill_to_name": "X", "lines": []map[string]any{
		{"description": "x", "quantity": 0, "unit_price_paise": 1}}}, owner), http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, s.do("POST", "/api/v1/invoices", map[string]any{"bill_to_gstin": "29ABCDE1234F1Z5"}, owner),
		http.StatusUnprocessableEntity, "validation_failed")

	// Five drafts issued at once get five consecutive numbers.
	var ids []any
	for i := range 5 {
		d := create(map[string]any{"bill_to_name": fmt.Sprint("Customer ", i), "place_of_supply": "29",
			"lines": []map[string]any{{"description": "Tour", "quantity": 1, "unit_price_paise": 100000}}})
		ids = append(ids, d["id"])
	}
	var mu sync.Mutex
	var numbers []string
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := s.invoiceAction(owner, id, "issue", nil)
			if rec.Code != http.StatusOK {
				t.Errorf("issue %v: %d %s", id, rec.Code, rec.Body.String())
				return
			}
			mu.Lock()
			numbers = append(numbers, decode(t, rec)["invoice"].(map[string]any)["number"].(string))
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Strings(numbers)
	if strings.Join(numbers, " ") != "INV/26-27/0001 INV/26-27/0002 INV/26-27/0003 INV/26-27/0004 INV/26-27/0005" {
		t.Errorf("numbers = %v", numbers)
	}

	// Cancelling keeps the number used; drafts can be deleted, issued ones cannot.
	wantProblem(t, s.invoiceAction(owner, ids[0], "cancel", map[string]any{"reason": ""}), http.StatusUnprocessableEntity, "validation_failed")
	rec := s.invoiceAction(owner, ids[0], "cancel", map[string]any{"reason": "Wrong customer"})
	if c := wantJSON(t, rec, http.StatusOK)["invoice"].(map[string]any); c["status"] != "cancelled" || c["cancelled_at"] == nil {
		t.Errorf("cancelled = %v", c)
	}
	wantProblem(t, s.invoiceAction(owner, ids[0], "cancel", map[string]any{"reason": "again"}), http.StatusConflict, "not_issued")
	wantProblem(t, s.do("DELETE", fmt.Sprintf("/api/v1/invoices/%v", ids[1]), nil, owner), http.StatusConflict, "not_draft")
	wantStatus(t, s.do("DELETE", fmt.Sprintf("/api/v1/invoices/%v", inv["id"]), nil, owner), http.StatusNoContent)
	next := create(map[string]any{"bill_to_name": "Next", "place_of_supply": "27",
		"lines": []map[string]any{{"description": "Tour", "quantity": 1, "unit_price_paise": 100}}})
	rec = s.invoiceAction(owner, next["id"], "issue", nil)
	if n := wantJSON(t, rec, http.StatusOK)["invoice"].(map[string]any)["number"]; n != "INV/26-27/0006" {
		t.Errorf("next number = %v", n)
	}

	// A draft PDF preview is available before issuing.
	draft := create(map[string]any{"bill_to_name": "Preview", "lines": []map[string]any{{"description": "x", "quantity": 1, "unit_price_paise": 1}}})
	rec = s.do("GET", fmt.Sprintf("/api/v1/invoices/%v/pdf", draft["id"]), nil, owner)
	if rec.Code != http.StatusOK || !bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")) {
		t.Errorf("draft preview: %d", rec.Code)
	}
}

func TestInvoicePermissions(t *testing.T) {
	s := newServer(t)
	ownerID, owner := s.userToken()
	s.billingProfile(owner)
	org := s.orgOf(ownerID)
	_, rep := s.member(org, "rep", "rep@example.com")
	_, rep2 := s.member(org, "rep", "rep2@example.com")
	_, viewer := s.member(org, "viewer", "viewer@example.com")

	rec := s.do("POST", "/api/v1/invoices", map[string]any{"bill_to_name": "Rep customer", "place_of_supply": "27",
		"lines": []map[string]any{{"description": "Tour", "quantity": 1, "unit_price_paise": 100000}}}, rep)
	inv := wantJSON(t, rec, http.StatusCreated)["invoice"].(map[string]any)
	wantStatus(t, s.invoiceAction(rep, inv["id"], "issue", nil), http.StatusOK)

	wantProblem(t, s.do("GET", fmt.Sprintf("/api/v1/invoices/%v", inv["id"]), nil, rep2), http.StatusNotFound, "not_found")
	wantProblem(t, s.do("POST", "/api/v1/invoices", map[string]any{"bill_to_name": "x"}, viewer), http.StatusForbidden, "forbidden")
	wantProblem(t, s.invoiceAction(rep, inv["id"], "cancel", map[string]any{"reason": "oops"}), http.StatusForbidden, "forbidden")
	wantProblem(t, s.invoiceAction(rep, inv["id"], "send", nil), http.StatusUnprocessableEntity, "validation_failed") // no email
	if list := wantJSON(t, s.do("GET", "/api/v1/invoices", nil, viewer), http.StatusOK)["invoices"].([]any); len(list) != 1 {
		t.Errorf("viewer sees %d invoices", len(list))
	}

	s.register("Out", "out@example.com", "secret-pass-1")
	outsider := s.login("out@example.com", "secret-pass-1")
	wantProblem(t, s.do("GET", fmt.Sprintf("/api/v1/invoices/%v/pdf", inv["id"]), nil, outsider), http.StatusNotFound, "not_found")
	// Each org numbers its own invoices.
	wantStatus(t, s.do("PUT", "/api/v1/billing-profile", map[string]any{"legal_name": "Out Ltd", "gstin": "27AAPFU0939F1ZV",
		"address": "x", "default_sac": "998552"}, outsider), http.StatusOK)
	rec = s.do("POST", "/api/v1/invoices", map[string]any{"bill_to_name": "C", "place_of_supply": "27",
		"lines": []map[string]any{{"description": "Tour", "quantity": 1, "unit_price_paise": 1}}}, outsider)
	other := wantJSON(t, rec, http.StatusCreated)["invoice"].(map[string]any)
	rec = s.invoiceAction(outsider, other["id"], "issue", nil)
	if n := wantJSON(t, rec, http.StatusOK)["invoice"].(map[string]any)["number"]; !strings.HasSuffix(n.(string), "/0001") {
		t.Errorf("outsider's first number = %v", n)
	}
}
