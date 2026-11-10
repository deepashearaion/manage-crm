package leads

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
	"github.com/deepashearaion/manage-crm/internal/platform/validate"
)

type Service struct {
	repo       *Repository
	contacts   Contacts
	events     Events
	summarizer Summarizer
	logger     *slog.Logger
	now        func() time.Time
}

// NewService builds the service. events may be nil (no workflow reactions).
func NewService(repo *Repository, contacts Contacts, events Events, logger *slog.Logger) *Service {
	return &Service{
		repo: repo, contacts: contacts, events: events, summarizer: RuleSummarizer{},
		logger: logger, now: time.Now,
	}
}

// Field limits, matching the contacts table.
const (
	maxName        = 100
	maxEmail       = 255
	maxMobile      = 30
	maxDestination = 150
	maxSource      = 100
	maxMessage     = 5000
)

// normalizeLead trims l and checks it is a usable lead: a first name, and
// an email or mobile to reach them and to dedupe on.
func normalizeLead(l *Lead) error {
	for _, f := range []*string{&l.FirstName, &l.LastName, &l.Email, &l.Mobile, &l.Destination, &l.Source, &l.Notes, &l.Message} {
		*f = strings.TrimSpace(*f)
	}
	l.Email = validate.NormalizeEmail(l.Email)
	switch {
	case l.FirstName == "":
		return required("first_name", "First name is required.")
	case l.Email == "" && l.Mobile == "":
		return required("email", "An email or mobile number is required.")
	case l.Email != "" && !validate.Email(l.Email):
		return invalid("email", "Invalid email address.")
	}
	for _, c := range []struct {
		field, value string
		max          int
	}{
		{"first_name", l.FirstName, maxName}, {"last_name", l.LastName, maxName}, {"email", l.Email, maxEmail},
		{"mobile", l.Mobile, maxMobile}, {"destination", l.Destination, maxDestination},
		{"source", l.Source, maxSource}, {"message", l.Message, maxMessage},
	} {
		if utf8.RuneCountInString(c.value) > c.max {
			return invalid(c.field, fmt.Sprintf("%s must be at most %d characters.", c.field, c.max))
		}
	}
	return nil
}

// Capture runs a lead through the workflow's first steps: dedupe and
// record it, score it, and route it to an owner if it is new and unowned.
func (s *Service) Capture(ctx context.Context, orgID int64, l Lead) (Result, error) {
	if err := normalizeLead(&l); err != nil {
		return Result{}, err
	}
	c, err := s.contacts.Capture(ctx, orgID, l)
	if err != nil {
		return Result{}, err
	}
	res := Result{ContactID: c.ContactID, Created: c.Created, OwnerID: c.OwnerID, AlreadyProcessed: c.AlreadyProcessed}
	if c.AlreadyProcessed {
		return res, nil
	}

	sc, err := s.rescore(ctx, orgID, c.ContactID)
	if err != nil {
		return Result{}, err
	}
	res.Score, res.Temperature, res.Reasons = sc.score.Score, sc.score.Temperature, sc.score.Reasons

	reason := ""
	if c.OwnerID == nil {
		owner, why, err := s.route(ctx, orgID, c.ContactID, routingAttrs{
			Source: l.Source, Channel: l.Channel, Destination: l.Destination, Temperature: sc.score.Temperature,
		})
		if err != nil {
			return Result{}, err
		}
		res.OwnerID, reason = owner, why
	}

	// Workflow reactions: a lead that just turned hot alerts its owner
	// (the hot alert covers a fresh assignment too); otherwise a routed
	// lead tells its new owner.
	switch {
	case sc.turnedHot():
		s.raiseHot(ctx, orgID, c.ContactID, res.OwnerID, sc)
	case reason != "":
		s.raise(ctx, "lead assigned", func() error {
			return s.events.LeadAssigned(ctx, LeadAssigned{
				OrgID: orgID, ContactID: c.ContactID, OwnerID: *res.OwnerID, Name: sc.facts.Name(), Reason: reason,
			})
		})
	}

	// Nurture: a known contact's message stops their sequences (the
	// diagram's "re-score on reply" already happened above); a new lead
	// that is not hot starts one.
	if !c.Created && l.Message != "" {
		s.raise(ctx, "lead replied", func() error { return s.events.LeadReplied(ctx, orgID, c.ContactID) })
	}
	if c.Created && sc.score.Temperature != Hot {
		s.raise(ctx, "lead not hot", func() error { return s.events.LeadNotHot(ctx, orgID, c.ContactID) })
	}
	return res, nil
}

