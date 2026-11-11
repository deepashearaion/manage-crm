package principal

import (
	"fmt"
	"slices"
)

// Module is an area of the CRM a role grants access to.
type Module string

const (
	Contacts Module = "contacts" // contacts, leads, timelines
	Deals    Module = "deals"    // deals, pipeline, forecast
	Tasks    Module = "tasks"
	Nurture  Module = "nurture"  // sequences and enrollments
	Invoices Module = "invoices" // GST invoices
	Reports  Module = "reports"
	Settings Module = "settings" // lead routing and scoring, deal stages, billing profile, report settings
	Users    Module = "users"
	Roles    Module = "roles"
)

// Action is what a permission allows.
type Action string

const (
	View   Action = "view"
	Create Action = "create"
	Edit   Action = "edit"
	Delete Action = "delete"
)

// Scope is how far a permission reaches: nothing, the records the user
// owns (or is assigned), or every record in the org.
type Scope string

const (
	None Scope = "none"
	Own  Scope = "own"
	All  Scope = "all"
)

func (s Scope) rank() int {
	switch s {
	case Own:
		return 1
	case All:
		return 2
	}
	return 0
}

// ActionSpec is one action a module offers and the scopes it accepts.
type ActionSpec struct {
	Action      Action  `json:"action"`
	Scopes      []Scope `json:"scopes"`
	Description string  `json:"description"`
}

// ModuleSpec describes a module's permissions for the role editor.
type ModuleSpec struct {
	Module  Module       `json:"module"`
	Label   string       `json:"label"`
	Actions []ActionSpec `json:"actions"`
}

var (
	scoped = []Scope{None, Own, All}
	orgAll = []Scope{None, All}
)

// Catalog lists every module and action a role can grant, in the order a
// role editor shows them. "own" means records the user owns (contacts,
// deals, invoices) or is assigned (tasks).
var Catalog = []ModuleSpec{
	{Contacts, "Contacts and leads", []ActionSpec{
		{View, scoped, "See contacts, leads and their timelines."},
		{Create, scoped, "Add contacts and leads. All: may also make someone else the owner."},
		{Edit, scoped, "Change contacts, log activities, set lifecycle stages. All: may also reassign owners."},
		{Delete, scoped, "Delete contacts."},
	}},
	{Deals, "Deals", []ActionSpec{
		{View, scoped, "See deals, their history and the forecast."},
		{Create, scoped, "Add deals. All: may also make someone else the owner."},
		{Edit, scoped, "Change deals and move them between stages. All: may also reassign owners."},
		{Delete, scoped, "Delete deals."},
	}},
	{Tasks, "Tasks", []ActionSpec{
		{View, scoped, "See tasks."},
		{Create, scoped, "Add tasks. All: may also assign them to others."},
		{Edit, scoped, "Change and complete tasks. All: may also reassign them."},
		{Delete, scoped, "Delete tasks."},
	}},
	{Nurture, "Nurture sequences", []ActionSpec{
		{View, scoped, "See sequences and enrollments."},
		{Create, scoped, "Enroll contacts in sequences. All: may also create sequences."},
		{Edit, scoped, "Stop enrollments. All: may also change sequences."},
		{Delete, orgAll, "Delete sequences."},
	}},
	{Invoices, "Invoices", []ActionSpec{
		{View, scoped, "See invoices and download their PDFs."},
		{Create, scoped, "Create draft invoices."},
		{Edit, scoped, "Change drafts, issue them and email them."},
		{Delete, scoped, "Delete drafts. All: may also cancel issued invoices."},
	}},
	{Reports, "Reports", []ActionSpec{
		{View, scoped, "See dashboards and reports. Own: only the user's own figures. All: the whole org, including the rep comparison."},
	}},
	{Settings, "Settings", []ActionSpec{
		{View, orgAll, "See lead settings, sources, WhatsApp numbers, scoring and routing rules."},
		{Edit, orgAll, "Change lead settings and rules, deal stages, the billing profile and report settings."},
	}},
	{Users, "Users", []ActionSpec{
		{View, orgAll, "See the org's users."},
		{Create, orgAll, "Create users."},
		{Edit, orgAll, "Rename users, reset their passwords and assign their roles."},
	}},
	{Roles, "Roles", []ActionSpec{
		{View, orgAll, "See roles and their permissions."},
		{Create, orgAll, "Create roles."},
		{Edit, orgAll, "Change roles' names and permissions."},
		{Delete, orgAll, "Delete roles nobody has."},
	}},
}

func spec(m Module, a Action) (ActionSpec, bool) {
	for _, ms := range Catalog {
		if ms.Module != m {
			continue
		}
		for _, as := range ms.Actions {
			if as.Action == a {
				return as, true
			}
		}
	}
	return ActionSpec{}, false
}

// Permissions maps module → action → scope. Missing entries are None.
type Permissions map[Module]map[Action]Scope

// Scope is the scope granted for action on module.
func (ps Permissions) Scope(m Module, a Action) Scope {
	if s, ok := ps[m][a]; ok {
		return s
	}
	return None
}

// Normalized returns a copy holding every catalog entry (None where
// missing) and nothing else.
func (ps Permissions) Normalized() Permissions {
	out := Permissions{}
	for _, ms := range Catalog {
		out[ms.Module] = map[Action]Scope{}
		for _, as := range ms.Actions {
			out[ms.Module][as.Action] = ps.Scope(ms.Module, as.Action)
		}
	}
	return out
}

// FullAccess grants every action on every module, org-wide.
func FullAccess() Permissions {
	out := Permissions{}
	for _, ms := range Catalog {
		out[ms.Module] = map[Action]Scope{}
		for _, as := range ms.Actions {
			out[ms.Module][as.Action] = All
		}
	}
	return out
}

// PermissionError names the first invalid entry of a permission set.
type PermissionError struct {
	Field   string // e.g. "permissions.deals.edit"
	Message string
}

func (e *PermissionError) Error() string { return e.Message }

// Validate checks that ps names only catalog modules and actions with
// scopes they accept, and that edit and delete never reach further than
// view (nobody can change what they cannot see).
func (ps Permissions) Validate() error {
	for m, actions := range ps {
		for a, s := range actions {
			field := fmt.Sprintf("permissions.%s.%s", m, a)
			as, ok := spec(m, a)
			if !ok {
				return &PermissionError{field, fmt.Sprintf("Unknown permission %s.%s.", m, a)}
			}
			if !slices.Contains(as.Scopes, s) {
				return &PermissionError{field, fmt.Sprintf("%s.%s must be one of %v.", m, a, as.Scopes)}
			}
		}
		view := ps.Scope(m, View)
		for _, a := range []Action{Edit, Delete} {
			if _, ok := spec(m, View); ok && ps.Scope(m, a).rank() > view.rank() {
				return &PermissionError{fmt.Sprintf("permissions.%s.%s", m, a),
					fmt.Sprintf("%s.%s cannot reach further than %s.view.", m, a, m)}
			}
		}
	}
	return nil
}

// Within reports whether ps grants nothing beyond limit, and if not, the
// first permission that exceeds it.
func (ps Permissions) Within(limit Permissions) (bool, string) {
	for _, ms := range Catalog {
		for _, as := range ms.Actions {
			if ps.Scope(ms.Module, as.Action).rank() > limit.Scope(ms.Module, as.Action).rank() {
				return false, fmt.Sprintf("%s.%s", ms.Module, as.Action)
			}
		}
	}
	return true, ""
}
