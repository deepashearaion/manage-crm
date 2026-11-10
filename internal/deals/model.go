// Package deals runs the deal pipeline: guided stages (what to do, which
// fields must be filled to enter), deals with money in integer paise,
// stage history, won/lost outcomes, and a rule-based win probability and
// forecast. Won and lost deals update the contact's lifecycle stage and
// raise events that the rest of the workflow reacts to.
package deals

import (
	"context"
	"errors"
	"time"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// Stage kinds and deal statuses (a deal's status is its stage's kind).
const (
	KindOpen = "open"
	KindWon  = "won"
	KindLost = "lost"
)

// Fields a stage can require before a deal enters it.
const (
	FieldAmount            = "amount"
	FieldExpectedCloseDate = "expected_close_date"
	FieldContact           = "contact"
)

var requirableFields = []string{FieldAmount, FieldExpectedCloseDate, FieldContact}

const (
	BillingOneTime   = "one_time"
	BillingRecurring = "recurring"
)

var billingPeriods = []string{"monthly", "quarterly", "yearly"}

type Stage struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Position       int       `json:"position"`
	Kind           string    `json:"kind"`
	Probability    int       `json:"probability"`
	Guidance       string    `json:"guidance"`
	RequiredFields []string  `json:"required_fields"`
	OpenDeals      int       `json:"open_deals"`
	OpenValuePaise int64     `json:"open_value_paise"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Deal struct {
	ID                int64      `json:"id"`
	Title             string     `json:"title"`
	Description       string     `json:"description"`
	ContactID         *int64     `json:"contact_id"`
	OwnerID           *int64     `json:"owner_id"`
	StageID           int64      `json:"stage_id"`
	StageName         string     `json:"stage_name"`
	Status            string     `json:"status"`
	AmountPaise       int64      `json:"amount_paise"`
	BillingType       string     `json:"billing_type"`
	BillingPeriod     *string    `json:"billing_period"`
	ValuePaise        int64      `json:"value_paise"` // one-time amount or first-year value
	ExpectedCloseDate *string    `json:"expected_close_date"`
	ClosedAt          *time.Time `json:"closed_at"`
	LostReason        string     `json:"lost_reason"`
	StageEnteredAt    time.Time  `json:"stage_entered_at"`
	WinProbability    int        `json:"win_probability"`
	ProbabilityNotes  []string   `json:"probability_notes"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`

	stageProbability int
}

// Input creates a deal. A nil StageID starts it in the first open stage; a
// nil OwnerID makes the caller the owner.
type Input struct {
	Title             string  `json:"title"`
	Description       string  `json:"description"`
	ContactID         *int64  `json:"contact_id"`
	OwnerID           *int64  `json:"owner_id"`
	StageID           *int64  `json:"stage_id"`
	AmountPaise       int64   `json:"amount_paise"`
	BillingType       string  `json:"billing_type"`
	BillingPeriod     *string `json:"billing_period"`
	ExpectedCloseDate *string `json:"expected_close_date"`
}

// Patch lists fields to change (not the stage: see Move); nil fields are
// unchanged. Clear* remove optional values.
type Patch struct {
	Title                  *string `json:"title"`
	Description            *string `json:"description"`
	ContactID              *int64  `json:"contact_id"`
	ClearContact           bool    `json:"clear_contact"`
	OwnerID                *int64  `json:"owner_id"`
	AmountPaise            *int64  `json:"amount_paise"`
	BillingType            *string `json:"billing_type"`
	BillingPeriod          *string `json:"billing_period"`
	ExpectedCloseDate      *string `json:"expected_close_date"`
	ClearExpectedCloseDate bool    `json:"clear_expected_close_date"`
}

// Filter narrows a deal list.
type Filter struct {
	Status    string
	StageID   int64
	OwnerID   int64
	ContactID int64
	Limit     int
	Offset    int
}

// HistoryEntry is one stage change.
type HistoryEntry struct {
	FromStageID *int64    `json:"from_stage_id"`
	FromStage   *string   `json:"from_stage"`
	ToStageID   int64     `json:"to_stage_id"`
	ToStage     string    `json:"to_stage"`
	ChangedByID *int64    `json:"changed_by_id"`
	ChangedAt   time.Time `json:"changed_at"`
}

// Outcome is a deal that was just won or lost.
type Outcome struct {
	OrgID         int64
	DealID        int64
	Title         string
	ContactID     *int64
	OwnerID       *int64
	ValuePaise    int64
	BillingType   string
	AmountPaise   int64
	BillingPeriod *string
	LostReason    string
	ClosedAt      time.Time
	ActorID       int64
}

// Contacts is what this module needs from the contacts module (wired in
// internal/app).
type Contacts interface {
	// Visible returns ErrContactNotFound unless p may see the contact.
	Visible(ctx context.Context, p principal.Principal, contactID int64) error
	// SetLifecycle moves the contact to stage `to` if its current stage is
	// one of `from` (any stage when from is empty).
	SetLifecycle(ctx context.Context, orgID, contactID int64, to string, from []string, actor int64) error
	// Record puts a deal event on the contact's timeline.
	Record(ctx context.Context, orgID, contactID int64, kind, summary string, meta map[string]any, actor int64) error
}

// Events receives won and lost deals; internal/app reacts (re-contact
// task for lost deals; onboarding for won deals comes in phase 7e). A
// failing handler is logged and does not undo the stage change.
type Events interface {
	DealWon(ctx context.Context, o Outcome) error
	DealLost(ctx context.Context, o Outcome) error
}

// FieldError is a validation failure on one request field.
type FieldError struct {
	Field, Problem, Message string
}

func (e *FieldError) Error() string { return e.Message }

func fieldErr(field, problem, msg string) error { return &FieldError{field, problem, msg} }

// MissingFieldsError lists the fields a stage requires that the deal lacks.
type MissingFieldsError struct {
	Stage  string
	Fields []string
}

func (e *MissingFieldsError) Error() string { return "stage " + e.Stage + " requires more fields" }

var (
	ErrNotFound         = errors.New("deal not found")
	ErrStageNotFound    = errors.New("stage not found")
	ErrForbidden        = errors.New("not allowed")
	ErrContactNotFound  = errors.New("contact not found")
	ErrInvalidOwner     = errors.New("owner is not a user in this organization")
	ErrStageInUse       = errors.New("stage has deals")
	ErrLastOutcomeStage = errors.New("an org needs at least one open, one won and one lost stage")
	ErrStageNameTaken   = errors.New("stage name already used")
)
