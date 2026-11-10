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
  contacts   contacts, lifecycle stage, lead scores, activity timeline
  leads      lead intake (forms, WhatsApp, manual), dedupe, scoring, routing, lead summaries
  tasks      to-dos for users (manual, and workflow follow-ups)
  notifications  in-app inbox and email alerts
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

## Lead workflow (phase 7a)

A lead is a contact whose `lifecycle_stage` is `lead` (then `mql`, `sql`,
`opportunity`, `customer`, `lost`). Every lead, whatever the channel, goes
through the same steps:

1. **Capture**: a web form, a WhatsApp message, or a user. Leads are matched
   to an existing contact in the org by email (case-insensitive) or phone
   (normalized by `crm_phone_key`: `98765 43210`, `+91-98765-43210` and
   WhatsApp's `919876543210` are the same number). A match only fills in
   missing details and never overwrites. Everything goes on the contact's
   timeline.
2. **Score**: the sum of the org's active scoring rules, clamped to 0–100.
   The temperature is `hot` at or above `hot_threshold` (default 70), `warm`
   at or above `warm_threshold` (default 40), and `cold` otherwise. Every
   inbound WhatsApp reply re-scores the contact.
3. **Route**: a new, unowned lead goes to the first matching active
   assignment rule (round robin among its members), or else to the default
   owner. Reps own the leads they enter themselves.

| Endpoint | Who | Purpose |
|---|---|---|
| `POST /api/v1/intake/forms/{key}` | public | Web form or integration submission (JSON or url-encoded; unknown fields ignored; `website` is a honeypot). Answers 202. |
| `GET/POST /api/v1/webhooks/whatsapp` | Meta | Subscription handshake; signed (`X-Hub-Signature-256`) inbound messages |
| `POST /api/v1/leads` | writers | Enter a lead by hand (`channel`: manual, email, phone, api) |
| `POST /api/v1/leads/{id}/rescore` | writers | Recompute a contact's score |
| `GET /api/v1/contacts?temperature=&lifecycle_stage=&limit=&before_id=` | all | Contacts with score, newest first |
| `GET /api/v1/contacts/{id}` | all | One contact with score |
| `GET /api/v1/contacts/{id}/timeline?limit=&before_id=` | all | Activity timeline, newest first |
| `POST /api/v1/contacts/{id}/activities` | writers | Log a note, call or email |
| `PUT /api/v1/contacts/{id}/lifecycle-stage` | writers | Change lifecycle stage |
| `GET/PATCH /api/v1/lead-settings` | manager+ | Thresholds, default owner |
| `GET/POST /api/v1/lead-sources`, `PATCH …/{id}`, `POST …/{id}/rotate-key` | manager+ | Web form sources and their public keys |
| `GET/POST /api/v1/whatsapp-accounts`, `DELETE …/{id}` | manager+ | Which WhatsApp number belongs to the org |
| `GET/POST /api/v1/scoring-rules`, `PATCH/DELETE …/{id}` | manager+ | Scoring rules |
| `GET/POST /api/v1/assignment-rules`, `PATCH/DELETE …/{id}` | manager+ | Routing rules |

Scoring criteria: `has_email`, `has_mobile`, `has_company`, `source_equals`,
`destination_equals`, `channel_used`, `text_contains`,
`inbound_messages_at_least`. Assignment criteria: `any`, `source_equals`,
`channel_equals`, `destination_equals`, `temperature_equals`. New orgs get
eight default scoring rules.

## Rep alerts and tasks (phase 7b)

When a lead's temperature **becomes** hot (at capture, on a WhatsApp reply
or on a rescore):

- With an owner, the owner gets an urgent `follow_up` task, due after the
  org's `hot_followup_minutes` (default 15, set in `/api/v1/lead-settings`).
  Its description is the lead summary: who they are, where they came from,
  why they scored hot, their last message and the suggested next step
  (rule-based; times in IST). They also get an in-app `hot_lead`
  notification and an email linking to `FRONTEND_URL/contacts/{id}`. A
  contact has at most one open hot-lead task, so a lead that cools and heats
  up again is re-alerted without duplicating the task.
- Without an owner, the org's owners, admins and managers are alerted
  (in-app and email) so someone assigns it.

A new lead that routing assigns (and that is not hot) sends its owner a
`lead_assigned` notification. A task assigned to someone else sends a
`task_assigned` notification. Task creation and completion appear on the
contact timeline.

| Endpoint | Who | Purpose |
|---|---|---|
| `GET /api/v1/tasks?status=&assignee_id=&contact_id=&overdue=true&limit=&offset=` | all (reps: own) | Open tasks first, soonest due first |
| `POST /api/v1/tasks` | writers | Create (`title`, `type`, `priority`, `due_at`, `contact_id`, `assignee_id`: managers+ for others) |
| `GET/PATCH/DELETE /api/v1/tasks/{id}` | assignee or manager+ | Read, edit (`status`: open, done, cancelled), delete |
| `GET /api/v1/notifications?unread=true&limit=&before_id=` | self | Inbox plus `unread_count` |
| `POST /api/v1/notifications/{id}/read`, `POST /api/v1/notifications/read-all` | self | Mark read |
