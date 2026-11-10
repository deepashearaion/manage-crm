package contacts

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// System operations, used by the leads module (through an interface wired
// in internal/app) for intake that has no signed-in caller, such as
// webhooks. The org comes from the intake source, never from the payload.

// Capture records an inbound lead: see Repository.Capture.
func (s *Service) Capture(ctx context.Context, orgID int64, in CaptureInput) (CaptureResult, error) {
	in.FirstName = strings.TrimSpace(in.FirstName)
	in.LastName = strings.TrimSpace(in.LastName)
	in.Email = strings.TrimSpace(in.Email)
	in.Mobile = strings.TrimSpace(in.Mobile)
	if in.FirstName == "" {
		return CaptureResult{}, ErrFirstNameRequired
	}
	return s.repo.Capture(ctx, orgID, in)
}

// Facts returns the scoring facts for a contact in orgID.
func (s *Service) Facts(ctx context.Context, orgID, contactID int64) (Facts, error) {
	return s.repo.Facts(ctx, orgID, contactID)
}

// SaveScore stores a contact's score and returns its previous temperature.
func (s *Service) SaveScore(ctx context.Context, orgID, contactID int64, score Score) (string, error) {
	return s.repo.SaveScore(ctx, orgID, contactID, score)
}

// AssignOwner gives an unowned contact to userID.
func (s *Service) AssignOwner(ctx context.Context, orgID, contactID, userID int64, reason string) (bool, error) {
	return s.repo.AssignOwner(ctx, orgID, contactID, userID, reason)
}

// AddSystemActivity puts another module's event on a contact's timeline.
// Only task and deal events are accepted; other kinds belong to this module.
func (s *Service) AddSystemActivity(ctx context.Context, orgID, contactID int64, a SystemActivity) error {
	if !slices.Contains(systemKinds, a.Kind) {
		return ErrInvalidKind
	}
	return s.repo.AddSystemActivity(ctx, newActivity{
		OrgID: orgID, ContactID: contactID, Kind: a.Kind, Channel: ChannelSystem,
		Summary: a.Summary, Metadata: a.Metadata, ActorUserID: a.ActorUserID,
	})
}

// Caller operations, for /api/v1.

// Visible returns the contact if p may see it, otherwise ErrNotFound.
func (s *Service) Visible(ctx context.Context, p principal.Principal, id int64) (View, error) {
	return s.repo.GetView(ctx, scope(p), id)
}

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

func pageSize(n int) int {
	if n <= 0 {
		return defaultPageSize
	}
	return min(n, maxPageSize)
}

// ListViews returns the contacts p may see that match f.
func (s *Service) ListViews(ctx context.Context, p principal.Principal, f ListFilter) ([]View, error) {
	if f.Stage != "" && !ValidStage(f.Stage) {
		return nil, ErrInvalidStage
	}
	switch f.Temperature {
	case "", "cold", "warm", "hot":
	default:
		return nil, ErrInvalidTemperature
	}
	f.Limit = pageSize(f.Limit)
	return s.repo.ListViews(ctx, scope(p), f)
}

// Timeline returns the activities of a contact p may see.
func (s *Service) Timeline(ctx context.Context, p principal.Principal, id int64, limit int, beforeID int64) ([]Activity, error) {
	if _, err := s.repo.GetView(ctx, scope(p), id); err != nil {
		return nil, err
	}
	return s.repo.Timeline(ctx, p.OrgID, id, pageSize(limit), beforeID)
}

// ActivityInput is a note, call or email a user logs by hand.
type ActivityInput struct {
	Kind       string     `json:"kind"`      // note, call or email
	Direction  string     `json:"direction"` // inbound, outbound or "" (notes)
	Summary    string     `json:"summary"`
	Body       string     `json:"body"`
	OccurredAt *time.Time `json:"occurred_at"`
}

// LogActivity adds a note, call or email to a contact p may change.
func (s *Service) LogActivity(ctx context.Context, p principal.Principal, id int64, in ActivityInput) (Activity, error) {
	c, err := s.repo.GetView(ctx, scope(p), id)
	if err != nil {
		return Activity{}, err
	}
	if !p.CanWriteRecord(c.LeadOwnerID) {
		return Activity{}, ErrForbidden
	}
	channel := ""
	switch in.Kind {
	case KindNote:
		in.Direction = ""
		channel = ChannelManual
	case KindCall:
		channel = ChannelPhone
	case KindEmail:
		channel = ChannelEmail
	default:
		return Activity{}, ErrInvalidKind
	}
	if in.Kind != KindNote && in.Direction != "inbound" && in.Direction != "outbound" {
		return Activity{}, ErrInvalidDirection
	}
	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" {
		return Activity{}, ErrEmptyBody
	}
	if in.Summary = strings.TrimSpace(in.Summary); in.Summary == "" {
		in.Summary = map[string]string{KindNote: "Note", KindCall: "Call", KindEmail: "Email"}[in.Kind]
	}
	return s.repo.AddActivity(ctx, newActivity{
		OrgID: p.OrgID, ContactID: id, Kind: in.Kind, Channel: channel, Direction: in.Direction,
		Summary: in.Summary, Body: in.Body, ActorUserID: &p.UserID, OccurredAt: in.OccurredAt,
	})
}

// SetStage moves a contact p may change to another lifecycle stage.
func (s *Service) SetStage(ctx context.Context, p principal.Principal, id int64, stage string) (View, error) {
	if !ValidStage(stage) {
		return View{}, ErrInvalidStage
	}
	c, err := s.repo.GetView(ctx, scope(p), id)
	if err != nil {
		return View{}, err
	}
	if !p.CanWriteRecord(c.LeadOwnerID) {
		return View{}, ErrForbidden
	}
	return s.repo.SetStage(ctx, scope(p), id, stage, p.UserID)
}

// SetStageFrom moves a contact's lifecycle stage for another module (a deal
// won or lost), only from the given stages (any when empty).
func (s *Service) SetStageFrom(ctx context.Context, orgID, contactID int64, to string, from []string, actor *int64) (bool, error) {
	if !ValidStage(to) {
		return false, ErrInvalidStage
	}
	return s.repo.SetStageFrom(ctx, orgID, contactID, to, from, actor)
}

// Recipient returns what nurture needs to message a contact in orgID.
func (s *Service) Recipient(ctx context.Context, orgID, contactID int64) (Recipient, error) {
	return s.repo.Recipient(ctx, orgID, contactID)
}

// OptOut records a marketing opt-out (via: "email link", "whatsapp").
func (s *Service) OptOut(ctx context.Context, orgID, contactID int64, via string) error {
	return s.repo.OptOut(ctx, orgID, contactID, via)
}

// RecordOutbound puts a message another module sent on the timeline.
func (s *Service) RecordOutbound(ctx context.Context, orgID, contactID int64, m OutboundMessage) error {
	if m.Channel != ChannelEmail && m.Channel != ChannelWhatsApp {
		return ErrInvalidKind
	}
	return s.repo.AddSystemActivity(ctx, newActivity{
		OrgID: orgID, ContactID: contactID, Kind: KindMessage, Channel: m.Channel, Direction: "outbound",
		Summary: m.Summary, Body: m.Body, ExternalID: m.ExternalID, Metadata: m.Metadata,
	})
}
