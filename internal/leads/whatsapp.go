package leads

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Meta WhatsApp Cloud API webhooks. Meta signs each POST body with the
// app secret (X-Hub-Signature-256: sha256=<hex HMAC>) and verifies the
// endpoint once with a GET handshake carrying our verify token. See
// https://developers.facebook.com/docs/graph-api/webhooks/getting-started
// and the WhatsApp Cloud API webhook payload reference.

// validSignature reports whether header is the HMAC-SHA256 of body under secret.
func validSignature(secret string, body []byte, header string) bool {
	got, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	sig, err := hex.DecodeString(got)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(sig, mac.Sum(nil))
}

// validVerifyToken compares the handshake token in constant time.
func validVerifyToken(want, got string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

// waPayload is the part of a webhook notification we read.
type waPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		Changes []struct {
			Field string `json:"field"`
			Value struct {
				Metadata struct {
					PhoneNumberID      string `json:"phone_number_id"`
					DisplayPhoneNumber string `json:"display_phone_number"`
				} `json:"metadata"`
				Contacts []struct {
					WaID    string `json:"wa_id"`
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
				} `json:"contacts"`
				Messages []waMessage `json:"messages"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

type waMessage struct {
	From      string `json:"from"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Text      struct {
		Body string `json:"body"`
	} `json:"text"`
	Button struct {
		Text string `json:"text"`
	} `json:"button"`
	Interactive struct {
		ButtonReply struct {
			Title string `json:"title"`
		} `json:"button_reply"`
		ListReply struct {
			Title string `json:"title"`
		} `json:"list_reply"`
	} `json:"interactive"`
	Image    waMedia `json:"image"`
	Video    waMedia `json:"video"`
	Document waMedia `json:"document"`
}

type waMedia struct {
	Caption string `json:"caption"`
}

// inboundMessage is one customer message from a webhook.
type inboundMessage struct {
	PhoneNumberID string
	WaID          string
	Name          string
	ID            string
	Body          string
	At            time.Time
}

// parseWebhook extracts the customer messages from a webhook body. Status
// updates (sent, delivered, read), reactions and unsupported message types
// are skipped.
func parseWebhook(body []byte) ([]inboundMessage, error) {
	var p waPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}
	if p.Object != "whatsapp_business_account" {
		return nil, errors.New("not a whatsapp_business_account notification")
	}
	var out []inboundMessage
	for _, e := range p.Entry {
		for _, ch := range e.Changes {
			if ch.Field != "messages" {
				continue
			}
			names := map[string]string{}
			for _, c := range ch.Value.Contacts {
				names[c.WaID] = c.Profile.Name
			}
			for _, m := range ch.Value.Messages {
				text, ok := messageText(m)
				if !ok || m.From == "" || m.ID == "" {
					continue
				}
				at := time.Now().UTC()
				if sec, err := strconv.ParseInt(m.Timestamp, 10, 64); err == nil {
					at = time.Unix(sec, 0).UTC()
				}
				out = append(out, inboundMessage{
					PhoneNumberID: ch.Value.Metadata.PhoneNumberID,
					WaID:          m.From,
					Name:          strings.TrimSpace(names[m.From]),
					ID:            m.ID,
					Body:          text,
					At:            at,
				})
			}
		}
	}
	return out, nil
}

// messageText renders a message as timeline text; ok is false for types
// that are not customer messages worth recording.
func messageText(m waMessage) (string, bool) {
	media := func(kind, caption string) string {
		if caption = strings.TrimSpace(caption); caption != "" {
			return "[" + kind + "] " + caption
		}
		return "[" + kind + "]"
	}
	switch m.Type {
	case "text":
		return m.Text.Body, true
	case "button":
		return m.Button.Text, true
	case "interactive":
		if t := m.Interactive.ButtonReply.Title; t != "" {
			return t, true
		}
		return m.Interactive.ListReply.Title, true
	case "image":
		return media("image", m.Image.Caption), true
	case "video":
		return media("video", m.Video.Caption), true
	case "document":
		return media("document", m.Document.Caption), true
	case "audio", "sticker", "location", "contacts":
		return "[" + m.Type + "]", true
	}
	return "", false // reaction, order, system, unsupported, ...
}

// splitName turns a WhatsApp profile name into first and last names,
// falling back to the number when there is no name.
func splitName(name, waID string) (string, string) {
	name = truncate(strings.Join(strings.Fields(name), " "), maxName*2)
	if name == "" {
		return "+" + waID, ""
	}
	first, last, _ := strings.Cut(name, " ")
	return truncate(first, maxName), truncate(last, maxName)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// HandleWhatsAppMessages captures every customer message in a verified
// webhook body. Messages to numbers no org has registered are logged and
// skipped; other failures are returned so Meta retries (captures are
// idempotent by message id).
func (s *Service) HandleWhatsAppMessages(ctx context.Context, body []byte) error {
	msgs, err := parseWebhook(body)
	if err != nil {
		return err
	}
	var errs []error
	for _, m := range msgs {
		acct, err := s.repo.WhatsAppAccountByPhoneNumberID(ctx, m.PhoneNumberID)
		if errors.Is(err, ErrNotFound) {
			s.logger.WarnContext(ctx, "whatsapp message for an unregistered number",
				slog.String("phone_number_id", m.PhoneNumberID))
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		first, last := splitName(m.Name, m.WaID)
		_, err = s.Capture(ctx, acct.OrgID, Lead{
			FirstName:  first,
			LastName:   last,
			Mobile:     "+" + m.WaID,
			Source:     acct.SourceName,
			Channel:    ChannelWhatsApp,
			Message:    truncate(m.Body, maxMessage),
			ExternalID: m.ID,
			OccurredAt: m.At,
		})
		var fe *FieldError
		if errors.As(err, &fe) {
			s.logger.WarnContext(ctx, "whatsapp message not captured", slog.String("reason", fe.Message))
			continue
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
