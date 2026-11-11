package nurture

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/deepashearaion/manage-crm/internal/platform/mail"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
	"github.com/deepashearaion/manage-crm/internal/platform/whatsapp"
)

// Settings configure sending.
type Settings struct {
	FrontendURL    string // unsubscribe links point to FRONTEND_URL/unsubscribe
	UnsubscribeKey []byte // signs unsubscribe tokens
}

type Service struct {
	repo     *Repository
	contacts Contacts
	numbers  Numbers
	mailer   mail.Mailer
	wa       whatsapp.Sender
	settings Settings
	logger   *slog.Logger
	now      func() time.Time
}

func NewService(repo *Repository, contacts Contacts, numbers Numbers, mailer mail.Mailer, wa whatsapp.Sender,
	settings Settings, logger *slog.Logger, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, contacts: contacts, numbers: numbers, mailer: mailer, wa: wa,
		settings: settings, logger: logger, now: now}
}

// Sequences (configuration: managers and above change them; everyone reads).

const module = principal.Nurture

// Sequences are org-wide: seeing them needs nurture.view; creating, changing
// and deleting them needs scope "all" on nurture.create, edit and delete.
// Enrolling and stopping contacts follow the contact's owner.

func (s *Service) Sequences(ctx context.Context, p principal.Principal) ([]Sequence, error) {
	if !p.Can(module, principal.View) {
		return nil, ErrForbidden
	}
	return s.repo.Sequences(ctx, p.OrgID)
}

func (s *Service) Sequence(ctx context.Context, p principal.Principal, id int64) (Sequence, error) {
	if !p.Can(module, principal.View) {
		return Sequence{}, ErrForbidden
	}
	return s.repo.Sequence(ctx, p.OrgID, id)
}

func apply(seq *Sequence, in SequenceInput) {
	if in.Name != nil {
		seq.Name = *in.Name
	}
	if in.Trigger != nil {
		seq.Trigger = *in.Trigger
	}
	if in.Active != nil {
		seq.Active = *in.Active
	}
	if in.StopOnReply != nil {
		seq.StopOnReply = *in.StopOnReply
	}
	if in.Steps != nil {
		seq.Steps = *in.Steps
	}
}

func (s *Service) CreateSequence(ctx context.Context, p principal.Principal, in SequenceInput) (Sequence, error) {
	if p.Scope(module, principal.Create) != principal.All {
		return Sequence{}, ErrForbidden
	}
	seq := Sequence{Trigger: TriggerManual, StopOnReply: true}
	apply(&seq, in)
	if err := validateSequence(&seq); err != nil {
		return Sequence{}, err
	}
	return s.repo.Save(ctx, p.OrgID, seq)
}

// UpdateSequence changes a sequence. Active enrollments continue from their
// current step index in the new steps.
func (s *Service) UpdateSequence(ctx context.Context, p principal.Principal, id int64, in SequenceInput) (Sequence, error) {
	if p.Scope(module, principal.Edit) != principal.All {
		return Sequence{}, ErrForbidden
	}
	seq, err := s.repo.Sequence(ctx, p.OrgID, id)
	if err != nil {
		return Sequence{}, err
	}
	apply(&seq, in)
	if err := validateSequence(&seq); err != nil {
		return Sequence{}, err
	}
	return s.repo.Save(ctx, p.OrgID, seq)
}

func (s *Service) DeleteSequence(ctx context.Context, p principal.Principal, id int64) error {
	if p.Scope(module, principal.Delete) != principal.All {
		return ErrForbidden
	}
	return s.repo.Delete(ctx, p.OrgID, id)
}

// Enrollments

// Enroll puts a contact p may see into a sequence by hand.
func (s *Service) Enroll(ctx context.Context, p principal.Principal, sequenceID, contactID int64) (Enrollment, error) {
	owner, err := s.contacts.Visible(ctx, p, contactID)
	if err != nil {
		return Enrollment{}, err
	}
	if !p.CanOn(module, principal.Create, owner) {
		return Enrollment{}, ErrForbidden
	}
	seq, err := s.repo.Sequence(ctx, p.OrgID, sequenceID)
	if err != nil {
		return Enrollment{}, err
	}
	return s.enroll(ctx, p.OrgID, seq, contactID, &p.UserID)
}

func (s *Service) enroll(ctx context.Context, orgID int64, seq Sequence, contactID int64, by *int64) (Enrollment, error) {
	if len(seq.Steps) == 0 {
		return Enrollment{}, ErrNoSteps
	}
	rc, err := s.contacts.Recipient(ctx, orgID, contactID)
	if err != nil {
		return Enrollment{}, err
	}
	if rc.OptedOut && seq.Trigger != TriggerDealWon {
		return Enrollment{}, fieldErr("contact_id", "invalid", "This contact opted out of marketing messages.")
	}
	first := s.now().Add(time.Duration(seq.Steps[0].DelayMinutes) * time.Minute)
	e, err := s.repo.Enroll(ctx, orgID, seq.ID, contactID, by, first)
	if err != nil {
		return Enrollment{}, err
	}
	s.event(ctx, orgID, contactID, "nurture_enrolled", "Enrolled in nurture: "+seq.Name,
		map[string]any{"sequence_id": seq.ID, "enrollment_id": e.ID})
	return e, nil
}

