package nurture

import (
	"testing"
	"time"
)

func TestQuietHours(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 10, h, m, 0, 0, ist) }
	for _, tc := range []struct {
		t     time.Time
		quiet bool
		until time.Time
	}{
		{at(9, 0), false, time.Time{}},
		{at(20, 59), false, time.Time{}},
		{at(21, 0), true, at(9, 0).AddDate(0, 0, 1)},
		{at(23, 30), true, at(9, 0).AddDate(0, 0, 1)},
		{at(3, 0), true, at(9, 0)},
	} {
		quiet, until := inQuietHours(tc.t.UTC())
		if quiet != tc.quiet || !until.Equal(tc.until) {
			t.Errorf("%v: quiet %v until %v", tc.t, quiet, until)
		}
	}
}

func TestRender(t *testing.T) {
	got := render("Hi {{first_name}} ({{full_name}}), {{unknown}}", Recipient{FirstName: "Priya", LastName: "Sharma"})
	if got != "Hi Priya (Priya Sharma), {{unknown}}" {
		t.Errorf("render = %q", got)
	}
}

func TestUnsubscribeToken(t *testing.T) {
	s := &Service{settings: Settings{UnsubscribeKey: []byte("k")}}
	tok := s.unsubscribeToken(3, 42)
	other := (&Service{settings: Settings{UnsubscribeKey: []byte("other")}}).unsubscribeToken(3, 42)
	if tok == other {
		t.Error("token does not depend on the key")
	}
	if s.sign("3.42") == s.sign("3.43") {
		t.Error("signature does not depend on the contact")
	}
}

func TestValidateSequence(t *testing.T) {
	ok := Sequence{Name: "N", Trigger: TriggerNewLead, Steps: []Step{
		{Channel: ChannelEmail, Subject: "Hi", Body: "Hello"},
		{Channel: ChannelWhatsApp, DelayMinutes: 60, WhatsAppTemplate: "followup"},
	}}
	if err := validateSequence(&ok); err != nil || ok.Steps[1].WhatsAppLanguage != "en" {
		t.Errorf("valid sequence: %v", err)
	}
	for name, s := range map[string]Sequence{
		"no steps":      {Name: "N", Trigger: TriggerManual},
		"bad trigger":   {Name: "N", Trigger: "weekly", Steps: ok.Steps},
		"email no body": {Name: "N", Trigger: TriggerManual, Steps: []Step{{Channel: ChannelEmail, Subject: "x"}}},
		"empty wa":      {Name: "N", Trigger: TriggerManual, Steps: []Step{{Channel: ChannelWhatsApp}}},
		"sms":           {Name: "N", Trigger: TriggerManual, Steps: []Step{{Channel: "sms", Body: "x"}}},
		"negative":      {Name: "N", Trigger: TriggerManual, Steps: []Step{{Channel: ChannelEmail, Subject: "x", Body: "y", DelayMinutes: -1}}},
	} {
		if err := validateSequence(&s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
