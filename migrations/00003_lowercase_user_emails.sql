-- Emails are compared case-insensitively: store them trimmed and lowercased
-- and enforce it, so "A@x.com" and "a@x.com" cannot become two accounts.
-- If existing rows differ only by case this fails on the unique constraint
-- and must be resolved by hand first.

-- +goose Up
UPDATE users SET email = lower(btrim(email)) WHERE email <> lower(btrim(email));

ALTER TABLE users ADD CONSTRAINT users_email_normalized CHECK (email = lower(btrim(email)));

-- +goose Down
ALTER TABLE users DROP CONSTRAINT users_email_normalized;
