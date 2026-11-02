// Package app wires configuration, the database and every module into the
// HTTP handler served by cmd/api. Tests build the same handler.
package app

import (
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepashearaion/manage-crm/internal/auth"
	"github.com/deepashearaion/manage-crm/internal/contacts"
	"github.com/deepashearaion/manage-crm/internal/deals"
	"github.com/deepashearaion/manage-crm/internal/platform/config"
	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/mail"
	"github.com/deepashearaion/manage-crm/internal/reports"
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

	authService := auth.NewService(auth.NewRepository(d.DB), tokens, d.Mailer, auth.Settings{
		RefreshTokenTTL: cfg.RefreshTokenTTL,
		FrontendURL:     cfg.FrontendURL,
		OnOrgCreated:    deals.SeedDefaultStages,
	})
	auth.Routes(router, auth.NewHandler(authService, d.Logger, auth.HandlerSettings{
		CookieSecure:               cfg.RefreshCookieSecure,
		ReturnResetTokenInResponse: cfg.ReturnResetTokenInResponse,
	}), requireUser)

	contacts.Routes(router,
		contacts.NewHandler(contacts.NewService(contacts.NewRepository(d.DB)), d.Logger),
		requireUser)

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
