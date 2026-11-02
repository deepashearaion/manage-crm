package reports

import "context"

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) SalesDashboard(ctx context.Context) (SalesDashboard, error) {
	return s.repo.SalesDashboard(ctx)
}
