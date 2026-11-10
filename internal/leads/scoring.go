package leads

import (
	"slices"
	"strconv"
	"strings"
)

// Evaluate scores facts with the active rules: the sum of the points of
// every matching rule, clamped to 0..100, and its temperature by the
// thresholds in settings. Reasons name the matching rules with their points.
func Evaluate(rules []ScoringRule, f Facts, s Settings) Score {
	total := 0
	reasons := make([]string, 0)
	text := strings.ToLower(f.Text)
	for _, r := range rules {
		if !r.Active || !matchesScoring(r, f, text) {
			continue
		}
		total += r.Points
		reasons = append(reasons, r.Name+" ("+signed(r.Points)+")")
	}
	total = max(0, min(100, total))
	return Score{Score: total, Temperature: temperature(total, s), Reasons: reasons, Method: "rules"}
}

func temperature(score int, s Settings) string {
	switch {
	case score >= s.HotThreshold:
		return Hot
	case score >= s.WarmThreshold:
		return Warm
	}
	return Cold
}

func signed(n int) string {
	if n >= 0 {
		return "+" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// matchesScoring reports whether r matches f; text is f.Text lowercased.
func matchesScoring(r ScoringRule, f Facts, text string) bool {
	switch r.Criterion {
	case CriterionHasEmail:
		return f.HasEmail
	case CriterionHasMobile:
		return f.HasMobile
	case CriterionHasCompany:
		return f.HasCompany
	case CriterionSourceEquals:
		return equalFold(f.Source, r.Value)
	case CriterionDestinationEquals:
		return equalFold(f.Destination, r.Value)
	case CriterionChannelUsed:
		return slices.Contains(f.Channels, r.Value)
	case CriterionTextContains:
		v := strings.ToLower(strings.TrimSpace(r.Value))
		return v != "" && strings.Contains(text, v)
	case CriterionInboundMessagesAtLeast:
		n, err := strconv.Atoi(r.Value)
		return err == nil && f.InboundMessages >= n
	}
	return false
}

func equalFold(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// matchesAssignment reports whether r applies to a new lead with a.
func matchesAssignment(r AssignmentRule, a routingAttrs) bool {
	switch r.Criterion {
	case AssignAny:
		return true
	case AssignSourceEquals:
		return equalFold(a.Source, r.Value)
	case AssignChannelEquals:
		return a.Channel == r.Value
	case AssignDestinationEquals:
		return equalFold(a.Destination, r.Value)
	case AssignTemperatureEquals:
		return a.Temperature == r.Value
	}
	return false
}

var leadChannels = []string{ChannelWebForm, ChannelWhatsApp, ChannelEmail, ChannelPhone, ChannelManual, ChannelAPI}

// validateScoringRule checks a rule before it is stored, normalizing its
// name and value.
func validateScoringRule(r *ScoringRule) error {
	r.Name = strings.TrimSpace(r.Name)
	r.Value = strings.TrimSpace(r.Value)
	if r.Name == "" {
		return required("name", "Name is required.")
	}
	if len(r.Name) > 100 {
		return invalid("name", "Name must be at most 100 characters.")
	}
	if r.Points < -100 || r.Points > 100 {
		return invalid("points", "Points must be between -100 and 100.")
	}
	switch r.Criterion {
	case CriterionHasEmail, CriterionHasMobile, CriterionHasCompany:
		r.Value = ""
	case CriterionSourceEquals, CriterionDestinationEquals, CriterionTextContains:
		if r.Value == "" {
			return required("value", "This criterion needs a value.")
		}
	case CriterionChannelUsed:
		if !slices.Contains(leadChannels, r.Value) {
			return invalid("value", "Value must be one of: "+strings.Join(leadChannels, ", ")+".")
		}
	case CriterionInboundMessagesAtLeast:
		if n, err := strconv.Atoi(r.Value); err != nil || n < 1 {
			return invalid("value", "Value must be a whole number of at least 1.")
		}
	default:
		return invalid("criterion", "Criterion must be one of: has_email, has_mobile, has_company, source_equals, "+
			"destination_equals, channel_used, text_contains, inbound_messages_at_least.")
	}
	return nil
}

// validateAssignmentRule checks a rule before it is stored.
func validateAssignmentRule(r *AssignmentRule) error {
	r.Name = strings.TrimSpace(r.Name)
	r.Value = strings.TrimSpace(r.Value)
	if r.Name == "" {
		return required("name", "Name is required.")
	}
	if len(r.Name) > 100 {
		return invalid("name", "Name must be at most 100 characters.")
	}
	switch r.Criterion {
	case AssignAny:
		r.Value = ""
	case AssignSourceEquals, AssignDestinationEquals:
		if r.Value == "" {
			return required("value", "This criterion needs a value.")
		}
	case AssignChannelEquals:
		if !slices.Contains(leadChannels, r.Value) {
			return invalid("value", "Value must be one of: "+strings.Join(leadChannels, ", ")+".")
		}
	case AssignTemperatureEquals:
		if r.Value != Hot && r.Value != Warm && r.Value != Cold {
			return invalid("value", "Value must be hot, warm or cold.")
		}
	default:
		return invalid("criterion", "Criterion must be one of: any, source_equals, channel_equals, destination_equals, temperature_equals.")
	}
	if len(r.UserIDs) == 0 {
		return required("user_ids", "At least one user is required.")
	}
	slices.Sort(r.UserIDs)
	r.UserIDs = slices.Compact(r.UserIDs)
	return nil
}

func validateSettings(s Settings) error {
	if s.HotThreshold < 1 || s.HotThreshold > 100 {
		return invalid("hot_threshold", "Hot threshold must be between 1 and 100.")
	}
	if s.WarmThreshold < 1 || s.WarmThreshold >= s.HotThreshold {
		return invalid("warm_threshold", "Warm threshold must be at least 1 and below the hot threshold.")
	}
	return nil
}

// defaultScoringRules are seeded for every new org. Migration 00009 seeds
// the same list for orgs that existed before; keep the two in step.
var defaultScoringRules = []ScoringRule{
	{Name: "Has email", Criterion: CriterionHasEmail, Points: 10},
	{Name: "Has mobile", Criterion: CriterionHasMobile, Points: 15},
	{Name: "Reached us on WhatsApp", Criterion: CriterionChannelUsed, Value: ChannelWhatsApp, Points: 15},
	{Name: "Replied at least twice", Criterion: CriterionInboundMessagesAtLeast, Value: "2", Points: 20},
	{Name: "Asked about price", Criterion: CriterionTextContains, Value: "price", Points: 15},
	{Name: "Asked for a quote", Criterion: CriterionTextContains, Value: "quote", Points: 15},
	{Name: "Mentioned budget", Criterion: CriterionTextContains, Value: "budget", Points: 10},
	{Name: "Urgent", Criterion: CriterionTextContains, Value: "urgent", Points: 10},
}
