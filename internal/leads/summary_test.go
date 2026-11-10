package leads

import (
	"strings"
	"testing"
	"time"
)

func TestRuleSummary(t *testing.T) {
	at := time.Date(2026, 11, 10, 7, 35, 0, 0, time.UTC) // 13:05 IST
	got := RuleSummarizer{}.Summarize(Facts{
		FirstName: "Priya", LastName: "Sharma", Source: "Website", Destination: "Goa",
		Channels: []string{"web_form", "whatsapp"}, Mobile: "+919876543210", Email: "priya@example.com",
		LastMessage: "Please send\n a quote", LastMessageChannel: "whatsapp", LastMessageAt: &at,
		InboundMessages: 2, Text: "price for 4 nights? Please send a quote",
	}, Score{Score: 80, Temperature: Hot, Reasons: []string{"Has mobile (+15)", "Asked for a quote (+15)"}}, 15)

	for _, want := range []string{
		"Priya Sharma: hot lead, score 80/100.",
		"Source: Website. Channels: web form, whatsapp. Destination: Goa.",
		"Why: Has mobile (+15), Asked for a quote (+15).",
		`Last message (whatsapp, 10 Nov 13:05 IST, 2 in total): "Please send a quote"`,
		"Contact: +919876543210 · priya@example.com",
		"Next step: Call or WhatsApp within 15 minutes; they asked about price, asked for a quote.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q:\n%s", want, got)
		}
	}

	if got := (RuleSummarizer{}).Summarize(Facts{FirstName: "A", Email: "a@b.co"}, Score{Score: 70, Temperature: Hot}, 30); !strings.Contains(got, "Next step: Email within 30 minutes.") {
		t.Errorf("email-only summary:\n%s", got)
	}
}
