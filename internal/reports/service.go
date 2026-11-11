package reports

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/deepashearaion/manage-crm/internal/platform/mail"
	"github.com/deepashearaion/manage-crm/internal/platform/money"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

type Service struct {
	repo        *Repository
	mailer      mail.Mailer
	frontendURL string
	logger      *slog.Logger
	now         func() time.Time
}

func NewService(repo *Repository, mailer mail.Mailer, frontendURL string, logger *slog.Logger, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, mailer: mailer, frontendURL: frontendURL, logger: logger, now: now}
}

// owner is the record owner a report is limited to: with reports.view "own"
// a user sees only their own figures.
func owner(p principal.Principal) *int64 {
	if p.SeesAll(principal.Reports) {
		return nil
	}
	return &p.UserID
}

// SalesDashboard is the legacy dashboard for the caller.
func (s *Service) SalesDashboard(ctx context.Context, p principal.Principal) (SalesDashboard, error) {
	if !p.Can(principal.Reports, principal.View) {
		return SalesDashboard{}, ErrForbidden
	}
	return s.repo.SalesDashboard(ctx, p.OrgID, owner(p), s.now())
}

func (s *Service) Dashboard(ctx context.Context, p principal.Principal, from, to string) (Dashboard, error) {
	if !p.Can(principal.Reports, principal.View) {
		return Dashboard{}, ErrForbidden
	}
	period, err := parsePeriod(from, to, s.now())
	if err != nil {
		return Dashboard{}, err
	}
	return s.repo.Dashboard(ctx, p.OrgID, owner(p), period)
}

func (s *Service) Funnel(ctx context.Context, p principal.Principal, from, to string) (Funnel, error) {
	if !p.Can(principal.Reports, principal.View) {
		return Funnel{}, ErrForbidden
	}
	period, err := parsePeriod(from, to, s.now())
	if err != nil {
		return Funnel{}, err
	}
	return s.repo.Funnel(ctx, p.OrgID, owner(p), period)
}

func (s *Service) Pipeline(ctx context.Context, p principal.Principal) ([]PipelineStage, error) {
	if !p.Can(principal.Reports, principal.View) {
		return nil, ErrForbidden
	}
	return s.repo.Pipeline(ctx, p.OrgID, owner(p), s.now())
}

func (s *Service) Sources(ctx context.Context, p principal.Principal, from, to string) ([]SourceRow, error) {
	if !p.Can(principal.Reports, principal.View) {
		return nil, ErrForbidden
	}
	period, err := parsePeriod(from, to, s.now())
	if err != nil {
		return nil, err
	}
	return s.repo.Sources(ctx, p.OrgID, owner(p), period)
}

// Reps compares users; it needs reports.view for the whole org.
func (s *Service) Reps(ctx context.Context, p principal.Principal, from, to string) ([]RepRow, error) {
	if !p.SeesAll(principal.Reports) {
		return nil, ErrForbidden
	}
	period, err := parsePeriod(from, to, s.now())
	if err != nil {
		return nil, err
	}
	return s.repo.Reps(ctx, p.OrgID, period, s.now())
}

// Revenue covers the last `months` IST months, ending with the current one.
func (s *Service) Revenue(ctx context.Context, p principal.Principal, months int) ([]RevenueMonth, error) {
	if !p.Can(principal.Reports, principal.View) {
		return nil, ErrForbidden
	}
	if months == 0 {
		months = 12
	}
	if months < 1 || months > 36 {
		return nil, &FieldError{"months", "invalid", "months must be between 1 and 36."}
	}
	now := s.now().In(ist)
	end := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, ist)
	start := end.AddDate(0, -months, 0)
	out := make([]RevenueMonth, months)
	for i := range out {
		out[i].Month = start.AddDate(0, i, 0).Format("2006-01")
	}
	return out, s.repo.Revenue(ctx, p.OrgID, owner(p), start, end, out)
}

func (s *Service) Settings(ctx context.Context, p principal.Principal) (Settings, error) {
	return s.repo.Settings(ctx, p.OrgID)
}

func (s *Service) SaveSettings(ctx context.Context, p principal.Principal, weekly bool) (Settings, error) {
	if !p.Can(principal.Settings, principal.Edit) {
		return Settings{}, ErrForbidden
	}
	return s.repo.SaveSettings(ctx, p.OrgID, weekly)
}

// Weekly reports (the diagram's "auto reports").

// sendAfter is when, on Monday (IST), last week's report goes out.
const sendAfter = 9 * time.Hour

