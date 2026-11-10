package app

import (
	"context"
	"errors"

	"github.com/deepashearaion/manage-crm/internal/contacts"
	"github.com/deepashearaion/manage-crm/internal/leads"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// leadsContacts gives the leads module the contacts it needs
// (leads.Contacts), translating types and errors between the two modules.
type leadsContacts struct {
	svc *contacts.Service
}

var _ leads.Contacts = leadsContacts{}

func mapContactErr(err error) error {
	switch {
	case errors.Is(err, contacts.ErrNotFound):
		return leads.ErrContactNotFound
	case errors.Is(err, contacts.ErrInvalidOwner):
		return leads.ErrInvalidOwner
	}
	return err
}

func (c leadsContacts) Capture(ctx context.Context, orgID int64, l leads.Lead) (leads.Captured, error) {
	res, err := c.svc.Capture(ctx, orgID, contacts.CaptureInput{
		FirstName: l.FirstName, LastName: l.LastName, Email: l.Email, Mobile: l.Mobile,
		Destination: l.Destination, Source: l.Source, Notes: l.Notes, Channel: l.Channel,
		Message: l.Message, ExternalID: l.ExternalID, OccurredAt: l.OccurredAt,
		ActorUserID: l.ActorUserID, OwnerID: l.OwnerID,
	})
	if err != nil {
		return leads.Captured{}, mapContactErr(err)
	}
	return leads.Captured{
		ContactID: res.ContactID, OwnerID: res.OwnerID, Created: res.Created, AlreadyProcessed: res.AlreadyProcessed,
	}, nil
}

func (c leadsContacts) Facts(ctx context.Context, orgID, contactID int64) (leads.Facts, error) {
	f, err := c.svc.Facts(ctx, orgID, contactID)
	if err != nil {
		return leads.Facts{}, mapContactErr(err)
	}
	return leads.Facts{
		Source: f.Source, Destination: f.Destination, HasEmail: f.HasEmail, HasMobile: f.HasMobile,
		HasCompany: f.HasCompany, Channels: f.Channels, InboundMessages: f.InboundMessages,
		Text: f.Text, OwnerID: f.OwnerID,
		FirstName: f.FirstName, LastName: f.LastName, Email: f.Email, Mobile: f.Mobile,
		LastMessage: f.LastMessage, LastMessageChannel: f.LastMessageChannel, LastMessageAt: f.LastMessageAt,
	}, nil
}

func (c leadsContacts) SaveScore(ctx context.Context, orgID, contactID int64, s leads.Score) (string, error) {
	prev, err := c.svc.SaveScore(ctx, orgID, contactID, contacts.Score{
		Score: s.Score, Temperature: s.Temperature, Reasons: s.Reasons, Method: s.Method,
	})
	return prev, mapContactErr(err)
}

func (c leadsContacts) AssignOwner(ctx context.Context, orgID, contactID, userID int64, reason string) (bool, error) {
	ok, err := c.svc.AssignOwner(ctx, orgID, contactID, userID, reason)
	return ok, mapContactErr(err)
}

func (c leadsContacts) Visible(ctx context.Context, p principal.Principal, contactID int64) (*int64, error) {
	v, err := c.svc.Visible(ctx, p, contactID)
	if err != nil {
		return nil, mapContactErr(err)
	}
	return v.LeadOwnerID, nil
}
