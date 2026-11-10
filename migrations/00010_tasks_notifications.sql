-- Tasks and notifications (workflow phase 7b): when a lead turns hot its
-- owner is alerted at once (in-app notification and email) and gets a
-- follow-up task carrying a summary of the lead.

-- +goose Up

-- How soon a hot lead's follow-up task is due.
ALTER TABLE lead_settings
    ADD COLUMN hot_followup_minutes INT NOT NULL DEFAULT 15
        CONSTRAINT lead_settings_hot_followup_minutes_check CHECK (hot_followup_minutes BETWEEN 1 AND 10080);

-- Tasks appear on the contact timeline.
ALTER TABLE contact_activities
    DROP CONSTRAINT contact_activities_kind_check,
    ADD CONSTRAINT contact_activities_kind_check CHECK (kind IN (
        'captured', 'duplicate_captured', 'message', 'note', 'call', 'email',
        'score_changed', 'owner_assigned', 'lifecycle_changed',
        'task_created', 'task_completed'
    ));

-- A to-do for one user, usually about a contact. origin says what created
-- it: 'manual' (a user) or a workflow step such as 'hot_lead'. A contact has
-- at most one open task per workflow origin, so a lead that cools down and
-- turns hot again does not pile up duplicate follow-ups.
CREATE TABLE tasks (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    contact_id BIGINT,
    assignee_id BIGINT NOT NULL,
    created_by_id BIGINT REFERENCES users (id) ON DELETE SET NULL,
    title VARCHAR(200) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    type TEXT NOT NULL DEFAULT 'todo' CHECK (type IN ('follow_up', 'call', 'email', 'whatsapp', 'meeting', 'todo')),
    priority TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high', 'urgent')),
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'done', 'cancelled')),
    origin TEXT NOT NULL DEFAULT 'manual' CHECK (origin IN ('manual', 'hot_lead')),
    due_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT tasks_completed_at_check CHECK ((status = 'done') = (completed_at IS NOT NULL)),
    CONSTRAINT tasks_contact_same_org_fkey
        FOREIGN KEY (contact_id, org_id) REFERENCES contacts (id, org_id) ON DELETE CASCADE,
    CONSTRAINT tasks_assignee_same_org_fkey
        FOREIGN KEY (assignee_id, org_id) REFERENCES users (id, org_id),
    CONSTRAINT tasks_id_org_id_key UNIQUE (id, org_id)
);
CREATE INDEX tasks_org_id_status_due_at_idx ON tasks (org_id, status, due_at);
CREATE INDEX tasks_assignee_id_status_due_at_idx ON tasks (assignee_id, status, due_at);
CREATE INDEX tasks_contact_id_idx ON tasks (contact_id);
CREATE INDEX tasks_created_by_id_idx ON tasks (created_by_id);
CREATE UNIQUE INDEX tasks_open_workflow_task_key
    ON tasks (contact_id, origin) WHERE status = 'open' AND origin <> 'manual';

-- In-app notifications for one user. link is a frontend path.
CREATE TABLE notifications (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('hot_lead', 'lead_assigned', 'task_assigned')),
    title VARCHAR(200) NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    link TEXT NOT NULL DEFAULT '',
    contact_id BIGINT,
    task_id BIGINT,
    read_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT notifications_user_same_org_fkey
        FOREIGN KEY (user_id, org_id) REFERENCES users (id, org_id) ON DELETE CASCADE,
    CONSTRAINT notifications_contact_same_org_fkey
        FOREIGN KEY (contact_id, org_id) REFERENCES contacts (id, org_id) ON DELETE CASCADE,
    CONSTRAINT notifications_task_same_org_fkey
        FOREIGN KEY (task_id, org_id) REFERENCES tasks (id, org_id) ON DELETE CASCADE
);
CREATE INDEX notifications_user_id_created_at_idx ON notifications (user_id, created_at DESC, id DESC);
CREATE INDEX notifications_user_id_unread_idx ON notifications (user_id) WHERE read_at IS NULL;
CREATE INDEX notifications_contact_id_idx ON notifications (contact_id);
CREATE INDEX notifications_task_id_idx ON notifications (task_id);

-- +goose Down
DROP TABLE notifications;
DROP TABLE tasks;
DELETE FROM contact_activities WHERE kind IN ('task_created', 'task_completed');
ALTER TABLE contact_activities
    DROP CONSTRAINT contact_activities_kind_check,
    ADD CONSTRAINT contact_activities_kind_check CHECK (kind IN (
        'captured', 'duplicate_captured', 'message', 'note', 'call', 'email',
        'score_changed', 'owner_assigned', 'lifecycle_changed'
    ));
ALTER TABLE lead_settings DROP COLUMN hot_followup_minutes;
