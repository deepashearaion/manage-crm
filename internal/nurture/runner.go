package nurture

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/deepashearaion/manage-crm/internal/platform/mail"
	"github.com/deepashearaion/manage-crm/internal/platform/whatsapp"
)

// ist is India Standard Time (UTC+5:30, no daylight saving).
var ist = time.FixedZone("IST", 5*3600+1800)

const (
	// Quiet hours (IST): nothing is sent from 21:00 to 09:00.
	quietFrom = 21
	quietTo   = 9

	batchSize   = 50
	maxAttempts = 5
	// WhatsApp's customer service window: free-form messages are allowed
	// within 24 hours of the customer's last message.
	serviceWindow = 24 * time.Hour
)

// inQuietHours reports whether t is in quiet hours, and if so when they end.
func inQuietHours(t time.Time) (bool, time.Time) {
	local := t.In(ist)
	h := local.Hour()
	if h >= quietTo && h < quietFrom {
		return false, time.Time{}
	}
	end := time.Date(local.Year(), local.Month(), local.Day(), quietTo, 0, 0, 0, ist)
	if h >= quietFrom {
		end = end.AddDate(0, 0, 1)
	}
	return true, end
}

// backoff is the wait before retry n (1-based): 5, 20, 45, 80 minutes.
func backoff(n int) time.Duration { return time.Duration(n*n*5) * time.Minute }

// render fills a step's placeholders.
func render(text string, rc Recipient) string {
	full := strings.TrimSpace(rc.FirstName + " " + rc.LastName)
	return strings.NewReplacer(
		"{{first_name}}", rc.FirstName,
		"{{last_name}}", rc.LastName,
		"{{full_name}}", full,
	).Replace(text)
}

// RunDue sends every due step. It is the scheduler job.
func (s *Service) RunDue(ctx context.Context) error {
	for {
		now := s.now()
		batch, err := s.repo.ClaimDue(ctx, now, batchSize)
		if err != nil {
			return err
		}
		sequences := map[int64]Sequence{}
		for _, e := range batch {
			if err := s.process(ctx, e, sequences, now); err != nil {
				s.logger.ErrorContext(ctx, "nurture step", slog.Int64("enrollment_id", e.ID), slog.Any("error", err))
			}
		}
		if len(batch) < batchSize || ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

// skip is a step that cannot be sent to this contact; the enrollment moves on.
type skip struct{ reason string }

func (e skip) Error() string { return e.reason }

func (s *Service) process(ctx context.Context, e Enrollment, cache map[int64]Sequence, now time.Time) error {
	seq, ok := cache[e.SequenceID]
	if !ok {
		var err error
		if seq, err = s.repo.Sequence(ctx, e.orgID, e.SequenceID); err != nil {
			return err
		}
		cache[e.SequenceID] = seq
	}
	if e.NextStep >= len(seq.Steps) {
		return s.repo.Complete(ctx, e.ID, e.NextStep, "")
	}
	rc, err := s.contacts.Recipient(ctx, e.orgID, e.ContactID)
	if err != nil {
		return err
	}
	switch {
	case rc.OptedOut:
		return s.finish(ctx, e, StopOptedOut)
	case rc.LifecycleStage == "customer":
		return s.finish(ctx, e, StopCustomer)
	}
	if quiet, until := inQuietHours(now); quiet {
		return s.repo.Reschedule(ctx, e.ID, until)
	}

	step := seq.Steps[e.NextStep]
	summary := fmt.Sprintf("Nurture: %s, step %d of %d", seq.Name, e.NextStep+1, len(seq.Steps))
	body, externalID, err := s.send(ctx, e, step, rc, now)
	var sk skip
	switch {
	case errors.As(err, &sk):
		return s.advance(ctx, e, seq, now, "step "+fmt.Sprint(e.NextStep+1)+" skipped: "+sk.reason)
	case err != nil:
		attempts := e.Attempts + 1
		var apiErr *whatsapp.APIError
		permanent := errors.As(err, &apiErr) && !apiErr.Retryable()
		if permanent || attempts >= maxAttempts {
			return s.advance(ctx, e, seq, now, fmt.Sprintf("step %d failed after %d attempts: %v", e.NextStep+1, attempts, err))
		}
		return s.repo.Retry(ctx, e.ID, attempts, now.Add(backoff(attempts)), err.Error())
	}
	if err := s.contacts.RecordOutbound(ctx, e.orgID, e.ContactID, step.Channel, summary, body, externalID); err != nil {
		s.logger.ErrorContext(ctx, "nurture timeline entry", slog.Any("error", err))
	}
	return s.advance(ctx, e, seq, now, "")
}

// send delivers one step and returns the text sent and the channel's
// message id.
func (s *Service) send(ctx context.Context, e Enrollment, step Step, rc Recipient, now time.Time) (string, string, error) {
	switch step.Channel {
	case ChannelEmail:
		if rc.Email == "" {
			return "", "", skip{"no email address"}
		}
		body := render(step.Body, rc)
		text := body + "\n\n--\nTo stop receiving these messages: " + s.unsubscribeURL(e.orgID, e.ContactID)
		err := s.mailer.Send(ctx, mail.Message{To: rc.Email, Subject: render(step.Subject, rc), Text: text})
		return body, "", err

	case ChannelWhatsApp:
		if rc.PhoneKey == "" {
			return "", "", skip{"no mobile number"}
		}
		from, err := s.numbers.SenderNumber(ctx, e.orgID)
		if errors.Is(err, ErrNoWhatsAppNumber) {
			return "", "", skip{"no WhatsApp number registered for the organization"}
		}
		if err != nil {
			return "", "", err
		}
		inWindow := rc.LastWhatsAppInboundAt != nil && now.Sub(*rc.LastWhatsAppInboundAt) < serviceWindow
		if inWindow && step.Body != "" {
			body := render(step.Body, rc)
			id, err := s.wa.SendText(ctx, from, rc.PhoneKey, body)
			return body, id, err
		}
		if step.WhatsAppTemplate == "" {
			return "", "", skip{"outside WhatsApp's 24-hour window and the step has no template"}
		}
		params := make([]string, len(step.WhatsAppTemplateParams))
		for i, p := range step.WhatsAppTemplateParams {
			params[i] = render(p, rc)
		}
		id, err := s.wa.SendTemplate(ctx, from, rc.PhoneKey, step.WhatsAppTemplate, step.WhatsAppLanguage, params)
		return "[template " + step.WhatsAppTemplate + "] " + strings.Join(params, ", "), id, err
	}
	return "", "", skip{"unknown channel " + step.Channel}
}

// advance moves to the next step (due after its delay) or completes.
func (s *Service) advance(ctx context.Context, e Enrollment, seq Sequence, now time.Time, note string) error {
	next := e.NextStep + 1
	if next >= len(seq.Steps) {
		return s.repo.Complete(ctx, e.ID, next, note)
	}
	return s.repo.Advance(ctx, e.ID, next, now.Add(time.Duration(seq.Steps[next].DelayMinutes)*time.Minute), note)
}

func (s *Service) finish(ctx context.Context, e Enrollment, reason string) error {
	if _, err := s.repo.Finish(ctx, e.orgID, e.ID, StatusStopped, reason); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	s.event(ctx, e.orgID, e.ContactID, "nurture_stopped", fmt.Sprintf("Nurture stopped (%s): %s", reason, e.SequenceName),
		map[string]any{"sequence_id": e.SequenceID, "enrollment_id": e.ID, "reason": reason})
	return nil
}