// scored is the outcome of rescoring a contact.
type scored struct {
	score    Score
	previous string // temperature before, "" if never scored
	facts    Facts
	settings Settings
}

func (s scored) turnedHot() bool { return s.score.Temperature == Hot && s.previous != Hot }

func (s *Service) raiseHot(ctx context.Context, orgID, contactID int64, owner *int64, sc scored) {
	s.raise(ctx, "lead turned hot", func() error {
		return s.events.LeadTurnedHot(ctx, HotLead{
			OrgID: orgID, ContactID: contactID, OwnerID: owner, Name: sc.facts.Name(), Score: sc.score.Score,
			Summary:     s.summarizer.Summarize(sc.facts, sc.score, sc.settings.HotFollowupMinutes),
			FollowUpDue: s.now().Add(time.Duration(sc.settings.HotFollowupMinutes) * time.Minute).UTC(),
		})
	})
}

// raise runs an event handler, logging failures: the lead is already
// captured, and reporting an error would make webhooks retry needlessly.
func (s *Service) raise(ctx context.Context, what string, fn func() error) {
	if s.events == nil {
		return
	}
	if err := fn(); err != nil {
		s.logger.ErrorContext(ctx, "workflow event failed", slog.String("event", what), slog.Any("error", err))
	}
}

// CaptureByUser is POST /api/v1/leads: a signed-in user entering a lead.
// Reps own the leads they enter; for managers and above, new leads are
// routed. A rep who enters someone already owned by another user gets
// ErrDuplicateContact, without the contact's details.
func (s *Service) CaptureByUser(ctx context.Context, p principal.Principal, l Lead) (Result, error) {
	if !p.CanWrite() {
		return Result{}, ErrForbidden
	}
	switch l.Channel {
	case "":
		l.Channel = ChannelManual
	case ChannelManual, ChannelEmail, ChannelPhone, ChannelAPI:
	default:
		return Result{}, invalid("channel", "Channel must be manual, email, phone or api.")
	}
	l.ActorUserID = &p.UserID
	l.ExternalID = ""
	if !p.SeesAllRecords() {
		l.OwnerID = &p.UserID
	}
	res, err := s.Capture(ctx, p.OrgID, l)
	if err != nil {
		return Result{}, err
	}
	if _, err := s.contacts.Visible(ctx, p, res.ContactID); errors.Is(err, ErrContactNotFound) {
		return Result{}, ErrDuplicateContact
	} else if err != nil {
		return Result{}, err
	}
	return res, nil
}

// Rescore recomputes the score of a contact p may see (after the rules
// changed, for instance).
func (s *Service) Rescore(ctx context.Context, p principal.Principal, contactID int64) (Result, error) {
	if !p.CanWrite() {
		return Result{}, ErrForbidden
	}
	owner, err := s.contacts.Visible(ctx, p, contactID)
	if err != nil {
		return Result{}, err
	}
	sc, err := s.rescore(ctx, p.OrgID, contactID)
	if err != nil {
		return Result{}, err
	}
	if sc.turnedHot() {
		s.raiseHot(ctx, p.OrgID, contactID, owner, sc)
	}
	return Result{ContactID: contactID, Score: sc.score.Score, Temperature: sc.score.Temperature, Reasons: sc.score.Reasons, OwnerID: owner}, nil
}

