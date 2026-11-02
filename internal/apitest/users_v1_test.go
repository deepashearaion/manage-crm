package apitest

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// wantProblem asserts an RFC 7807 problem+json response and returns it.
func wantProblem(t *testing.T, rec *httpResult, status int, code string) map[string]any {
	t.Helper()
	wantStatus(t, rec, status)
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	p := decode(t, rec)
	if p["status"] != float64(status) || p["code"] != code || p["title"] != http.StatusText(status) || p["type"] != "about:blank" {
		t.Errorf("problem = %v, want status %d code %q", p, status, code)
	}
	if id, _ := p["request_id"].(string); id == "" {
		t.Error("problem has no request_id")
	}
	return p
}

var inviteLink = regexp.MustCompile(`http://frontend\.test/reset-password\?token=(\S+)`)

// invite creates a user through the API, sets their password from the
// emailed link, and returns their id and access token.
func (s *server) invite(adminToken, name, email, role string) (int64, string) {
	s.t.Helper()
	rec := s.do("POST", "/api/v1/users", map[string]string{"name": name, "email": email, "role": role}, adminToken)
	user := wantJSON(s.t, rec, http.StatusCreated)["user"].(map[string]any)

	msgs := s.mail.messages()
	m := inviteLink.FindStringSubmatch(msgs[len(msgs)-1].Text)
	if m == nil {
		s.t.Fatalf("no link in invite email: %q", msgs[len(msgs)-1].Text)
	}
	token, _ := url.QueryUnescape(m[1])
	wantStatus(s.t, s.reset(token, "invited-pass-1"), http.StatusOK)
	return int64(user["id"].(float64)), s.login(email, "invited-pass-1")
}

func TestInviteUser(t *testing.T) {
	s := newServer(t)
	ownerID, ownerToken := s.userToken()

	rec := s.do("POST", "/api/v1/users", map[string]string{"name": " Mona ", "email": "Mona@Example.com", "role": "manager"}, ownerToken)
	user := wantJSON(t, rec, http.StatusCreated)["user"].(map[string]any)
	if user["name"] != "Mona" || user["email"] != "mona@example.com" || user["role"] != "manager" || user["created_at"] == nil {
		t.Errorf("user = %v", user)
	}
	if _, leaked := user["password_hash"]; leaked {
		t.Error("password hash returned")
	}

	// No usable password until the invite link is used.
	wantText(t, s.do("POST", "/api/auth/login", map[string]string{"email": "mona@example.com", "password": "!"}, ""),
		http.StatusUnauthorized, "Invalid email or password")

	msg := s.mail.messages()[0]
	if msg.To != "mona@example.com" || !strings.Contains(msg.Text, "72 hours") {
		t.Errorf("invite email = %+v", msg)
	}
	token, _ := url.QueryUnescape(inviteLink.FindStringSubmatch(msg.Text)[1])
	wantStatus(t, s.reset(token, "mona-pass-1"), http.StatusOK)

	claims := decodeJWTClaims(t, s.login("mona@example.com", "mona-pass-1"))
	if claims["role"] != "manager" || claims["org_id"] != float64(s.orgOf(ownerID)) {
		t.Errorf("invited user's token claims = %v", claims)
	}
}

func TestInvitePermissions(t *testing.T) {
	s := newServer(t)
	_, ownerToken := s.userToken()
	_, adminToken := s.invite(ownerToken, "Adi", "adi@example.com", "admin")
	_, managerToken := s.invite(ownerToken, "Mo", "mo@example.com", "manager")
	_, repToken := s.invite(ownerToken, "Raj", "raj@example.com", "rep")
	_, viewerToken := s.invite(ownerToken, "Vee", "vee@example.com", "viewer")

	body := func(email, role string) map[string]string {
		return map[string]string{"name": "X", "email": email, "role": role}
	}
	wantStatus(t, s.do("POST", "/api/v1/users", body("by-admin@example.com", "rep"), adminToken), http.StatusCreated)
	wantProblem(t, s.do("POST", "/api/v1/users", body("owner2@example.com", "owner"), adminToken), http.StatusForbidden, "forbidden")
	wantStatus(t, s.do("POST", "/api/v1/users", body("owner2@example.com", "owner"), ownerToken), http.StatusCreated)
	for name, token := range map[string]string{"manager": managerToken, "rep": repToken, "viewer": viewerToken} {
		t.Run(name+" cannot invite", func(t *testing.T) {
			wantProblem(t, s.do("POST", "/api/v1/users", body(name+"-x@example.com", "rep"), token), http.StatusForbidden, "forbidden")
		})
	}
}

