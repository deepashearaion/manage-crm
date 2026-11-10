// Package app wires configuration, the database and every module into the
// HTTP handler served by cmd/api. Tests build the same handler.
package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepashearaion/manage-crm/internal/auth"
	"github.com/deepashearaion/manage-crm/internal/contacts"
	"github.com/deepashearaion/manage-crm/internal/deals"
	"github.com/deepashearaion/manage-crm/internal/leads"
	"github.com/deepashearaion/manage-crm/internal/notifications"
	"github.com/deepashearaion/manage-crm/internal/platform/config"
	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/mail"
	"github.com/deepashearaion/manage-crm/internal/reports"
	"github.com/deepashearaion/manage-crm/internal/tasks"
)

const maxRequestBodyBytes = 1 << 20 // 1 MiB

type Deps struct {
	Config *config.Config
	DB     *pgxpool.Pool
	Logger *slog.Logger
	Mailer mail.Mailer
}

// NewHandler returns the complete HTTP stack: middleware plus all routes.
func NewHandler(d Deps) http.Handler {
	cfg := d.Config
	router := mux.NewRouter()

	tokens := auth.NewJWT(cfg.JWTSecret, cfg.AccessTokenTTL)
	requireUser := auth.RequireUser(tokens)
	requireUserV1 := auth.RequireUserV1(tokens)

	authService := auth.NewService(auth.NewRepository(d.DB), tokens, d.Mailer, auth.Settings{
		RefreshTokenTTL: cfg.RefreshTokenTTL,
		FrontendURL:     cfg.FrontendURL,
		// Every module that keeps per-org defaults seeds them here.
		OnOrgCreated: func(ctx context.Context, tx pgx.Tx, orgID int64) error {
			if err := deals.SeedDefaultStages(ctx, tx, orgID); err != nil {
				return err
			}
			return leads.SeedDefaults(ctx, tx, orgID)
		},
	})
	auth.Routes(router, auth.NewHandler(authService, d.Logger, auth.HandlerSettings{
		CookieSecure:               cfg.RefreshCookieSecure,
		ReturnResetTokenInResponse: cfg.ReturnResetTokenInResponse,
	}), tokens)

	contactService := contacts.NewService(contacts.NewRepository(d.DB))
	contacts.Routes(router, contacts.NewHandler(contactService, d.Logger), requireUser, requireUserV1)

	notificationService := notifications.NewService(d.DB, userDirectory{users: authService}, d.Mailer, cfg.FrontendURL, d.Logger)
	notifications.Routes(router, notifications.NewHandler(notificationService, d.Logger), requireUserV1)

	taskService := tasks.NewService(tasks.NewRepository(d.DB), tasksContacts{svc: contactService},
		tasksNotifier{notifications: notificationService}, d.Logger)
	tasks.Routes(router, tasks.NewHandler(taskService, d.Logger), requireUserV1)

	events := workflowEvents{tasks: taskService, notifications: notificationService, users: authService}

	leadService := leads.NewService(leads.NewRepository(d.DB), leadsContacts{svc: contactService}, events, d.Logger)
	leads.Routes(router, leads.NewHandler(leadService, d.Logger, leads.WhatsAppSettings{
		AppSecret:   cfg.WhatsApp.AppSecret,
		VerifyToken: cfg.WhatsApp.VerifyToken,
	}), requireUserV1)

	dealService := deals.NewService(deals.NewRepository(d.DB), dealsContacts{svc: contactService}, events, d.Logger)
	deals.Routes(router, deals.NewHandler(dealService, d.Logger), requireUserV1)

	reports.Routes(router,
		reports.NewHandler(reports.NewService(reports.NewRepository(d.DB)), d.Logger),
		requireUser)

	return httpx.Chain(router,
		httpx.RequestID,
		httpx.AccessLog(d.Logger),
		httpx.Recover(d.Logger),
		httpx.CORS(cfg.CORSAllowedOrigins),
		httpx.MaxBodyBytes(maxRequestBodyBytes),
	)
}
