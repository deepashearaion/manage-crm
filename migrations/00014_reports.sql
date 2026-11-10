-- Reports (workflow phase 7f): settings for the automatic weekly report
-- email, and a log of sent reports so each period is sent once.

-- +goose Up
CREATE TABLE report_settings (
    org_id BIGINT PRIMARY KEY REFERENCES orgs (id) ON DELETE CASCADE,
    weekly_email BOOLEAN NOT NULL DEFAULT true,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- period_start is the first IST day the report covers (a Monday for weekly).
CREATE TABLE report_runs (
    org_id BIGINT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('weekly')),
    period_start DATE NOT NULL,
    recipients INT NOT NULL DEFAULT 0,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, kind, period_start)
);

-- Reports filter deals by close time and contacts by creation time.
CREATE INDEX contacts_org_id_created_at_idx ON contacts (org_id, created_at);
CREATE INDEX invoices_org_id_issue_date_idx ON invoices (org_id, issue_date) WHERE status = 'issued';

-- +goose Down
DROP INDEX invoices_org_id_issue_date_idx;
DROP INDEX contacts_org_id_created_at_idx;
DROP TABLE report_runs;
DROP TABLE report_settings;
