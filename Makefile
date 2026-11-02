# Development tasks. Every recipe is a plain `go` command, so it works the
# same with GNU make on Windows, macOS and Linux.

EXE := $(if $(filter Windows_NT,$(OS)),.exe,)

# golangci-lint is a dev tool, not a go.mod dependency. By default it is run
# at a pinned version via `go run`; use an installed binary with
#   make lint GOLANGCI_LINT=golangci-lint
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

.PHONY: run build test test-db lint fmt migrate migrate-status migrate-down mailpit

## run: start the API (reads .env)
run:
	go run ./cmd/api

## build: compile the API to bin/api
build:
	go build -o bin/api$(EXE) ./cmd/api

## test: run all tests (needs crm_test; see test-db)
test:
	go test -count=1 ./...

## test-db: drop and recreate the test database from TEST_DATABASE_URL (name must end in _test)
test-db:
	go run ./cmd/testdb

## lint: run golangci-lint
lint:
	$(GOLANGCI_LINT) run ./...

## fmt: format the code
fmt:
	$(GOLANGCI_LINT) fmt ./...

## migrate: apply pending migrations to the .env database
migrate:
	go run ./cmd/migrate up

## migrate-status: list applied and pending migrations
migrate-status:
	go run ./cmd/migrate status

## migrate-down: roll back the most recent migration
migrate-down:
	go run ./cmd/migrate down

## mailpit: start Mailpit (SMTP localhost:1025, UI http://localhost:8025)
mailpit:
	docker compose up -d mailpit
