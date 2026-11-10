package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/deepashearaion/manage-crm/internal/auth"
	"github.com/deepashearaion/manage-crm/internal/contacts"
	"github.com/deepashearaion/manage-crm/internal/deals"
	"github.com/deepashearaion/manage-crm/internal/leads"
	"github.com/deepashearaion/manage-crm/internal/notifications"
	"github.com/deepashearaion/manage-crm/internal/nurture"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
	"github.com/deepashearaion/manage-crm/internal/tasks"
)

// Glue between modules. Modules never import each other; each declares the
// interface it needs and these adapters implement them. Keep logic here
// thin: the one decision made here is how the workflow reacts to events.

// workflowEvents reacts to the leads module's events (leads.Events): the
// diagram's "Rep alerted instantly: task and summary ready" step.
type workflowEvents struct {
	tasks         *tasks.Service
	notifications *notifications.Service
	users         *auth.Service
	nurture       *nurture.Service
}

var _ leads.Events = workflowEvents{}

func contactLink(id int64) string { return fmt.Sprintf("/contacts/%d", id) }

// LeadTurnedHot gives the owner a follow-up task carrying the summary and
// alerts them in the app and by email. An unowned hot lead alerts the
// org's owners, admins and managers instead, so someone assigns it.
func (w workflowEvents) LeadTurnedHot(ctx context.Context, e leads.HotLead) error {
	// A hot lead leaves nurture: a rep takes over.
	if err := w.nurture.StopForContact(ctx, e.OrgID, e.ContactID, nurture.StopHot, false); err != nil {
		return fmt.Errorf("stop nurture: %w", err)
	}
	if e.OwnerID == nil {
		managers, err := w.managers(ctx, e.OrgID)
		if err != nil {
			return err
		}
		return w.notifications.Notify(ctx, e.OrgID, managers, notifications.Message{
			Kind:      notifications.KindHotLead,
			Title:     fmt.Sprintf("Unassigned hot lead: %s (score %d)", e.Name, e.Score),
			Body:      e.Summary + "\n\nThis lead has no owner yet: assign it so they can follow up.",
			Link:      contactLink(e.ContactID),
			ContactID: &e.ContactID,
			Email:     true,
		})
	}

	due := e.FollowUpDue
	task, _, err := w.tasks.CreateWorkflow(ctx, e.OrgID, tasks.SystemTask{
		ContactID:   e.ContactID,
		AssigneeID:  *e.OwnerID,
		Title:       "Follow up hot lead: " + e.Name,
		Description: e.Summary,
		Type:        "follow_up",
		Priority:    "urgent",
		Origin:      tasks.OriginHotLead,
		DueAt:       &due,
	})
	if err != nil {
		return fmt.Errorf("create follow-up task: %w", err)
	}
	return w.notifications.Notify(ctx, e.OrgID, []int64{*e.OwnerID}, notifications.Message{
		Kind:      notifications.KindHotLead,
		Title:     fmt.Sprintf("Hot lead: %s (score %d)", e.Name, e.Score),
		Body:      e.Summary,
		Link:      contactLink(e.ContactID),
		ContactID: &e.ContactID,
		TaskID:    &task.ID,
		Email:     true,
	})
}

// LeadAssigned tells a user that routing gave them a new lead.
func (w workflowEvents) LeadAssigned(ctx context.Context, e leads.LeadAssigned) error {
	return w.notifications.Notify(ctx, e.OrgID, []int64{e.OwnerID}, notifications.Message{
		Kind:      notifications.KindLeadAssigned,
		Title:     "New lead assigned: " + e.Name,
		Body:      e.Reason,
		Link:      contactLink(e.ContactID),
		ContactID: &e.ContactID,
	})
}

