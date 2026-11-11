# CRM API reference for the React frontend

Every endpoint the Go API serves, with the request and response shapes, written for the `view-crm` app (React 19, TanStack Query, `src/services/http.ts`).

- Base URL: `VITE_API_URL`, `http://localhost:8080` in development.
- New screens should use `/api/v1`. The legacy `/api/auth`, `/api/contacts`, `/api/profile` and `/api/dashboard/sales` routes still work for the screens already built on them.

---

## 1. Conventions

| Topic | Rule |
|---|---|
| Auth | Send `Authorization: Bearer <token>` on everything except the public endpoints (auth, web forms, the WhatsApp webhook, unsubscribe). |
| Request body | JSON. `/api/v1` rejects **unknown fields** with `400 invalid_body`, so send only the fields listed here. |
| Money | Always **integer paise** (`amount_paise: 150000` is ₹1,500.00). Format with `Intl.NumberFormat('en-IN', { style: 'currency', currency: 'INR' }).format(paise / 100)`. The legacy dashboard is the only exception: it returns rupees. |
| GST rates | Basis points (`1800` is 18%). |
| Timestamps | RFC 3339 UTC (`2026-11-10T04:30:00Z`). Display them in IST. |
| Dates | `YYYY-MM-DD` (IST calendar days); months are `YYYY-MM`. |
| IDs | Numbers (int64). |
| Envelope | Responses wrap the data in a named key: `{ "deal": {...} }`, `{ "deals": [...] }`. Deletes and mark-read return `204` with no body. |
| Partial updates | `PATCH` changes only the fields you send. To clear a field, send the matching `clear_*: true` flag (for example `clear_due_at`); `null` doesn't clear. |
| Paging | Contacts, notifications and timelines use cursor paging: `limit` plus `before_id` = the last id of the previous page. Tasks, deals, invoices and enrollments use `limit` plus `offset`. The default `limit` is 50 and the maximum is 200. |
| Request ID | Every response has an `X-Request-ID` header, and errors repeat it as `request_id`. Show it in error toasts for support. |

### Roles

Each org defines its own roles, and every user has one.

- **What a role grants:** for each module, the actions `view`, `create`, `edit` and `delete`.
- **Scope of each action:** `none`; `own`, meaning records the user owns or tasks assigned to them; or `all`, meaning every record in the org.

After signing in, call `GET /api/v1/me` and use its `permissions` map to show or hide screens and buttons. The server enforces every rule anyway. Role changes apply on the user's next request, so refetch `/me` when a 403 comes back.

Every org starts with these roles, which are editable except Owner:

| Role | Access |
|---|---|
| Owner (locked) | Everything. |
| Admin | Everything. |
| Manager | All records and settings; can't create users or manage roles. |
| Rep | Only records they own; their own figures in reports. |
| Viewer | Reads everything, changes nothing. |

The modules and their actions are in section 3.

### Errors on `/api/v1`: `application/problem+json`

```json
{
  "type": "about:blank",
  "title": "Unprocessable Entity",
  "status": 422,
  "detail": "Title is required.",
  "code": "validation_failed",
  "errors": { "title": "required" },
  "request_id": "a1b2c3"
}
```

- Use `code` to decide what to do and `detail` as the message to show.
- `errors` maps field names to `required`, `invalid` or `taken`. Map it onto `react-hook-form` with `setError(field, ...)`.

Common codes:

| Code | Status | Meaning |
|---|---|---|
| `unauthorized` | 401 | Token missing or expired: refresh, then retry. |
| `forbidden` | 403 | The role doesn't allow this. |
| `not_found` | 404 | No such record, or the caller can't see it. |
| `invalid_body` | 400 | The body isn't JSON or has unknown fields. |
| `invalid_query` | 400 | A bad query parameter. |
| `validation_failed` | 422 | Field errors are in `errors`. |
| `internal` | 500 | Server error. |

Codes specific to one module are listed under that module.

Legacy routes (`/api/auth/*`, `/api/contacts*`, `/api/profile`, `/api/dashboard/sales`) answer errors as **plain text**, for example `Invalid email or password`.

---

## 2. Changes `src/services` needs before using `/api/v1`

1. **Read problem+json bodies as JSON.** `fetch-adapter.ts` only parses bodies whose content type contains `application/json`, so `application/problem+json` errors currently arrive as raw text. Test for `json` instead:
   ```ts
   return contentType.includes('json') ? response.json() : response.text();
   ```
2. **Pull the message from problem bodies.** In `messageFromBody`, also look at `detail`:
   ```ts
   for (const key of ['detail', 'message', 'error'] as const) { ... }
   ```
   Keep the whole body on `HttpError.body` so forms can read `code` and `errors`.
