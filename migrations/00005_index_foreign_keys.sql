-- Index every foreign-key column. CONCURRENTLY avoids blocking writes on a
-- live database, which requires running outside a transaction.

-- +goose NO TRANSACTION

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS contacts_lead_owner_id_idx ON contacts (lead_owner_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS deals_stage_id_idx ON deals (stage_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS deals_owner_id_idx ON deals (owner_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS deals_contact_id_idx ON deals (contact_id);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS deals_contact_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS deals_owner_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS deals_stage_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS contacts_lead_owner_id_idx;
