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

const module = principal.Contacts

// scope is what p may see: their org, and only the contacts they own unless
// their role sees all contacts.
func scope(p principal.Principal) Scope {
	s := Scope{OrgID: p.OrgID}
	if !p.SeesAll(module) {
		s.OwnerID = &p.UserID
	}
	return s
}

// canView is the gate for every read.
func canView(p principal.Principal) error {
	if !p.Can(module, principal.View) {
		return ErrForbidden
	}
	return nil
}

// Create adds a contact owned by the caller; any owner in c is ignored
// (owners are changed with Update).
func (s *Service) Create(ctx context.Context, p principal.Principal, c Contact) (Contact, error) {
	if !p.CanOn(module, principal.Create, &p.UserID) {
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
	if err := canView(p); err != nil {
		return nil, err
	}
	return s.repo.List(ctx, scope(p))
}

func (s *Service) Get(ctx context.Context, p principal.Principal, id int64) (Contact, error) {
	if err := canView(p); err != nil {
		return Contact{}, err
	}
	return s.repo.Get(ctx, scope(p), id)
}

// Update changes a contact the caller may edit. Changing the owner needs
// edit access to all contacts; the new owner must be in the same org.
func (s *Service) Update(ctx context.Context, p principal.Principal, id int64, patch Patch) (Contact, error) {
	current, err := s.Get(ctx, p, id)
	if err != nil {
		return Contact{}, err
	}
	if !p.CanOn(module, principal.Edit, current.LeadOwnerID) {
		return Contact{}, ErrForbidden
	}
	// Re-sending the current owner (as full-form saves do) is not a change.
	if patch.LeadOwnerID != nil && !p.CanReassign(module) &&
		(current.LeadOwnerID == nil || *current.LeadOwnerID != *patch.LeadOwnerID) {
		return Contact{}, ErrForbidden
	}
	return s.repo.Update(ctx, scope(p), id, patch)
}

func (s *Service) Delete(ctx context.Context, p principal.Principal, id int64) error {
	current, err := s.Get(ctx, p, id)
	if err != nil {
		return err
	}
	if !p.CanOn(module, principal.Delete, current.LeadOwnerID) {
		return ErrForbidden
	}
	return s.repo.Delete(ctx, scope(p), id)
}
