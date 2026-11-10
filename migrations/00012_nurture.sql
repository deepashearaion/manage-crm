-- Nurture (workflow phase 7d): automated email and WhatsApp sequences for
-- leads that are not hot and for lost deals, marketing opt-out, and overdue
-- task reminders. A scheduler in cmd/api runs due enrollments and reminders.

-- +goose Up

-- A contact who opted out of marketing (unsubscribe link, WhatsApp STOP)
-- receives no nurture messages.
ALTER TABLE contacts ADD COLUMN marketing_opt_out_at TIMESTAMPTZ;

ALTER TABLE contact_activities
    DROP CONSTRAINT contact_activities_kind_check,
    ADD CONSTRAINT contact_activities_kind_check CHECK (kind IN (
        'captured', 'duplicate_captured', 'message', 'note', 'call', 'email',
        'score_changed', 'owner_assigned', 'lifecycle_changed',
        'task_created', 'task_completed',
        'deal_created', 'deal_stage_changed', 'deal_won', 'deal_lost',
        'nurture_enrolled', 'nurture_stopped', 'opted_out'
    ));

-- Overdue reminders are sent once per due date.
ALTER TABLE tasks ADD COLUMN reminded_at TIMESTAMPTZ;
CREATE INDEX tasks_overdue_idx ON tasks (due_at) WHERE status = 'open' AND reminded_at IS NULL;

ALTER TABLE notifications
    DROP CONSTRAINT notifications_kind_check,
    ADD CONSTRAINT notifications_kind_check CHECK (kind IN ('hot_lead', 'lead_assigned', 'task_assigned', 'task_overdue'));

-- A sequence of timed steps. trigger says what enrolls contacts
-- automatically: 'new_lead' (a new lead that is not hot), 'deal_lost', or
-- 'manual' (only by hand). An org has at most one active sequence per
-- automatic trigger.
CREATE TABLE nurture_sequences (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    trigger TEXT NOT NULL CHECK (trigger IN ('manual', 'new_lead', 'deal_lost')),
    active BOOLEAN NOT NULL DEFAULT false,
    stop_on_reply BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT nurture_sequences_org_id_name_key UNIQUE (org_id, name),
    CONSTRAINT nurture_sequences_id_org_id_key UNIQUE (id, org_id)
);
CREATE UNIQUE INDEX nurture_sequences_active_trigger_key
    ON nurture_sequences (org_id, trigger) WHERE active AND trigger <> 'manual';

-- A step waits delay_minutes after the previous step (or the enrollment)
-- and sends one message. Bodies may use {{first_name}}, {{last_name}} and
-- {{full_name}}. WhatsApp free text is only allowed within 24 hours of the
-- contact's last WhatsApp message; outside that window the step sends the
-- approved template whatsapp_template (with whatsapp_template_params as
-- body parameters), or is skipped when it has none.
CREATE TABLE nurture_steps (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL,
    sequence_id BIGINT NOT NULL,
    position INT NOT NULL,
    delay_minutes INT NOT NULL CHECK (delay_minutes BETWEEN 0 AND 525600),
    channel TEXT NOT NULL CHECK (channel IN ('email', 'whatsapp')),
    subject VARCHAR(200) NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    whatsapp_template VARCHAR(512) NOT NULL DEFAULT '',
    whatsapp_language VARCHAR(20) NOT NULL DEFAULT 'en',
    whatsapp_template_params TEXT[] NOT NULL DEFAULT '{}',
    FOREIGN KEY (sequence_id, org_id) REFERENCES nurture_sequences (id, org_id) ON DELETE CASCADE,
    CONSTRAINT nurture_steps_sequence_id_position_key UNIQUE (sequence_id, position)
);

-- A contact going through a sequence. next_step is the 0-based index of the
-- step to send next, at next_run_at. attempts counts failed sends of that
-- step.
CREATE TABLE nurture_enrollments (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL,
    sequence_id BIGINT NOT NULL,
    contact_id BIGINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'completed', 'stopped')),
    stop_reason TEXT NOT NULL DEFAULT '',
    next_step INT NOT NULL DEFAULT 0,
    next_run_at TIMESTAMPTZ NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    enrolled_by_id BIGINT REFERENCES users (id) ON DELETE SET NULL,
    enrolled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    CONSTRAINT nurture_enrollments_finished_check CHECK ((status = 'active') = (finished_at IS NULL)),
    FOREIGN KEY (sequence_id, org_id) REFERENCES nurture_sequences (id, org_id) ON DELETE CASCADE,
    FOREIGN KEY (contact_id, org_id) REFERENCES contacts (id, org_id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX nurture_enrollments_active_key ON nurture_enrollments (contact_id, sequence_id) WHERE status = 'active';
CREATE INDEX nurture_enrollments_sequence_id_idx ON nurture_enrollments (sequence_id);
CREATE INDEX nurture_enrollments_due_idx ON nurture_enrollments (next_run_at) WHERE status = 'active';
CREATE INDEX nurture_enrollments_enrolled_by_id_idx ON nurture_enrollments (enrolled_by_id);

-- +goose Down
DROP TABLE nurture_enrollments;
DROP TABLE nurture_steps;
DROP TABLE nurture_sequences;
DELETE FROM notifications WHERE kind = 'task_overdue';
ALTER TABLE notifications
    DROP CONSTRAINT notifications_kind_check,
    ADD CONSTRAINT notifications_kind_check CHECK (kind IN ('hot_lead', 'lead_assigned', 'task_assigned'));
DROP INDEX tasks_overdue_idx;
ALTER TABLE tasks DROP COLUMN reminded_at;
DELETE FROM contact_activities WHERE kind IN ('nurture_enrolled', 'nurture_stopped', 'opted_out');
ALTER TABLE contact_activities
    DROP CONSTRAINT contact_activities_kind_check,
    ADD CONSTRAINT contact_activities_kind_check CHECK (kind IN (
        'captured', 'duplicate_captured', 'message', 'note', 'call', 'email',
        'score_changed', 'owner_assigned', 'lifecycle_changed',
        'task_created', 'task_completed',
        'deal_created', 'deal_stage_changed', 'deal_won', 'deal_lost'
    ));
ALTER TABLE contacts DROP COLUMN marketing_opt_out_at;
