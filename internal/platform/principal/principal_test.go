package principal

import (
	"errors"
	"testing"
)

func TestScopes(t *testing.T) {
	own, other := int64(1), int64(2)
	rep := Principal{UserID: own, OrgID: 9, Perms: Permissions{
		Deals: {View: Own, Create: Own, Edit: Own},
		Tasks: {View: All, Edit: All},
	}}
	cases := []struct {
		name string
		got  bool
		want bool
	}{
		{"own deal edit", rep.CanOn(Deals, Edit, &own), true},
		{"other deal edit", rep.CanOn(Deals, Edit, &other), false},
		{"unowned deal edit", rep.CanOn(Deals, Edit, nil), false},
		{"deal delete", rep.CanOn(Deals, Delete, &own), false},
		{"sees all deals", rep.SeesAll(Deals), false},
		{"sees all tasks", rep.SeesAll(Tasks), true},
		{"reassign tasks", rep.CanReassign(Tasks), true},
		{"reassign deals", rep.CanReassign(Deals), false},
		{"unknown module", rep.Can(Invoices, View), false},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}

	owner := Principal{UserID: own, Owner: true}
	if !owner.CanOn(Roles, Delete, &other) || !owner.SeesAll(Reports) {
		t.Error("owner lacks access")
	}
	if eff := owner.Effective(); eff.Scope(Users, Create) != All {
		t.Errorf("owner effective = %v", eff)
	}
	if eff := rep.Effective(); eff.Scope(Invoices, View) != None || len(eff) != len(Catalog) {
		t.Errorf("rep effective = %v", eff)
	}
}

func TestValidate(t *testing.T) {
	ok := []Permissions{
		{},
		FullAccess(),
		{Deals: {View: Own, Edit: Own}, Settings: {View: All, Edit: All}},
		{Deals: {Create: All}}, // may create for others without seeing deals
	}
	for _, ps := range ok {
		if err := ps.Validate(); err != nil {
			t.Errorf("%v: %v", ps, err)
		}
	}
	bad := map[string]Permissions{
		"permissions.billing.view":  {"billing": {View: All}},
		"permissions.deals.approve": {Deals: {"approve": All}},
		"permissions.settings.view": {Settings: {View: Own}}, // org-wide only
		"permissions.deals.view":    {Deals: {View: "some"}},
		"permissions.deals.edit":    {Deals: {View: Own, Edit: All}},
		"permissions.tasks.delete":  {Tasks: {Delete: Own}},
		"permissions.reports.edit":  {Reports: {Edit: All}},
	}
	for field, ps := range bad {
		err := ps.Validate()
		var pe *PermissionError
		if !errors.As(err, &pe) || pe.Field != field {
			t.Errorf("%v: error %v, want field %s", ps, err, field)
		}
	}
}

func TestWithin(t *testing.T) {
	limit := Permissions{Deals: {View: All, Edit: Own}}
	if ok, _ := (Permissions{Deals: {View: Own, Edit: Own}}).Within(limit); !ok {
		t.Error("narrower set rejected")
	}
	if ok, what := (Permissions{Deals: {Edit: All}}).Within(limit); ok || what != "deals.edit" {
		t.Errorf("wider set: %v %s", ok, what)
	}
	if ok, what := (Permissions{Users: {View: All}}).Within(limit); ok || what != "users.view" {
		t.Errorf("other module: %v %s", ok, what)
	}
}
