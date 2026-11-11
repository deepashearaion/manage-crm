package apitest

import (
	"fmt"
	"net/http"
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

// orgOfToken is the org an access token belongs to.
func orgOfToken(t *testing.T, token string) int64 {
	t.Helper()
	return int64(decodeJWTClaims(t, token)["org_id"].(float64))
}

// invite creates a user with the named role (e.g. "rep") through the API
// and returns their id and access token.
func (s *server) invite(adminToken, name, email, role string) (int64, string) {
	s.t.Helper()
	body := map[string]any{"name": name, "email": email, "password": "invited-pass-1",
		"role_id": s.roleID(orgOfToken(s.t, adminToken), role)}
	user := wantJSON(s.t, s.do("POST", "/api/v1/users", body, adminToken), http.StatusCreated)["user"].(map[string]any)
	return int64(user["id"].(float64)), s.login(email, "invited-pass-1")
}

func userPath(id any) string { return fmt.Sprintf("/api/v1/users/%v", id) }
func rolePath(id any) string { return fmt.Sprintf("/api/v1/roles/%v", id) }

func TestCreateUser(t *testing.T) {
	s := newServer(t)
	ownerID, ownerToken := s.userToken()
	manager := s.roleID(s.orgOf(ownerID), "manager")

	rec := s.do("POST", "/api/v1/users", map[string]any{"name": " Mona ", "email": "Mona@Example.com",
		"password": "mona-pass-1", "role_id": manager}, ownerToken)
	user := wantJSON(t, rec, http.StatusCreated)["user"].(map[string]any)
	if user["name"] != "Mona" || user["email"] != "mona@example.com" || user["role_id"] != float64(manager) ||
		user["role_name"] != "Manager" || user["created_at"] == nil {
		t.Errorf("user = %v", user)
	}
	if _, leaked := user["password_hash"]; leaked {
		t.Error("password hash returned")
	}
	if len(s.mail.messages()) != 0 {
		t.Error("creating a user sent an email")
	}

	// The user signs in with the password the owner chose.
	monaToken := s.login("mona@example.com", "mona-pass-1")
	if claims := decodeJWTClaims(t, monaToken); claims["org_id"] != float64(s.orgOf(ownerID)) {
		t.Errorf("token claims = %v", claims)
	}
	got := wantJSON(t, s.do("GET", userPath(user["id"]), nil, ownerToken), http.StatusOK)["user"].(map[string]any)
	if got["email"] != "mona@example.com" {
		t.Errorf("get user = %v", got)
	}
}

func TestCreateUserValidation(t *testing.T) {
	s := newServer(t)
	ownerID, ownerToken := s.userToken()
	rep := s.roleID(s.orgOf(ownerID), "rep")
	outsider := s.register("Bea", "bea@example.com", "secret-pass-1")

	body := func(changes map[string]any) map[string]any {
		b := map[string]any{"name": "A", "email": "a@example.com", "password": "a-password", "role_id": rep}
		for k, v := range changes {
			if v == nil {
				delete(b, k)
			} else {
				b[k] = v
			}
		}
		return b
	}
	for name, c := range map[string]struct {
		changes map[string]any
		field   string
	}{
		"bad email":      {map[string]any{"email": "nope"}, "email"},
		"short password": {map[string]any{"password": "short"}, "password"},
		"no password":    {map[string]any{"password": nil}, "password"},
		"no name":        {map[string]any{"name": nil}, "name"},
		"no role":        {map[string]any{"role_id": nil}, "role_id"},
		"unknown role":   {map[string]any{"role_id": 999999}, "role_id"},
		"other org role": {map[string]any{"role_id": s.roleID(s.orgOf(outsider), "rep")}, "role_id"},
	} {
		t.Run(name, func(t *testing.T) {
			p := wantProblem(t, s.do("POST", "/api/v1/users", body(c.changes), ownerToken), http.StatusUnprocessableEntity, "validation_failed")
			if p["errors"].(map[string]any)[c.field] == nil {
				t.Errorf("errors = %v, want %s", p["errors"], c.field)
			}
		})
	}
	wantProblem(t, s.do("POST", "/api/v1/users", body(map[string]any{"email": "asha@example.com"}), ownerToken),
		http.StatusConflict, "email_taken")
	wantProblem(t, s.do("POST", "/api/v1/users", body(map[string]any{"org_id": 2}), ownerToken), http.StatusBadRequest, "invalid_body")
	wantProblem(t, s.do("GET", "/api/v1/users", nil, ""), http.StatusUnauthorized, "unauthorized")
}

func TestCreateUserPermissions(t *testing.T) {
	s := newServer(t)
	ownerID, ownerToken := s.userToken()
	org := s.orgOf(ownerID)
	_, adminToken := s.invite(ownerToken, "Adi", "adi@example.com", "admin")
	_, managerToken := s.invite(ownerToken, "Mo", "mo@example.com", "manager")
	_, repToken := s.invite(ownerToken, "Raj", "raj@example.com", "rep")
	_, viewerToken := s.invite(ownerToken, "Vee", "vee@example.com", "viewer")

	body := func(email, role string) map[string]any {
		return map[string]any{"name": "X", "email": email, "password": "x-password", "role_id": s.roleID(org, role)}
	}
	wantStatus(t, s.do("POST", "/api/v1/users", body("by-admin@example.com", "rep"), adminToken), http.StatusCreated)
	wantProblem(t, s.do("POST", "/api/v1/users", body("owner2@example.com", "owner"), adminToken), http.StatusForbidden, "forbidden")
	wantStatus(t, s.do("POST", "/api/v1/users", body("owner2@example.com", "owner"), ownerToken), http.StatusCreated)
	for name, token := range map[string]string{"manager": managerToken, "rep": repToken, "viewer": viewerToken} {
		t.Run(name+" cannot create users", func(t *testing.T) {
			wantProblem(t, s.do("POST", "/api/v1/users", body(name+"-x@example.com", "rep"), token), http.StatusForbidden, "forbidden")
		})
	}
}

func TestListUsersIsOrgScoped(t *testing.T) {
	s := newServer(t)
	_, ownerToken := s.userToken()
	_, viewerToken := s.invite(ownerToken, "Vee", "vee@example.com", "viewer")
	s.register("Bea", "bea@example.com", "secret-pass-1") // another org

	for _, token := range []string{ownerToken, viewerToken} {
		users := wantJSON(t, s.do("GET", "/api/v1/users", nil, token), http.StatusOK)["users"].([]any)
		var got []string
		for _, u := range users {
			m := u.(map[string]any)
			got = append(got, fmt.Sprintf("%s:%s", m["email"], m["role_name"]))
		}
		if strings.Join(got, ",") != "asha@example.com:Owner,vee@example.com:Viewer" {
			t.Errorf("users = %v", got)
		}
	}
}

func TestAssignRole(t *testing.T) {
	s := newServer(t)
	ownerID, ownerToken := s.userToken()
	org := s.orgOf(ownerID)
	repID, repToken := s.invite(ownerToken, "Raj", "raj@example.com", "rep")
	adminID, adminToken := s.invite(ownerToken, "Adi", "adi@example.com", "admin")
	outsider := s.register("Bea", "bea@example.com", "secret-pass-1")
	role := func(name string) map[string]any { return map[string]any{"role_id": s.roleID(org, name)} }
	assign := func(id any, name, token string) *httpResult {
		return s.do("PUT", userPath(id)+"/role", role(name), token)
	}

	// A rep sees only their own contacts until promoted; the new role applies
	// on the very next request, with the same token.
	createContact(t, s, ownerToken, map[string]any{"first_name": "Owned by Asha"})
	if got := len(s.v1Contacts(repToken, "")); got != 0 {
		t.Fatalf("rep sees %d contacts", got)
	}
	user := wantJSON(t, assign(repID, "manager", adminToken), http.StatusOK)["user"].(map[string]any)
	if user["role_name"] != "Manager" {
		t.Errorf("user = %v", user)
	}
	if got := len(s.v1Contacts(repToken, "")); got != 1 {
		t.Errorf("promoted rep sees %d contacts, want 1", got)
	}

	wantProblem(t, assign(ownerID, "admin", adminToken), http.StatusForbidden, "forbidden")
	wantProblem(t, assign(repID, "owner", adminToken), http.StatusForbidden, "forbidden")
	wantProblem(t, assign(adminID, "rep", adminToken), http.StatusConflict, "own_role")
	wantProblem(t, assign(ownerID, "admin", ownerToken), http.StatusConflict, "last_owner")
	wantProblem(t, assign(outsider, "rep", ownerToken), http.StatusNotFound, "not_found")
	wantProblem(t, s.do("PUT", userPath(repID)+"/role", map[string]any{"role_id": 999999}, ownerToken),
		http.StatusUnprocessableEntity, "validation_failed")
	wantProblem(t, assign(repID, "viewer", repToken), http.StatusForbidden, "forbidden") // now a manager: no users.edit

	// With a second owner, the first may step down.
	wantStatus(t, assign(repID, "owner", ownerToken), http.StatusOK)
	wantStatus(t, assign(ownerID, "admin", ownerToken), http.StatusOK)

	// Renaming.
	got := wantJSON(t, s.do("PATCH", userPath(adminID), map[string]any{"name": " Adi K "}, repToken), http.StatusOK)["user"].(map[string]any)
	if got["name"] != "Adi K" {
		t.Errorf("renamed = %v", got)
	}
}

func TestSetUserPassword(t *testing.T) {
	s := newServer(t)
	_, ownerToken := s.userToken()
	repID, _ := s.invite(ownerToken, "Raj", "raj@example.com", "rep")
	_, adminToken := s.invite(ownerToken, "Adi", "adi@example.com", "admin")
	_, cookie := s.loginSession("raj@example.com", "invited-pass-1")

	wantProblem(t, s.do("PUT", userPath(repID)+"/password", map[string]any{"password": "short"}, adminToken),
		http.StatusUnprocessableEntity, "validation_failed")
	wantStatus(t, s.do("PUT", userPath(repID)+"/password", map[string]any{"password": "new-raj-pass"}, adminToken), http.StatusNoContent)

	wantText(t, s.do("POST", "/api/auth/login", map[string]string{"email": "raj@example.com", "password": "invited-pass-1"}, ""),
		http.StatusUnauthorized, "Invalid email or password")
	s.login("raj@example.com", "new-raj-pass")
	// The old session is over.
	wantStatus(t, s.postWithCookie("/api/auth/refresh", cookie), http.StatusUnauthorized)

	ownerID := s.idOf("asha@example.com")
	wantProblem(t, s.do("PUT", userPath(ownerID)+"/password", map[string]any{"password": "hijacked-1"}, adminToken),
		http.StatusForbidden, "forbidden")
}

func TestMeAndCatalog(t *testing.T) {
	s := newServer(t)
	_, ownerToken := s.userToken()
	_, repToken := s.invite(ownerToken, "Raj", "raj@example.com", "rep")

	me := wantJSON(t, s.do("GET", "/api/v1/me", nil, repToken), http.StatusOK)
	perms := me["permissions"].(map[string]any)
	if me["user"].(map[string]any)["email"] != "raj@example.com" || me["role"].(map[string]any)["name"] != "Rep" ||
		fmt.Sprint(perms["deals"]) != "map[create:own delete:own edit:own view:own]" ||
		fmt.Sprint(perms["settings"]) != "map[edit:none view:none]" {
		t.Errorf("me = %v", me)
	}
	me = wantJSON(t, s.do("GET", "/api/v1/me", nil, ownerToken), http.StatusOK)
	if me["role"].(map[string]any)["locked"] != true || fmt.Sprint(me["permissions"].(map[string]any)["roles"]) != "map[create:all delete:all edit:all view:all]" {
		t.Errorf("owner me = %v", me)
	}

	modules := wantJSON(t, s.do("GET", "/api/v1/permissions", nil, repToken), http.StatusOK)["modules"].([]any)
	var names []string
	for _, m := range modules {
		names = append(names, m.(map[string]any)["module"].(string))
	}
	if strings.Join(names, ",") != "contacts,deals,tasks,nurture,invoices,reports,settings,users,roles" {
		t.Errorf("modules = %v", names)
	}
}

func TestRoles(t *testing.T) {
	s := newServer(t)
	_, ownerToken := s.userToken()

	roles := wantJSON(t, s.do("GET", "/api/v1/roles", nil, ownerToken), http.StatusOK)["roles"].([]any)
	var got []string
	for _, r := range roles {
		m := r.(map[string]any)
		got = append(got, fmt.Sprintf("%s:%v:%v", m["name"], m["locked"], m["user_count"]))
	}
	if strings.Join(got, " ") != "Owner:true:1 Admin:false:0 Manager:false:0 Rep:false:0 Viewer:false:0" {
		t.Errorf("default roles = %v", got)
	}
	owner := roles[0].(map[string]any)

	// Create: modules left out get no access.
	rec := s.do("POST", "/api/v1/roles", map[string]any{"name": " Support ", "description": "Answers customers",
		"permissions": map[string]any{"contacts": map[string]any{"view": "all", "edit": "own"}, "tasks": map[string]any{"view": "own", "create": "own", "edit": "own"}}}, ownerToken)
	role := wantJSON(t, rec, http.StatusCreated)["role"].(map[string]any)
	perms := role["permissions"].(map[string]any)
	if role["name"] != "Support" || role["locked"] != false || fmt.Sprint(perms["contacts"]) != "map[create:none delete:none edit:own view:all]" ||
		fmt.Sprint(perms["invoices"]) != "map[create:none delete:none edit:none view:none]" {
		t.Errorf("role = %v", role)
	}

	// Update replaces the permission set.
	rec = s.do("PATCH", rolePath(role["id"]), map[string]any{"permissions": map[string]any{"deals": map[string]any{"view": "all"}}}, ownerToken)
	perms = wantJSON(t, rec, http.StatusOK)["role"].(map[string]any)["permissions"].(map[string]any)
	if fmt.Sprint(perms["deals"]) != "map[create:none delete:none edit:none view:all]" || fmt.Sprint(perms["contacts"]) != "map[create:none delete:none edit:none view:none]" {
		t.Errorf("updated permissions = %v", perms)
	}
	if r := wantJSON(t, s.do("GET", rolePath(role["id"]), nil, ownerToken), http.StatusOK)["role"].(map[string]any); r["name"] != "Support" {
		t.Errorf("get role = %v", r)
	}

	invalid := map[string]map[string]any{
		"name":                      {"name": " "},
		"permissions.billing.view":  {"name": "X", "permissions": map[string]any{"billing": map[string]any{"view": "all"}}},
		"permissions.deals.view":    {"name": "X", "permissions": map[string]any{"deals": map[string]any{"view": "everything"}}},
		"permissions.settings.view": {"name": "X", "permissions": map[string]any{"settings": map[string]any{"view": "own"}}},
		"permissions.deals.edit":    {"name": "X", "permissions": map[string]any{"deals": map[string]any{"view": "own", "edit": "all"}}},
	}
	for field, body := range invalid {
		p := wantProblem(t, s.do("POST", "/api/v1/roles", body, ownerToken), http.StatusUnprocessableEntity, "validation_failed")
		if p["errors"].(map[string]any)[field] == nil {
			t.Errorf("%s: errors = %v", field, p["errors"])
		}
	}
	wantProblem(t, s.do("POST", "/api/v1/roles", map[string]any{"name": "rep"}, ownerToken), http.StatusConflict, "name_taken")
	wantProblem(t, s.do("PATCH", rolePath(owner["id"]), map[string]any{"name": "Boss"}, ownerToken), http.StatusConflict, "role_locked")
	wantProblem(t, s.do("DELETE", rolePath(owner["id"]), nil, ownerToken), http.StatusConflict, "role_locked")

	// A role in use cannot be deleted.
	s.invite(ownerToken, "Sam", "sam@example.com", "support")
	wantProblem(t, s.do("DELETE", rolePath(role["id"]), nil, ownerToken), http.StatusConflict, "role_in_use")
	wantStatus(t, s.do("PUT", userPath(s.idOf("sam@example.com"))+"/role", map[string]any{"role_id": s.roleID(s.orgOf(s.idOf("sam@example.com")), "rep")}, ownerToken), http.StatusOK)
	wantStatus(t, s.do("DELETE", rolePath(role["id"]), nil, ownerToken), http.StatusNoContent)
	wantProblem(t, s.do("GET", rolePath(role["id"]), nil, ownerToken), http.StatusNotFound, "not_found")

	// Other orgs' roles are invisible.
	s.register("Bea", "bea@example.com", "secret-pass-1")
	beaToken := s.login("bea@example.com", "secret-pass-1")
	wantProblem(t, s.do("GET", rolePath(owner["id"]), nil, beaToken), http.StatusNotFound, "not_found")
}

func TestRolePermissionsApply(t *testing.T) {
	s := newServer(t)
	_, ownerToken := s.userToken()

	// "Deals desk" works every deal but has no access to contacts.
	rec := s.do("POST", "/api/v1/roles", map[string]any{"name": "Deals desk", "permissions": map[string]any{
		"deals": map[string]any{"view": "all", "create": "all", "edit": "all", "delete": "none"}}}, ownerToken)
	roleID := wantJSON(t, rec, http.StatusCreated)["role"].(map[string]any)["id"]
	_, deskToken := s.invite(ownerToken, "Dee", "dee@example.com", "deals desk")

	deal := s.createDeal(ownerToken, map[string]any{"title": "Owner's deal"})
	wantStatus(t, s.do("GET", "/api/v1/deals", nil, deskToken), http.StatusOK)
	wantStatus(t, s.do("PATCH", fmt.Sprintf("/api/v1/deals/%v", deal["id"]), map[string]any{"title": "Renamed"}, deskToken), http.StatusOK)
	wantProblem(t, s.do("DELETE", fmt.Sprintf("/api/v1/deals/%v", deal["id"]), nil, deskToken), http.StatusForbidden, "forbidden")
	wantProblem(t, s.do("GET", "/api/v1/contacts", nil, deskToken), http.StatusForbidden, "forbidden")
	wantText(t, s.do("GET", "/api/contacts", nil, deskToken), http.StatusForbidden, "You do not have permission to perform this action")
	wantProblem(t, s.do("GET", "/api/v1/tasks", nil, deskToken), http.StatusForbidden, "forbidden")
	wantProblem(t, s.do("GET", "/api/v1/reports/dashboard", nil, deskToken), http.StatusForbidden, "forbidden")
	wantProblem(t, s.do("GET", "/api/v1/users", nil, deskToken), http.StatusForbidden, "forbidden")

	// Taking deals away applies to the next request with the same token.
	wantStatus(t, s.do("PATCH", rolePath(roleID), map[string]any{"permissions": map[string]any{}}, ownerToken), http.StatusOK)
	wantProblem(t, s.do("GET", "/api/v1/deals", nil, deskToken), http.StatusForbidden, "forbidden")
}

func TestRoleEscalation(t *testing.T) {
	s := newServer(t)
	ownerID, ownerToken := s.userToken()
	org := s.orgOf(ownerID)

	// A team lead may manage users and roles but sees only their own deals.
	rec := s.do("POST", "/api/v1/roles", map[string]any{"name": "Team lead", "permissions": map[string]any{
		"deals": map[string]any{"view": "own", "create": "own", "edit": "own"},
		"users": map[string]any{"view": "all", "create": "all", "edit": "all"},
		"roles": map[string]any{"view": "all", "create": "all", "edit": "all", "delete": "all"}}}, ownerToken)
	wantStatus(t, rec, http.StatusCreated)
	_, leadToken := s.invite(ownerToken, "Lee", "lee@example.com", "team lead")
	repID, _ := s.invite(ownerToken, "Raj", "raj@example.com", "rep")

	// Within their own access: allowed.
	wantStatus(t, s.do("POST", "/api/v1/roles", map[string]any{"name": "Junior", "permissions": map[string]any{
		"deals": map[string]any{"view": "own"}}}, leadToken), http.StatusCreated)
	// Beyond it: refused, for new roles, changes and assignments alike.
	wantProblem(t, s.do("POST", "/api/v1/roles", map[string]any{"name": "Senior", "permissions": map[string]any{
		"deals": map[string]any{"view": "all"}}}, leadToken), http.StatusForbidden, "exceeds_own_access")
	wantProblem(t, s.do("PATCH", rolePath(s.roleID(org, "team lead")), map[string]any{"permissions": map[string]any{
		"invoices": map[string]any{"view": "all"}}}, leadToken), http.StatusForbidden, "exceeds_own_access")
	wantProblem(t, s.do("PATCH", rolePath(s.roleID(org, "admin")), map[string]any{"name": "Admins"}, leadToken),
		http.StatusForbidden, "exceeds_own_access")
	wantProblem(t, s.do("PUT", userPath(repID)+"/role", map[string]any{"role_id": s.roleID(org, "admin")}, leadToken),
		http.StatusForbidden, "exceeds_own_access")
	wantProblem(t, s.do("POST", "/api/v1/users", map[string]any{"name": "X", "email": "x@example.com", "password": "x-password",
		"role_id": s.roleID(org, "manager")}, leadToken), http.StatusForbidden, "exceeds_own_access")
	// The rep's current role (Rep) reaches beyond the lead's, so the lead
	// cannot move them either.
	wantProblem(t, s.do("PUT", userPath(repID)+"/role", map[string]any{"role_id": s.roleID(org, "junior")}, leadToken),
		http.StatusForbidden, "exceeds_own_access")
}
