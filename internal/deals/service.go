package deals

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

type Service struct {
	repo     *Repository
	contacts Contacts
	events   Events
	logger   *slog.Logger
	now      func() time.Time
}

// NewService builds the service; events may be nil.
func NewService(repo *Repository, contacts Contacts, events Events, logger *slog.Logger) *Service {
	return &Service{repo: repo, contacts: contacts, events: events, logger: logger, now: time.Now}
}

// Contact lifecycle stages this module moves contacts between.
const (
	lifecycleOpportunity = "opportunity"
	lifecycleCustomer    = "customer"
	lifecycleLost        = "lost"
)

const module = principal.Deals

// scope is what p may see: their org, and only the deals they own unless
// their role sees all deals.
func scope(p principal.Principal) Scope {
	s := Scope{OrgID: p.OrgID}
	if !p.SeesAll(module) {
		s.OwnerID = &p.UserID
	}
	return s
}

// withProbability fills the computed win probability.
func (s *Service) withProbability(d Deal) Deal {
	d.WinProbability, d.ProbabilityNotes = winProbability(d.Status, d.stageProbability, d.StageEnteredAt, d.ExpectedCloseDate, s.now())
	return d
}

// Stages

func (s *Service) Stages(ctx context.Context, p principal.Principal) ([]Stage, error) {
	return s.repo.Stages(ctx, scope(p))
}

// StageInput creates or changes a stage. Kind is fixed once created.
type StageInput struct {
	Name           *string   `json:"name"`
	Position       *int      `json:"position"`
	Kind           *string   `json:"kind"`
	Probability    *int      `json:"probability"`
	Guidance       *string   `json:"guidance"`
	RequiredFields *[]string `json:"required_fields"`
}

func validateStage(st *Stage) error {
	st.Name = strings.TrimSpace(st.Name)
	st.Guidance = strings.TrimSpace(st.Guidance)
	switch {
	case st.Name == "":
		return fieldErr("name", "required", "Name is required.")
	case utf8.RuneCountInString(st.Name) > 100:
		return fieldErr("name", "invalid", "Name must be at most 100 characters.")
	case !slices.Contains([]string{KindOpen, KindWon, KindLost}, st.Kind):
		return fieldErr("kind", "invalid", "Kind must be open, won or lost.")
	case st.Probability < 0 || st.Probability > 100:
		return fieldErr("probability", "invalid", "Probability must be between 0 and 100.")
	}
	// Outcomes are certain.
	switch st.Kind {
	case KindWon:
		st.Probability = 100
	case KindLost:
		st.Probability = 0
	}
	if st.RequiredFields == nil {
		st.RequiredFields = []string{}
	}
	for _, f := range st.RequiredFields {
		if !slices.Contains(requirableFields, f) {
			return fieldErr("required_fields", "invalid", "Required fields may be amount, expected_close_date and contact.")
		}
	}
	slices.Sort(st.RequiredFields)
	st.RequiredFields = slices.Compact(st.RequiredFields)
	return nil
}

func (s *Service) CreateStage(ctx context.Context, p principal.Principal, in StageInput) (Stage, error) {
	if !p.Can(principal.Settings, principal.Edit) {
		return Stage{}, ErrForbidden
	}
	st := Stage{Kind: KindOpen, Probability: 10}
	applyStage(&st, in)
	if in.Kind != nil {
		st.Kind = *in.Kind
	}
	if err := validateStage(&st); err != nil {
		return Stage{}, err
	}
	return s.repo.CreateStage(ctx, p.OrgID, st)
}

func (s *Service) UpdateStage(ctx context.Context, p principal.Principal, id int64, in StageInput) (Stage, error) {
	if !p.Can(principal.Settings, principal.Edit) {
		return Stage{}, ErrForbidden
	}
	st, err := s.repo.Stage(ctx, p.OrgID, id)
	if err != nil {
		return Stage{}, err
	}
	if in.Kind != nil && *in.Kind != st.Kind {
		return Stage{}, fieldErr("kind", "invalid", "A stage's kind cannot change; create a new stage instead.")
	}
	applyStage(&st, in)
	if err := validateStage(&st); err != nil {
		return Stage{}, err
	}
	return s.repo.UpdateStage(ctx, p.OrgID, st)
}

func applyStage(st *Stage, in StageInput) {
	apply(&st.Name, in.Name)
	apply(&st.Position, in.Position)
	apply(&st.Probability, in.Probability)
	apply(&st.Guidance, in.Guidance)
	apply(&st.RequiredFields, in.RequiredFields)
}

func (s *Service) DeleteStage(ctx context.Context, p principal.Principal, id int64) error {
	if !p.Can(principal.Settings, principal.Edit) {
		return ErrForbidden
	}
	return s.repo.DeleteStage(ctx, p.OrgID, id)
}