3. **Send cookies on auth calls.** The refresh token is an httpOnly cookie scoped to `/api/auth`. Without `credentials: 'include'`, the browser never stores or sends it from `localhost:5173`. Add `credentials: 'include'` to the `fetch` call.
4. **Refresh on 401.** Access tokens last 15 minutes. On a 401 from anything other than `/api/auth/*`, call `POST /api/auth/refresh` once, store the new token and retry the request. If the refresh also fails, sign the user out. A sketch is in section 3.
5. **Hybrid mode.** Add `'/api/v1/'` to `BACKEND_ROUTES` in `http.ts`, or switch to `VITE_API_MODE=real`.

---

## 3. Auth (`/api/auth`, legacy plain-text errors)

| Method and path | Body | Response |
|---|---|---|
| `POST /api/auth/register` | `{ name, email, password, organization_name? }` | `201 { message, user_id }`. Creates a new org with this user as its owner. |
| `POST /api/auth/login` | `{ email, password }` | `200 { message, user_id, token, expires_in }`, and sets the refresh cookie. |
| `POST /api/auth/refresh` | none (uses the cookie) | `200 { user_id, token, expires_in }` and rotates the cookie. Otherwise `401`. |
| `POST /api/auth/logout` | none | `200 { message }` and clears the cookie. Safe to call more than once. |
| `POST /api/auth/forgot-password` | `{ email }` | Always `200 { message }`. The emailed link goes to `FRONTEND_URL/reset-password?token=...`. |
| `POST /api/auth/reset-password` | `{ token, new_password }` | `200 { message }`. A bad token gets `400`. |
| `GET /api/profile` | none | `200 { message }`. Only checks that the token works. |

Passwords must be 8 to 72 characters. `expires_in` is in seconds.

The JWT payload carries only `user_id` and `org_id`, not the role, because the role can change at any time. Get the role and permissions from `GET /api/v1/me`.

```ts
// Refresh-and-retry, inside fetchAdapter
let refreshing: Promise<string | null> | null = null;

async function refreshToken(): Promise<string | null> {
  refreshing ??= fetch(`${API_URL}/api/auth/refresh`, { method: 'POST', credentials: 'include' })
    .then(async (r) => (r.ok ? ((await r.json()) as { token: string }).token : null))
    .finally(() => { refreshing = null; });
  const token = await refreshing;
  setAuthToken(token);
  return token;
}

// After the first response:
if (response.status === 401 && !path.startsWith('/api/auth/') && !retried) {
  const token = await refreshToken();
  if (token) return fetchAdapter({ ...req, retried: true });
  // else: redirect to /login
}
```

### Me and the permission catalog

| Method and path | Response |
|---|---|
| `GET /api/v1/me` | `{ user, role: { id, name, locked }, permissions }`. `permissions` contains every module and action. |
| `GET /api/v1/permissions` | `{ modules: ModuleSpec[] }`. Build the role editor's grid from this. |

```ts
type Module = 'contacts' | 'deals' | 'tasks' | 'nurture' | 'invoices' | 'reports' | 'settings' | 'users' | 'roles';
type Action = 'view' | 'create' | 'edit' | 'delete';
type Scope = 'none' | 'own' | 'all';
type Permissions = Record<Module, Partial<Record<Action, Scope>>>;
interface ModuleSpec { module: Module; label: string; actions: { action: Action; scopes: Scope[]; description: string }[] }

// e.g. can(me.permissions, 'deals', 'edit') to show the Edit button
const can = (p: Permissions, m: Module, a: Action) => (p[m]?.[a] ?? 'none') !== 'none';
```