func (s *Service) rescore(ctx context.Context, orgID, contactID int64) (scored, error) {
	settings, err := s.repo.Settings(ctx, orgID)
	if err != nil {
		return scored{}, err
	}
	rules, err := s.repo.ScoringRules(ctx, orgID)
	if err != nil {
		return scored{}, err
	}
	facts, err := s.contacts.Facts(ctx, orgID, contactID)
	if err != nil {
		return scored{}, err
	}
	score := Evaluate(rules, facts, settings)
	previous, err := s.contacts.SaveScore(ctx, orgID, contactID, score)
	if err != nil {
		return scored{}, err
	}
	return scored{score: score, previous: previous, facts: facts, settings: settings}, nil
}

// route assigns a new lead to the next member of the first matching
// assignment rule, or to the org's default owner. It returns the owner, or
// nil when nothing applies (managers then assign by hand), and why.
func (s *Service) route(ctx context.Context, orgID, contactID int64, a routingAttrs) (*int64, string, error) {
	rules, err := s.repo.AssignmentRules(ctx, orgID)
	if err != nil {
		return nil, "", err
	}
	var owner int64
	reason := ""
	for _, rule := range rules {
		if !rule.Active || len(rule.UserIDs) == 0 || !matchesAssignment(rule, a) {
			continue
		}
		if owner, err = s.repo.NextMember(ctx, orgID, rule); err != nil {
			return nil, "", err
		}
		reason = "Assignment rule: " + rule.Name
		break
	}
	if reason == "" {
		settings, err := s.repo.Settings(ctx, orgID)
		if err != nil {
			return nil, "", err
		}
		if settings.DefaultOwnerID == nil {
			return nil, "", nil
		}
		owner, reason = *settings.DefaultOwnerID, "Default owner"
	}
	assigned, err := s.contacts.AssignOwner(ctx, orgID, contactID, owner, reason)
	if err != nil || !assigned {
		return nil, "", err
	}
	return &owner, reason, nil
}

// Configuration. Reading and changing it needs principal.ManagesSettings.

func settingsAccess(p principal.Principal) error {
	if !p.ManagesSettings() {
		return ErrForbidden
	}
	return nil
}

func (s *Service) Settings(ctx context.Context, p principal.Principal) (Settings, error) {
	if err := settingsAccess(p); err != nil {
		return Settings{}, err
	}
	return s.repo.Settings(ctx, p.OrgID)
}

// SettingsPatch lists settings to change; nil fields are unchanged.
// ClearDefaultOwner removes the default owner.
type SettingsPatch struct {
	HotThreshold       *int   `json:"hot_threshold"`
	WarmThreshold      *int   `json:"warm_threshold"`
	DefaultOwnerID     *int64 `json:"default_owner_id"`
	ClearDefaultOwner  bool   `json:"clear_default_owner"`
	HotFollowupMinutes *int   `json:"hot_followup_minutes"`
}

func (s *Service) UpdateSettings(ctx context.Context, p principal.Principal, patch SettingsPatch) (Settings, error) {
	cur, err := s.Settings(ctx, p)
	if err != nil {
		return Settings{}, err
	}
	if patch.HotThreshold != nil {
		cur.HotThreshold = *patch.HotThreshold
	}
	if patch.WarmThreshold != nil {
		cur.WarmThreshold = *patch.WarmThreshold
	}
	if patch.DefaultOwnerID != nil {
		cur.DefaultOwnerID = patch.DefaultOwnerID
	}
	if patch.HotFollowupMinutes != nil {
		cur.HotFollowupMinutes = *patch.HotFollowupMinutes
	}
	if patch.ClearDefaultOwner {
		cur.DefaultOwnerID = nil
	}
	if err := validateSettings(cur); err != nil {
		return Settings{}, err
	}
	return s.repo.SaveSettings(ctx, p.OrgID, cur)
}

