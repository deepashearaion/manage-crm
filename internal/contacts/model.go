// Package contacts manages people the CRM tracks: create, list, read,
// update and delete; their lifecycle stage (lead → customer), their lead
// score, and their activity timeline. Leads are contacts; inbound capture,
// dedupe and timeline writes for other modules go through Service.
package contacts

import (
	"encoding/json"
	"errors"
	"time"
)

type Contact struct {
	ID              int64     `json:"id"`
	FirstName       string    `json:"first_name"`
	LastName        string    `json:"last_name"`
	Email           string    `json:"email"`
	Mobile          string    `json:"mobile"`
	AlternateMobile string    `json:"alternate_mobile"`
	CompanyID       *int64    `json:"company_id"`
	LeadStatusID    *int64    `json:"lead_status_id"`
	LeadOwnerID     *int64    `json:"lead_owner_id"`
	Destination     string    `json:"destination"`
	Source          string    `json:"source"`
	Notes           string    `json:"notes"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Patch lists the fields to change; nil fields are left as they are.
type Patch struct {
	FirstName       *string `json:"first_name"`
	LastName        *string `json:"last_name"`
	Email           *string `json:"email"`
	Mobile          *string `json:"mobile"`
	AlternateMobile *string `json:"alternate_mobile"`
	CompanyID       *int64  `json:"company_id"`
	LeadStatusID    *int64  `json:"lead_status_id"`
	LeadOwnerID     *int64  `json:"lead_owner_id"`
	Destination     *string `json:"destination"`
	Source          *string `json:"source"`
	Notes           *string `json:"notes"`
}

var (
	// ErrNotFound is also returned for contacts outside the caller's scope,
	// so their existence is not revealed.
	ErrNotFound          = errors.New("contact not found")
	ErrFirstNameRequired = errors.New("first name is required")
	ErrForbidden         = errors.New("not allowed")
	ErrInvalidOwner      = errors.New("lead owner is not a user in this organization")
)

// Lifecycle stages. A lead is a contact in the "lead" stage; there is no
// separate lead entity.
const (
	StageLead        = "lead"
	StageMQL         = "mql"
	StageSQL         = "sql"
	StageOpportunity = "opportunity"
	StageCustomer    = "customer"
	StageLost        = "lost"
)

// ValidStage reports whether s is a lifecycle stage.
func ValidStage(s string) bool {
	switch s {
	case StageLead, StageMQL, StageSQL, StageOpportunity, StageCustomer, StageLost:
		return true
	}
	return false
}

// Channels a contact can reach us through, or we reach them through.
const (
	ChannelWebForm  = "web_form"
	ChannelWhatsApp = "whatsapp"
	ChannelEmail    = "email"
	ChannelPhone    = "phone"
	ChannelManual   = "manual"
	ChannelAPI      = "api"
	ChannelSystem   = "system"
)

// Activity kinds on the contact timeline.
const (
	KindCaptured          = "captured"
	KindDuplicateCaptured = "duplicate_captured"
	KindMessage           = "message"
	KindNote              = "note"
	KindCall              = "call"
	KindEmail             = "email"
	KindScoreChanged      = "score_changed"
	KindOwnerAssigned     = "owner_assigned"
	KindLifecycleChanged  = "lifecycle_changed"
	KindTaskCreated       = "task_created"
	KindTaskCompleted     = "task_completed"
)

// Activity is one entry on a contact's timeline.
type Activity struct {
	ID          int64           `json:"id"`
	ContactID   int64           `json:"contact_id"`
	Kind        string          `json:"kind"`
	Channel     *string         `json:"channel"`
	Direction   *string         `json:"direction"`
	Summary     string          `json:"summary"`
	Body        string          `json:"body"`
	Metadata    json.RawMessage `json:"metadata"`
	ActorUserID *int64          `json:"actor_user_id"`
	OccurredAt  time.Time       `json:"occurred_at"`
}

// Score is a contact's current lead score.
type Score struct {
	Score       int       `json:"score"`
	Temperature string    `json:"temperature"` // cold, warm or hot
	Reasons     []string  `json:"reasons"`
	Method      string    `json:"method"` // rules (later also ai)
	ScoredAt    time.Time `json:"scored_at"`
}

// View is a contact as /api/v1 returns it: the legacy fields plus the
// lifecycle stage and the current score (nil until scored).
type View struct {
	ID              int64     `json:"id"`
	FirstName       string    `json:"first_name"`
	LastName        string    `json:"last_name"`
	Email           string    `json:"email"`
	Mobile          string    `json:"mobile"`
	AlternateMobile string    `json:"alternate_mobile"`
	CompanyID       *int64    `json:"company_id"`
	LeadStatusID    *int64    `json:"lead_status_id"`
	LeadOwnerID     *int64    `json:"lead_owner_id"`
	Destination     string    `json:"destination"`
	Source          string    `json:"source"`
	Notes           string    `json:"notes"`
	LifecycleStage  string    `json:"lifecycle_stage"`
	Score           *Score    `json:"score"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ListFilter narrows GET /api/v1/contacts. Results are newest first and
// paged by BeforeID (the last id of the previous page).
type ListFilter struct {
	Temperature string
	Stage       string
	Limit       int
	BeforeID    int64
}

// CaptureInput is a lead arriving from any channel. The contact is matched
// to an existing one in the org by email or phone; otherwise it is created.
type CaptureInput struct {
	FirstName   string
	LastName    string
	Email       string
	Mobile      string
	Destination string
	Source      string
	Notes       string
	Channel     string
	// Message is the lead's own words (form message, WhatsApp text), logged
	// on the timeline as an inbound message.
	Message string
	// ExternalID is the channel's id for Message (e.g. a WhatsApp wamid);
	// a capture whose ExternalID was already recorded is not repeated.
	ExternalID  string
	OccurredAt  time.Time
	ActorUserID *int64
	// OwnerID owns a newly created contact; nil leaves it for routing.
	OwnerID *int64
}

// CaptureResult reports what a capture did.
type CaptureResult struct {
	ContactID int64
	OwnerID   *int64
	Created   bool
	// AlreadyProcessed means ExternalID had been captured before (a webhook
	// retry); nothing was changed.
	AlreadyProcessed bool
}

// Facts are what lead scoring looks at.
type Facts struct {
	Source          string
	Destination     string
	HasEmail        bool
	HasMobile       bool
	HasCompany      bool
	Channels        []string // channels the contact has used
	InboundMessages int
	Text            string // notes and recent inbound messages
	OwnerID         *int64

	// For lead summaries.
	FirstName          string
	LastName           string
	Email              string
	Mobile             string
	LastMessage        string // latest inbound message, "" if none
	LastMessageChannel string
	LastMessageAt      *time.Time
}

// SystemActivity is a timeline entry another module records.
type SystemActivity struct {
	Kind        string // KindTaskCreated or KindTaskCompleted
	Summary     string
	Metadata    map[string]any
	ActorUserID *int64
}

var (
	ErrInvalidStage = errors.New("invalid lifecycle stage")
	ErrInvalidKind  = errors.New("activity kind must be note, call or email")
	ErrEmptyBody    = errors.New("activity body is required")

	ErrInvalidDirection   = errors.New("direction must be inbound or outbound")
	ErrInvalidTemperature = errors.New("temperature must be cold, warm or hot")
)