// Deals

// validateDeal normalizes and checks a deal's editable fields.
func validateDeal(d *Deal) error {
	d.Title = strings.TrimSpace(d.Title)
	d.Description = strings.TrimSpace(d.Description)
	if d.BillingType == "" {
		d.BillingType = BillingOneTime
	}
	switch {
	case d.Title == "":
		return fieldErr("title", "required", "Title is required.")
	case utf8.RuneCountInString(d.Title) > 200:
		return fieldErr("title", "invalid", "Title must be at most 200 characters.")
	case utf8.RuneCountInString(d.Description) > 10000:
		return fieldErr("description", "invalid", "Description must be at most 10000 characters.")
	case d.AmountPaise < 0:
		return fieldErr("amount_paise", "invalid", "Amount must not be negative.")
	}
	switch d.BillingType {
	case BillingOneTime:
		d.BillingPeriod = nil
	case BillingRecurring:
		if d.BillingPeriod == nil || !slices.Contains(billingPeriods, *d.BillingPeriod) {
			return fieldErr("billing_period", "invalid", "Recurring deals need a billing period: monthly, quarterly or yearly.")
		}
	default:
		return fieldErr("billing_type", "invalid", "Billing type must be one_time or recurring.")
	}
	if d.ExpectedCloseDate != nil {
		if _, err := time.Parse(dateLayout, *d.ExpectedCloseDate); err != nil {
			return fieldErr("expected_close_date", "invalid", "Expected close date must be YYYY-MM-DD.")
		}
	}
	return nil
}

// missingFields lists what stage st requires that d lacks.
func missingFields(st Stage, d Deal) []string {
	var missing []string
	for _, f := range st.RequiredFields {
		switch {
		case f == FieldAmount && d.AmountPaise <= 0,
			f == FieldExpectedCloseDate && d.ExpectedCloseDate == nil,
			f == FieldContact && d.ContactID == nil:
			missing = append(missing, f)
		}
	}
	return missing
}

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

func (s *Service) List(ctx context.Context, p principal.Principal, f Filter) ([]Deal, error) {
	if !p.Can(module, principal.View) {
		return nil, ErrForbidden
	}
	if f.Status != "" && !slices.Contains([]string{KindOpen, KindWon, KindLost}, f.Status) {
		return nil, fieldErr("status", "invalid", "Status must be open, won or lost.")
	}
	if f.Limit <= 0 {
		f.Limit = defaultPageSize
	}
	f.Limit = min(f.Limit, maxPageSize)
	list, err := s.repo.List(ctx, scope(p), f)
	for i := range list {
		list[i] = s.withProbability(list[i])
	}
	return list, err
}

func (s *Service) Get(ctx context.Context, p principal.Principal, id int64) (Deal, error) {
	if !p.Can(module, principal.View) {
		return Deal{}, ErrForbidden
	}
	d, err := s.repo.Get(ctx, scope(p), id)
	if err != nil {
		return Deal{}, err
	}
	return s.withProbability(d), nil
}

// Create adds a deal in an open stage (the first one by default), owned by
// the caller unless they may create deals for others; the stage's
// required fields must be filled.
func (s *Service) Create(ctx context.Context, p principal.Principal, in Input) (Deal, error) {
	owner := p.UserID
	if in.OwnerID != nil {
		owner = *in.OwnerID
	}
	if !p.CanOn(module, principal.Create, &owner) {
		return Deal{}, ErrForbidden
	}
	d := Deal{
		Title: in.Title, Description: in.Description, ContactID: in.ContactID, OwnerID: &owner,
		AmountPaise: in.AmountPaise, BillingType: in.BillingType, BillingPeriod: in.BillingPeriod,
		ExpectedCloseDate: in.ExpectedCloseDate,
	}
	if err := validateDeal(&d); err != nil {
		return Deal{}, err
	}
	var st Stage
	var err error
	if in.StageID != nil {
		st, err = s.repo.Stage(ctx, p.OrgID, *in.StageID)
		if errors.Is(err, ErrStageNotFound) {
			return Deal{}, fieldErr("stage_id", "invalid", "No such stage.")
		}
	} else {
		st, err = s.repo.FirstOpenStage(ctx, p.OrgID)
	}
	if err != nil {
		return Deal{}, err
	}
	if st.Kind != KindOpen {
		return Deal{}, fieldErr("stage_id", "invalid", "New deals start in an open stage; move them to won or lost afterwards.")
	}
	if missing := missingFields(st, d); len(missing) > 0 {
		return Deal{}, &MissingFieldsError{Stage: st.Name, Fields: missing}
	}
	if d.ContactID != nil {
		if err := s.contacts.Visible(ctx, p, *d.ContactID); err != nil {
			return Deal{}, err
		}
	}
	d.StageID = st.ID
	id, err := s.repo.Create(ctx, p.OrgID, d, p.UserID)
	if err != nil {
		return Deal{}, err
	}
	if d.ContactID != nil {
		s.contactEffects(ctx, p, *d.ContactID, "deal_created", "Deal created: "+d.Title, id, st, KindOpen)
	}
	return s.Get(ctx, p, id)
}