func (s *Service) Sources(ctx context.Context, p principal.Principal) ([]Source, error) {
	if err := settingsAccess(p); err != nil {
		return nil, err
	}
	return s.repo.Sources(ctx, p.OrgID)
}

func validSourceName(name string) error {
	if name == "" {
		return required("name", "Name is required.")
	}
	if utf8.RuneCountInString(name) > maxSource {
		return invalid("name", "Name must be at most 100 characters.")
	}
	return nil
}

func (s *Service) CreateSource(ctx context.Context, p principal.Principal, name, channel string) (Source, error) {
	if err := settingsAccess(p); err != nil {
		return Source{}, err
	}
	name = strings.TrimSpace(name)
	if err := validSourceName(name); err != nil {
		return Source{}, err
	}
	if channel == "" {
		channel = ChannelWebForm
	}
	if channel != ChannelWebForm && channel != ChannelAPI {
		return Source{}, invalid("channel", "Channel must be web_form or api.")
	}
	return s.repo.CreateSource(ctx, p.OrgID, name, channel)
}

func (s *Service) UpdateSource(ctx context.Context, p principal.Principal, id int64, name *string, active *bool, rotateKey bool) (Source, error) {
	if err := settingsAccess(p); err != nil {
		return Source{}, err
	}
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if err := validSourceName(trimmed); err != nil {
			return Source{}, err
		}
		name = &trimmed
	}
	return s.repo.UpdateSource(ctx, p.OrgID, id, name, active, rotateKey)
}

// SubmitForm is a public form submission to the source with key.
func (s *Service) SubmitForm(ctx context.Context, key string, l Lead) (Result, error) {
	src, orgID, err := s.repo.SourceByKey(ctx, key)
	if err != nil {
		return Result{}, err
	}
	l.Channel, l.Source = src.Channel, src.Name
	l.ExternalID, l.ActorUserID, l.OwnerID = "", nil, nil
	return s.Capture(ctx, orgID, l)
}

func (s *Service) WhatsAppAccounts(ctx context.Context, p principal.Principal) ([]WhatsAppAccount, error) {
	if err := settingsAccess(p); err != nil {
		return nil, err
	}
	return s.repo.WhatsAppAccounts(ctx, p.OrgID)
}

func (s *Service) CreateWhatsAppAccount(ctx context.Context, p principal.Principal, a WhatsAppAccount) (WhatsAppAccount, error) {
	if err := settingsAccess(p); err != nil {
		return WhatsAppAccount{}, err
	}
	a.PhoneNumberID = strings.TrimSpace(a.PhoneNumberID)
	a.DisplayPhone = strings.TrimSpace(a.DisplayPhone)
	a.SourceName = strings.TrimSpace(a.SourceName)
	if a.PhoneNumberID == "" || strings.Trim(a.PhoneNumberID, "0123456789") != "" {
		return WhatsAppAccount{}, invalid("phone_number_id", "phone_number_id must be the numeric id Meta shows for the number.")
	}
	if a.SourceName == "" {
		a.SourceName = "WhatsApp"
	}
	if err := validSourceName(a.SourceName); err != nil {
		return WhatsAppAccount{}, err
	}
	return s.repo.CreateWhatsAppAccount(ctx, p.OrgID, a)
}

func (s *Service) DeleteWhatsAppAccount(ctx context.Context, p principal.Principal, id int64) error {
	if err := settingsAccess(p); err != nil {
		return err
	}
	return s.repo.DeleteWhatsAppAccount(ctx, p.OrgID, id)
}

func (s *Service) ScoringRules(ctx context.Context, p principal.Principal) ([]ScoringRule, error) {
	if err := settingsAccess(p); err != nil {
		return nil, err
	}
	return s.repo.ScoringRules(ctx, p.OrgID)
}

