-- Multi-tenancy, step 2 (contract): org_id and role are required, roles
-- are limited to the five defined ones, stage names are unique per org,
-- and a contact's owner must belong to the contact's org (enforced by a
-- composite foreign key, not only by application code).

-- +goose Up
ALTER TABLE users
    ALTER COLUMN org_id SET NOT NULL,
    ALTER COLUMN role SET NOT NULL,
    ADD CONSTRAINT users_role_check CHECK (role IN ('owner', 'admin', 'manager', 'rep', 'viewer')),
    ADD CONSTRAINT users_id_org_id_key UNIQUE (id, org_id);
CREATE INDEX users_org_id_idx ON users (org_id);

ALTER TABLE contacts ALTER COLUMN org_id SET NOT NULL;
CREATE INDEX contacts_org_id_idx ON contacts (org_id);
ALTER TABLE contacts
    DROP CONSTRAINT contacts_lead_owner_id_fkey,
    ADD CONSTRAINT contacts_lead_owner_same_org_fkey
        FOREIGN KEY (lead_owner_id, org_id) REFERENCES users (id, org_id);

ALTER TABLE deals ALTER COLUMN org_id SET NOT NULL;
CREATE INDEX deals_org_id_idx ON deals (org_id);

ALTER TABLE deal_stages
    ALTER COLUMN org_id SET NOT NULL,
    DROP CONSTRAINT deal_stages_name_key,
    ADD CONSTRAINT deal_stages_org_id_name_key UNIQUE (org_id, name);

-- +goose Down
-- Best effort: restoring the global stage-name uniqueness fails once two
-- orgs share a stage name.
ALTER TABLE deal_stages
    DROP CONSTRAINT deal_stages_org_id_name_key,
    ADD CONSTRAINT deal_stages_name_key UNIQUE (name),
    ALTER COLUMN org_id DROP NOT NULL;

DROP INDEX deals_org_id_idx;
ALTER TABLE deals ALTER COLUMN org_id DROP NOT NULL;

ALTER TABLE contacts
    DROP CONSTRAINT contacts_lead_owner_same_org_fkey,
    ADD CONSTRAINT contacts_lead_owner_id_fkey FOREIGN KEY (lead_owner_id) REFERENCES users (id);
DROP INDEX contacts_org_id_idx;
ALTER TABLE contacts ALTER COLUMN org_id DROP NOT NULL;

DROP INDEX users_org_id_idx;
ALTER TABLE users
    DROP CONSTRAINT users_id_org_id_key,
    DROP CONSTRAINT users_role_check,
    ALTER COLUMN role DROP NOT NULL,
    ALTER COLUMN org_id DROP NOT NULL;
