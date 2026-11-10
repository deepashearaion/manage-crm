-- Deal pipeline (workflow phase 7c): guided stages with win probabilities,
-- deals with money in integer paise and billing type, stage history, and
-- the won/lost outcome.
--
-- The legacy NUMERIC rupee columns (amount, monthly_recurring_revenue,
-- annual_recurring_revenue) are converted and dropped. A deal's amount is
-- its value per billing period: the whole value for one_time deals, the
-- monthly / quarterly / yearly fee for recurring ones. ARR is derived from
-- it (amount × periods per year) and MRR is ARR / 12.

-- +goose Up

-- crm_deal_value_paise is what a deal is worth for pipeline, forecast and
-- won/lost totals: the amount of a one-time deal, or the first-year value
-- (ARR) of a recurring one. Every query uses this one definition.
-- +goose StatementBegin
CREATE FUNCTION crm_deal_value_paise(amount_paise bigint, billing_type text, billing_period text) RETURNS bigint
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN billing_type <> 'recurring' THEN amount_paise
        WHEN billing_period = 'monthly' THEN amount_paise * 12
        WHEN billing_period = 'quarterly' THEN amount_paise * 4
        ELSE amount_paise
    END
$$;
-- +goose StatementEnd

-- Stages: order, kind (open, won, lost), win probability, and guidance:
-- what to do in the stage and which deal fields must be filled to enter it.
ALTER TABLE deal_stages
    ADD COLUMN position INT NOT NULL DEFAULT 0,
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'open'
        CONSTRAINT deal_stages_kind_check CHECK (kind IN ('open', 'won', 'lost')),
    ADD COLUMN probability SMALLINT NOT NULL DEFAULT 10
        CONSTRAINT deal_stages_probability_check CHECK (probability BETWEEN 0 AND 100),
    ADD COLUMN guidance TEXT NOT NULL DEFAULT '',
    ADD COLUMN required_fields TEXT[] NOT NULL DEFAULT '{}'
        CONSTRAINT deal_stages_required_fields_check
        CHECK (required_fields <@ ARRAY['amount', 'expected_close_date', 'contact']::text[]),
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD CONSTRAINT deal_stages_id_org_id_key UNIQUE (id, org_id);

-- The default stages (seeded for every org) get their settings; any other
-- stage stays open, ordered after them by id.
UPDATE deal_stages SET position = d.position, kind = d.kind, probability = d.probability,
    guidance = d.guidance, required_fields = d.required_fields
FROM (VALUES
    ('Qualification', 1, 'open', 10, 'Confirm the need, budget, decision maker and timeline.', '{}'::text[]),
    ('Needs Analysis', 2, 'open', 25, 'Understand the requirements: dates, group size, preferences.', '{contact}'::text[]),
    ('Proposal', 3, 'open', 50, 'Send the proposal or quote with pricing.', '{amount,contact}'::text[]),
    ('Negotiation', 4, 'open', 75, 'Agree the final terms and price, and confirm the close date.', '{amount,expected_close_date,contact}'::text[]),
    ('Closed Won', 5, 'won', 100, 'Payment or signed confirmation received.', '{amount,contact}'::text[]),
    ('Closed Lost', 6, 'lost', 0, 'Record why the deal was lost.', '{}'::text[])
) AS d (name, position, kind, probability, guidance, required_fields)
WHERE deal_stages.name = d.name;
UPDATE deal_stages SET position = 100 + id WHERE position = 0;

ALTER TABLE deal_stages ADD CONSTRAINT deal_stages_outcome_probability_check
    CHECK ((kind <> 'won' OR probability = 100) AND (kind <> 'lost' OR probability = 0));
CREATE INDEX deal_stages_org_id_position_idx ON deal_stages (org_id, position);

