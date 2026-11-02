-- Store every timestamp as timestamptz (an absolute instant). The old
-- `timestamp` columns held wall-clock values written by sessions using the
-- server's default TimeZone (Asia/Calcutta on the dev server). Converting
-- without USING interprets each value in the migration session's TimeZone,
-- which is that same server default, so the instants come out right. If a
-- server's default TimeZone was changed after data was written, run this
-- with `SET TimeZone` to the zone the data was written in.

-- +goose Up
ALTER TABLE users
    ALTER COLUMN created_at TYPE TIMESTAMPTZ,
    ALTER COLUMN updated_at TYPE TIMESTAMPTZ;

ALTER TABLE contacts
    ALTER COLUMN created_at TYPE TIMESTAMPTZ,
    ALTER COLUMN updated_at TYPE TIMESTAMPTZ;

ALTER TABLE deal_stages
    ALTER COLUMN created_at TYPE TIMESTAMPTZ;

ALTER TABLE deals
    ALTER COLUMN created_at TYPE TIMESTAMPTZ,
    ALTER COLUMN updated_at TYPE TIMESTAMPTZ;

-- +goose Down
ALTER TABLE users
    ALTER COLUMN created_at TYPE TIMESTAMP,
    ALTER COLUMN updated_at TYPE TIMESTAMP;

ALTER TABLE contacts
    ALTER COLUMN created_at TYPE TIMESTAMP,
    ALTER COLUMN updated_at TYPE TIMESTAMP;

ALTER TABLE deal_stages
    ALTER COLUMN created_at TYPE TIMESTAMP;

ALTER TABLE deals
    ALTER COLUMN created_at TYPE TIMESTAMP,
    ALTER COLUMN updated_at TYPE TIMESTAMP;
