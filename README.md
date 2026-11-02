# manage-crm

Go REST API for the CRM, backed by PostgreSQL. The React frontend lives in a
separate repo (view-crm).

## Setup

Requirements: Go (see `go.mod`), PostgreSQL 16, GNU make (optional; every
target is a plain `go` command you can run directly), Docker (optional, for
Mailpit).

```sh
cp .env.example .env          # fill in DB_PASSWORD, JWT_SECRET, TEST_DATABASE_URL
make migrate                  # go run ./cmd/migrate up
make mailpit                  # optional: catch emails at http://localhost:8025
make run                      # go run ./cmd/api  -> http://localhost:8080
```

## Development

| Target | Command | What it does |
|---|---|---|
| `make run` | `go run ./cmd/api` | Start the API |
| `make build` | `go build -o bin/api ./cmd/api` | Compile |
| `make test-db` | `go run ./cmd/testdb` | Recreate the test database (name must end in `_test`) |
| `make test` | `go test -count=1 ./...` | Run all tests against `TEST_DATABASE_URL` |
| `make lint` | golangci-lint v2.15.0 | Lint (config in `.golangci.yml`) |
| `make migrate` / `migrate-status` / `migrate-down` | `go run ./cmd/migrate …` | Manage schema migrations |

Migrations are SQL files in `migrations/`, embedded into the binaries and
applied with [goose](https://github.com/pressly/goose). Add one with the next
number, e.g. `migrations/00004_add_companies.sql`, containing
`-- +goose Up` and `-- +goose Down` sections.

## Layout

```
cmd/api        HTTP server entry point
cmd/migrate    migration runner
cmd/testdb     test database setup
internal/app   wires config, database and modules into the HTTP handler
internal/<module>   one package per business module:
    routes.go      endpoint registration
    handler.go     HTTP decoding/encoding only
    service.go     business rules
    repository.go  SQL (the only place that touches the module's tables)
    model.go       types and errors
  auth       users, login, sessions (refresh tokens), password reset
  contacts   contacts
  reports    dashboards (read-only queries across modules' tables)
internal/platform/{config,database,httpx,logging,mail,principal,validate}  shared infrastructure
internal/apitest  black-box tests for the /api routes
migrations     SQL migrations
```

Modules never import each other (a test enforces this); they may import
`internal/platform/*`. The authenticated caller reaches modules through
`platform/principal`, set by `auth.RequireUser`.

## Configuration

All settings come from environment variables (or `.env`); see `.env.example`.
The server validates them at startup and refuses to start if any are missing
or invalid. `APP_ENV` defaults to `production`.

## API conventions

- Legacy routes (`/api/auth/*`, `/api/contacts`, `/api/dashboard/sales`,
  `/api/profile`) keep plain-text error bodies until the frontend moves off
  them.
- New routes live under `/api/v1` and return errors as RFC 7807
  `application/problem+json` with a stable `code` (e.g. `forbidden`,
  `validation_failed`, `email_taken`) and, for validation, per-field `errors`.
  Request bodies are decoded strictly (unknown fields are rejected).
- Every request is scoped to the caller's organization, taken from the
  access token (never from the request). Roles: owner and admin can do
  everything; manager everything except user management; rep only their own
  records; viewer read-only.
- Users join an org by invitation (`POST /api/v1/users`, owners/admins):
  they get an email link to set their password. Self sign-up
  (`POST /api/auth/register`) creates a new org with the user as owner.
