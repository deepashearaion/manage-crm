// Package tasks manages to-dos for users, usually about a contact: follow
// up a hot lead, call back, send a quote. Users create them by hand; the
// workflow creates them too (e.g. a follow-up when a lead turns hot), at
// most one open task per contact and workflow origin.
package tasks

import (
	"context"
	"errors"
	"time"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

const (
	StatusOpen      = "open"
	StatusDone      = "done"
	StatusCancelled = "cancelled"

	OriginManual   = "manual"
	OriginHotLead  = "hot_lead"
	OriginDealLost = "deal_lost"
)

var (
	types      = []string{"follow_up", "call", "email", "whatsapp", "meeting", "todo"}
	priorities = []string{"low", "normal", "high", "urgent"}
	statuses   = []string{StatusOpen, StatusDone, StatusCancelled}
)

type Task struct {
	ID          int64      `json:"id"`
	ContactID   *int64     `json:"contact_id"`
	AssigneeID  int64      `json:"assignee_id"`
	CreatedByID *int64     `json:"created_by_id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Type        string     `json:"type"`
	Priority    string     `json:"priority"`
	Status      string     `json:"status"`
	Origin      string     `json:"origin"`
	DueAt       *time.Time `json:"due_at"`
	CompletedAt *time.Time `json:"completed_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Input creates a task. Empty Type and Priority default to todo and normal;
// a nil AssigneeID assigns the caller.
type Input struct {
	ContactID   *int64     `json:"contact_id"`
	AssigneeID  *int64     `json:"assignee_id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Type        string     `json:"type"`
	Priority    string     `json:"priority"`
	DueAt       *time.Time `json:"due_at"`
}

// Patch lists fields to change; nil fields are unchanged. ClearDueAt
// removes the due date.
type Patch struct {
	AssigneeID  *int64     `json:"assignee_id"`
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	Type        *string    `json:"type"`
	Priority    *string    `json:"priority"`
	Status      *string    `json:"status"`
	DueAt       *time.Time `json:"due_at"`
	ClearDueAt  bool       `json:"clear_due_at"`
}

// Filter narrows a task list. Open tasks come soonest due first.
type Filter struct {
	Status     string
	AssigneeID int64
	ContactID  int64
	Overdue    bool // open and past due
	Limit      int
	Offset     int
}

// SystemTask is a task the workflow creates.
type SystemTask struct {
	ContactID   int64
	AssigneeID  int64
	Title       string
	Description string
	Type        string
	Priority    string
	Origin      string
	DueAt       *time.Time
}

// Contacts is what this module needs from the contacts module (wired in
// internal/app): visibility checks and timeline entries.
type Contacts interface {
	// Visible returns ErrContactNotFound unless p may see the contact.
	Visible(ctx context.Context, p principal.Principal, contactID int64) error
	// TaskCreated / TaskCompleted put the task on the contact's timeline.
	TaskCreated(ctx context.Context, orgID int64, t Task) error
	TaskCompleted(ctx context.Context, orgID int64, t Task, by int64) error
}

// Notifier tells users about their tasks: assigned by someone else, or overdue.
type Notifier interface {
	TaskAssigned(ctx context.Context, orgID int64, t Task, by int64) error
	TaskOverdue(ctx context.Context, orgID int64, t Task) error
}

// FieldError is a validation failure on one request field.
type FieldError struct {
	Field, Problem, Message string
}

func (e *FieldError) Error() string { return e.Message }

var (
	ErrNotFound        = errors.New("task not found")
	ErrForbidden       = errors.New("not allowed")
	ErrContactNotFound = errors.New("contact not found")
	ErrInvalidAssignee = errors.New("assignee is not a user in this organization")
)
