-- Custom roles: each org defines its roles, and each role grants, per
-- module, the actions view/create/edit/delete with a scope of none, own or
-- all records. Users have exactly one role. The five fixed roles become
-- every org's starting roles with the same permissions they had; "Owner"
-- is locked (full access, cannot be changed or deleted).
--
-- Keep the seeded permissions in step with auth.DefaultRoles.

-- +goose Up
CREATE TABLE roles (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name VARCHAR(60) NOT NULL CHECK (btrim(name) <> ''),
    description VARCHAR(300) NOT NULL DEFAULT '',
    is_owner BOOLEAN NOT NULL DEFAULT false,
    permissions JSONB NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(permissions) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (id, org_id)
);
CREATE UNIQUE INDEX roles_org_id_name_key ON roles (org_id, lower(name));
CREATE UNIQUE INDEX roles_one_owner_per_org ON roles (org_id) WHERE is_owner;

INSERT INTO roles (org_id, name, description, is_owner, permissions)
SELECT o.id, d.name, d.description, d.is_owner, d.permissions::jsonb
FROM orgs o
CROSS JOIN (VALUES
    ('Owner', 'Full access to everything. Cannot be changed or deleted.', true,
     '{"contacts":{"view":"all","create":"all","edit":"all","delete":"all"},"deals":{"view":"all","create":"all","edit":"all","delete":"all"},"tasks":{"view":"all","create":"all","edit":"all","delete":"all"},"nurture":{"view":"all","create":"all","edit":"all","delete":"all"},"invoices":{"view":"all","create":"all","edit":"all","delete":"all"},"reports":{"view":"all"},"settings":{"view":"all","edit":"all"},"users":{"view":"all","create":"all","edit":"all"},"roles":{"view":"all","create":"all","edit":"all","delete":"all"}}'),
    ('Admin', 'Everything, including users and roles.', false,
     '{"contacts":{"view":"all","create":"all","edit":"all","delete":"all"},"deals":{"view":"all","create":"all","edit":"all","delete":"all"},"tasks":{"view":"all","create":"all","edit":"all","delete":"all"},"nurture":{"view":"all","create":"all","edit":"all","delete":"all"},"invoices":{"view":"all","create":"all","edit":"all","delete":"all"},"reports":{"view":"all"},"settings":{"view":"all","edit":"all"},"users":{"view":"all","create":"all","edit":"all"},"roles":{"view":"all","create":"all","edit":"all","delete":"all"}}'),
    ('Manager', 'All records and settings; cannot manage users or roles.', false,
     '{"contacts":{"view":"all","create":"all","edit":"all","delete":"all"},"deals":{"view":"all","create":"all","edit":"all","delete":"all"},"tasks":{"view":"all","create":"all","edit":"all","delete":"all"},"nurture":{"view":"all","create":"all","edit":"all","delete":"all"},"invoices":{"view":"all","create":"all","edit":"all","delete":"all"},"reports":{"view":"all"},"settings":{"view":"all","edit":"all"},"users":{"view":"all","create":"none","edit":"none"},"roles":{"view":"all","create":"none","edit":"none","delete":"none"}}'),
    ('Rep', 'Works only on the records they own or are assigned.', false,
     '{"contacts":{"view":"own","create":"own","edit":"own","delete":"own"},"deals":{"view":"own","create":"own","edit":"own","delete":"own"},"tasks":{"view":"own","create":"own","edit":"own","delete":"own"},"nurture":{"view":"own","create":"own","edit":"own","delete":"none"},"invoices":{"view":"own","create":"own","edit":"own","delete":"own"},"reports":{"view":"own"},"settings":{"view":"none","edit":"none"},"users":{"view":"all","create":"none","edit":"none"},"roles":{"view":"none","create":"none","edit":"none","delete":"none"}}'),
    ('Viewer', 'Read-only access to every record.', false,
     '{"contacts":{"view":"all","create":"none","edit":"none","delete":"none"},"deals":{"view":"all","create":"none","edit":"none","delete":"none"},"tasks":{"view":"all","create":"none","edit":"none","delete":"none"},"nurture":{"view":"all","create":"none","edit":"none","delete":"none"},"invoices":{"view":"all","create":"none","edit":"none","delete":"none"},"reports":{"view":"all"},"settings":{"view":"none","edit":"none"},"users":{"view":"all","create":"none","edit":"none"},"roles":{"view":"none","create":"none","edit":"none","delete":"none"}}')
) AS d (name, description, is_owner, permissions);

ALTER TABLE users ADD COLUMN role_id BIGINT;
UPDATE users u SET role_id = r.id
FROM roles r
WHERE r.org_id = u.org_id AND lower(r.name) = u.role;

ALTER TABLE users
    ALTER COLUMN role_id SET NOT NULL,
    ADD CONSTRAINT users_role_id_org_id_fkey FOREIGN KEY (role_id, org_id) REFERENCES roles (id, org_id),
    DROP CONSTRAINT users_role_check,
    DROP COLUMN role;
CREATE INDEX users_role_id_org_id_idx ON users (role_id, org_id);

-- +goose Down
-- Custom roles map to the closest fixed role by name, else to viewer.
ALTER TABLE users ADD COLUMN role TEXT;
UPDATE users u SET role = CASE
    WHEN r.is_owner THEN 'owner'
    WHEN lower(r.name) IN ('admin', 'manager', 'rep', 'viewer') THEN lower(r.name)
    ELSE 'viewer' END
FROM roles r
WHERE r.id = u.role_id;
ALTER TABLE users
    ALTER COLUMN role SET NOT NULL,
    ADD CONSTRAINT users_role_check CHECK (role IN ('owner', 'admin', 'manager', 'rep', 'viewer')),
    DROP COLUMN role_id;
DROP TABLE roles;
