-- Multi-tenancy, step 1 (expand and backfill): add orgs, give users an org
-- and a role, and give tenant data an org_id. Everything that already exists
-- moves into one "Default organization"; its lowest-id user becomes the
-- owner and other existing users become admins, keeping the full access
-- they had before. Step 2 (00008) makes the columns required.

-- +goose Up
CREATE TABLE orgs (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(200) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE users
    ADD COLUMN org_id BIGINT REFERENCES orgs(id),
    ADD COLUMN role TEXT;
ALTER TABLE contacts ADD COLUMN org_id BIGINT REFERENCES orgs(id);
ALTER TABLE deals ADD COLUMN org_id BIGINT REFERENCES orgs(id);
ALTER TABLE deal_stages ADD COLUMN org_id BIGINT REFERENCES orgs(id);

INSERT INTO orgs (name)
SELECT 'Default organization'
WHERE EXISTS (SELECT 1 FROM users)
   OR EXISTS (SELECT 1 FROM contacts)
   OR EXISTS (SELECT 1 FROM deals)
   OR EXISTS (SELECT 1 FROM deal_stages);

UPDATE users SET org_id = (SELECT min(id) FROM orgs);
UPDATE users SET role = CASE WHEN id = (SELECT min(id) FROM users) THEN 'owner' ELSE 'admin' END;
UPDATE contacts SET org_id = (SELECT min(id) FROM orgs);
UPDATE deals SET org_id = (SELECT min(id) FROM orgs);
UPDATE deal_stages SET org_id = (SELECT min(id) FROM orgs);

-- +goose Down
ALTER TABLE deal_stages DROP COLUMN org_id;
ALTER TABLE deals DROP COLUMN org_id;
ALTER TABLE contacts DROP COLUMN org_id;
ALTER TABLE users DROP COLUMN role, DROP COLUMN org_id;
DROP TABLE orgs;
