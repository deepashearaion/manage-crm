package apitest

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// member adds a user with role to orgID directly in the database and
// returns its id and an access token.
func (s *server) member(orgID int64, role, email string) (int64, string) {
	s.t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("member-pass-1"), bcrypt.MinCost)
	if err != nil {
		s.t.Fatal(err)
	}
	var id int64
	if err := s.db.QueryRow(context.Background(),
		`INSERT INTO users (org_id, role, name, email, password_hash) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		orgID, role, role, email, string(hash),
	).Scan(&id); err != nil {
		s.t.Fatal(err)
	}
	return id, s.login(email, "member-pass-1")
}

func (s *server) orgOf(userID int64) int64 {
	s.t.Helper()
	var org int64
	if err := s.db.QueryRow(context.Background(), `SELECT org_id FROM users WHERE id = $1`, userID).Scan(&org); err != nil {
		s.t.Fatal(err)
	}
	return org
}

func contactPath(c map[string]any) string { return fmt.Sprintf("/api/contacts/%v", c["id"]) }

func listNames(t *testing.T, s *server, token string) []string {
	t.Helper()
	var names []string
	for _, c := range wantJSON(t, s.do("GET", "/api/contacts", nil, token), http.StatusOK)["contacts"].([]any) {
		names = append(names, c.(map[string]any)["first_name"].(string))
	}
	return names
}

func TestRegisterCreatesOrg(t *testing.T) {
	s := newServer(t)

	id := s.register("Asha", "asha@example.com", "secret-pass-1")
	rec := s.do("POST", "/api/auth/register", map[string]string{
		"name": "Ravi", "email": "ravi@example.com", "password": "secret-pass-1", "organization_name": "  Ravi Travels ",
	}, "")
	ravi := int64(wantJSON(t, rec, http.StatusCreated)["user_id"].(float64))

	ctx := context.Background()
	for userID, wantOrg := range map[int64]string{id: "Asha's organization", ravi: "Ravi Travels"} {
		var org, role string
		var stages int
		if err := s.db.QueryRow(ctx,
			`SELECT o.name, u.role, (SELECT count(*) FROM deal_stages ds WHERE ds.org_id = o.id)
			FROM users u JOIN orgs o ON o.id = u.org_id WHERE u.id = $1`, userID,
		).Scan(&org, &role, &stages); err != nil {
			t.Fatal(err)
		}
		if org != wantOrg || role != "owner" || stages != 6 {
			t.Errorf("user %d: org %q role %q stages %d, want %q owner 6", userID, org, role, stages, wantOrg)
		}
	}
	if s.orgOf(id) == s.orgOf(ravi) {
		t.Error("two sign-ups share an org")
	}

	claims := decodeJWTClaims(t, s.login("asha@example.com", "secret-pass-1"))
	if claims["org_id"] != float64(s.orgOf(id)) || claims["role"] != "owner" {
		t.Errorf("token claims = %v", claims)
	}
}

func TestOrgIsolation(t *testing.T) {
	s := newServer(t)
	_, tokenA := s.userToken()
	s.register("Bea", "bea@example.com", "secret-pass-1")
	tokenB := s.login("bea@example.com", "secret-pass-1")

	c := createContact(t, s, tokenA, map[string]any{"first_name": "Only in A"})

	if names := listNames(t, s, tokenB); len(names) != 0 {
		t.Errorf("org B lists %v", names)
	}
	wantText(t, s.do("GET", contactPath(c), nil, tokenB), http.StatusNotFound, "Contact not found")
	wantText(t, s.do("PATCH", contactPath(c), map[string]any{"notes": "x"}, tokenB), http.StatusNotFound, "Contact not found")
	wantText(t, s.do("DELETE", contactPath(c), nil, tokenB), http.StatusNotFound, "Contact not found")

	dash := wantJSON(t, s.do("GET", "/api/dashboard/sales", nil, tokenB), http.StatusOK)
	if dash["total_new_customers"] != float64(0) {
		t.Errorf("org B dashboard counts org A's contact: %v", dash)
	}
	// A's contact is untouched.
	if names := listNames(t, s, tokenA); len(names) != 1 {
		t.Errorf("org A lists %v", names)
	}
}

func TestOwnerMustBeInSameOrg(t *testing.T) {
	s := newServer(t)
	_, tokenA := s.userToken()
	outsider := s.register("Bea", "bea@example.com", "secret-pass-1")

	c := createContact(t, s, tokenA, map[string]any{"first_name": "Ravi"})
	wantText(t, s.do("PATCH", contactPath(c), map[string]any{"lead_owner_id": outsider}, tokenA),
		http.StatusBadRequest, "Invalid lead owner")
}

func TestRolePermissions(t *testing.T) {
	s := newServer(t)
	ownerID, ownerToken := s.userToken()
	org := s.orgOf(ownerID)
	repID, repToken := s.member(org, "rep", "rep@example.com")
	_, managerToken := s.member(org, "manager", "manager@example.com")
	_, viewerToken := s.member(org, "viewer", "viewer@example.com")

	ownersContact := createContact(t, s, ownerToken, map[string]any{"first_name": "Owner's"})
	repsContact := createContact(t, s, repToken, map[string]any{"first_name": "Rep's"})
	if repsContact["lead_owner_id"] != float64(repID) {
		t.Fatalf("rep's contact owner = %v", repsContact["lead_owner_id"])
	}

	t.Run("rep sees and changes only own contacts", func(t *testing.T) {
		if names := listNames(t, s, repToken); len(names) != 1 || names[0] != "Rep's" {
			t.Errorf("rep lists %v", names)
		}
		for _, method := range []string{"GET", "PATCH", "DELETE"} {
			wantText(t, s.do(method, contactPath(ownersContact), map[string]any{"notes": "x"}, repToken),
				http.StatusNotFound, "Contact not found")
		}
		wantStatus(t, s.do("PATCH", contactPath(repsContact), map[string]any{"notes": "mine"}, repToken), http.StatusOK)
		// One first win today each for the rep and the owner.
		s.exec(`INSERT INTO deals (org_id, title, contact_id, owner_id, stage_id, status, closed_at)
			SELECT $1, 'won', c.id, c.lead_owner_id, s.id, 'won', now()
			FROM contacts c JOIN deal_stages s ON s.org_id = $1 AND s.name = 'Closed Won' WHERE c.org_id = $1`, org)
		dash := wantJSON(t, s.do("GET", "/api/dashboard/sales", nil, repToken), http.StatusOK)
		if dash["total_new_customers"] != float64(1) {
			t.Errorf("rep dashboard new customers = %v, want 1 (own only)", dash["total_new_customers"])
		}
	})

	t.Run("rep cannot reassign", func(t *testing.T) {
		wantText(t, s.do("PATCH", contactPath(repsContact), map[string]any{"lead_owner_id": ownerID}, repToken),
			http.StatusForbidden, "You do not have permission to perform this action")
		// Re-sending the current owner, as a full-form save does, is allowed.
		wantStatus(t, s.do("PATCH", contactPath(repsContact), map[string]any{"lead_owner_id": repID, "notes": "same owner"}, repToken),
			http.StatusOK)
	})

	t.Run("viewer reads everything, changes nothing", func(t *testing.T) {
		if names := listNames(t, s, viewerToken); len(names) != 2 {
			t.Errorf("viewer lists %v", names)
		}
		const forbidden = "You do not have permission to perform this action"
		wantText(t, s.do("POST", "/api/contacts", map[string]any{"first_name": "X"}, viewerToken), http.StatusForbidden, forbidden)
		wantText(t, s.do("PATCH", contactPath(repsContact), map[string]any{"notes": "x"}, viewerToken), http.StatusForbidden, forbidden)
		wantText(t, s.do("DELETE", contactPath(repsContact), nil, viewerToken), http.StatusForbidden, forbidden)
	})

	t.Run("manager reassigns within the org", func(t *testing.T) {
		body := wantJSON(t, s.do("PATCH", contactPath(repsContact), map[string]any{"lead_owner_id": ownerID}, managerToken), http.StatusOK)
		if body["contact"].(map[string]any)["lead_owner_id"] != float64(ownerID) {
			t.Errorf("owner after reassign = %v", body["contact"])
		}
		if names := listNames(t, s, repToken); len(names) != 0 {
			t.Errorf("rep still lists %v after reassignment", names)
		}
	})

	t.Run("manager and owner delete any contact", func(t *testing.T) {
		wantStatus(t, s.do("DELETE", contactPath(repsContact), nil, managerToken), http.StatusOK)
		wantStatus(t, s.do("DELETE", contactPath(ownersContact), nil, ownerToken), http.StatusOK)
	})
}
