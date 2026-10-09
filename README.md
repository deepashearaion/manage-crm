# CRM Backend

A Go and PostgreSQL backend for a CRM (Customer Relationship Management) application. This repository provides authentication, JWT-protected contact management, and a Sales Dashboard summary API for integration with a frontend application.

> **Project status:** The APIs listed under “Implemented API Endpoints” reflect the backend work completed and tested so far. Other CRM analytics modules may be planned, but are not described as implemented here.

## Table of Contents

- [Project Overview](#project-overview)
- [Implemented Features](#implemented-features)
- [Technology Stack](#technology-stack)
- [Project Structure](#project-structure)
- [Implemented API Endpoints](#implemented-api-endpoints)
- [Authentication and Authorization](#authentication-and-authorization)
- [Database Design](#database-design)
- [Prerequisites](#prerequisites)
- [Local Setup](#local-setup)
- [Environment Configuration](#environment-configuration)
- [Running the Backend](#running-the-backend)
- [Testing with Postman](#testing-with-postman)
- [Example Sales Dashboard Response](#example-sales-dashboard-response)
- [Security Notes](#security-notes)
- [Troubleshooting](#troubleshooting)

## Project Overview

The backend provides API endpoints used by a CRM frontend. It stores user and contact data in PostgreSQL and uses JSON over HTTP for client-server communication.

The main goals of this backend are to:
- Support user registration and login.
- Issue and validate JSON Web Tokens (JWTs) for protected routes.
- Support creating, listing, updating, and deleting contacts.
- Provide summary metrics for the Sales Dashboard, including open deals, won/lost values, new customers, ARR, and MRR.

## Implemented Features

### 1. Authentication
- User registration.
- User login with password-hash verification.
- JWT-based access to protected routes.
- Logout endpoint.
- Forgot-password and reset-password endpoints.
- Authenticated profile endpoint.

**Note:** The exact email-delivery behavior for password-reset flows depends on the current implementation and configuration. Do not assume that an email provider is configured unless it has been set up separately.

### 2. Contact Management
- Create a contact.
- List contacts.
- Update a contact by ID.
- Delete a contact by ID.
- Associate a contact with the authenticated user through `lead_owner_id`.

### 3. Sales Dashboard
- Return the total number of open deals.
- Return the combined value of open deals.
- Return total won and lost deal values.
- Return the count used for new customers.
- Return annual recurring revenue (ARR) and monthly recurring revenue (MRR).

The current implementation calculates `total_new_customers` by counting contacts created since the start of the current date (`CURRENT_DATE`). This is the implemented calculation, and it may need to be adjusted if the business definition of “new customer” differs from “contact created today.”

## Technology Stack

- **Go** — backend language.
- **PostgreSQL** — relational database.
- **pgx** — PostgreSQL driver/library for Go.
- **Gorilla Mux** — HTTP routing.
- **JWT** — token-based authentication.
- **bcrypt** — password hashing.
- **Postman** — API testing.

The repository's Go module import path is `authentication-backend`. This is the Go module path, not the PostgreSQL database name.

## Project Structure

The main files and directories include:

```text
manage-crm-contacts/
├── database/
│   └── schema.sql          # Database schema and default deal stages
├── handlers/
│   ├── auth.go             # Authentication handlers
│   ├── contact.go          # Contact API handlers
│   └── dashboard.go        # Sales Dashboard handler
├── middleware/
│   └── ...                 # JWT validation/authentication middleware
├── models/
│   └── deal.go             # Deal data model
├── routes/
│   └── routes.go           # API route definitions
├── utils/
│   ├── jwt.go              # JWT creation/validation utilities
│   └── password.go         # Password hashing/verification utilities
├── main.go                 # Application entry point
├── go.mod                  # Go module and dependency definitions
└── go.sum                  # Dependency checksums
```

The repository may contain additional files not shown above.

## Implemented API Endpoints

Base URL for local development:

```text
http://localhost:8080
```

| Method | Endpoint | Purpose | Authentication |
|---|---|---|---|
| POST | `/api/auth/register` | Register a user | No |
| POST | `/api/auth/login` | Log in and obtain a JWT | No |
| POST | `/api/auth/logout` | Logout endpoint | No, unless the current route configuration is changed |
| POST | `/api/auth/forgot-password` | Start password recovery | No |
| POST | `/api/auth/reset-password` | Reset password using reset-token data | No |
| GET | `/api/profile` | Get the authenticated user's profile | Bearer JWT |
| GET | `/api/contacts` | List contacts | Bearer JWT |
| POST | `/api/contacts` | Create a contact | Bearer JWT |
| PATCH | `/api/contacts/{id}` | Update a contact by ID | Bearer JWT |
| DELETE | `/api/contacts/{id}` | Delete a contact by ID | Bearer JWT |
| GET | `/api/dashboard/sales` | Get Sales Dashboard metrics | Bearer JWT |

**Important:** `GET /api/contacts/{id}` is not currently registered in the route configuration described in the project handoff. Use `GET /api/contacts` to list contacts.

### Contact API notes

- Contact creation uses the authenticated user's ID as `lead_owner_id`.
- For update requests, send the contact ID in the URL.
- The current update handler uses optional fields so omitted fields remain unchanged.
- In the current implementation, sending `null` does not necessarily clear an existing field; confirm the handler behavior before relying on null to clear values.

## Authentication and Authorization

The login endpoint returns a JWT when authentication succeeds. Send that token in the `Authorization` header for protected endpoints:

```http
Authorization: Bearer YOUR_JWT_TOKEN
```

The JWT implementation uses HS256 and includes user identity and timing claims. Protected endpoints validate the token through JWT middleware.

For local testing:
1. Register a user if needed.
2. Log in with that user's credentials.
3. Copy the returned token.
4. Add it as a Bearer token in Postman for protected requests.

Do not commit tokens or share them in public documentation.

## Database Design

The schema currently includes these main tables:

### `users`
Stores user account information, including name, email, password hash, reset-token fields, and timestamps.

### `contacts`
Stores contact information, including names, contact details, destination, source, notes, and optional ownership/status/company IDs. `lead_owner_id` references `users(id)`.

### `deal_stages`
Stores deal pipeline stages. The schema seeds the following values if they do not already exist:
- Qualification
- Needs Analysis
- Proposal
- Negotiation
- Closed Won
- Closed Lost

### `deals`
Stores deal title, amount, stage, optional owner/contact links, MRR, ARR, and timestamps.

The schema file is:

```text
database/schema.sql
```

The `deals` and `deal_stages` tables support the Sales Dashboard API. Calls, emails, tasks, meetings, and other analytics tables/endpoints are not listed as implemented in this README.

## Prerequisites

Install or have available:
- Go (a version compatible with the repository's `go.mod`).
- PostgreSQL.
- Git.
- Postman (optional, for API testing).

The exact versions may differ between development machines.

## Local Setup

### 1. Clone the repository

```powershell
git clone https://github.com/deepashearaion/manage-crm.git
cd manage-crm
```

If the project branch has not yet been merged into `main`, switch to the branch containing the implementation:

```powershell
git checkout contacts
git pull origin contacts
```

If the implementation has already been merged into `main`, use the default branch instead.

### 2. Create the PostgreSQL database

Open pgAdmin or `psql` and create the database if it does not already exist:

```sql
CREATE DATABASE auth_project;
```

Connect to `auth_project` before running the schema. For `psql`, for example:

```text
\connect auth_project
```

In pgAdmin, select the `auth_project` database and open the Query Tool.

### 3. Run the schema

Run the SQL in:

```text
database/schema.sql
```

This creates the tables and inserts the default deal stages. Run it against the intended development database.

### 4. Create a local `.env` file

Create `.env` in the repository root, next to `main.go`. Use the variable names expected by `database.Connect()` in the repository. A typical configuration may look like this:

```dotenv
DB_HOST=localhost
DB_PORT=5432
DB_USER=your_postgres_username
DB_PASSWORD=your_local_postgres_password
DB_NAME=auth_project
```

**Important:** These variable names are an example. Check `database/database.go` (or the file containing `database.Connect()`) and use the exact variable names the code reads. Do not upload or commit `.env`, passwords, JWT signing secrets, or real tokens. Each developer should create their own local `.env`.

### 5. Start the backend

From the repository root:

```powershell
go run .
```

Expected startup message:

```text
Server running on http://localhost:8080
```

Keep this terminal running while testing APIs. Use a separate terminal for Git commands.

## Testing with Postman

1. Make sure PostgreSQL is running and the `.env` values are correct.
2. Start the backend with `go run .`.
3. Set the base URL to `http://localhost:8080`.
4. Send `POST /api/auth/register` if you need to create a user.
5. Send `POST /api/auth/login` with that user's credentials.
6. Copy the token from the login response.
7. For protected endpoints, configure **Authorization → Bearer Token** in Postman and paste the token.
8. Test the contacts endpoints and `GET /api/dashboard/sales`.

Example request URLs:

```text
POST   http://localhost:8080/api/auth/register
POST   http://localhost:8080/api/auth/login
GET    http://localhost:8080/api/profile
GET    http://localhost:8080/api/contacts
POST   http://localhost:8080/api/contacts
PATCH  http://localhost:8080/api/contacts/{id}
DELETE http://localhost:8080/api/contacts/{id}
GET    http://localhost:8080/api/dashboard/sales
```

Request body fields depend on the selected endpoint and handler validation. Use the Postman collection shared with the project for the tested request bodies. Never place a real JWT in a shared collection; use a variable such as `{{jwt_token}}` and leave its shared value blank.

## Example Sales Dashboard Response

`GET /api/dashboard/sales` returns a JSON response similar to:

```json
{
  "total_open_deals": 1,
  "total_open_deal_value": 100000,
  "total_won": 0,
  "total_loss": 0,
  "total_new_customers": 0,
  "total_arr": 60000,
  "total_mrr": 5000
}
```

These values are example test data, not guaranteed defaults. Actual values depend on records in the database.

| Response field | Meaning |
|---|---|
| `total_open_deals` | Number of deals not in Closed Won or Closed Lost stages |
| `total_open_deal_value` | Sum of amounts for open deals |
| `total_won` | Sum of amounts in Closed Won |
| `total_loss` | Sum of amounts in Closed Lost |
| `total_new_customers` | Count of contacts created since the current date began |
| `total_arr` | Sum of annual recurring revenue values |
| `total_mrr` | Sum of monthly recurring revenue values |

## Security Notes

- Never commit `.env` or database credentials.
- Do not put real JWTs, passwords, or reset tokens in README files or shared Postman exports.
- Use each developer's own local database credentials.
- Use HTTPS and appropriate secret management in a deployed environment.
- The local setup described here is for development, not production deployment.

## Troubleshooting

### `cannot find main module`
Run `go run .` from the directory containing `go.mod`, not from a parent folder.

### Database connection failed
- Confirm PostgreSQL is running.
- Confirm the database exists.
- Check the `.env` variable names against the code.
- Confirm the username, password, port, and database name.
- Ensure `database/schema.sql` has been run in the correct database.

### `404 page not found`
- Confirm the HTTP method matches the endpoint.
- Confirm the route path is exact.
- Confirm the server is running on port `8080`.

### `401 Unauthorized` or JWT validation error
- Log in again and use the token from the current login response.
- Send `Authorization: Bearer <token>` for protected routes.
- Check that the JWT signing secret used by the server is configured consistently.

### Port 8080 is already in use
Stop the other process using port `8080` or adjust the application port consistently in code and documentation.

## Current Scope and Future Work

This README documents the authentication, contact-management, and Sales Dashboard API scope described above. Additional dashboard analytics such as leads, calls, emails, tasks, meetings, and revenue analytics should be documented as implemented only after their handlers, routes, and database requirements are added and tested.

---

Maintained as project documentation for the CRM backend.
