// Package nurture runs automated email and WhatsApp sequences: leads that
// are not hot and customers whose deal was lost get timed messages until
// they reply, turn hot, win a deal, opt out, or the sequence ends. A
// scheduler job (RunDue) sends the due steps.
package nurture

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// Triggers.
const (
	TriggerManual   = "manual"
	TriggerNewLead  = "new_lead"
	TriggerDealLost = "deal_lost"
	// TriggerDealWon sequences onboard new customers (welcome messages).
	// They are transactional: not stopped by the win itself, and their
	// emails go out without an unsubscribe footer even to contacts who
	// opted out of marketing (WhatsApp steps still respect the opt-out).
	TriggerDealWon = "deal_won"
)

const (
	ChannelEmail    = "email"
	ChannelWhatsApp = "whatsapp"

	StatusActive    = "active"
	StatusCompleted = "completed"
	StatusStopped   = "stopped"
)

// Stop reasons.
const (
	StopReplied  = "replied"
	StopHot      = "hot"
	StopWon      = "won"
	StopOptedOut = "opted_out"
	StopCustomer = "customer"
	StopManual   = "stopped by user"
)

type Sequence struct {
	ID                int64     `json:"id"`
	Name              string    `json:"name"`
	Trigger           string    `json:"trigger"`
	Active            bool      `json:"active"`
	StopOnReply       bool      `json:"stop_on_reply"`
	Steps             []Step    `json:"steps"`
	ActiveEnrollments int       `json:"active_enrollments"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// Step waits DelayMinutes after the previous step (or the enrollment) and
// sends one message.
type Step struct {
	DelayMinutes           int      `json:"delay_minutes"`
	Channel                string   `json:"channel"`
	Subject                string   `json:"subject"`
	Body                   string   `json:"body"`
	WhatsAppTemplate       string   `json:"whatsapp_template"`
	WhatsAppLanguage       string   `json:"whatsapp_language"`
	WhatsAppTemplateParams []string `json:"whatsapp_template_params"`
}

// SequenceInput creates or replaces a sequence; Steps replaces all steps.
type SequenceInput struct {
	Name        *string `json:"name"`
	Trigger     *string `json:"trigger"`
	Active      *bool   `json:"active"`
	StopOnReply *bool   `json:"stop_on_reply"`
	Steps       *[]Step `json:"steps"`
}

type Enrollment struct {
	ID           int64      `json:"id"`
	SequenceID   int64      `json:"sequence_id"`
	SequenceName string     `json:"sequence_name"`
	ContactID    int64      `json:"contact_id"`
	Status       string     `json:"status"`
	StopReason   string     `json:"stop_reason"`
	NextStep     int        `json:"next_step"`
	NextRunAt    time.Time  `json:"next_run_at"`
	Attempts     int        `json:"attempts"`
	LastError    string     `json:"last_error"`
	EnrolledByID *int64     `json:"enrolled_by_id"`
	EnrolledAt   time.Time  `json:"enrolled_at"`
	FinishedAt   *time.Time `json:"finished_at"`

	orgID int64
}

// Recipient is what sending needs to know about a contact.
type Recipient struct {
	FirstName             string
	LastName              string
	Email                 string
	PhoneKey              string // digits with country code
	LifecycleStage        string
	OptedOut              bool
	LastWhatsAppInboundAt *time.Time
}

// Contacts is what this module needs from the contacts module.
type Contacts interface {
	// Visible returns the owner of a contact p may see, else ErrContactNotFound.
	Visible(ctx context.Context, p principal.Principal, contactID int64) (*int64, error)
	Recipient(ctx context.Context, orgID, contactID int64) (Recipient, error)
	RecordOutbound(ctx context.Context, orgID, contactID int64, channel, summary, body, externalID string) error
	RecordEvent(ctx context.Context, orgID, contactID int64, kind, summary string, meta map[string]any) error
	OptOut(ctx context.Context, orgID, contactID int64, via string) error
}

// Numbers finds the WhatsApp number an org sends from (owned by the leads
// module); ErrNoWhatsAppNumber when it has none.
type Numbers interface {
	SenderNumber(ctx context.Context, orgID int64) (string, error)
}

// FieldError is a validation failure on one request field.
type FieldError struct {
	Field, Problem, Message string
}

func (e *FieldError) Error() string { return e.Message }

func fieldErr(field, problem, msg string) error { return &FieldError{field, problem, msg} }

var (
	ErrNotFound         = errors.New("not found")
	ErrForbidden        = errors.New("not allowed")
	ErrContactNotFound  = errors.New("contact not found")
	ErrNameTaken        = errors.New("sequence name already used")
	ErrTriggerTaken     = errors.New("another active sequence has this trigger")
	ErrAlreadyEnrolled  = errors.New("contact is already in this sequence")
	ErrNoSteps          = errors.New("sequence has no steps")
	ErrInvalidToken     = errors.New("invalid unsubscribe token")
	ErrNoWhatsAppNumber = errors.New("no WhatsApp number registered")
)

const maxSteps = 20

func validateSequence(s *Sequence) error {
	s.Name = strings.TrimSpace(s.Name)
	switch {
	case s.Name == "":
		return fieldErr("name", "required", "Name is required.")
	case utf8.RuneCountInString(s.Name) > 100:
		return fieldErr("name", "invalid", "Name must be at most 100 characters.")
	case !slices.Contains([]string{TriggerManual, TriggerNewLead, TriggerDealLost, TriggerDealWon}, s.Trigger):
		return fieldErr("trigger", "invalid", "Trigger must be manual, new_lead, deal_lost or deal_won.")
	case len(s.Steps) == 0:
		return fieldErr("steps", "required", "A sequence needs at least one step.")
	case len(s.Steps) > maxSteps:
		return fieldErr("steps", "invalid", "A sequence can have at most 20 steps.")
	}
	for i := range s.Steps {
		st := &s.Steps[i]
		st.Subject = strings.TrimSpace(st.Subject)
		st.Body = strings.TrimSpace(st.Body)
		st.WhatsAppTemplate = strings.TrimSpace(st.WhatsAppTemplate)
		if st.WhatsAppLanguage == "" {
			st.WhatsAppLanguage = "en"
		}
		if st.WhatsAppTemplateParams == nil {
			st.WhatsAppTemplateParams = []string{}
		}
		field := func(name string) string { return "steps[" + strconv.Itoa(i) + "]." + name }
		switch {
		case st.DelayMinutes < 0 || st.DelayMinutes > 525600:
			return fieldErr(field("delay_minutes"), "invalid", "Delay must be between 0 and 525600 minutes (a year).")
		case st.Channel == ChannelEmail && (st.Subject == "" || st.Body == ""):
			return fieldErr(field("body"), "required", "Email steps need a subject and a body.")
		case st.Channel == ChannelEmail && utf8.RuneCountInString(st.Subject) > 200:
			return fieldErr(field("subject"), "invalid", "Subject must be at most 200 characters.")
		case st.Channel == ChannelWhatsApp && st.Body == "" && st.WhatsAppTemplate == "":
			return fieldErr(field("body"), "required", "WhatsApp steps need a body, a template, or both.")
		case st.Channel != ChannelEmail && st.Channel != ChannelWhatsApp:
			return fieldErr(field("channel"), "invalid", "Channel must be email or whatsapp.")
		case utf8.RuneCountInString(st.Body) > 4096:
			return fieldErr(field("body"), "invalid", "Body must be at most 4096 characters.")
		}
		if st.Channel == ChannelEmail {
			st.WhatsAppTemplate, st.WhatsAppTemplateParams = "", []string{}
		}
	}
	return nil
}