func (s *Service) CreateScoringRule(ctx context.Context, p principal.Principal, r ScoringRule) (ScoringRule, error) {
	if err := settingsAccess(p); err != nil {
		return ScoringRule{}, err
	}
	if err := validateScoringRule(&r); err != nil {
		return ScoringRule{}, err
	}
	return s.repo.CreateScoringRule(ctx, p.OrgID, r)
}

// ScoringRulePatch lists fields to change; nil fields are unchanged.
type ScoringRulePatch struct {
	Name      *string `json:"name"`
	Criterion *string `json:"criterion"`
	Value     *string `json:"value"`
	Points    *int    `json:"points"`
	Active    *bool   `json:"active"`
}

func (s *Service) UpdateScoringRule(ctx context.Context, p principal.Principal, id int64, patch ScoringRulePatch) (ScoringRule, error) {
	if err := settingsAccess(p); err != nil {
		return ScoringRule{}, err
	}
	r, err := s.repo.ScoringRule(ctx, p.OrgID, id)
	if err != nil {
		return ScoringRule{}, err
	}
	apply(&r.Name, patch.Name)
	apply(&r.Criterion, patch.Criterion)
	apply(&r.Value, patch.Value)
	apply(&r.Points, patch.Points)
	apply(&r.Active, patch.Active)
	if err := validateScoringRule(&r); err != nil {
		return ScoringRule{}, err
	}
	return s.repo.UpdateScoringRule(ctx, p.OrgID, r)
}

func (s *Service) DeleteScoringRule(ctx context.Context, p principal.Principal, id int64) error {
	if err := settingsAccess(p); err != nil {
		return err
	}
	return s.repo.DeleteScoringRule(ctx, p.OrgID, id)
}

func (s *Service) AssignmentRules(ctx context.Context, p principal.Principal) ([]AssignmentRule, error) {
	if err := settingsAccess(p); err != nil {
		return nil, err
	}
	return s.repo.AssignmentRules(ctx, p.OrgID)
}

func (s *Service) CreateAssignmentRule(ctx context.Context, p principal.Principal, r AssignmentRule) (AssignmentRule, error) {
	if err := settingsAccess(p); err != nil {
		return AssignmentRule{}, err
	}
	if err := validateAssignmentRule(&r); err != nil {
		return AssignmentRule{}, err
	}
	r.ID = 0
	return s.repo.SaveAssignmentRule(ctx, p.OrgID, r)
}

// AssignmentRulePatch lists fields to change; nil fields are unchanged.
// UserIDs, when set, replaces the members.
type AssignmentRulePatch struct {
	Name      *string  `json:"name"`
	Position  *int     `json:"position"`
	Criterion *string  `json:"criterion"`
	Value     *string  `json:"value"`
	Active    *bool    `json:"active"`
	UserIDs   *[]int64 `json:"user_ids"`
}

func (s *Service) UpdateAssignmentRule(ctx context.Context, p principal.Principal, id int64, patch AssignmentRulePatch) (AssignmentRule, error) {
	if err := settingsAccess(p); err != nil {
		return AssignmentRule{}, err
	}
	r, err := s.repo.AssignmentRule(ctx, p.OrgID, id)
	if err != nil {
		return AssignmentRule{}, err
	}
	apply(&r.Name, patch.Name)
	apply(&r.Position, patch.Position)
	apply(&r.Criterion, patch.Criterion)
	apply(&r.Value, patch.Value)
	apply(&r.Active, patch.Active)
	apply(&r.UserIDs, patch.UserIDs)
	if err := validateAssignmentRule(&r); err != nil {
		return AssignmentRule{}, err
	}
	return s.repo.SaveAssignmentRule(ctx, p.OrgID, r)
}

func (s *Service) DeleteAssignmentRule(ctx context.Context, p principal.Principal, id int64) error {
	if err := settingsAccess(p); err != nil {
		return err
	}
	return s.repo.DeleteAssignmentRule(ctx, p.OrgID, id)
}

func apply[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}