// Update changes a deal's fields (not its stage) for a caller who may change it.
func (s *Service) Update(ctx context.Context, p principal.Principal, id int64, patch Patch) (Deal, error) {
	d, err := s.repo.Get(ctx, scope(p), id)
	if err != nil {
		return Deal{}, err
	}
	if !p.CanOn(module, principal.Edit, d.OwnerID) {
		return Deal{}, ErrForbidden
	}
	if patch.OwnerID != nil && (d.OwnerID == nil || *patch.OwnerID != *d.OwnerID) {
		if !p.CanReassign(module) {
			return Deal{}, ErrForbidden
		}
		d.OwnerID = patch.OwnerID
	}
	apply(&d.Title, patch.Title)
	apply(&d.Description, patch.Description)
	apply(&d.AmountPaise, patch.AmountPaise)
	apply(&d.BillingType, patch.BillingType)
	if patch.BillingPeriod != nil {
		d.BillingPeriod = patch.BillingPeriod
	}
	if patch.ExpectedCloseDate != nil {
		d.ExpectedCloseDate = patch.ExpectedCloseDate
	}
	if patch.ClearExpectedCloseDate {
		d.ExpectedCloseDate = nil
	}
	contactChanged := false
	if patch.ContactID != nil && (d.ContactID == nil || *patch.ContactID != *d.ContactID) {
		if err := s.contacts.Visible(ctx, p, *patch.ContactID); err != nil {
			return Deal{}, err
		}
		d.ContactID, contactChanged = patch.ContactID, true
	}
	if patch.ClearContact {
		d.ContactID = nil
	}
	if err := validateDeal(&d); err != nil {
		return Deal{}, err
	}
	if err := s.repo.Save(ctx, scope(p), d); err != nil {
		return Deal{}, err
	}
	if contactChanged && d.Status == KindOpen {
		st, err := s.repo.Stage(ctx, p.OrgID, d.StageID)
		if err == nil {
			s.contactEffects(ctx, p, *d.ContactID, "deal_created", "Deal linked: "+d.Title, d.ID, st, KindOpen)
		}
	}
	return s.Get(ctx, p, id)
}

// Move puts a deal in another stage: the guided step. The stage's
// required fields must be filled, and a lost deal needs a reason. Winning
// makes the contact a customer; losing makes them lost unless they have
// other open or won deals.
func (s *Service) Move(ctx context.Context, p principal.Principal, id, stageID int64, lostReason string) (Deal, error) {
	d, err := s.repo.Get(ctx, scope(p), id)
	if err != nil {
		return Deal{}, err
	}
	if !p.CanOn(module, principal.Edit, d.OwnerID) {
		return Deal{}, ErrForbidden
	}
	st, err := s.repo.Stage(ctx, p.OrgID, stageID)
	if errors.Is(err, ErrStageNotFound) {
		return Deal{}, fieldErr("stage_id", "invalid", "No such stage.")
	}
	if err != nil {
		return Deal{}, err
	}
	if st.ID == d.StageID {
		return s.withProbability(d), nil
	}
	if missing := missingFields(st, d); len(missing) > 0 {
		return Deal{}, &MissingFieldsError{Stage: st.Name, Fields: missing}
	}
	lostReason = strings.TrimSpace(lostReason)
	if st.Kind == KindLost {
		if lostReason == "" {
			return Deal{}, fieldErr("lost_reason", "required", "Say why the deal was lost.")
		}
		if utf8.RuneCountInString(lostReason) > 500 {
			return Deal{}, fieldErr("lost_reason", "invalid", "Lost reason must be at most 500 characters.")
		}
	} else {
		lostReason = ""
	}
	if err := s.repo.Move(ctx, scope(p), id, st, lostReason, p.UserID); err != nil {
		return Deal{}, err
	}
	moved, err := s.Get(ctx, p, id)
	if err != nil {
		return Deal{}, err
	}

	if d.ContactID != nil {
		kind, summary := "deal_stage_changed", fmt.Sprintf("Deal %s: %s → %s", d.Title, d.StageName, st.Name)
		switch {
		case st.Kind == KindWon && d.Status != KindWon:
			kind, summary = "deal_won", "Deal won: "+d.Title
		case st.Kind == KindLost && d.Status != KindLost:
			kind, summary = "deal_lost", fmt.Sprintf("Deal lost: %s (%s)", d.Title, lostReason)
		}
		s.contactEffects(ctx, p, *d.ContactID, kind, summary, id, st, d.Status)
	}

	if s.events != nil && st.Kind != d.Status && st.Kind != KindOpen {
		o := Outcome{
			OrgID: p.OrgID, DealID: id, Title: moved.Title, ContactID: moved.ContactID, OwnerID: moved.OwnerID,
			ValuePaise: moved.ValuePaise, BillingType: moved.BillingType, AmountPaise: moved.AmountPaise,
			BillingPeriod: moved.BillingPeriod, LostReason: lostReason, ActorID: p.UserID,
		}
		if moved.ClosedAt != nil {
			o.ClosedAt = *moved.ClosedAt
		}
		handler, name := s.events.DealWon, "deal won"
		if st.Kind == KindLost {
			handler, name = s.events.DealLost, "deal lost"
		}
		if err := handler(ctx, o); err != nil {
			s.logger.ErrorContext(ctx, "workflow event failed", slog.String("event", name), slog.Any("error", err))
		}
	}
	return moved, nil
}