-- Deals.
ALTER TABLE deals
    ADD COLUMN amount_paise BIGINT NOT NULL DEFAULT 0 CONSTRAINT deals_amount_paise_check CHECK (amount_paise >= 0),
    ADD COLUMN billing_type TEXT NOT NULL DEFAULT 'one_time'
        CONSTRAINT deals_billing_type_check CHECK (billing_type IN ('one_time', 'recurring')),
    ADD COLUMN billing_period TEXT
        CONSTRAINT deals_billing_period_check CHECK (billing_period IN ('monthly', 'quarterly', 'yearly')),
    ADD COLUMN status TEXT NOT NULL DEFAULT 'open' CONSTRAINT deals_status_check CHECK (status IN ('open', 'won', 'lost')),
    ADD COLUMN expected_close_date DATE,
    ADD COLUMN closed_at TIMESTAMPTZ,
    ADD COLUMN lost_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN description TEXT NOT NULL DEFAULT '',
    ADD COLUMN stage_entered_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Backfill: recurring revenue becomes a recurring deal (yearly when ARR was
-- set, else monthly); otherwise the amount is a one-time value.
UPDATE deals SET
    billing_type = CASE WHEN COALESCE(annual_recurring_revenue, 0) > 0 OR COALESCE(monthly_recurring_revenue, 0) > 0
        THEN 'recurring' ELSE 'one_time' END,
    billing_period = CASE
        WHEN COALESCE(annual_recurring_revenue, 0) > 0 THEN 'yearly'
        WHEN COALESCE(monthly_recurring_revenue, 0) > 0 THEN 'monthly' END,
    amount_paise = round(100 * CASE
        WHEN COALESCE(annual_recurring_revenue, 0) > 0 THEN annual_recurring_revenue
        WHEN COALESCE(monthly_recurring_revenue, 0) > 0 THEN monthly_recurring_revenue
        ELSE COALESCE(amount, 0) END),
    stage_entered_at = COALESCE(updated_at, created_at, now());

-- Deals without a stage go to their org's first stage.
UPDATE deals SET stage_id = (
    SELECT s.id FROM deal_stages s WHERE s.org_id = deals.org_id ORDER BY s.position, s.id LIMIT 1
) WHERE stage_id IS NULL;
UPDATE deals SET status = s.kind,
    closed_at = CASE WHEN s.kind <> 'open' THEN COALESCE(deals.updated_at, deals.created_at, now()) END
FROM deal_stages s WHERE s.id = deals.stage_id;

UPDATE deals SET title = 'Untitled deal' WHERE btrim(title) = '';
ALTER TABLE deals
    DROP COLUMN amount,
    DROP COLUMN monthly_recurring_revenue,
    DROP COLUMN annual_recurring_revenue,
    ALTER COLUMN stage_id SET NOT NULL,
    ALTER COLUMN created_at SET NOT NULL,
    ALTER COLUMN updated_at SET NOT NULL,
    ADD CONSTRAINT deals_billing_period_required_check
        CHECK ((billing_type = 'recurring') = (billing_period IS NOT NULL)),
    ADD CONSTRAINT deals_closed_at_check CHECK ((status = 'open') = (closed_at IS NULL)),
    -- Stage, owner and contact must be in the deal's org.
    DROP CONSTRAINT deals_stage_id_fkey,
    ADD CONSTRAINT deals_stage_same_org_fkey FOREIGN KEY (stage_id, org_id) REFERENCES deal_stages (id, org_id),
    DROP CONSTRAINT deals_owner_id_fkey,
    ADD CONSTRAINT deals_owner_same_org_fkey FOREIGN KEY (owner_id, org_id) REFERENCES users (id, org_id),
    DROP CONSTRAINT deals_contact_id_fkey,
    ADD CONSTRAINT deals_contact_same_org_fkey FOREIGN KEY (contact_id, org_id)
        REFERENCES contacts (id, org_id) ON DELETE SET NULL (contact_id),
    ADD CONSTRAINT deals_id_org_id_key UNIQUE (id, org_id);
CREATE INDEX deals_org_id_status_expected_close_date_idx ON deals (org_id, status, expected_close_date);
CREATE INDEX deals_org_id_status_closed_at_idx ON deals (org_id, status, closed_at);

-- Every stage change, for audit and pipeline velocity.
CREATE TABLE deal_stage_history (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL,
    deal_id BIGINT NOT NULL,
    from_stage_id BIGINT,
    to_stage_id BIGINT NOT NULL,
    changed_by_id BIGINT REFERENCES users (id) ON DELETE SET NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (deal_id, org_id) REFERENCES deals (id, org_id) ON DELETE CASCADE,
    FOREIGN KEY (from_stage_id, org_id) REFERENCES deal_stages (id, org_id) ON DELETE CASCADE,
    FOREIGN KEY (to_stage_id, org_id) REFERENCES deal_stages (id, org_id) ON DELETE CASCADE
);
CREATE INDEX deal_stage_history_deal_id_idx ON deal_stage_history (deal_id, changed_at);
CREATE INDEX deal_stage_history_from_stage_id_idx ON deal_stage_history (from_stage_id);
CREATE INDEX deal_stage_history_to_stage_id_idx ON deal_stage_history (to_stage_id);
CREATE INDEX deal_stage_history_changed_by_id_idx ON deal_stage_history (changed_by_id);

