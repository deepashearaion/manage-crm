-- Lead capture (workflow phase 7a): lifecycle stage and dedupe keys on
-- contacts, the contact activity timeline, lead scores, and the per-org
-- configuration for intake (form sources, WhatsApp numbers), rule-based
-- scoring and owner routing.
--
-- There is no separate lead entity: a lead is a contact whose
-- lifecycle_stage is 'lead' (later mql, sql, opportunity, customer, lost).

-- +goose Up

-- crm_phone_key normalizes a phone number for duplicate detection: digits
-- only, Indian national numbers (10 digits, or 11 with a leading 0) get the
-- 91 country code, a 00 international prefix is dropped, and anything
-- shorter than 8 digits is not a usable key. Lookups call the same function,
-- so Go code never re-implements it.
-- +goose StatementBegin
CREATE FUNCTION crm_phone_key(raw text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN d ~ '^0[0-9]{10}$' THEN '91' || substr(d, 2)
        WHEN length(d) = 10 THEN '91' || d
        WHEN d LIKE '00%' AND length(d) >= 10 THEN substr(d, 3)
        WHEN length(d) >= 8 THEN d
    END
    FROM (SELECT regexp_replace(COALESCE(raw, ''), '[^0-9]', '', 'g') AS d) AS digits
$$;
-- +goose StatementEnd

ALTER TABLE contacts
    ADD COLUMN lifecycle_stage TEXT NOT NULL DEFAULT 'lead'
        CONSTRAINT contacts_lifecycle_stage_check
        CHECK (lifecycle_stage IN ('lead', 'mql', 'sql', 'opportunity', 'customer', 'lost')),
    ADD COLUMN email_key TEXT GENERATED ALWAYS AS (NULLIF(lower(btrim(email)), '')) STORED,
    ADD COLUMN phone_key TEXT GENERATED ALWAYS AS (crm_phone_key(mobile)) STORED,
    ADD CONSTRAINT contacts_id_org_id_key UNIQUE (id, org_id);
CREATE INDEX contacts_org_id_email_key_idx ON contacts (org_id, email_key) WHERE email_key IS NOT NULL;
CREATE INDEX contacts_org_id_phone_key_idx ON contacts (org_id, phone_key) WHERE phone_key IS NOT NULL;
CREATE INDEX contacts_org_id_lifecycle_stage_idx ON contacts (org_id, lifecycle_stage);

-- Everything that happened with a contact, newest first: captures, inbound
-- and outbound messages, notes, calls, score and owner changes. external_id
-- is the channel's message id (e.g. a WhatsApp wamid); its uniqueness makes
-- webhook retries harmless.
CREATE TABLE contact_activities (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL,
    contact_id BIGINT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN (
        'captured', 'duplicate_captured', 'message', 'note', 'call', 'email',
        'score_changed', 'owner_assigned', 'lifecycle_changed'
    )),
    channel TEXT CHECK (channel IN ('web_form', 'whatsapp', 'email', 'phone', 'manual', 'api', 'system')),
    direction TEXT CHECK (direction IN ('inbound', 'outbound')),
    summary TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}',
    external_id TEXT,
    actor_user_id BIGINT REFERENCES users (id) ON DELETE SET NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (contact_id, org_id) REFERENCES contacts (id, org_id) ON DELETE CASCADE
);
CREATE INDEX contact_activities_contact_id_occurred_at_idx ON contact_activities (contact_id, occurred_at DESC, id DESC);
CREATE INDEX contact_activities_actor_user_id_idx ON contact_activities (actor_user_id);
CREATE UNIQUE INDEX contact_activities_external_id_key
    ON contact_activities (org_id, channel, external_id) WHERE external_id IS NOT NULL;

-- The current score of each scored contact. reasons lists the rules that
-- matched; method is 'rules' until AI scoring is added.
CREATE TABLE lead_scores (
    contact_id BIGINT PRIMARY KEY,
    org_id BIGINT NOT NULL,
    score SMALLINT NOT NULL CHECK (score BETWEEN 0 AND 100),
    temperature TEXT NOT NULL CHECK (temperature IN ('cold', 'warm', 'hot')),
    reasons JSONB NOT NULL DEFAULT '[]',
    method TEXT NOT NULL DEFAULT 'rules' CHECK (method IN ('rules', 'ai')),
    scored_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (contact_id, org_id) REFERENCES contacts (id, org_id) ON DELETE CASCADE
);
CREATE INDEX lead_scores_org_id_temperature_idx ON lead_scores (org_id, temperature, score DESC);