func (w workflowEvents) managers(ctx context.Context, orgID int64) ([]int64, error) {
	users, err := w.users.OrgUsers(ctx, orgID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, u := range users {
		if u.Principal().ManagesSettings() {
			ids = append(ids, u.ID)
		}
	}
	return ids, nil
}

// tasksContacts gives the tasks module contact visibility and timeline
// entries (tasks.Contacts).
type tasksContacts struct {
	svc *contacts.Service
}

var _ tasks.Contacts = tasksContacts{}

func (c tasksContacts) Visible(ctx context.Context, p principal.Principal, contactID int64) error {
	_, err := c.svc.Visible(ctx, p, contactID)
	if errors.Is(err, contacts.ErrNotFound) {
		return tasks.ErrContactNotFound
	}
	return err
}

func (c tasksContacts) TaskCreated(ctx context.Context, orgID int64, t tasks.Task) error {
	return c.record(ctx, orgID, t, contacts.KindTaskCreated, "Task created: "+t.Title, t.CreatedByID)
}

func (c tasksContacts) TaskCompleted(ctx context.Context, orgID int64, t tasks.Task, by int64) error {
	return c.record(ctx, orgID, t, contacts.KindTaskCompleted, "Task completed: "+t.Title, &by)
}

func (c tasksContacts) record(ctx context.Context, orgID int64, t tasks.Task, kind, summary string, actor *int64) error {
	err := c.svc.AddSystemActivity(ctx, orgID, *t.ContactID, contacts.SystemActivity{
		Kind: kind, Summary: summary, ActorUserID: actor,
		Metadata: map[string]any{"task_id": t.ID, "assignee_id": t.AssigneeID, "origin": t.Origin},
	})
	if errors.Is(err, contacts.ErrNotFound) {
		return tasks.ErrContactNotFound
	}
	return err
}

// tasksNotifier tells users about tasks assigned to them (tasks.Notifier).
type tasksNotifier struct {
	notifications *notifications.Service
}

func (n tasksNotifier) TaskAssigned(ctx context.Context, orgID int64, t tasks.Task, _ int64) error {
	return n.notifications.Notify(ctx, orgID, []int64{t.AssigneeID}, notifications.Message{
		Kind:      notifications.KindTaskAssigned,
		Title:     "New task: " + t.Title,
		Body:      t.Description,
		Link:      fmt.Sprintf("/tasks/%d", t.ID),
		ContactID: t.ContactID,
		TaskID:    &t.ID,
	})
}

// userDirectory gives the notifications module users' email addresses.
type userDirectory struct {
	users *auth.Service
}

func (d userDirectory) Email(ctx context.Context, orgID, userID int64) (string, error) {
	u, err := d.users.OrgUser(ctx, orgID, userID)
	return u.Email, err
}

// recontactAfter is how long after a lost deal its owner re-contacts the
// customer (the diagram's "Back to nurture: re-contact after 30 days").
const recontactAfter = 30 * 24 * time.Hour

var _ deals.Events = workflowEvents{}

// DealWon takes the new customer out of nurture. Onboarding (welcome
// message, invoice) arrives in phase 7e.
func (w workflowEvents) DealWon(ctx context.Context, o deals.Outcome) error {
	if o.ContactID == nil {
		return nil
	}
	return w.nurture.StopForContact(ctx, o.OrgID, *o.ContactID, nurture.StopWon, false)
}

// DealLost sends the contact back to nurture: into the org's active
// deal_lost sequence (whose first step's delay is the "re-contact after 30
// days"), or, when there is none, a re-contact task for the owner due 30
// days later.
func (w workflowEvents) DealLost(ctx context.Context, o deals.Outcome) error {
	if o.ContactID == nil {
		return nil
	}
	enrolled, err := w.nurture.AutoEnroll(ctx, o.OrgID, *o.ContactID, nurture.TriggerDealLost)
	if err != nil || enrolled || o.OwnerID == nil {
		return err
	}
	due := o.ClosedAt.Add(recontactAfter)
	_, _, err = w.tasks.CreateWorkflow(ctx, o.OrgID, tasks.SystemTask{
		ContactID:   *o.ContactID,
		AssigneeID:  *o.OwnerID,
		Title:       "Re-contact: " + o.Title,
		Description: fmt.Sprintf("Deal %q was lost %s. Reason: %s\nCheck in to see whether their needs have changed.", o.Title, o.ClosedAt.In(ist).Format("2 Jan 2006"), o.LostReason),
		Type:        "follow_up",
		Priority:    "normal",
		Origin:      tasks.OriginDealLost,
		DueAt:       &due,
	})
	return err
}

var ist = time.FixedZone("IST", 5*3600+1800)

// dealsContacts gives the deals module contact visibility, lifecycle
// changes and timeline entries (deals.Contacts).
type dealsContacts struct {
	svc *contacts.Service
}

var _ deals.Contacts = dealsContacts{}

func mapDealContactErr(err error) error {
	if errors.Is(err, contacts.ErrNotFound) {
		return deals.ErrContactNotFound
	}
	return err
}

func (c dealsContacts) Visible(ctx context.Context, p principal.Principal, contactID int64) error {
	_, err := c.svc.Visible(ctx, p, contactID)
	return mapDealContactErr(err)
}

func (c dealsContacts) SetLifecycle(ctx context.Context, orgID, contactID int64, to string, from []string, actor int64) error {
	_, err := c.svc.SetStageFrom(ctx, orgID, contactID, to, from, &actor)
	return mapDealContactErr(err)
}

func (c dealsContacts) Record(ctx context.Context, orgID, contactID int64, kind, summary string, meta map[string]any, actor int64) error {
	return mapDealContactErr(c.svc.AddSystemActivity(ctx, orgID, contactID, contacts.SystemActivity{
		Kind: kind, Summary: summary, Metadata: meta, ActorUserID: &actor,
	}))
}

// LeadReplied stops the contact's sequences that stop on reply (the lead
// was already re-scored).
func (w workflowEvents) LeadReplied(ctx context.Context, orgID, contactID int64) error {
	return w.nurture.StopForContact(ctx, orgID, contactID, nurture.StopReplied, true)
}

// LeadNotHot puts a new lead that is not hot into the org's new_lead
// sequence (the diagram's "Auto nurture"), if it has an active one.
func (w workflowEvents) LeadNotHot(ctx context.Context, orgID, contactID int64) error {
	_, err := w.nurture.AutoEnroll(ctx, orgID, contactID, nurture.TriggerNewLead)
	return err
}

// nurtureContacts gives the nurture module contact details, timeline
// entries and opt-out (nurture.Contacts).
type nurtureContacts struct {
	svc *contacts.Service
}

var _ nurture.Contacts = nurtureContacts{}

func mapNurtureContactErr(err error) error {
	if errors.Is(err, contacts.ErrNotFound) {
		return nurture.ErrContactNotFound
	}
	return err
}

func (c nurtureContacts) Visible(ctx context.Context, p principal.Principal, contactID int64) error {
	_, err := c.svc.Visible(ctx, p, contactID)
	return mapNurtureContactErr(err)
}

func (c nurtureContacts) Recipient(ctx context.Context, orgID, contactID int64) (nurture.Recipient, error) {
	r, err := c.svc.Recipient(ctx, orgID, contactID)
	if err != nil {
		return nurture.Recipient{}, mapNurtureContactErr(err)
	}
	return nurture.Recipient{
		FirstName: r.FirstName, LastName: r.LastName, Email: r.Email, PhoneKey: r.PhoneKey,
		LifecycleStage: r.LifecycleStage, OptedOut: r.OptedOut, LastWhatsAppInboundAt: r.LastWhatsAppInboundAt,
	}, nil
}

func (c nurtureContacts) RecordOutbound(ctx context.Context, orgID, contactID int64, channel, summary, body, externalID string) error {
	return mapNurtureContactErr(c.svc.RecordOutbound(ctx, orgID, contactID, contacts.OutboundMessage{
		Channel: channel, Summary: summary, Body: body, ExternalID: externalID,
	}))
}

func (c nurtureContacts) RecordEvent(ctx context.Context, orgID, contactID int64, kind, summary string, meta map[string]any) error {
	return mapNurtureContactErr(c.svc.AddSystemActivity(ctx, orgID, contactID, contacts.SystemActivity{
		Kind: kind, Summary: summary, Metadata: meta,
	}))
}

func (c nurtureContacts) OptOut(ctx context.Context, orgID, contactID int64, via string) error {
	return mapNurtureContactErr(c.svc.OptOut(ctx, orgID, contactID, via))
}

// nurtureNumbers gives the nurture module the org's WhatsApp sending number.
type nurtureNumbers struct {
	leads *leads.Service
}

func (n nurtureNumbers) SenderNumber(ctx context.Context, orgID int64) (string, error) {
	id, err := n.leads.SenderNumber(ctx, orgID)
	if errors.Is(err, leads.ErrNoWhatsAppNumber) {
		return "", nurture.ErrNoWhatsAppNumber
	}
	return id, err
}

// TaskOverdue reminds the assignee of an overdue task in the app.
func (n tasksNotifier) TaskOverdue(ctx context.Context, orgID int64, t tasks.Task) error {
	return n.notifications.Notify(ctx, orgID, []int64{t.AssigneeID}, notifications.Message{
		Kind:      notifications.KindTaskOverdue,
		Title:     "Overdue: " + t.Title,
		Body:      t.Description,
		Link:      fmt.Sprintf("/tasks/%d", t.ID),
		ContactID: t.ContactID,
		TaskID:    &t.ID,
	})
}

func (c leadsContacts) OptOut(ctx context.Context, orgID, contactID int64, via string) error {
	return mapContactErr(c.svc.OptOut(ctx, orgID, contactID, via))
}