-- Deal events on the contact timeline.
ALTER TABLE contact_activities
    DROP CONSTRAINT contact_activities_kind_check,
    ADD CONSTRAINT contact_activities_kind_check CHECK (kind IN (
        'captured', 'duplicate_captured', 'message', 'note', 'call', 'email',
        'score_changed', 'owner_assigned', 'lifecycle_changed',
        'task_created', 'task_completed',
        'deal_created', 'deal_stage_changed', 'deal_won', 'deal_lost'
    ));

-- A lost deal schedules a re-contact task.
ALTER TABLE tasks
    DROP CONSTRAINT tasks_origin_check,
    ADD CONSTRAINT tasks_origin_check CHECK (origin IN ('manual', 'hot_lead', 'deal_lost'));

-- +goose Down
DELETE FROM tasks WHERE origin = 'deal_lost';
ALTER TABLE tasks
    DROP CONSTRAINT tasks_origin_check,
    ADD CONSTRAINT tasks_origin_check CHECK (origin IN ('manual', 'hot_lead'));
DELETE FROM contact_activities WHERE kind IN ('deal_created', 'deal_stage_changed', 'deal_won', 'deal_lost');
ALTER TABLE contact_activities
    DROP CONSTRAINT contact_activities_kind_check,
    ADD CONSTRAINT contact_activities_kind_check CHECK (kind IN (
        'captured', 'duplicate_captured', 'message', 'note', 'call', 'email',
        'score_changed', 'owner_assigned', 'lifecycle_changed',
        'task_created', 'task_completed'
    ));
DROP TABLE deal_stage_history;

DROP INDEX deals_org_id_status_closed_at_idx;
DROP INDEX deals_org_id_status_expected_close_date_idx;
ALTER TABLE deals
    DROP CONSTRAINT deals_id_org_id_key,
    DROP CONSTRAINT deals_contact_same_org_fkey,
    ADD CONSTRAINT deals_contact_id_fkey FOREIGN KEY (contact_id) REFERENCES contacts (id),
    DROP CONSTRAINT deals_owner_same_org_fkey,
    ADD CONSTRAINT deals_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES users (id),
    DROP CONSTRAINT deals_stage_same_org_fkey,
    ADD CONSTRAINT deals_stage_id_fkey FOREIGN KEY (stage_id) REFERENCES deal_stages (id),
    ALTER COLUMN stage_id DROP NOT NULL,
    ALTER COLUMN created_at DROP NOT NULL,
    ALTER COLUMN updated_at DROP NOT NULL,
    ADD COLUMN amount NUMERIC(15,2) DEFAULT 0,
    ADD COLUMN monthly_recurring_revenue NUMERIC(15,2) DEFAULT 0,
    ADD COLUMN annual_recurring_revenue NUMERIC(15,2) DEFAULT 0;
UPDATE deals SET
    amount = amount_paise / 100.0,
    annual_recurring_revenue = CASE billing_period
        WHEN 'yearly' THEN amount_paise / 100.0
        WHEN 'quarterly' THEN amount_paise * 4 / 100.0
        WHEN 'monthly' THEN amount_paise * 12 / 100.0 ELSE 0 END,
    monthly_recurring_revenue = CASE billing_period
        WHEN 'yearly' THEN amount_paise / 1200.0
        WHEN 'quarterly' THEN amount_paise / 300.0
        WHEN 'monthly' THEN amount_paise / 100.0 ELSE 0 END;
ALTER TABLE deals
    DROP CONSTRAINT deals_closed_at_check,
    DROP CONSTRAINT deals_billing_period_required_check,
    DROP COLUMN stage_entered_at,
    DROP COLUMN description,
    DROP COLUMN lost_reason,
    DROP COLUMN closed_at,
    DROP COLUMN expected_close_date,
    DROP COLUMN status,
    DROP COLUMN billing_period,
    DROP COLUMN billing_type,
    DROP COLUMN amount_paise;

DROP INDEX deal_stages_org_id_position_idx;
ALTER TABLE deal_stages
    DROP CONSTRAINT deal_stages_outcome_probability_check,
    DROP CONSTRAINT deal_stages_id_org_id_key,
    DROP COLUMN updated_at,
    DROP COLUMN required_fields,
    DROP COLUMN guidance,
    DROP COLUMN probability,
    DROP COLUMN kind,
    DROP COLUMN position;
DROP FUNCTION crm_deal_value_paise(bigint, text, text);
