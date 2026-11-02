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
internal/app   wires config, database and routes into the HTTP handler
internal/platform/{config,database,httpx,logging,mail}  shared infrastructure
internal/apitest  black-box tests for the /api routes
handlers, routes, middleware, models, utils  legacy code, moving into internal/<module>
migrations     SQL migrations
```

## Configuration

All settings come from environment variables (or `.env`); see `.env.example`.
The server validates them at startup and refuses to start if any are missing
or invalid. `APP_ENV` defaults to `production`.
