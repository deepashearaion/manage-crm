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
  deals      pipeline: guided stages, deals (paise), won/lost, rule-based forecast
  tasks      to-dos for users (manual, and workflow follow-ups)
  notifications  in-app inbox and email alerts
  nurture    automated email/WhatsApp sequences (incl. onboarding), opt-out
  invoices   GST tax invoices: drafts, gapless numbering, PDF, email
internal/platform/scheduler  background jobs run by cmd/api
internal/platform/whatsapp   WhatsApp Cloud API client
  reports    dashboards and reports, weekly report email (read-only across modules)
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
  access token (never from the request). What the caller may do comes from
  their role, loaded fresh on every request (see "Users and roles").
- Self sign-up (`POST /api/auth/register`) creates a new org with the user as
  owner. Other users are created by an admin, who sets their first password
  (`POST /api/v1/users`).

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

## Deal pipeline (phase 7c)

Money is integer **paise** (INR). A deal's `amount_paise` is its value per
billing period: the whole value for `one_time` deals, or the monthly,
quarterly or yearly fee for `recurring` ones. `value_paise` (SQL
`crm_deal_value_paise`) is the one-time amount or the first-year value, and
is used by the pipeline, the forecast and the dashboard. ARR counts only
won recurring deals; MRR is ARR / 12.

**Guided stages.** Each stage has a `kind` (`open`, `won`, `lost`), a win
`probability`, `guidance` (what to do in it) and `required_fields`
(`amount`, `expected_close_date`, `contact`) that must be filled before a
deal can enter it; otherwise the move fails with 422 `stage_requirements`,
listing what's missing. Deals change stage only via
`POST /api/v1/deals/{id}/stage`, which records the history. Losing needs a
`lost_reason`.

**Outcomes.** A deal in an open stage makes its contact an `opportunity`.
Winning makes the contact a `customer`. Losing makes them `lost`, unless
they have another open or won deal, and gives the owner a "Re-contact"
task due 30 days later. Reopening a closed deal clears its outcome. All of
these go on the contact timeline.

**Win probability** (rule-based) is the stage probability, ×0.5 when the
deal has not changed stage for 30 days, ×0.5 when its expected close date
has passed, and ×0.8 when it has no close date. Each deal returns
`win_probability` and `probability_notes`.

**Forecast** (`GET /api/v1/deals/forecast?from=YYYY-MM&months=3&owner_id=`)
covers IST months. For each month it gives the deals won, and for the open
deals expected to close that month (overdue ones count in the current
month): `best_case_paise` (all close), `weighted_paise` (× stage
probability), `forecast_paise` (× adjusted win probability) and
`commit_paise` (adjusted probability ≥ 70%). It also returns `overdue` and
`undated` totals.

| Endpoint | Who | Purpose |
|---|---|---|
| `GET /api/v1/deal-stages` | all | Stages in order, with open deal count and value (reps: their own) |
| `POST /api/v1/deal-stages`, `PATCH/DELETE …/{id}` | manager+ | Manage stages (kind fixed; deletion blocked while in use or if it is the last of its kind) |
| `GET /api/v1/deals?status=&stage_id=&owner_id=&contact_id=&limit=&offset=` | all (reps: own) | Deals by stage order, then close date |
| `POST /api/v1/deals` | writers | Create in an open stage (first one by default) |
| `GET/PATCH/DELETE /api/v1/deals/{id}` | owner or manager+ | Read, edit fields, delete |
| `POST /api/v1/deals/{id}/stage` | owner or manager+ | Move (`stage_id`, `lost_reason`) |
| `GET /api/v1/deals/{id}/history` | all | Stage changes |
| `GET /api/v1/deals/forecast` | all (reps: own) | Forecast |

## Nurture and background jobs (phase 7d)

`cmd/api` runs background jobs next to the HTTP server: **nurture** every
minute and **overdue task reminders** every 5 minutes. Jobs claim their
rows with `FOR UPDATE SKIP LOCKED`, so several API instances can run at
once.

