-- Rotating refresh tokens, stored only as SHA-256 hashes. Every token
-- belongs to a family (one login session); using a token rotates it into a
-- new one in the same family. Presenting an already-rotated token is treated
-- as theft and revokes the whole family.

-- +goose Up
CREATE TABLE refresh_tokens (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id UUID NOT NULL,
    token_hash BYTEA NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    rotated_at TIMESTAMPTZ, -- exchanged for a successor
    revoked_at TIMESTAMPTZ  -- logout, reuse detection or password change
);

CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);
CREATE INDEX refresh_tokens_family_id_idx ON refresh_tokens (family_id);

-- +goose Down
DROP TABLE refresh_tokens;
