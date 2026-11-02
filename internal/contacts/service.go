package contacts

import (
	"context"
	"strings"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// Create adds a contact owned by ownerID; any owner in c is ignored.
func (s *Service) Create(ctx context.Context, ownerID int64, c Contact) (Contact, error) {
	if strings.TrimSpace(c.FirstName) == "" {
		return Contact{}, ErrFirstNameRequired
	}
	c.LeadOwnerID = &ownerID
	if err := s.repo.Create(ctx, &c); err != nil {
		return Contact{}, err
	}
	return c, nil
}

func (s *Service) List(ctx context.Context) ([]Contact, error) {
	return s.repo.List(ctx)
}

func (s *Service) Get(ctx context.Context, id int64) (Contact, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) Update(ctx context.Context, id int64, p Patch) (Contact, error) {
	return s.repo.Update(ctx, id, p)
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}