| Module | Actions | What scope `all` adds |
|---|---|---|
| contacts | view, create, edit, delete | create: owner other than self; edit: reassign owner |
| deals | view, create, edit, delete | create: owner other than self; edit: reassign owner |
| tasks | view, create, edit, delete | create: assign to others; edit: reassign |
| nurture | view, create, edit, delete | create, edit, delete: manage sequences. With `own`, create enrolls and edit stops enrollments for the user's own contacts. |
| invoices | view, create, edit, delete | delete: also cancel issued invoices |
| reports | view | the whole org, plus the rep comparison (`own` gives only the user's own figures) |
| settings | view, edit (`none` or `all` only) | lead settings and rules, deal stages, billing profile, report settings |
| users | view, create, edit (`none` or `all` only) | edit covers renaming, password reset and role assignment |
| roles | view, create, edit, delete (`none` or `all` only) | |

`edit` and `delete` can't reach further than `view`.

### Users (`/api/v1/users`)

| Method and path | Needs | Body | Response |
|---|---|---|---|
| `GET /api/v1/users` | users.view | none | `{ users: User[] }` |
| `GET /api/v1/users/{id}` | users.view | none | `{ user }` |
| `POST /api/v1/users` | users.create | `{ name, email, password, role_id }` | `201 { user }`. The user can sign in with that password straight away; no email is sent. |
| `PATCH /api/v1/users/{id}` | users.edit | `{ name }` | `{ user }` |
| `PUT /api/v1/users/{id}/role` | users.edit | `{ role_id }` | `{ user }`. Takes effect on that user's next request. |
| `PUT /api/v1/users/{id}/password` | users.edit | `{ password }` | `204`. Signs that user out everywhere. |

```ts
interface User { id: number; name: string; email: string; role_id: number; role_name: string; created_at: string }
```

Errors:

| Code | When |
|---|---|
| `409 email_taken` | The email is already registered. |
| `422 validation_failed` | A field is invalid; `errors` names `role_id`, `password`, `name` or `email`. |
| `403 exceeds_own_access` | The role grants more than the caller has. |
| `403 forbidden` | Only owners can give or take the Owner role, or change an owner. |
| `409 own_role` | Users can't change their own role. |
| `409 last_owner` | The org must keep at least one owner. |

### Roles (`/api/v1/roles`)

| Method and path | Needs | Body | Response |
|---|---|---|---|
| `GET /api/v1/roles` | roles.view | none | `{ roles: Role[] }`, with Owner first. |
| `GET /api/v1/roles/{id}` | roles.view | none | `{ role }` |
| `POST /api/v1/roles` | roles.create | `{ name, description?, permissions }` | `201 { role }` |
| `PATCH /api/v1/roles/{id}` | roles.edit | `{ name?, description?, permissions? }` | `{ role }`. `permissions` replaces the whole map. |
| `DELETE /api/v1/roles/{id}` | roles.delete | none | `204` |

```ts
interface Role { id: number; name: string; description: string; locked: boolean; permissions: Permissions; user_count: number; created_at: string; updated_at: string }
```

- In `permissions`, modules or actions left out become `none`. Send the full grid from the editor.
- Invalid entries come back as `422` with a field such as `errors["permissions.deals.edit"]`.

Errors:

| Code | When |
|---|---|
| `409 name_taken` | Another role has that name. |
| `409 role_locked` | Owner can't be edited or deleted. |
| `409 role_in_use` | Move the role's users to another role first. |
| `403 exceeds_own_access` | Nobody can create or change a role, or assign one, with more access than they have themselves. |

---

## 4. Contacts and leads

### Contacts (`/api/v1/contacts`)

| Method and path | Query or body | Response |
|---|---|---|
| `GET /api/v1/contacts` | `?temperature=cold\|warm\|hot&lifecycle_stage=&limit=&before_id=` | `{ contacts: Contact[] }`, newest first. |
| `GET /api/v1/contacts/{id}` | none | `{ contact }` |
| `GET /api/v1/contacts/{id}/timeline` | `?limit=&before_id=` | `{ activities: Activity[] }`, newest first. |
| `POST /api/v1/contacts/{id}/activities` | `{ kind: 'note'\|'call'\|'email', direction?: 'inbound'\|'outbound', summary?, body, occurred_at? }` | `201 { activity }` |
| `PUT /api/v1/contacts/{id}/lifecycle-stage` | `{ lifecycle_stage }` | `{ contact }` |

To create or edit a contact's basic fields, use the legacy routes until v1 versions exist:

| Method and path | Response |
|---|---|
| `GET /api/contacts` | `{ contacts }` |
| `POST /api/contacts` | `201 { message, contact }`. Needs `first_name`. |
| `GET /api/contacts/{id}` | `{ contact }` |
| `PATCH /api/contacts/{id}` | `{ message, contact }` |
| `DELETE /api/contacts/{id}` | `{ message }` |

For new leads, prefer `POST /api/v1/leads`, which also scores, routes and deduplicates.

```ts
type LifecycleStage = 'lead' | 'mql' | 'sql' | 'opportunity' | 'customer' | 'lost';
type Temperature = 'cold' | 'warm' | 'hot';

interface Contact {
  id: number; first_name: string; last_name: string; email: string; mobile: string;
  alternate_mobile: string; company_id: number | null; lead_status_id: number | null;
  lead_owner_id: number | null; destination: string; source: string; notes: string;
  lifecycle_stage: LifecycleStage;
  score: { score: number; temperature: Temperature; reasons: string[]; method: 'rules'; scored_at: string } | null;
  marketing_opt_out_at: string | null;
  created_at: string; updated_at: string;
}

interface Activity {
  id: number; contact_id: number;
  kind: 'captured' | 'duplicate_captured' | 'message' | 'note' | 'call' | 'email' | 'score_changed'
      | 'owner_assigned' | 'lifecycle_changed' | 'task_created' | 'task_completed'
      | 'deal_created' | 'deal_stage_changed' | 'deal_won' | 'deal_lost'
      | 'nurture_enrolled' | 'nurture_stopped' | 'opted_out'
      | 'invoice_issued' | 'invoice_sent' | 'invoice_cancelled';
  channel: 'web_form' | 'whatsapp' | 'email' | 'phone' | 'manual' | 'api' | 'system' | null;
  direction: 'inbound' | 'outbound' | null;
  summary: string; body: string; metadata: Record<string, unknown>;
  actor_user_id: number | null; occurred_at: string;
}
```

Render the timeline by `kind`: an icon per kind, `summary` as the line and `body` when expanded.

### Leads (`/api/v1/leads`)

| Method and path | Body | Response |
|---|---|---|
| `POST /api/v1/leads` | `{ first_name, last_name?, email?, mobile?, destination?, source?, notes?, message?, channel? }` | `201 { lead }` when a new contact is created. `200 { lead }` when the email or mobile matched an existing contact, which is merged and re-scored. |
| `POST /api/v1/leads/{contactId}/rescore` | none | `{ lead }` |

- `first_name` and at least one of email or mobile are required.
- `channel` defaults to `manual`; it can also be `phone`, `email`, `web_form` or `api`.

```ts
interface LeadResult { contact_id: number; created: boolean; score: number; temperature: Temperature; reasons: string[]; owner_id: number | null }
```

Other errors:
- `409 duplicate_contact`: the match is owned by someone else and the caller is a rep.
- `422 invalid_owner`.

A lead that becomes **hot** automatically gets a follow-up task and a notification for its owner, so the UI doesn't need to do anything.

### Public web-form intake (no auth)

`POST /api/v1/intake/forms/{public_key}` takes JSON or `application/x-www-form-urlencoded` with any of these fields: `first_name`, `last_name`, `name`, `email`, `mobile`, `phone`, `destination` and `message`. Other fields are ignored.

- `website` is a honeypot: keep it hidden and empty.
- The response is always `202 { status: "received" }`, or `404` when the key is unknown or inactive.
- `public_key` comes from the lead-source settings.

### Lead settings (needs settings.view to read, settings.edit to change)

| Method and path | Body | Response |
|---|---|---|
| `GET /api/v1/lead-settings` | none | `{ settings }` |
| `PATCH /api/v1/lead-settings` | `{ hot_threshold?, warm_threshold?, default_owner_id?, clear_default_owner?, hot_followup_minutes? }` | `{ settings }` |
| `GET /api/v1/lead-sources` | none | `{ sources }` |
| `POST /api/v1/lead-sources` | `{ name, channel: 'web_form'\|'api' }` | `201 { source }` |
| `PATCH /api/v1/lead-sources/{id}` | `{ name?, active? }` | `{ source }` |
| `POST /api/v1/lead-sources/{id}/rotate-key` | none | `{ source }` with a new `public_key`. The old form URL stops working. |
| `GET /api/v1/whatsapp-accounts` | none | `{ accounts }` |
| `POST /api/v1/whatsapp-accounts` | `{ phone_number_id, display_phone, source_name }` | `201 { account }` |
| `DELETE /api/v1/whatsapp-accounts/{id}` | none | `204` |
| `GET /api/v1/scoring-rules` | none | `{ rules }` |
| `POST /api/v1/scoring-rules` | `{ name, criterion, value?, points, active? }` | `201 { rule }` |
| `PATCH /api/v1/scoring-rules/{id}` | any of `name`, `criterion`, `value`, `points`, `active` | `{ rule }` |
| `DELETE /api/v1/scoring-rules/{id}` | none | `204` |
| `GET /api/v1/assignment-rules` | none | `{ rules }`, in evaluation order. |
| `POST /api/v1/assignment-rules` | `{ name, position, criterion, value?, active?, user_ids: number[] }` | `201 { rule }` |
| `PATCH /api/v1/assignment-rules/{id}` | any of the fields above | `{ rule }` |
| `DELETE /api/v1/assignment-rules/{id}` | none | `204` |

```ts
interface LeadSettings { hot_threshold: number; warm_threshold: number; default_owner_id: number | null; hot_followup_minutes: number; updated_at: string }
// Defaults: hot 70, warm 40, follow-up within 15 minutes.
interface LeadSource { id: number; name: string; channel: 'web_form' | 'api'; public_key: string; active: boolean; created_at: string; updated_at: string }
interface WhatsAppAccount { id: number; phone_number_id: string; display_phone: string; source_name: string; created_at: string }
interface ScoringRule { id: number; name: string; criterion: ScoringCriterion; value: string; points: number; active: boolean; created_at: string; updated_at: string }
type ScoringCriterion = 'has_email' | 'has_mobile' | 'has_company' | 'source_equals' | 'destination_equals'
  | 'channel_used' | 'text_contains' | 'inbound_messages_at_least';
interface AssignmentRule { id: number; name: string; position: number; criterion: AssignCriterion; value: string; active: boolean; user_ids: number[]; created_at: string; updated_at: string }
type AssignCriterion = 'any' | 'source_equals' | 'channel_equals' | 'destination_equals' | 'temperature_equals';
```

- Every criterion needs a `value` except `has_*` and `any`.
- An assignment rule with several `user_ids` hands leads out round-robin.

Other errors: `409 name_taken`, `409 phone_number_taken`.

---

## 5. Tasks and notifications

### Tasks (`/api/v1/tasks`)

| Method and path | Query or body | Response |
|---|---|---|
| `GET /api/v1/tasks` | `?status=open\|done\|cancelled&assignee_id=&contact_id=&overdue=true&limit=&offset=` | `{ tasks }` |
| `GET /api/v1/tasks/{id}` | none | `{ task }` |
| `POST /api/v1/tasks` | `{ title, contact_id?, assignee_id?, description?, type?, priority?, due_at? }` | `201 { task }`. `assignee_id` defaults to the caller. |
| `PATCH /api/v1/tasks/{id}` | any of `title`, `description`, `type`, `priority`, `status`, `assignee_id`, `due_at`, plus `clear_due_at` | `{ task }` |
| `DELETE /api/v1/tasks/{id}` | none | `204` |

```ts
interface Task {
  id: number; contact_id: number | null; assignee_id: number; created_by_id: number | null;
  title: string; description: string;
  type: 'follow_up' | 'call' | 'email' | 'whatsapp' | 'meeting' | 'todo';
  priority: 'low' | 'normal' | 'high' | 'urgent';
  status: 'open' | 'done' | 'cancelled';
  origin: 'manual' | 'hot_lead' | 'deal_lost' | 'invoice_review'; // non-manual tasks were created by the workflow
  due_at: string | null; completed_at: string | null; created_at: string; updated_at: string;
}
```

To complete a task, send `PATCH { status: 'done' }`.

### Notifications (`/api/v1/notifications`)

| Method and path | Query | Response |
|---|---|---|
| `GET /api/v1/notifications` | `?unread=true&limit=&before_id=` | `{ notifications, unread_count }` |
| `POST /api/v1/notifications/{id}/read` | none | `204` |
| `POST /api/v1/notifications/read-all` | none | `204` |

```ts
interface Notification {
  id: number; kind: 'hot_lead' | 'lead_assigned' | 'task_assigned' | 'task_overdue';
  title: string; body: string; link: string; // a frontend path: /contacts/{id} or /tasks/{id}
  contact_id: number | null; task_id: number | null; read_at: string | null; created_at: string;
}
```

There's no push channel yet. For the bell badge, poll with `useQuery({ queryKey: ['notifications'], refetchInterval: 30_000 })`. The `link` values imply that `/contacts/:id` and `/tasks/:id` routes should exist.

---

## 6. Deals

### Stages (`/api/v1/deal-stages`)

| Method and path | Needs | Body | Response |
|---|---|---|---|
| `GET /api/v1/deal-stages` | anyone | none | `{ stages }`, in pipeline order. |
| `POST /api/v1/deal-stages` | settings.edit | `{ name, position?, kind, probability?, guidance?, required_fields? }` | `201 { stage }` |
| `PATCH /api/v1/deal-stages/{id}` | settings.edit | same as POST except `kind`, which can't change | `{ stage }` |
| `DELETE /api/v1/deal-stages/{id}` | settings.edit | none | `204` |

```ts
interface Stage {
  id: number; name: string; position: number; kind: 'open' | 'won' | 'lost'; probability: number; // 0–100
  guidance: string; required_fields: ('amount' | 'expected_close_date' | 'contact')[];
  open_deals: number; open_value_paise: number; created_at: string; updated_at: string;
}
```

- The default stages are Qualification, Needs Analysis, Proposal, Negotiation, Closed Won and Closed Lost.
- Show each stage's `guidance` on the deal card or Kanban column.

Other errors:
- `409 stage_in_use`: the stage still has deals.
- `409 last_stage_of_kind`: the org would be left without an open, won or lost stage.
- `409 name_taken`.

### Deals (`/api/v1/deals`)

| Method and path | Query or body | Response |
|---|---|---|
| `GET /api/v1/deals` | `?status=open\|won\|lost&stage_id=&owner_id=&contact_id=&limit=&offset=` | `{ deals }` |
| `GET /api/v1/deals/{id}` | none | `{ deal }` |
| `POST /api/v1/deals` | `{ title, description?, contact_id?, owner_id?, stage_id?, amount_paise?, billing_type?, billing_period?, expected_close_date? }` | `201 { deal }` |
| `PATCH /api/v1/deals/{id}` | any of the create fields **except `stage_id`**, plus `clear_contact` and `clear_expected_close_date` | `{ deal }` |
| `POST /api/v1/deals/{id}/stage` | `{ stage_id, lost_reason? }` | `{ deal }`. This is the **only** way to change a deal's stage. |
| `DELETE /api/v1/deals/{id}` | none | `204` |
| `GET /api/v1/deals/{id}/history` | none | `{ history }` |
| `GET /api/v1/deals/forecast` | `?from=YYYY-MM&months=1..12&owner_id=` | `{ forecast }`. `months` defaults to 3. |

```ts
interface Deal {
  id: number; title: string; description: string; contact_id: number | null; owner_id: number | null;
  stage_id: number; stage_name: string; status: 'open' | 'won' | 'lost';
  amount_paise: number; billing_type: 'one_time' | 'recurring';
  billing_period: 'monthly' | 'quarterly' | 'yearly' | null; // required when recurring
  value_paise: number;                 // the one-time amount, or a recurring deal's first-year value
  expected_close_date: string | null;  // YYYY-MM-DD
  closed_at: string | null; lost_reason: string; stage_entered_at: string;
  win_probability: number; probability_notes: string[]; // the forecast's reasoning; show it as a tooltip
  created_at: string; updated_at: string;
}
interface HistoryEntry { from_stage_id: number | null; from_stage: string | null; to_stage_id: number; to_stage: string; changed_by_id: number | null; changed_at: string }
interface Forecast {
  method: string;
  months: { month: string; won_deals: number; won_paise: number; open_deals: number;
            best_case_paise: number; weighted_paise: number; forecast_paise: number; commit_paise: number }[];
  overdue: { deals: number; value_paise: number };  // open deals whose expected close date has passed
  undated: { deals: number; value_paise: number };  // open deals with no expected close date
}
```

**Moving a deal (Kanban drag and drop).** Call `POST /deals/{id}/stage`. The response can be:
- `422` with `code: 'stage_requirements'` and `errors`, for example `{ amount: 'required', contact: 'required' }`. Open the deal form with those fields highlighted, save it, then retry the move.
- `422 validation_failed` on `lost_reason`. Moving to a lost stage needs `lost_reason`, so ask for it in a dialog first.

**What winning or losing a deal does automatically:**
- Won: the contact becomes a `customer`, nurture stops, the onboarding sequence starts, and a **draft invoice** plus a review task are created.
- Lost: the contact goes into the lost-deal nurture sequence, or gets a task to re-contact them in 30 days.

After a move, invalidate the `tasks`, `invoices` and `contacts` queries.

---

## 7. Nurture sequences

| Method and path | Needs | Body or query | Response |
|---|---|---|---|
| `GET /api/v1/nurture-sequences` | nurture.view | none | `{ sequences }` |
| `GET /api/v1/nurture-sequences/{id}` | nurture.view | none | `{ sequence }` |
| `POST /api/v1/nurture-sequences` | nurture.create: all | `{ name, trigger, active?, stop_on_reply?, steps }` | `201 { sequence }` |
| `PATCH /api/v1/nurture-sequences/{id}` | nurture.edit: all | any of the same fields (`steps` replaces the whole list) | `{ sequence }` |
| `DELETE /api/v1/nurture-sequences/{id}` | nurture.delete: all | none | `204` |
| `POST /api/v1/nurture-sequences/{id}/enrollments` | nurture.create (own: the user's own contacts) | `{ contact_id }` | `201 { enrollment }` |
| `GET /api/v1/nurture-enrollments` | nurture.view (`own` must pass `contact_id`) | `?contact_id=&sequence_id=&status=active\|completed\|stopped&limit=&offset=` | `{ enrollments }` |
| `POST /api/v1/nurture-enrollments/{id}/stop` | nurture.edit (own: the user's own contacts) | none | `{ enrollment }` |
| `POST /api/v1/unsubscribe` | **public** | `{ token }` | `{ status: 'unsubscribed' }`. A bad token gets `400 invalid_token`. |

```ts
interface Sequence {
  id: number; name: string; trigger: 'manual' | 'new_lead' | 'deal_lost' | 'deal_won';
  active: boolean; stop_on_reply: boolean; steps: Step[]; active_enrollments: number;
  created_at: string; updated_at: string;
}
interface Step {
  delay_minutes: number;  // wait after the previous step (0–525600)
  channel: 'email' | 'whatsapp';
  subject: string;        // email only
  body: string;           // supports {{first_name}}, {{last_name}} and {{full_name}}
  whatsapp_template: string; whatsapp_language: string; whatsapp_template_params: string[];
}
interface Enrollment {
  id: number; sequence_id: number; sequence_name: string; contact_id: number;
  status: 'active' | 'completed' | 'stopped';
  stop_reason: string; // replied, hot, won, opted_out, customer, or "stopped by user"
  next_step: number; next_run_at: string; attempts: number; last_error: string;
  enrolled_by_id: number | null; enrolled_at: string; finished_at: string | null;
}
```

- A sequence can have at most 20 steps.
- A WhatsApp step needs a `body`, which is sent inside the 24-hour reply window, or a `whatsapp_template`, which is approved in Meta and used outside the window. Give it both.
- Messages are held overnight (21:00 to 09:00 IST).

**Frontend page:** create `/unsubscribe?token=...`. It should call `POST /api/v1/unsubscribe` with the token from the query string and then show a confirmation.

Other errors:
- `409 name_taken`.
- `409 trigger_taken`: only one active sequence is allowed per automatic trigger.
- `409 already_enrolled`.

---

## 8. Invoices (GST)

### Billing profile

| Method and path | Needs | Body | Response |
|---|---|---|---|
| `GET /api/v1/billing-profile` | anyone | none | `{ billing_profile }` |
| `PUT /api/v1/billing-profile` | settings.edit | any of the profile fields | `{ billing_profile }` |

```ts
interface BillingProfile {
  legal_name: string; gstin: string; state_code: string; // two-digit GST state code, e.g. '27' for Maharashtra
  address: string; email: string; phone: string;
  invoice_prefix: string;        // 1–5 of A–Z and 0–9; defaults to 'INV'
  default_sac: string; default_gst_rate_bp: number; // 1800 by default
  prices_include_gst: boolean; payment_terms_days: number; // 7 by default
  bank_details: string; terms: string; updated_at: string;
}
```

### Invoices (`/api/v1/invoices`)

| Method and path | Body or query | Response |
|---|---|---|
| `GET /api/v1/invoices` | `?status=draft\|issued\|cancelled&contact_id=&deal_id=&limit=&offset=` | `{ invoices }` |
| `POST /api/v1/invoices` | `{ deal_id?, contact_id?, bill_to_name?, bill_to_address?, bill_to_gstin?, bill_to_email?, bill_to_phone?, place_of_supply?, prices_include_gst?, due_date?, notes?, lines? }` | `201 { invoice }` (a draft) |
| `GET /api/v1/invoices/{id}` | none | `{ invoice }` |
| `PATCH /api/v1/invoices/{id}` | the same fields as POST except `deal_id` and `contact_id`; `lines` replaces every line | `{ invoice }`. **Drafts only.** |
| `DELETE /api/v1/invoices/{id}` | none | `204`. **Drafts only.** |
| `POST /api/v1/invoices/{id}/issue` | none | `{ invoice }`. Assigns the number (for example `INV/26-27/0001`) and freezes the invoice. |
| `POST /api/v1/invoices/{id}/send` | none | `{ invoice }`. Emails the PDF to `bill_to_email`. |
| `POST /api/v1/invoices/{id}/cancel` | `{ reason }` | `{ invoice }`. **Issued only**; issued invoices are never deleted. |
| `GET /api/v1/invoices/{id}/pdf` | none | `application/pdf` |

```ts
interface LineInput { description: string; quantity: number; unit_price_paise: number; sac?: string; gst_rate_bp?: number } // sac and rate default from the profile
interface Line extends Required<LineInput> { taxable_paise: number; cgst_paise: number; sgst_paise: number; igst_paise: number; total_paise: number }
interface Invoice {
  id: number; status: 'draft' | 'issued' | 'cancelled'; number: string | null; financial_year: string | null;
  deal_id: number | null; contact_id: number | null; owner_id: number | null;
  bill_to_name: string; bill_to_address: string; bill_to_gstin: string; bill_to_email: string; bill_to_phone: string;
  place_of_supply: string; // a state code; the same state as the seller gives CGST+SGST, otherwise IGST
  seller_name: string; seller_gstin: string; seller_state_code: string; seller_address: string;
  prices_include_gst: boolean; issue_date: string | null; due_date: string | null; notes: string;
  taxable_paise: number; cgst_paise: number; sgst_paise: number; igst_paise: number;
  round_off_paise: number; total_paise: number; lines: Line[];
  issued_at: string | null; sent_at: string | null; cancelled_at: string | null; cancel_reason: string;
  created_at: string; updated_at: string;
}
```

**Downloading the PDF.** The endpoint needs the bearer token, so `<a href>` won't work. Fetch it as a blob instead:

```ts
const res = await fetch(`${API_URL}/api/v1/invoices/${id}/pdf`, { headers: { Authorization: `Bearer ${getAuthToken()}` } });
window.open(URL.createObjectURL(await res.blob()));
```

**Issuing.** Issuing needs these fields; anything missing comes back as `422` with `code: 'invoice_incomplete'`:
- in the billing profile: `billing_profile.legal_name`, `billing_profile.gstin`, `billing_profile.state_code` and `billing_profile.address`
- on the invoice: `bill_to_name`, `place_of_supply`, at least one line, and `lines[i].sac` on every line

Fields starting with `billing_profile.` are fixed in the settings page; the rest are fixed on the invoice.

Other errors:
- `409 deal_invoiced`: the deal already has an invoice; won deals get their draft automatically.
- `409 not_draft`, `409 not_issued`.
- `422` on `bill_to_email` when sending.

---

## 9. Reports

Every report takes `?from=YYYY-MM-DD&to=YYYY-MM-DD` (IST, both days inclusive). The default is the current month up to today, and the longest period is 731 days. All reports need reports.view; with scope `own` they show only the user's own figures.

| Method and path | Response |
|---|---|
| `GET /api/v1/reports/dashboard` | `{ dashboard }` |
| `GET /api/v1/reports/funnel` | `{ funnel }` |
| `GET /api/v1/reports/pipeline` | `{ stages }`. Current open deals, so no period. |
| `GET /api/v1/reports/sources` | `{ sources }` |
| `GET /api/v1/reports/reps` | `{ reps }`. Needs reports.view: all (otherwise `403`). |
| `GET /api/v1/reports/revenue?months=1..36` | `{ months }`. The default is 12, oldest first. |
| `GET /api/v1/reports/settings` | `{ settings }` |
| `PUT /api/v1/reports/settings` | Takes `{ weekly_email: boolean }` and returns `{ settings }`. Needs settings.edit. |
| `GET /api/dashboard/sales` | Legacy home-page tiles, **in rupees**. |

```ts
interface Count { count: number; value_paise: number }
interface Period { from: string; to: string }
interface Dashboard {
  period: Period; open_deals: Count; won: Count; lost: Count; win_rate_percent: number;
  avg_deal_size_paise: number; avg_sales_cycle_days: number; new_customers: number; new_leads: number;
  hot_leads: number; invoiced_paise: number; arr_paise: number; mrr_paise: number;
}
interface Funnel { period: Period; lost: number;
  stages: { stage: 'lead' | 'mql' | 'sql' | 'opportunity' | 'customer'; reached: number; from_previous_percent: number; from_first_stage_percent: number }[] }
interface PipelineStage { stage_id: number; name: string; probability: number; deals: number; value_paise: number; weighted_paise: number; avg_days_in_stage: number }
interface SourceRow { source: string; leads: number; hot_leads: number; customers: number; conversion_percent: number; won_paise: number } // a source of "(none)" means unknown
interface RepRow { user_id: number; name: string; role: string /* role name */; leads_assigned: number; open_deals: Count; won: Count; lost: Count; win_rate_percent: number; overdue_tasks: number }
interface RevenueMonth { month: string; won_deals: number; won_paise: number; new_arr_paise: number; new_customers: number; invoiced_paise: number }
interface ReportSettings { weekly_email: boolean; updated_at: string }
interface SalesDashboard { total_open_deals: number; total_open_deal_value: number; total_won: number; total_loss: number; total_new_customers: number; total_arr: number; total_mrr: number }
```

**Building the `/reports` page with recharts (already installed):**
- The weekly email links to `FRONTEND_URL/reports`.
- The dashboard response becomes KPI tiles.
- The funnel works as a horizontal bar chart of `reached`, labelled with `from_previous_percent`.
- Pipeline is a bar chart of `value_paise` with `weighted_paise` beside it.
- Revenue is a line or bar chart by `month`.
- Sources and reps are tables.
- Use one date-range picker (react-day-picker) to drive every query key, as in `['reports', 'dashboard', from, to]`.

---

## 10. Suggested service layout

The pattern for each feature, using the existing `http` helper:

```ts
// src/features/deals/api.ts
import { http } from '@/services';
import type { Deal, Stage, Forecast } from './types';

export const dealsApi = {
  list: (q: { status?: string; stage_id?: number; owner_id?: number; contact_id?: number; limit?: number; offset?: number }) =>
    http.get<{ deals: Deal[] }>('/api/v1/deals', { query: q }).then((r) => r.deals),
  get: (id: number) => http.get<{ deal: Deal }>(`/api/v1/deals/${id}`).then((r) => r.deal),
  create: (body: Partial<Deal> & { title: string }) => http.post<{ deal: Deal }>('/api/v1/deals', body).then((r) => r.deal),
  update: (id: number, patch: Record<string, unknown>) => http.patch<{ deal: Deal }>(`/api/v1/deals/${id}`, patch).then((r) => r.deal),
  move: (id: number, stage_id: number, lost_reason?: string) =>
    http.post<{ deal: Deal }>(`/api/v1/deals/${id}/stage`, { stage_id, ...(lost_reason ? { lost_reason } : {}) }).then((r) => r.deal),
  remove: (id: number) => http.delete(`/api/v1/deals/${id}`),
  stages: () => http.get<{ stages: Stage[] }>('/api/v1/deal-stages').then((r) => r.stages),
  forecast: (q: { from?: string; months?: number; owner_id?: number }) =>
    http.get<{ forecast: Forecast }>('/api/v1/deals/forecast', { query: q }).then((r) => r.forecast),
};

// Showing a problem's field errors in a form
import { isHttpError } from '@/services/http-error';
interface Problem { code: string; detail?: string; errors?: Record<string, string> }

export function applyProblem(err: unknown, setError: (f: string, e: { message: string }) => void): string | undefined {
  if (!isHttpError(err)) return undefined;
  const p = err.body as Problem;
  for (const [field, problem] of Object.entries(p.errors ?? {})) setError(field, { message: problem });
  return p.code;
}
```

Leave out fields the user didn't fill in rather than sending `undefined` or `null`, because `/api/v1` rejects unknown fields and treats `null` as "no change".

### Frontend routes the backend links to

| Route | Linked from |
|---|---|
| `/reset-password?token=` | the password reset email |
| `/unsubscribe?token=` | the footer of nurture emails |
| `/contacts/:id` | notifications |
| `/tasks/:id` | notifications |
| `/reports` | the weekly report email |