func TestInviteValidation(t *testing.T) {
	s := newServer(t)
	_, ownerToken := s.userToken()

	p := wantProblem(t, s.do("POST", "/api/v1/users", map[string]string{"name": "A", "email": "asha@example.com", "role": "rep"}, ownerToken),
		http.StatusConflict, "email_taken")
	if p["errors"].(map[string]any)["email"] == nil {
		t.Errorf("no field error: %v", p)
	}
	wantProblem(t, s.do("POST", "/api/v1/users", map[string]string{"name": "A", "email": "nope", "role": "rep"}, ownerToken),
		http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, s.do("POST", "/api/v1/users", map[string]string{"name": "A", "email": "a@example.com", "role": "superuser"}, ownerToken),
		http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, s.do("POST", "/api/v1/users", map[string]string{"email": "a@example.com", "role": "rep"}, ownerToken),
		http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, s.do("POST", "/api/v1/users", map[string]string{"name": "A", "email": "a@example.com", "role": "rep", "org_id": "2"}, ownerToken),
		http.StatusBadRequest, "invalid_body")
	wantProblem(t, s.do("POST", "/api/v1/users", "{", ownerToken), http.StatusBadRequest, "invalid_body")
	wantProblem(t, s.do("GET", "/api/v1/users", nil, ""), http.StatusUnauthorized, "unauthorized")
}

func TestListUsersIsOrgScoped(t *testing.T) {
	s := newServer(t)
	_, ownerToken := s.userToken()
	_, viewerToken := s.invite(ownerToken, "Vee", "vee@example.com", "viewer")
	s.register("Bea", "bea@example.com", "secret-pass-1") // another org

	for _, token := range []string{ownerToken, viewerToken} {
		users := wantJSON(t, s.do("GET", "/api/v1/users", nil, token), http.StatusOK)["users"].([]any)
		var emails []string
		for _, u := range users {
			emails = append(emails, u.(map[string]any)["email"].(string))
		}
		if strings.Join(emails, ",") != "asha@example.com,vee@example.com" {
			t.Errorf("users = %v", emails)
		}
	}
}

func TestChangeRole(t *testing.T) {
	s := newServer(t)
	ownerID, ownerToken := s.userToken()
	repID, _ := s.invite(ownerToken, "Raj", "raj@example.com", "rep")
	_, adminToken := s.invite(ownerToken, "Adi", "adi@example.com", "admin")
	outsider := s.register("Bea", "bea@example.com", "secret-pass-1")

	path := func(id int64) string { return "/api/v1/users/" + strconv.FormatInt(id, 10) }

	user := wantJSON(t, s.do("PATCH", path(repID), map[string]string{"role": "manager"}, adminToken), http.StatusOK)["user"].(map[string]any)
	if user["role"] != "manager" {
		t.Errorf("role = %v", user["role"])
	}

	// The new role reaches the user's token on the next refresh.
	_, c := s.loginSession("raj@example.com", "invited-pass-1")
	rec := s.postWithCookie("/api/auth/refresh", c)
	if role := decodeJWTClaims(t, wantJSON(t, rec, http.StatusOK)["token"].(string))["role"]; role != "manager" {
		t.Errorf("refreshed role = %v", role)
	}

	wantProblem(t, s.do("PATCH", path(ownerID), map[string]string{"role": "admin"}, adminToken), http.StatusForbidden, "forbidden")
	wantProblem(t, s.do("PATCH", path(repID), map[string]string{"role": "owner"}, adminToken), http.StatusForbidden, "forbidden")
	wantProblem(t, s.do("PATCH", path(ownerID), map[string]string{"role": "admin"}, ownerToken), http.StatusConflict, "last_owner")
	wantProblem(t, s.do("PATCH", path(outsider), map[string]string{"role": "rep"}, ownerToken), http.StatusNotFound, "not_found")
	wantProblem(t, s.do("PATCH", path(repID), map[string]string{"role": "boss"}, ownerToken), http.StatusUnprocessableEntity, "validation_failed")

	// With a second owner, the first may step down.
	wantStatus(t, s.do("PATCH", path(repID), map[string]string{"role": "owner"}, ownerToken), http.StatusOK)
	wantStatus(t, s.do("PATCH", path(ownerID), map[string]string{"role": "admin"}, ownerToken), http.StatusOK)
}
