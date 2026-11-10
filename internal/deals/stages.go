package deals

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// DefaultStages are created for every new org, in pipeline order.
// Migration 00011 gives existing orgs' default stages the same settings;
// keep the two in step.
var DefaultStages = []Stage{
	{Name: "Qualification", Position: 1, Kind: KindOpen, Probability: 10,
		Guidance: "Confirm the need, budget, decision maker and timeline.", RequiredFields: []string{}},
	{Name: "Needs Analysis", Position: 2, Kind: KindOpen, Probability: 25,
		Guidance: "Understand the requirements: dates, group size, preferences.", RequiredFields: []string{FieldContact}},
	{Name: "Proposal", Position: 3, Kind: KindOpen, Probability: 50,
		Guidance: "Send the proposal or quote with pricing.", RequiredFields: []string{FieldAmount, FieldContact}},
	{Name: "Negotiation", Position: 4, Kind: KindOpen, Probability: 75,
		Guidance:       "Agree the final terms and price, and confirm the close date.",
		RequiredFields: []string{FieldAmount, FieldExpectedCloseDate, FieldContact}},
	{Name: "Closed Won", Position: 5, Kind: KindWon, Probability: 100,
		Guidance: "Payment or signed confirmation received.", RequiredFields: []string{FieldAmount, FieldContact}},
	{Name: "Closed Lost", Position: 6, Kind: KindLost, Probability: 0,
		Guidance: "Record why the deal was lost.", RequiredFields: []string{}},
}

// SeedDefaultStages creates the default stages for orgID inside tx.
func SeedDefaultStages(ctx context.Context, tx pgx.Tx, orgID int64) error {
	for _, s := range DefaultStages {
		if _, err := tx.Exec(ctx,
			`INSERT INTO deal_stages (org_id, name, position, kind, probability, guidance, required_fields)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			orgID, s.Name, s.Position, s.Kind, s.Probability, s.Guidance, s.RequiredFields,
		); err != nil {
			return err
		}
	}
	return nil
}
