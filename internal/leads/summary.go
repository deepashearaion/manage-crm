package leads

import (
	"fmt"
	"strings"
	"time"
)

// ist is India Standard Time (UTC+5:30, no daylight saving), the zone the
// org works in; storage stays UTC.
var ist = time.FixedZone("IST", 5*3600+1800)

// Summarizer writes the summary a rep reads before following up a lead.
// The rule-based one below is the default; an AI one can replace it later
// behind the same interface (with the org's AI kill switch and redaction).
type Summarizer interface {
	Summarize(f Facts, s Score, followUpMinutes int) string
}

// RuleSummarizer builds the summary from the facts and the matched rules.
type RuleSummarizer struct{}

// interestWords map words in a lead's messages to what they signal.
var interestWords = []struct{ word, signal string }{
	{"price", "asked about price"},
	{"cost", "asked about cost"},
	{"quote", "asked for a quote"},
	{"budget", "mentioned a budget"},
	{"book", "wants to book"},
	{"urgent", "said it is urgent"},
	{"today", "wants it today"},
	{"call", "asked for a call"},
}

func (RuleSummarizer) Summarize(f Facts, s Score, followUpMinutes int) string {
	var b strings.Builder
	name := strings.TrimSpace(f.FirstName + " " + f.LastName)
	fmt.Fprintf(&b, "%s: %s lead, score %d/100.\n", name, s.Temperature, s.Score)

	var origin []string
	if f.Source != "" {
		origin = append(origin, "Source: "+f.Source)
	}
	if len(f.Channels) > 0 {
		origin = append(origin, "Channels: "+strings.ReplaceAll(strings.Join(f.Channels, ", "), "_", " "))
	}
	if f.Destination != "" {
		origin = append(origin, "Destination: "+f.Destination)
	}
	if len(origin) > 0 {
		b.WriteString(strings.Join(origin, ". ") + ".\n")
	}
	if len(s.Reasons) > 0 {
		b.WriteString("Why: " + strings.Join(s.Reasons, ", ") + ".\n")
	}
	if f.LastMessage != "" {
		when := ""
		if f.LastMessageAt != nil {
			when = ", " + f.LastMessageAt.In(ist).Format("2 Jan 15:04 IST")
		}
		ch := strings.ReplaceAll(f.LastMessageChannel, "_", " ")
		fmt.Fprintf(&b, "Last message (%s%s, %d in total): %q\n", ch, when, f.InboundMessages, truncate(oneLine(f.LastMessage), 300))
	}
	var reach []string
	if f.Mobile != "" {
		reach = append(reach, f.Mobile)
	}
	if f.Email != "" {
		reach = append(reach, f.Email)
	}
	if len(reach) > 0 {
		b.WriteString("Contact: " + strings.Join(reach, " · ") + "\n")
	}

	text := strings.ToLower(f.Text)
	var signals []string
	for _, w := range interestWords {
		if strings.Contains(text, w.word) {
			signals = append(signals, w.signal)
		}
	}
	how := "Call"
	if f.Mobile == "" {
		how = "Email"
	} else if containsString(f.Channels, ChannelWhatsApp) {
		how = "Call or WhatsApp"
	}
	next := fmt.Sprintf("Next step: %s within %d minutes", how, followUpMinutes)
	if len(signals) > 0 {
		next += "; they " + strings.Join(signals, ", ")
	}
	b.WriteString(next + ".")
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func containsString(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