**Sequences** are timed steps over email or WhatsApp. Each step waits
`delay_minutes` after the previous one, and bodies may use
`{{first_name}}`, `{{last_name}}` and `{{full_name}}`. The `trigger` says
what enrolls contacts automatically (an org has one active sequence per
trigger):

- `new_lead`: a new lead that is not hot (the diagram's "Auto nurture").
- `deal_lost`: a lost deal (the diagram's "Back to nurture"); give the first
  step a 43200-minute (30-day) delay. Without an active `deal_lost`
  sequence, the owner gets a re-contact task due in 30 days instead.
- `manual`: only `POST /api/v1/nurture-sequences/{id}/enrollments`.

Enrollments stop when the contact **replies** (for sequences with
`stop_on_reply`; the reply also re-scores the lead), **turns hot** (a rep
takes over), **wins a deal**, becomes a customer, or **opts out**. Nothing
is sent during **quiet hours** (21:00–09:00 IST): due steps wait until
09:00. A failed send is retried after 5, 20, 45 and 80 minutes; after 5
failures, or a permanent WhatsApp error, the step is skipped and noted in
`last_error`. Sent messages appear on the contact timeline as outbound
messages.

**WhatsApp** messages go out from the org's first registered number
(`/api/v1/whatsapp-accounts`) using `WHATSAPP_TOKEN`. Inside WhatsApp's
24-hour window (the contact messaged within the last 24 hours) a step sends
its `body` as free text. Outside it, the step sends its approved
`whatsapp_template` with `whatsapp_template_params`, or is skipped if it
has none. Without `WHATSAPP_TOKEN`, messages are logged instead of sent.

**Opt-out.** Every nurture email ends with an unsubscribe link to
`FRONTEND_URL/unsubscribe?token=…`. The frontend page posts the token to
`POST /api/v1/unsubscribe`. A WhatsApp message saying just "STOP",
"UNSUBSCRIBE" or "OPT OUT" also opts the contact out. Opted-out contacts
(`marketing_opt_out_at` on the contact) get no nurture messages.

**Overdue tasks** send their assignee a `task_overdue` notification once per
due date; changing the due date or reopening the task re-arms it.

| Endpoint | Who | Purpose |
|---|---|---|
| `GET /api/v1/nurture-sequences`, `GET …/{id}` | all | Sequences with steps and active enrollment count |
| `POST /api/v1/nurture-sequences`, `PATCH/DELETE …/{id}` | manager+ | Manage (`steps` replaces all steps) |
| `POST /api/v1/nurture-sequences/{id}/enrollments` | writers | Enroll a contact (`contact_id`) |
| `GET /api/v1/nurture-enrollments?contact_id=&sequence_id=&status=` | all (reps: per contact) | Enrollments |
| `POST /api/v1/nurture-enrollments/{id}/stop` | writers | Stop one |
| `POST /api/v1/unsubscribe` | public | Opt out with an email token |

Example sequence:

```json
{"name": "Welcome", "trigger": "new_lead", "active": true, "steps": [
  {"channel": "email", "delay_minutes": 0, "subject": "Hello {{first_name}}", "body": "Thanks for your interest…"},
  {"channel": "whatsapp", "delay_minutes": 1440, "body": "Hi {{first_name}}, any questions?",
   "whatsapp_template": "lead_followup", "whatsapp_language": "en", "whatsapp_template_params": ["{{first_name}}"]},
  {"channel": "email", "delay_minutes": 4320, "subject": "Still planning?", "body": "…"}
]}
```

## Onboarding and GST invoices (phase 7e)

**Winning a deal** (the diagram's "Auto onboarding"):

1. The contact leaves nurture and enters the org's active `deal_won`
   sequence, if there is one (e.g. a welcome email, then a WhatsApp
   template). Onboarding messages are transactional: winning, turning hot
   and opting out of marketing don't stop them, and their emails have no
   unsubscribe footer. WhatsApp steps still skip opted-out contacts.
2. A **draft invoice** is created from the deal: one line with the deal
   title and amount, the profile's default SAC and GST rate, and the
   contact as the customer. The deal's owner gets a high-priority "Review
   and issue invoice" task due the next day. A deal has at most one live
   (not cancelled) invoice.

**Billing profile** (`GET`/`PUT /api/v1/billing-profile`, managers and
above) holds the seller's legal name, GSTIN (checksum-validated), state
code, address, bank details and terms, plus the invoice prefix, default
SAC, default GST rate, whether prices include GST, and the payment terms.

**GST.** Rates are basis points (`1800` = 18%) and money is paise. Each line
has its own SAC and rate. If the place of supply (a state code; it defaults
from the customer's GSTIN) equals the seller's state, tax is split into CGST
and SGST (SGST takes an odd paisa); otherwise it is IGST. With inclusive
prices the taxable value is backed out of the price. The total is rounded
to the rupee, with a round-off line.

**Issuing** (`POST /api/v1/invoices/{id}/issue`) needs a complete seller
profile, a customer name, a place of supply and a SAC on every line;
otherwise it returns 422 `invoice_incomplete` listing what's missing. It
assigns the next **gapless number for the Indian financial year (April to
March)**, e.g. `INV/26-27/0001` (16 characters at most). It also copies the
seller details into the invoice, sets the due date from the payment terms,
and renders and stores the **PDF**. Issued invoices never change: cancel
one (managers and above, with a reason; its number is not reused) and
issue a new one. The PDF (A4) includes the seller and customer GSTINs,
place of supply, reverse-charge statement, SAC/HSN, per-line tax,
CGST/SGST/IGST totals, the amount in words (Indian numbering) and a
signatory block. Fonts are Noto Sans (SIL OFL, `internal/invoices/fonts`).
Scripts it lacks, such as Devanagari, print as blanks. E-invoicing
(IRN/QR) is not implemented; its columns are reserved.

| Endpoint | Who | Purpose |
|---|---|---|
| `GET /api/v1/invoices?status=&contact_id=&deal_id=&limit=&offset=` | all (reps: own) | List (without lines) |
| `POST /api/v1/invoices` | writers | Create a draft (`deal_id` fills the customer and line) |
| `GET/PATCH/DELETE /api/v1/invoices/{id}` | owner or manager+ | Read; edit or delete drafts (`lines` replaces all lines) |
| `POST /api/v1/invoices/{id}/issue` | owner or manager+ | Issue |
| `GET /api/v1/invoices/{id}/pdf` | all | The stored PDF, or a DRAFT preview |
| `POST /api/v1/invoices/{id}/send` | owner or manager+ | Email the PDF to `bill_to_email` |
| `POST /api/v1/invoices/{id}/cancel` | manager+ | Cancel an issued invoice (`reason`) |

## Reports (phase 7f)

Reports read across the other modules' tables, read-only. Reps see only
their own deals, contacts and invoices. Periods are IST calendar days
(`from`/`to` as `YYYY-MM-DD`, both inclusive, at most 731 days apart; by
default the current month up to today).

**Definitions**

- **Deal value** is `crm_deal_value_paise`: the one-time amount, or a
  recurring deal's first-year value.
- **Won and lost** deals count in the period their `closed_at` falls in.
  The **win rate** is won / (won + lost), by count.
- A **new customer** is a contact whose **first-ever won deal** closed in
  the period.
- **ARR** is the value of all won recurring deals; **MRR** is ARR / 12. No
  churn is tracked yet, so won subscriptions count until the deal is
  reopened.
- The **funnel** follows the contacts created in the period. A contact has
  reached every stage up to the furthest it has ever been in (its current
  stage or any stage on its timeline), so a lost opportunity still counts as
  having reached `opportunity`.

The legacy `GET /api/dashboard/sales` keeps its shape (rupees). Its "new
customers" now uses the first-won-deal rule, for today (IST).

| Endpoint | Who | Purpose |
|---|---|---|
| `GET /api/v1/reports/dashboard?from=&to=` | all | Open, won and lost deals, win rate, average deal size and sales cycle, new customers, new and hot leads, invoiced, ARR and MRR |
| `GET /api/v1/reports/funnel?from=&to=` | all | Lifecycle funnel with step and overall conversion |
| `GET /api/v1/reports/pipeline` | all | Open deals by stage: count, value, weighted value, average days in stage |
| `GET /api/v1/reports/sources?from=&to=` | all | Leads, hot leads, customers, conversion and won value by lead source |
| `GET /api/v1/reports/reps?from=&to=` | non-reps | Per user: leads assigned, open/won/lost, win rate, overdue tasks |
| `GET /api/v1/reports/revenue?months=12` | all | Monthly (IST): won deals and value, new ARR, new customers, invoiced |
| `GET/PUT /api/v1/reports/settings` | all / manager+ | `weekly_email` (on by default) |

**Weekly report email** (the diagram's "auto reports"). From Monday 09:00
IST, a job emails the previous Monday-to-Sunday summary, once per week, to
users whose role sees all reports and edits settings (by default Owner,
Admin and Manager) (recorded in `report_runs`).
Orgs with no activity, or with `weekly_email` off, get none.

## Users and roles

Each org defines its own roles. A role grants, for each module, the actions
`view`, `create`, `edit` and `delete`, each with a scope:

- `none`: not allowed.
- `own`: only records the user owns (contacts, deals, invoices) or is
  assigned (tasks).
- `all`: every record in the org.

The modules are `contacts` (also leads and timelines), `deals`, `tasks`,
`nurture`, `invoices`, `reports`, `settings`, `users` and `roles`.
`GET /api/v1/permissions` lists them with the actions and scopes each one
accepts.

A few rules decide what some combinations mean:

- Changing a record's owner needs `edit: all` on its module.
- Creating a record for someone else needs `create: all`.
- `edit` and `delete` can't reach further than `view`.
- Nurture sequences are shared by the whole org, so changing them needs
  scope `all`. Enrolling contacts and stopping enrollments follow the
  contact's owner.
- Cancelling an issued invoice needs `invoices.delete: all`.

Every org starts with five roles, seeded with the same access the old fixed
roles had:

| Role | Access |
|---|---|
| Owner | Full access. Locked: it can't be edited or deleted. |
| Admin | Full access. |
| Manager | Everything except creating users and managing roles. |
| Rep | Their own records; their own figures in reports. |
| Viewer | Read-only access to every record. |

Each user has exactly one role. The role isn't stored in the access token:
the API loads it on every request, so changes take effect on the user's next
request.

Endpoints:

| Method and path | What it does |
|---|---|
| `GET /api/v1/me` | The caller, their role and their full permission map. |
| `GET /api/v1/permissions` | The permission catalog. |
| `GET`, `POST /api/v1/users` | List users, or create one with `{name, email, password, role_id}`. |
| `GET`, `PATCH /api/v1/users/{id}` | Read a user, or rename them with `{name}`. |
| `PUT /api/v1/users/{id}/role` | Assign a role with `{role_id}`. |
| `PUT /api/v1/users/{id}/password` | An admin resets the password; the user is signed out everywhere. |
| `GET`, `POST /api/v1/roles` | List roles, or create one with `{name, description, permissions}`. |
| `GET`, `PATCH`, `DELETE /api/v1/roles/{id}` | Read, change or delete a role. `permissions` replaces the whole map, and anything left out becomes `none`. |

Guard rails:

| Rule | Error |
|---|---|
| Nobody can create, change, delete or assign a role with more access than they have themselves. | `403 exceeds_own_access` |
| Only owners can assign or remove the Owner role, or change an owner's account. | `403 forbidden` |
| Users can't change their own role. Owners can step down while another owner remains. | `409 own_role` |
| The last owner can't move to another role. | `409 last_owner` |
| A role that users still have can't be deleted. | `409 role_in_use` |
