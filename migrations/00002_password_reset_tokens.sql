-- Password reset tokens move to their own table and are stored only as a
-- SHA-256 hash. Each token is single-use and expires; all of a user's
-- outstanding tokens are invalidated when the password changes.

-- +goose Up
CREATE TABLE password_reset_tokens (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX password_reset_tokens_user_id_idx ON password_reset_tokens (user_id);

-- The old plaintext tokens are dropped, not migrated: any in flight become invalid.
ALTER TABLE users
    DROP COLUMN reset_token,
    DROP COLUMN reset_token_expires_at;

-- +goose Down
ALTER TABLE users
    ADD COLUMN reset_token TEXT,
    ADD COLUMN reset_token_expires_at TIMESTAMP;

DROP TABLE password_reset_tokens;
