package reports

import (
	"context"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// SalesDashboard covers the caller's org; reps see only their own records.
func (s *Service) SalesDashboard(ctx context.Context, p principal.Principal) (SalesDashboard, error) {
	var ownerID *int64
	if !p.SeesAllRecords() {
		ownerID = &p.UserID
	}
	return s.repo.SalesDashboard(ctx, p.OrgID, ownerID)
}
