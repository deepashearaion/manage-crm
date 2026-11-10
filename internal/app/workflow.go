package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/deepashearaion/manage-crm/internal/auth"
	"github.com/deepashearaion/manage-crm/internal/contacts"
	"github.com/deepashearaion/manage-crm/internal/leads"
	"github.com/deepashearaion/manage-crm/internal/notifications"
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
}

var _ leads.Events = workflowEvents{}

func contactLink(id int64) string { return fmt.Sprintf("/contacts/%d", id) }

// LeadTurnedHot gives the owner a follow-up task carrying the summary and
// alerts them in the app and by email. An unowned hot lead alerts the
// org's owners, admins and managers instead, so someone assigns it.
func (w workflowEvents) LeadTurnedHot(ctx context.Context, e leads.HotLead) error {
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