// lastWeek returns the previous Monday-to-Sunday week (IST), or ok=false
// before this week's Monday 09:00 IST, when it is not yet time to send it.
func lastWeek(now time.Time) (Period, bool) {
	t := now.In(ist)
	daysSinceMonday := (int(t.Weekday()) + 6) % 7
	monday := time.Date(t.Year(), t.Month(), t.Day()-daysSinceMonday, 0, 0, 0, 0, ist)
	if t.Before(monday.Add(sendAfter)) {
		return Period{}, false
	}
	start := monday.AddDate(0, 0, -7)
	return Period{From: start.Format(dayLayout), To: monday.AddDate(0, 0, -1).Format(dayLayout), start: start, end: monday}, true
}

// SendWeekly emails last week's summary to each org's owners, admins and
// managers, once per week (from Monday 09:00 IST). It is a scheduler job.
// Orgs with no activity get no email.
func (s *Service) SendWeekly(ctx context.Context) error {
	period, ok := lastWeek(s.now())
	if !ok {
		return nil
	}
	orgs, err := s.repo.ClaimWeekly(ctx, period.From)
	if err != nil {
		return err
	}
	for _, orgID := range orgs {
		if err := s.sendWeekly(ctx, orgID, period); err != nil {
			s.logger.ErrorContext(ctx, "weekly report", slog.Int64("org_id", orgID), slog.Any("error", err))
		}
	}
	return nil
}

func (s *Service) sendWeekly(ctx context.Context, orgID int64, period Period) error {
	d, err := s.repo.Dashboard(ctx, orgID, nil, period)
	if err != nil {
		return err
	}
	if d.NewLeads == 0 && d.Won.Count == 0 && d.Lost.Count == 0 && d.OpenDeals.Count == 0 {
		return nil
	}
	sources, err := s.repo.Sources(ctx, orgID, nil, period)
	if err != nil {
		return err
	}
	name, err := s.repo.OrgName(ctx, orgID)
	if err != nil {
		return err
	}
	recipients, err := s.repo.Managers(ctx, orgID)
	if err != nil {
		return err
	}
	subject := fmt.Sprintf("%s: weekly report, %s to %s", name, period.From, period.To)
	text := weeklyText(name, d, sources, s.frontendURL)
	sent := 0
	for _, rc := range recipients {
		if err := s.mailer.Send(ctx, mail.Message{To: rc.Email, Subject: subject, Text: "Hi " + rc.Name + ",\n\n" + text}); err != nil {
			s.logger.ErrorContext(ctx, "weekly report email", slog.Int64("user_id", rc.ID), slog.Any("error", err))
			continue
		}
		sent++
	}
	return s.repo.SetRecipients(ctx, orgID, period.From, sent)
}

func weeklyText(org string, d Dashboard, sources []SourceRow, frontendURL string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Here is %s's week, %s to %s (IST).\n\n", org, d.Period.From, d.Period.To)
	fmt.Fprintf(&b, "New leads: %d (hot now: %d)\n", d.NewLeads, d.HotLeads)
	fmt.Fprintf(&b, "Deals won: %d, worth %s\n", d.Won.Count, money.Format(d.Won.ValuePaise))
	fmt.Fprintf(&b, "Deals lost: %d, worth %s\n", d.Lost.Count, money.Format(d.Lost.ValuePaise))
	fmt.Fprintf(&b, "Win rate: %.1f%%\n", d.WinRatePercent)
	fmt.Fprintf(&b, "New customers: %d\n", d.NewCustomers)
	fmt.Fprintf(&b, "Invoiced: %s\n", money.Format(d.InvoicedPaise))
	fmt.Fprintf(&b, "Open pipeline: %d deals, %s\n", d.OpenDeals.Count, money.Format(d.OpenDeals.ValuePaise))
	fmt.Fprintf(&b, "ARR: %s (MRR %s)\n", money.Format(d.ARRPaise), money.Format(d.MRRPaise))
	if len(sources) > 0 {
		b.WriteString("\nLeads by source:\n")
		for i, src := range sources {
			if i == 5 {
				break
			}
			fmt.Fprintf(&b, "  %s: %d leads, %d hot, %d customers\n", src.Source, src.Leads, src.HotLeads, src.Customers)
		}
	}
	fmt.Fprintf(&b, "\nSee the live dashboard: %s/reports\n", frontendURL)
	b.WriteString("\nOwners, admins and managers can turn this email off in the report settings.")
	return b.String()
}
