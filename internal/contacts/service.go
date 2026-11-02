package contacts

import (
	"context"
	"strings"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

type Service struct {
	repo *Repository
}

func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// scope is what p may see: their org, and only their own contacts for reps.
func scope(p principal.Principal) Scope {
	s := Scope{OrgID: p.OrgID}
	if !p.SeesAllRecords() {
		s.OwnerID = &p.UserID
	}
	return s
}

// Create adds a contact owned by the caller; any owner in c is ignored
// (managers and above reassign with Update).
func (s *Service) Create(ctx context.Context, p principal.Principal, c Contact) (Contact, error) {
	if !p.CanWrite() {
		return Contact{}, ErrForbidden
	}
	if strings.TrimSpace(c.FirstName) == "" {
		return Contact{}, ErrFirstNameRequired
	}
	c.LeadOwnerID = &p.UserID
	if err := s.repo.Create(ctx, p.OrgID, &c); err != nil {
		return Contact{}, err
	}
	return c, nil
}

func (s *Service) List(ctx context.Context, p principal.Principal) ([]Contact, error) {
	return s.repo.List(ctx, scope(p))
}

func (s *Service) Get(ctx context.Context, p principal.Principal, id int64) (Contact, error) {
	return s.repo.Get(ctx, scope(p), id)
}

// Update changes a contact the caller may write. Changing the owner needs
// manager or above; the new owner must be in the same org.
func (s *Service) Update(ctx context.Context, p principal.Principal, id int64, patch Patch) (Contact, error) {
	if !p.CanWrite() {
		return Contact{}, ErrForbidden
	}
	if patch.LeadOwnerID != nil && !p.CanReassign() {
		// Re-sending the current owner (as full-form saves do) is not a change.
		current, err := s.repo.Get(ctx, scope(p), id)
		if err != nil {
			return Contact{}, err
		}
		if current.LeadOwnerID == nil || *current.LeadOwnerID != *patch.LeadOwnerID {
			return Contact{}, ErrForbidden
		}
	}
	return s.repo.Update(ctx, scope(p), id, patch)
}

func (s *Service) Delete(ctx context.Context, p principal.Principal, id int64) error {
	if !p.CanWrite() {
		return ErrForbidden
	}
	return s.repo.Delete(ctx, scope(p), id)
}