// contactEffects records a deal event on the contact's timeline and moves
// the contact's lifecycle stage to match. Failures are logged: the deal
// change has happened.
func (s *Service) contactEffects(ctx context.Context, p principal.Principal, contactID int64, kind, summary string, dealID int64, st Stage, previousStatus string) {
	log := func(what string, err error) {
		if err != nil && !errors.Is(err, ErrContactNotFound) {
			s.logger.ErrorContext(ctx, what, slog.Int64("deal_id", dealID), slog.Any("error", err))
		}
	}
	log("deal timeline entry", s.contacts.Record(ctx, p.OrgID, contactID, kind, summary,
		map[string]any{"deal_id": dealID, "stage_id": st.ID, "stage": st.Name}, p.UserID))

	switch st.Kind {
	case KindOpen:
		log("contact lifecycle", s.contacts.SetLifecycle(ctx, p.OrgID, contactID, lifecycleOpportunity,
			[]string{"lead", "mql", "sql", lifecycleLost}, p.UserID))
	case KindWon:
		log("contact lifecycle", s.contacts.SetLifecycle(ctx, p.OrgID, contactID, lifecycleCustomer, nil, p.UserID))
	case KindLost:
		if previousStatus == KindLost {
			return
		}
		others, err := s.repo.OtherLiveDeals(ctx, p.OrgID, contactID, dealID)
		if err != nil || others > 0 {
			log("contact lifecycle", err)
			return
		}
		log("contact lifecycle", s.contacts.SetLifecycle(ctx, p.OrgID, contactID, lifecycleLost,
			[]string{lifecycleOpportunity}, p.UserID))
	}
}

func (s *Service) Delete(ctx context.Context, p principal.Principal, id int64) error {
	d, err := s.repo.Get(ctx, scope(p), id)
	if err != nil {
		return err
	}
	if !p.CanOn(module, principal.Delete, d.OwnerID) {
		return ErrForbidden
	}
	return s.repo.Delete(ctx, scope(p), id)
}

func (s *Service) History(ctx context.Context, p principal.Principal, id int64) ([]HistoryEntry, error) {
	if !p.Can(module, principal.View) {
		return nil, ErrForbidden
	}
	if _, err := s.repo.Get(ctx, scope(p), id); err != nil {
		return nil, err
	}
	return s.repo.History(ctx, p.OrgID, id)
}

// Forecast returns the forecast for `months` IST months from `from`
// (YYYY-MM, default this month), over the deals the caller may see,
// optionally only one owner's.
func (s *Service) Forecast(ctx context.Context, p principal.Principal, from string, months int, ownerID int64) (Forecast, error) {
	if !p.Can(module, principal.View) {
		return Forecast{}, ErrForbidden
	}
	now := s.now()
	start, err := monthStart(from, now)
	if err != nil {
		return Forecast{}, fieldErr("from", "invalid", "from must be a month as YYYY-MM.")
	}
	if months == 0 {
		months = defaultMonths
	}
	if months < 1 || months > maxMonths {
		return Forecast{}, fieldErr("months", "invalid", fmt.Sprintf("months must be between 1 and %d.", maxMonths))
	}
	open, won, err := s.repo.ForecastRows(ctx, scope(p), ownerID, start, start.AddDate(0, months, 0))
	if err != nil {
		return Forecast{}, err
	}
	return buildForecast(start, months, open, won, now), nil
}

func apply[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

// Visible returns a deal p may see (for other modules, e.g. invoicing).
func (s *Service) Visible(ctx context.Context, p principal.Principal, id int64) (Deal, error) {
	if !p.Can(module, principal.View) {
		return Deal{}, ErrNotFound
	}
	return s.repo.Get(ctx, scope(p), id)
}