// AutoEnroll puts a contact into the org's active sequence for trigger, if
// there is one. It reports whether it did.
func (s *Service) AutoEnroll(ctx context.Context, orgID, contactID int64, trigger string) (bool, error) {
	seq, err := s.repo.ActiveFor(ctx, orgID, trigger)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = s.enroll(ctx, orgID, seq, contactID, nil)
	var fe *FieldError
	if errors.Is(err, ErrAlreadyEnrolled) || errors.As(err, &fe) {
		return false, nil
	}
	return err == nil, err
}

// StopForContact stops a contact's active enrollments. With onReply, only
// sequences that stop on reply are stopped.
func (s *Service) StopForContact(ctx context.Context, orgID, contactID int64, reason string, onReply bool) error {
	stopped, err := s.repo.StopForContact(ctx, orgID, contactID, reason, onReply)
	for _, e := range stopped {
		s.event(ctx, orgID, contactID, "nurture_stopped", fmt.Sprintf("Nurture stopped (%s): %s", reason, e.SequenceName),
			map[string]any{"sequence_id": e.SequenceID, "enrollment_id": e.ID, "reason": reason})
	}
	return err
}

func (s *Service) Enrollments(ctx context.Context, p principal.Principal, contactID, sequenceID int64, status string, limit, offset int) ([]Enrollment, error) {
	if !p.Can(module, principal.View) {
		return nil, ErrForbidden
	}
	if contactID != 0 {
		owner, err := s.contacts.Visible(ctx, p, contactID)
		if err != nil {
			return nil, err
		}
		if !p.CanOn(module, principal.View, owner) {
			return nil, ErrForbidden
		}
	} else if !p.SeesAll(module) {
		return nil, fieldErr("contact_id", "required", "List enrollments per contact (your role sees only your own contacts' enrollments).")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return s.repo.Enrollments(ctx, p.OrgID, contactID, sequenceID, status, limit, max(offset, 0))
}

// Stop ends an enrollment by hand.
func (s *Service) Stop(ctx context.Context, p principal.Principal, id int64) (Enrollment, error) {
	e, err := s.repo.Enrollment(ctx, p.OrgID, id)
	if err != nil {
		return Enrollment{}, err
	}
	owner, err := s.contacts.Visible(ctx, p, e.ContactID)
	if err == nil && !p.CanOn(module, principal.Edit, owner) {
		return Enrollment{}, ErrForbidden
	}
	if err != nil {
		return Enrollment{}, ErrNotFound
	}
	e, err = s.repo.Finish(ctx, p.OrgID, id, StatusStopped, StopManual)
	if errors.Is(err, ErrNotFound) {
		return Enrollment{}, fieldErr("status", "invalid", "This enrollment has already finished.")
	}
	if err == nil {
		s.event(ctx, p.OrgID, e.ContactID, "nurture_stopped", "Nurture stopped by user: "+e.SequenceName,
			map[string]any{"sequence_id": e.SequenceID, "enrollment_id": e.ID, "reason": StopManual})
	}
	return e, err
}

func (s *Service) event(ctx context.Context, orgID, contactID int64, kind, summary string, meta map[string]any) {
	if err := s.contacts.RecordEvent(ctx, orgID, contactID, kind, summary, meta); err != nil {
		s.logger.ErrorContext(ctx, "nurture timeline entry", slog.Any("error", err))
	}
}

// Unsubscribe

// unsubscribeToken signs "orgID.contactID" so the link cannot be forged
// for another contact. It does not expire.
func (s *Service) unsubscribeToken(orgID, contactID int64) string {
	payload := strconv.FormatInt(orgID, 10) + "." + strconv.FormatInt(contactID, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + s.sign(payload)
}

func (s *Service) sign(payload string) string {
	mac := hmac.New(sha256.New, s.settings.UnsubscribeKey)
	mac.Write([]byte("unsubscribe:" + payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) unsubscribeURL(orgID, contactID int64) string {
	return s.settings.FrontendURL + "/unsubscribe?token=" + url.QueryEscape(s.unsubscribeToken(orgID, contactID))
}

// Unsubscribe opts out the contact a valid token names and stops their nurture.
func (s *Service) Unsubscribe(ctx context.Context, token string) error {
	encoded, sig, ok := strings.Cut(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if !ok || err != nil || !hmac.Equal([]byte(sig), []byte(s.sign(string(raw)))) {
		return ErrInvalidToken
	}
	orgStr, contactStr, _ := strings.Cut(string(raw), ".")
	orgID, err1 := strconv.ParseInt(orgStr, 10, 64)
	contactID, err2 := strconv.ParseInt(contactStr, 10, 64)
	if err1 != nil || err2 != nil {
		return ErrInvalidToken
	}
	err = s.contacts.OptOut(ctx, orgID, contactID, "email link")
	if errors.Is(err, ErrContactNotFound) {
		return nil // deleted since: nothing left to send to
	}
	if err != nil {
		return err
	}
	return s.StopForContact(ctx, orgID, contactID, StopOptedOut, false)
}
