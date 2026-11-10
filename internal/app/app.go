// Package app wires configuration, the database and every module into the
// HTTP handler and background jobs served by cmd/api. Tests build the same.
package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepashearaion/manage-crm/internal/auth"
	"github.com/deepashearaion/manage-crm/internal/contacts"
	"github.com/deepashearaion/manage-crm/internal/deals"
	"github.com/deepashearaion/manage-crm/internal/leads"
	"github.com/deepashearaion/manage-crm/internal/notifications"
	"github.com/deepashearaion/manage-crm/internal/nurture"
	"github.com/deepashearaion/manage-crm/internal/platform/config"
	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/mail"
	"github.com/deepashearaion/manage-crm/internal/platform/scheduler"
	"github.com/deepashearaion/manage-crm/internal/platform/whatsapp"
	"github.com/deepashearaion/manage-crm/internal/reports"
	"github.com/deepashearaion/manage-crm/internal/tasks"
)

const maxRequestBodyBytes = 1 << 20 // 1 MiB

type Deps struct {
	Config *config.Config
	DB     *pgxpool.Pool
	Logger *slog.Logger
	Mailer mail.Mailer
	// WhatsApp sends WhatsApp messages; nil builds the Cloud API client from
	// the config (or a logging stand-in when WHATSAPP_TOKEN is unset).
	WhatsApp whatsapp.Sender
	// Now is the clock for scheduled work; nil is time.Now (tests set it).
	Now func() time.Time
}

// App is the wired application: the HTTP handler and the background jobs
// cmd/api runs with the scheduler.
type App struct {
	Handler http.Handler
	Jobs    []scheduler.Job
}

// NewHandler returns the complete HTTP stack: middleware plus all routes.
func NewHandler(d Deps) http.Handler { return New(d).Handler }

// New wires every module.
func New(d Deps) *App {
	cfg := d.Config
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.WhatsApp == nil {
		if cfg.WhatsApp.SendingEnabled() {
			d.WhatsApp = whatsapp.NewClient(cfg.WhatsApp.AccessToken, cfg.WhatsApp.APIVersion)
		} else {
			d.WhatsApp = whatsapp.Log{Logger: d.Logger}
		}
	}
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
	taskService.SetClock(d.Now)
	tasks.Routes(router, tasks.NewHandler(taskService, d.Logger), requireUserV1)

	// The leads service is built below; nurture asks it for the org's
	// WhatsApp number only when sending, by which time it is set.
	numbers := &nurtureNumbers{}
	nurtureService := nurture.NewService(nurture.NewRepository(d.DB), nurtureContacts{svc: contactService}, numbers,
		d.Mailer, d.WhatsApp, nurture.Settings{FrontendURL: cfg.FrontendURL, UnsubscribeKey: cfg.JWTSecret}, d.Logger, d.Now)
	nurture.Routes(router, nurture.NewHandler(nurtureService, d.Logger), requireUserV1)

	events := workflowEvents{tasks: taskService, notifications: notificationService, users: authService, nurture: nurtureService}

	leadService := leads.NewService(leads.NewRepository(d.DB), leadsContacts{svc: contactService}, events, d.Logger)
	numbers.leads = leadService
	leads.Routes(router, leads.NewHandler(leadService, d.Logger, leads.WhatsAppSettings{
		AppSecret:   cfg.WhatsApp.AppSecret,
		VerifyToken: cfg.WhatsApp.VerifyToken,
	}), requireUserV1)

	dealService := deals.NewService(deals.NewRepository(d.DB), dealsContacts{svc: contactService}, events, d.Logger)
	deals.Routes(router, deals.NewHandler(dealService, d.Logger), requireUserV1)

	reports.Routes(router,
		reports.NewHandler(reports.NewService(reports.NewRepository(d.DB)), d.Logger),
		requireUser)

	return &App{
		Handler: httpx.Chain(router,
			httpx.RequestID,
			httpx.AccessLog(d.Logger),
			httpx.Recover(d.Logger),
			httpx.CORS(cfg.CORSAllowedOrigins),
			httpx.MaxBodyBytes(maxRequestBodyBytes),
		),
		Jobs: []scheduler.Job{
			{Name: "nurture", Every: time.Minute, Run: nurtureService.RunDue},
			{Name: "overdue task reminders", Every: 5 * time.Minute, Run: taskService.RemindOverdue},
		},
	}
}
