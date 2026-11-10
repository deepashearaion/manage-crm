package tasks

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

type Service struct {
	repo     *Repository
	contacts Contacts
	notifier Notifier
	logger   *slog.Logger
}

func NewService(repo *Repository, contacts Contacts, notifier Notifier, logger *slog.Logger) *Service {
	return &Service{repo: repo, contacts: contacts, notifier: notifier, logger: logger}
}

// scope is what p may see: their org, and only tasks assigned to them for reps.
func scope(p principal.Principal) Scope {
	s := Scope{OrgID: p.OrgID}
	if !p.SeesAllRecords() {
		s.AssigneeID = &p.UserID
	}
	return s
}

func fieldErr(field, problem, msg string) error { return &FieldError{field, problem, msg} }

// validate normalizes and checks t's editable fields.
func validate(t *Task) error {
	t.Title = strings.TrimSpace(t.Title)
	t.Description = strings.TrimSpace(t.Description)
	if t.Type == "" {
		t.Type = "todo"
	}
	if t.Priority == "" {
		t.Priority = "normal"
	}
	switch {
	case t.Title == "":
		return fieldErr("title", "required", "Title is required.")
	case utf8.RuneCountInString(t.Title) > 200:
		return fieldErr("title", "invalid", "Title must be at most 200 characters.")
	case utf8.RuneCountInString(t.Description) > 10000:
		return fieldErr("description", "invalid", "Description must be at most 10000 characters.")
	case !slices.Contains(types, t.Type):
		return fieldErr("type", "invalid", "Type must be one of: "+strings.Join(types, ", ")+".")
	case !slices.Contains(priorities, t.Priority):
		return fieldErr("priority", "invalid", "Priority must be low, normal, high or urgent.")
	case !slices.Contains(statuses, t.Status):
		return fieldErr("status", "invalid", "Status must be open, done or cancelled.")
	}
	return nil
}

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

func (s *Service) List(ctx context.Context, p principal.Principal, f Filter) ([]Task, error) {
	if f.Status != "" && !slices.Contains(statuses, f.Status) {
		return nil, fieldErr("status", "invalid", "Status must be open, done or cancelled.")
	}
	if f.Limit <= 0 {
		f.Limit = defaultPageSize
	}
	f.Limit = min(f.Limit, maxPageSize)
	return s.repo.List(ctx, scope(p), f)
}

func (s *Service) Get(ctx context.Context, p principal.Principal, id int64) (Task, error) {
	return s.repo.Get(ctx, scope(p), id)
}

// Create adds a task. Reps may only assign themselves and only attach
// contacts they can see.
func (s *Service) Create(ctx context.Context, p principal.Principal, in Input) (Task, error) {
	if !p.CanWrite() {
		return Task{}, ErrForbidden
	}
	t := Task{
		ContactID: in.ContactID, AssigneeID: p.UserID, CreatedByID: &p.UserID,
		Title: in.Title, Description: in.Description, Type: in.Type, Priority: in.Priority,
		Status: StatusOpen, Origin: OriginManual, DueAt: in.DueAt,
	}
	if in.AssigneeID != nil {
		t.AssigneeID = *in.AssigneeID
	}
	if t.AssigneeID != p.UserID && !p.CanReassign() {
		return Task{}, ErrForbidden
	}
	if err := validate(&t); err != nil {
		return Task{}, err
	}
	if t.ContactID != nil {
		if err := s.contacts.Visible(ctx, p, *t.ContactID); err != nil {
			return Task{}, err
		}
	}
	t, err := s.repo.Create(ctx, p.OrgID, t)
	if err != nil {
		return Task{}, err
	}
	s.afterCreate(ctx, p.OrgID, t, p.UserID)
	return t, nil
}

// CreateWorkflow adds a task the workflow decided on (no signed-in caller),
// or returns the contact's open task of the same origin (created false).
func (s *Service) CreateWorkflow(ctx context.Context, orgID int64, st SystemTask) (Task, bool, error) {
	t := Task{
		ContactID: &st.ContactID, AssigneeID: st.AssigneeID, Title: truncate(st.Title, 200),
		Description: st.Description, Type: st.Type, Priority: st.Priority,
		Status: StatusOpen, Origin: st.Origin, DueAt: st.DueAt,
	}
	if err := validate(&t); err != nil {
		return Task{}, false, err
	}
	t, created, err := s.repo.CreateWorkflow(ctx, orgID, t)
	if err != nil || !created {
		return t, false, err
	}
	s.afterCreate(ctx, orgID, t, 0)
	return t, true, nil
}

// afterCreate records the task on the timeline and tells the assignee when
// someone else assigned it. Failures are logged: the task itself exists.
func (s *Service) afterCreate(ctx context.Context, orgID int64, t Task, by int64) {
	if t.ContactID != nil {
		if err := s.contacts.TaskCreated(ctx, orgID, t); err != nil {
			s.logger.ErrorContext(ctx, "task timeline entry", slog.Int64("task_id", t.ID), slog.Any("error", err))
		}
	}
	if by != 0 && t.AssigneeID != by {
		if err := s.notifier.TaskAssigned(ctx, orgID, t, by); err != nil {
			s.logger.ErrorContext(ctx, "task assigned notification", slog.Int64("task_id", t.ID), slog.Any("error", err))
		}
	}
}

// Update changes a task p may change: reps only their own tasks, and only
// managers and above may reassign.
func (s *Service) Update(ctx context.Context, p principal.Principal, id int64, patch Patch) (Task, error) {
	cur, err := s.repo.Get(ctx, scope(p), id)
	if err != nil {
		return Task{}, err
	}
	if !p.CanWriteRecord(&cur.AssigneeID) {
		return Task{}, ErrForbidden
	}
	t := cur
	if patch.AssigneeID != nil && *patch.AssigneeID != cur.AssigneeID {
		if !p.CanReassign() {
			return Task{}, ErrForbidden
		}
		t.AssigneeID = *patch.AssigneeID
	}
	apply(&t.Title, patch.Title)
	apply(&t.Description, patch.Description)
	apply(&t.Type, patch.Type)
	apply(&t.Priority, patch.Priority)
	apply(&t.Status, patch.Status)
	if patch.DueAt != nil {
		t.DueAt = patch.DueAt
	}
	if patch.ClearDueAt {
		t.DueAt = nil
	}
	if err := validate(&t); err != nil {
		return Task{}, err
	}
	t, err = s.repo.Save(ctx, scope(p), t)
	if err != nil {
		return Task{}, err
	}
	if t.ContactID != nil && t.Status == StatusDone && cur.Status != StatusDone {
		if err := s.contacts.TaskCompleted(ctx, p.OrgID, t, p.UserID); err != nil && !errors.Is(err, ErrContactNotFound) {
			s.logger.ErrorContext(ctx, "task timeline entry", slog.Int64("task_id", t.ID), slog.Any("error", err))
		}
	}
	if t.AssigneeID != cur.AssigneeID && t.AssigneeID != p.UserID {
		if err := s.notifier.TaskAssigned(ctx, p.OrgID, t, p.UserID); err != nil {
			s.logger.ErrorContext(ctx, "task assigned notification", slog.Int64("task_id", t.ID), slog.Any("error", err))
		}
	}
	return t, nil
}

func (s *Service) Delete(ctx context.Context, p principal.Principal, id int64) error {
	t, err := s.repo.Get(ctx, scope(p), id)
	if err != nil {
		return err
	}
	if !p.CanWriteRecord(&t.AssigneeID) {
		return ErrForbidden
	}
	return s.repo.Delete(ctx, scope(p), id)
}

func apply[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
