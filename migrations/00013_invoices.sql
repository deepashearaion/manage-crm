-- Onboarding and GST invoices (workflow phase 7e). Money is integer paise
-- (INR) and GST rates are basis points (1800 = 18%). Issued invoices are
-- immutable snapshots: seller and customer details are copied in and the PDF
-- is stored. Numbering is gapless per org and Indian financial year
-- (April to March). E-invoicing (IRN) is not done yet; its columns are
-- reserved.

-- +goose Up

-- The org's details as a seller, and invoice defaults.
CREATE TABLE billing_profiles (
    org_id BIGINT PRIMARY KEY REFERENCES orgs (id) ON DELETE CASCADE,
    legal_name VARCHAR(200) NOT NULL DEFAULT '',
    gstin VARCHAR(15) NOT NULL DEFAULT '',
    state_code VARCHAR(2) NOT NULL DEFAULT '',
    address TEXT NOT NULL DEFAULT '',
    email VARCHAR(255) NOT NULL DEFAULT '',
    phone VARCHAR(30) NOT NULL DEFAULT '',
    invoice_prefix VARCHAR(5) NOT NULL DEFAULT 'INV',
    default_sac VARCHAR(8) NOT NULL DEFAULT '',
    default_gst_rate_bp INT NOT NULL DEFAULT 1800 CHECK (default_gst_rate_bp BETWEEN 0 AND 10000),
    prices_include_gst BOOLEAN NOT NULL DEFAULT false,
    payment_terms_days INT NOT NULL DEFAULT 7 CHECK (payment_terms_days BETWEEN 0 AND 365),
    bank_details TEXT NOT NULL DEFAULT '',
    terms TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE invoices (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'issued', 'cancelled')),
    -- Set when issued: e.g. INV/26-27/0001 (at most 16 characters, as GST requires).
    number VARCHAR(16),
    financial_year VARCHAR(7),
    sequence INT,
    deal_id BIGINT,
    contact_id BIGINT,
    owner_id BIGINT,
    -- Customer (bill to) and place of supply (state code).
    bill_to_name VARCHAR(200) NOT NULL DEFAULT '',
    bill_to_address TEXT NOT NULL DEFAULT '',
    bill_to_gstin VARCHAR(15) NOT NULL DEFAULT '',
    bill_to_email VARCHAR(255) NOT NULL DEFAULT '',
    bill_to_phone VARCHAR(30) NOT NULL DEFAULT '',
    place_of_supply VARCHAR(2) NOT NULL DEFAULT '',
    -- Seller snapshot, copied from the billing profile when issued.
    seller_name VARCHAR(200) NOT NULL DEFAULT '',
    seller_gstin VARCHAR(15) NOT NULL DEFAULT '',
    seller_state_code VARCHAR(2) NOT NULL DEFAULT '',
    seller_address TEXT NOT NULL DEFAULT '',
    seller_bank_details TEXT NOT NULL DEFAULT '',
    seller_terms TEXT NOT NULL DEFAULT '',
    prices_include_gst BOOLEAN NOT NULL DEFAULT false,
    issue_date DATE,
    due_date DATE,
    notes TEXT NOT NULL DEFAULT '',
    taxable_paise BIGINT NOT NULL DEFAULT 0,
    cgst_paise BIGINT NOT NULL DEFAULT 0,
    sgst_paise BIGINT NOT NULL DEFAULT 0,
    igst_paise BIGINT NOT NULL DEFAULT 0,
    round_off_paise BIGINT NOT NULL DEFAULT 0,
    total_paise BIGINT NOT NULL DEFAULT 0,
    pdf BYTEA,
    -- Reserved for e-invoicing (IRN from the Invoice Registration Portal).
    irn VARCHAR(64),
    irn_ack_no VARCHAR(20),
    irn_ack_date TIMESTAMPTZ,
    irn_signed_qr TEXT,
    created_by_id BIGINT REFERENCES users (id) ON DELETE SET NULL,
    issued_by_id BIGINT REFERENCES users (id) ON DELETE SET NULL,
    issued_at TIMESTAMPTZ,
    sent_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    cancel_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT invoices_issued_check CHECK (
        (status = 'draft') = (number IS NULL)
        AND (number IS NULL) = (issued_at IS NULL)
        AND (number IS NULL) = (pdf IS NULL)
        AND (status = 'cancelled') = (cancelled_at IS NOT NULL)),
    CONSTRAINT invoices_org_id_number_key UNIQUE (org_id, number),
    CONSTRAINT invoices_org_id_financial_year_sequence_key UNIQUE (org_id, financial_year, sequence),
    CONSTRAINT invoices_id_org_id_key UNIQUE (id, org_id),
    CONSTRAINT invoices_deal_same_org_fkey
        FOREIGN KEY (deal_id, org_id) REFERENCES deals (id, org_id) ON DELETE SET NULL (deal_id),
    CONSTRAINT invoices_contact_same_org_fkey
        FOREIGN KEY (contact_id, org_id) REFERENCES contacts (id, org_id) ON DELETE SET NULL (contact_id),
    CONSTRAINT invoices_owner_same_org_fkey
        FOREIGN KEY (owner_id, org_id) REFERENCES users (id, org_id) ON DELETE SET NULL (owner_id)
);
CREATE INDEX invoices_org_id_status_idx ON invoices (org_id, status, id DESC);
CREATE INDEX invoices_deal_id_idx ON invoices (deal_id);
CREATE INDEX invoices_contact_id_idx ON invoices (contact_id);
CREATE INDEX invoices_owner_id_idx ON invoices (owner_id);
CREATE INDEX invoices_created_by_id_idx ON invoices (created_by_id);
CREATE INDEX invoices_issued_by_id_idx ON invoices (issued_by_id);
-- One live invoice per deal (a won deal creates its draft once).
CREATE UNIQUE INDEX invoices_deal_live_key ON invoices (deal_id) WHERE status <> 'cancelled';