-- Per-org lead settings. Orgs without a row use the defaults below.
-- default_owner_id receives new leads that no assignment rule matches.
CREATE TABLE lead_settings (
    org_id BIGINT PRIMARY KEY REFERENCES orgs (id) ON DELETE CASCADE,
    hot_threshold SMALLINT NOT NULL DEFAULT 70,
    warm_threshold SMALLINT NOT NULL DEFAULT 40,
    default_owner_id BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT lead_settings_thresholds_check
        CHECK (0 < warm_threshold AND warm_threshold < hot_threshold AND hot_threshold <= 100),
    CONSTRAINT lead_settings_default_owner_same_org_fkey
        FOREIGN KEY (default_owner_id, org_id) REFERENCES users (id, org_id)
);
CREATE INDEX lead_settings_default_owner_id_idx ON lead_settings (default_owner_id);

-- Web forms and API integrations that submit leads. public_key identifies
-- the source in POST /api/v1/intake/forms/{key}; it is embedded in public
-- web pages, so it is an identifier, not a secret, and can be rotated.
CREATE TABLE lead_sources (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    channel TEXT NOT NULL CHECK (channel IN ('web_form', 'api')),
    public_key TEXT NOT NULL UNIQUE,
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT lead_sources_org_id_name_key UNIQUE (org_id, name)
);

-- WhatsApp Business phone numbers (Meta Cloud API phone_number_id) and the
-- org whose leads their inbound messages become.
CREATE TABLE whatsapp_accounts (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    phone_number_id TEXT NOT NULL UNIQUE,
    display_phone TEXT NOT NULL DEFAULT '',
    source_name VARCHAR(100) NOT NULL DEFAULT 'WhatsApp',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX whatsapp_accounts_org_id_idx ON whatsapp_accounts (org_id);

-- Rule-based scoring: a contact's score is the sum of the points of every
-- active rule it matches, clamped to 0..100.
CREATE TABLE lead_scoring_rules (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    criterion TEXT NOT NULL CHECK (criterion IN (
        'has_email', 'has_mobile', 'has_company', 'source_equals', 'destination_equals',
        'channel_used', 'text_contains', 'inbound_messages_at_least'
    )),
    value TEXT NOT NULL DEFAULT '',
    points SMALLINT NOT NULL CHECK (points BETWEEN -100 AND 100),
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX lead_scoring_rules_org_id_idx ON lead_scoring_rules (org_id);

-- Owner routing for new leads: the first active rule (by position, then id)
-- that matches assigns the lead to one of its members, round robin.
CREATE TABLE lead_assignment_rules (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    position INT NOT NULL DEFAULT 0,
    criterion TEXT NOT NULL CHECK (criterion IN (
        'any', 'source_equals', 'channel_equals', 'destination_equals', 'temperature_equals'
    )),
    value TEXT NOT NULL DEFAULT '',
    active BOOLEAN NOT NULL DEFAULT true,
    rr_cursor BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT lead_assignment_rules_id_org_id_key UNIQUE (id, org_id)
);
CREATE INDEX lead_assignment_rules_org_id_idx ON lead_assignment_rules (org_id, position, id);

CREATE TABLE lead_assignment_rule_members (
    rule_id BIGINT NOT NULL,
    org_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    PRIMARY KEY (rule_id, user_id),
    FOREIGN KEY (rule_id, org_id) REFERENCES lead_assignment_rules (id, org_id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, org_id) REFERENCES users (id, org_id) ON DELETE CASCADE
);
CREATE INDEX lead_assignment_rule_members_user_id_idx ON lead_assignment_rule_members (user_id);

-- Default scoring rules for existing orgs. New orgs get the same set from
-- leads.SeedDefaults; keep the two lists in step.
INSERT INTO lead_scoring_rules (org_id, name, criterion, value, points)
SELECT o.id, r.name, r.criterion, r.value, r.points
FROM orgs o
CROSS JOIN (VALUES
    ('Has email', 'has_email', '', 10),
    ('Has mobile', 'has_mobile', '', 15),
    ('Reached us on WhatsApp', 'channel_used', 'whatsapp', 15),
    ('Replied at least twice', 'inbound_messages_at_least', '2', 20),
    ('Asked about price', 'text_contains', 'price', 15),
    ('Asked for a quote', 'text_contains', 'quote', 15),
    ('Mentioned budget', 'text_contains', 'budget', 10),
    ('Urgent', 'text_contains', 'urgent', 10)
) AS r (name, criterion, value, points);

-- +goose Down
DROP TABLE lead_assignment_rule_members;
DROP TABLE lead_assignment_rules;
DROP TABLE lead_scoring_rules;
DROP TABLE whatsapp_accounts;
DROP TABLE lead_sources;
DROP TABLE lead_settings;
DROP TABLE lead_scores;
DROP TABLE contact_activities;
DROP INDEX contacts_org_id_lifecycle_stage_idx;
DROP INDEX contacts_org_id_phone_key_idx;
DROP INDEX contacts_org_id_email_key_idx;
ALTER TABLE contacts
    DROP CONSTRAINT contacts_id_org_id_key,
    DROP COLUMN phone_key,
    DROP COLUMN email_key,
    DROP COLUMN lifecycle_stage;
DROP FUNCTION crm_phone_key(text);
