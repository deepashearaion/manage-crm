// Package leads runs the front of the sales workflow: it takes leads in
// from web forms, WhatsApp and manual entry, has the contacts module dedupe
// and record them, scores them with per-org rules (hot / warm / cold), and
// routes new ones to an owner. It owns the configuration for all of that:
// lead sources, WhatsApp numbers, scoring rules, assignment rules and lead
// settings. Contacts, scores and timelines are stored by the contacts
// module, reached through the Contacts interface.
package leads

import (
	"context"
	"errors"
	"time"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// Channels leads arrive through.
const (
	ChannelWebForm  = "web_form"
	ChannelWhatsApp = "whatsapp"
	ChannelEmail    = "email"
	ChannelPhone    = "phone"
	ChannelManual   = "manual"
	ChannelAPI      = "api"
)

// Temperatures.
const (
	Cold = "cold"
	Warm = "warm"
	Hot  = "hot"
)

// Lead is an inbound lead to capture.
type Lead struct {
	FirstName   string
	LastName    string
	Email       string
	Mobile      string
	Destination string
	Source      string
	Notes       string
	Channel     string
	Message     string
	ExternalID  string
	OccurredAt  time.Time
	ActorUserID *int64
	OwnerID     *int64 // owner for a new contact; nil routes it
}

// Captured is what the contacts module did with a Lead.
type Captured struct {
	ContactID        int64
	OwnerID          *int64
	Created          bool
	AlreadyProcessed bool
}

// Facts are what scoring rules look at.
type Facts struct {
	Source          string
	Destination     string
	HasEmail        bool
	HasMobile       bool
	HasCompany      bool
	Channels        []string
	InboundMessages int
	Text            string
	OwnerID         *int64

	FirstName          string
	LastName           string
	Email              string
	Mobile             string
	LastMessage        string
	LastMessageChannel string
	LastMessageAt      *time.Time
}

// Name is the contact's full name.
func (f Facts) Name() string {
	if f.LastName == "" {
		return f.FirstName
	}
	return f.FirstName + " " + f.LastName
}

// HotLead is raised when a contact's temperature becomes hot. OwnerID is
// nil for an unowned lead.
type HotLead struct {
	OrgID       int64
	ContactID   int64
	OwnerID     *int64
	Name        string
	Score       int
	Summary     string
	FollowUpDue time.Time
}

// LeadAssigned is raised when routing gives a new lead to a user.
type LeadAssigned struct {
	OrgID     int64
	ContactID int64
	OwnerID   int64
	Name      string
	Reason    string
}

// Events receives the workflow events this module raises; internal/app
// turns them into tasks and notifications. A failing handler is logged and
// does not undo the capture.
type Events interface {
	LeadTurnedHot(ctx context.Context, e HotLead) error
	LeadAssigned(ctx context.Context, e LeadAssigned) error
	// LeadReplied: an existing contact sent a message (nurture stops).
	LeadReplied(ctx context.Context, orgID, contactID int64) error
	// LeadNotHot: a new lead that is not hot (nurture starts).
	LeadNotHot(ctx context.Context, orgID, contactID int64) error
}

// Score is a computed lead score.
type Score struct {
	Score       int
	Temperature string
	Reasons     []string
	Method      string
}

// Contacts is what this module needs from the contacts module. The
// implementation, wired in internal/app, maps the contacts module's errors
// to ErrContactNotFound and ErrInvalidOwner.
type Contacts interface {
	Capture(ctx context.Context, orgID int64, lead Lead) (Captured, error)
	Facts(ctx context.Context, orgID, contactID int64) (Facts, error)
	// SaveScore stores the score and returns the previous temperature.
	SaveScore(ctx context.Context, orgID, contactID int64, s Score) (string, error)
	// AssignOwner sets the owner of an unowned contact, reporting false if
	// it already had one.
	AssignOwner(ctx context.Context, orgID, contactID, userID int64, reason string) (bool, error)
	// Visible returns the owner of a contact p may see, or ErrContactNotFound.
	Visible(ctx context.Context, p principal.Principal, contactID int64) (*int64, error)
	// OptOut records that the contact opted out of marketing.
	OptOut(ctx context.Context, orgID, contactID int64, via string) error
}

// Result reports the outcome of a capture.
type Result struct {
	ContactID   int64    `json:"contact_id"`
	Created     bool     `json:"created"`
	Score       int      `json:"score"`
	Temperature string   `json:"temperature"`
	Reasons     []string `json:"reasons"`
	OwnerID     *int64   `json:"owner_id"`
	// AlreadyProcessed means the message was captured before (webhook retry).
	AlreadyProcessed bool `json:"-"`
}

// Settings are an org's lead settings.
type Settings struct {
	HotThreshold   int    `json:"hot_threshold"`
	WarmThreshold  int    `json:"warm_threshold"`
	DefaultOwnerID *int64 `json:"default_owner_id"`
	// HotFollowupMinutes is how soon a hot lead's follow-up task is due.
	HotFollowupMinutes int       `json:"hot_followup_minutes"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// DefaultSettings apply to orgs that have not changed theirs.
var DefaultSettings = Settings{HotThreshold: 70, WarmThreshold: 40, HotFollowupMinutes: 15}

// Source is a web form or API integration that submits leads.
type Source struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Channel   string    `json:"channel"` // web_form or api
	PublicKey string    `json:"public_key"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// WhatsAppAccount maps a WhatsApp Business number to the org.
type WhatsAppAccount struct {
	ID            int64     `json:"id"`
	PhoneNumberID string    `json:"phone_number_id"`
	DisplayPhone  string    `json:"display_phone"`
	SourceName    string    `json:"source_name"`
	CreatedAt     time.Time `json:"created_at"`
	OrgID         int64     `json:"-"`
}

// Scoring criteria.
const (
	CriterionHasEmail               = "has_email"
	CriterionHasMobile              = "has_mobile"
	CriterionHasCompany             = "has_company"
	CriterionSourceEquals           = "source_equals"
	CriterionDestinationEquals      = "destination_equals"
	CriterionChannelUsed            = "channel_used"
	CriterionTextContains           = "text_contains"
	CriterionInboundMessagesAtLeast = "inbound_messages_at_least"
)

// ScoringRule adds Points to a lead's score when it matches.
type ScoringRule struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Criterion string    `json:"criterion"`
	Value     string    `json:"value"`
	Points    int       `json:"points"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Assignment criteria.
const (
	AssignAny               = "any"
	AssignSourceEquals      = "source_equals"
	AssignChannelEquals     = "channel_equals"
	AssignDestinationEquals = "destination_equals"
	AssignTemperatureEquals = "temperature_equals"
)

// AssignmentRule routes matching new leads to its members, round robin.
type AssignmentRule struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Position  int       `json:"position"`
	Criterion string    `json:"criterion"`
	Value     string    `json:"value"`
	Active    bool      `json:"active"`
	UserIDs   []int64   `json:"user_ids"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// routingAttrs are what assignment rules look at.
type routingAttrs struct {
	Source, Channel, Destination, Temperature string
}

// FieldError is a validation failure on one request field.
type FieldError struct {
	Field   string
	Problem string // e.g. "required", "invalid"
	Message string
}

func (e *FieldError) Error() string { return e.Message }

func invalid(field, msg string) error  { return &FieldError{field, "invalid", msg} }
func required(field, msg string) error { return &FieldError{field, "required", msg} }

var (
	ErrForbidden        = errors.New("not allowed")
	ErrNotFound         = errors.New("not found")
	ErrContactNotFound  = errors.New("contact not found")
	ErrInvalidOwner     = errors.New("owner is not a user in this organization")
	ErrDuplicateContact = errors.New("a contact with this email or mobile already exists")
	ErrNameTaken        = errors.New("name already used")
	ErrPhoneNumberTaken = errors.New("phone number id already registered")
	ErrSourceInactive   = errors.New("lead source not found or inactive")
	ErrNoWhatsAppNumber = errors.New("no WhatsApp number registered")
)