CREATE TABLE invoice_lines (
    id BIGSERIAL PRIMARY KEY,
    org_id BIGINT NOT NULL,
    invoice_id BIGINT NOT NULL,
    position INT NOT NULL,
    description TEXT NOT NULL,
    sac VARCHAR(8) NOT NULL DEFAULT '',
    quantity INT NOT NULL CHECK (quantity > 0),
    unit_price_paise BIGINT NOT NULL CHECK (unit_price_paise >= 0),
    gst_rate_bp INT NOT NULL CHECK (gst_rate_bp BETWEEN 0 AND 10000),
    taxable_paise BIGINT NOT NULL,
    cgst_paise BIGINT NOT NULL,
    sgst_paise BIGINT NOT NULL,
    igst_paise BIGINT NOT NULL,
    total_paise BIGINT NOT NULL,
    FOREIGN KEY (invoice_id, org_id) REFERENCES invoices (id, org_id) ON DELETE CASCADE,
    CONSTRAINT invoice_lines_invoice_id_position_key UNIQUE (invoice_id, position)
);

-- The last invoice sequence used per org and financial year; issuing locks
-- the row, so numbers are gapless and never reused.
CREATE TABLE invoice_counters (
    org_id BIGINT NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    financial_year VARCHAR(7) NOT NULL,
    last_sequence INT NOT NULL,
    PRIMARY KEY (org_id, financial_year)
);

ALTER TABLE contact_activities
    DROP CONSTRAINT contact_activities_kind_check,
    ADD CONSTRAINT contact_activities_kind_check CHECK (kind IN (
        'captured', 'duplicate_captured', 'message', 'note', 'call', 'email',
        'score_changed', 'owner_assigned', 'lifecycle_changed',
        'task_created', 'task_completed',
        'deal_created', 'deal_stage_changed', 'deal_won', 'deal_lost',
        'nurture_enrolled', 'nurture_stopped', 'opted_out',
        'invoice_issued', 'invoice_sent', 'invoice_cancelled'
    ));

ALTER TABLE tasks
    DROP CONSTRAINT tasks_origin_check,
    ADD CONSTRAINT tasks_origin_check CHECK (origin IN ('manual', 'hot_lead', 'deal_lost', 'invoice_review'));

-- Onboarding: a 'deal_won' sequence welcomes new customers.
ALTER TABLE nurture_sequences
    DROP CONSTRAINT nurture_sequences_trigger_check,
    ADD CONSTRAINT nurture_sequences_trigger_check CHECK (trigger IN ('manual', 'new_lead', 'deal_lost', 'deal_won'));

-- +goose Down
DELETE FROM nurture_sequences WHERE trigger = 'deal_won';
ALTER TABLE nurture_sequences
    DROP CONSTRAINT nurture_sequences_trigger_check,
    ADD CONSTRAINT nurture_sequences_trigger_check CHECK (trigger IN ('manual', 'new_lead', 'deal_lost'));
DELETE FROM tasks WHERE origin = 'invoice_review';
ALTER TABLE tasks
    DROP CONSTRAINT tasks_origin_check,
    ADD CONSTRAINT tasks_origin_check CHECK (origin IN ('manual', 'hot_lead', 'deal_lost'));
DELETE FROM contact_activities WHERE kind IN ('invoice_issued', 'invoice_sent', 'invoice_cancelled');
ALTER TABLE contact_activities
    DROP CONSTRAINT contact_activities_kind_check,
    ADD CONSTRAINT contact_activities_kind_check CHECK (kind IN (
        'captured', 'duplicate_captured', 'message', 'note', 'call', 'email',
        'score_changed', 'owner_assigned', 'lifecycle_changed',
        'task_created', 'task_completed',
        'deal_created', 'deal_stage_changed', 'deal_won', 'deal_lost',
        'nurture_enrolled', 'nurture_stopped', 'opted_out'
    ));
DROP TABLE invoice_counters;
DROP TABLE invoice_lines;
DROP TABLE invoices;
DROP TABLE billing_profiles;
