package leads

import (
	"errors"
	"slices"
	"testing"
)

func TestEvaluate(t *testing.T) {
	rules := []ScoringRule{
		{Name: "Email", Criterion: CriterionHasEmail, Points: 10, Active: true},
		{Name: "Mobile", Criterion: CriterionHasMobile, Points: 15, Active: true},
		{Name: "WhatsApp", Criterion: CriterionChannelUsed, Value: ChannelWhatsApp, Points: 15, Active: true},
		{Name: "Engaged", Criterion: CriterionInboundMessagesAtLeast, Value: "2", Points: 20, Active: true},
		{Name: "Price", Criterion: CriterionTextContains, Value: "Price", Points: 15, Active: true},
		{Name: "Goa", Criterion: CriterionDestinationEquals, Value: "goa", Points: 10, Active: true},
		{Name: "Partner", Criterion: CriterionSourceEquals, Value: "Partner", Points: 40, Active: false},
		{Name: "Spam", Criterion: CriterionTextContains, Value: "crypto", Points: -100, Active: true},
	}

	cold := Evaluate(rules, Facts{HasEmail: true}, DefaultSettings)
	if cold.Score != 10 || cold.Temperature != Cold || !slices.Equal(cold.Reasons, []string{"Email (+10)"}) {
		t.Errorf("email only: %+v", cold)
	}

	hot := Evaluate(rules, Facts{
		HasEmail: true, HasMobile: true, Channels: []string{"web_form", "whatsapp"},
		InboundMessages: 2, Text: "What is the PRICE for 2 adults?", Destination: " Goa ",
		Source: "Partner", // inactive rule: ignored
	}, DefaultSettings)
	if hot.Score != 85 || hot.Temperature != Hot || len(hot.Reasons) != 6 || hot.Method != "rules" {
		t.Errorf("engaged lead: %+v", hot)
	}

	if s := Evaluate(rules, Facts{HasEmail: true, Text: "crypto offer"}, DefaultSettings); s.Score != 0 {
		t.Errorf("negative total not clamped to 0: %+v", s)
	}
	over := append(slices.Clone(rules), ScoringRule{Name: "Big", Criterion: CriterionHasEmail, Points: 100, Active: true})
	if s := Evaluate(over, Facts{HasEmail: true}, DefaultSettings); s.Score != 100 {
		t.Errorf("total not clamped to 100: %+v", s)
	}

	warm := Evaluate(rules, Facts{HasEmail: true, HasMobile: true, Channels: []string{"whatsapp"}}, DefaultSettings)
	if warm.Score != 40 || warm.Temperature != Warm {
		t.Errorf("warm threshold is inclusive: %+v", warm)
	}
}

func TestValidateScoringRule(t *testing.T) {
	ok := []ScoringRule{
		{Name: "a", Criterion: CriterionHasEmail, Value: "ignored", Points: 5},
		{Name: "b", Criterion: CriterionChannelUsed, Value: "whatsapp", Points: 5},
		{Name: "c", Criterion: CriterionInboundMessagesAtLeast, Value: "3", Points: -5},
	}
	for _, r := range ok {
		if err := validateScoringRule(&r); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := map[string]ScoringRule{
		"name":      {Criterion: CriterionHasEmail},
		"points":    {Name: "x", Criterion: CriterionHasEmail, Points: 101},
		"criterion": {Name: "x", Criterion: "is_vip"},
		"value":     {Name: "x", Criterion: CriterionChannelUsed, Value: "fax"},
	}
	for field, r := range bad {
		err := validateScoringRule(&r)
		var fe *FieldError
		if !errors.As(err, &fe) || fe.Field != field {
			t.Errorf("%+v: err = %v, want a %s field error", r, err, field)
		}
	}
	if err := validateScoringRule(&ScoringRule{Name: "x", Criterion: CriterionInboundMessagesAtLeast, Value: "0"}); err == nil {
		t.Error("inbound_messages_at_least 0 accepted")
	}
}

func TestMatchesAssignment(t *testing.T) {
	a := routingAttrs{Source: "Website", Channel: ChannelWebForm, Destination: "Goa", Temperature: Hot}
	for _, tc := range []struct {
		rule AssignmentRule
		want bool
	}{
		{AssignmentRule{Criterion: AssignAny}, true},
		{AssignmentRule{Criterion: AssignSourceEquals, Value: "website"}, true},
		{AssignmentRule{Criterion: AssignChannelEquals, Value: ChannelWhatsApp}, false},
		{AssignmentRule{Criterion: AssignDestinationEquals, Value: "goa"}, true},
		{AssignmentRule{Criterion: AssignTemperatureEquals, Value: Warm}, false},
	} {
		if got := matchesAssignment(tc.rule, a); got != tc.want {
			t.Errorf("%+v: got %v", tc.rule, got)
		}
	}
}
