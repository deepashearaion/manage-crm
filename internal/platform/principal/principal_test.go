package principal

import "testing"

func TestPermissions(t *testing.T) {
	own, other := int64(1), int64(2)
	type perms struct{ users, all, write, reassign, settings, ownRecord, otherRecord bool }
	for role, want := range map[Role]perms{
		RoleOwner:   {true, true, true, true, true, true, true},
		RoleAdmin:   {true, true, true, true, true, true, true},
		RoleManager: {false, true, true, true, true, true, true},
		RoleRep:     {false, false, true, false, false, true, false},
		RoleViewer:  {false, true, false, false, false, false, false},
	} {
		p := Principal{UserID: own, OrgID: 9, Role: role}
		got := perms{p.ManagesUsers(), p.SeesAllRecords(), p.CanWrite(), p.CanReassign(), p.ManagesSettings(), p.CanWriteRecord(&own), p.CanWriteRecord(&other)}
		if got != want {
			t.Errorf("%s: got %+v, want %+v", role, got, want)
		}
	}
	if (Principal{UserID: own, Role: RoleRep}).CanWriteRecord(nil) {
		t.Error("rep may write an unowned record")
	}
	if Role("superuser").Valid() || !RoleViewer.Valid() {
		t.Error("Role.Valid")
	}
}
