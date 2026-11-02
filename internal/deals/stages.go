// Package deals will hold the deal pipeline. For now it owns the default
// deal stages that every new org starts with.
package deals

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// DefaultStages are created for every new org, in pipeline order.
var DefaultStages = []string{
	"Qualification",
	"Needs Analysis",
	"Proposal",
	"Negotiation",
	"Closed Won",
	"Closed Lost",
}

// SeedDefaultStages creates the default stages for orgID inside tx.
func SeedDefaultStages(ctx context.Context, tx pgx.Tx, orgID int64) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO deal_stages (org_id, name) SELECT $1, unnest($2::text[])`,
		orgID, DefaultStages,
	)
	return err
}
