// Package app wires configuration, the database and every module into the
// HTTP handler served by cmd/api. Tests build the same handler.
package app

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepashearaion/manage-crm/handlers"
	"github.com/deepashearaion/manage-crm/internal/auth"
	"github.com/deepashearaion/manage-crm/internal/contacts"
	"github.com/deepashearaion/manage-crm/internal/platform/config"
	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/mail"
	"github.com/deepashearaion/manage-crm/middleware"
	"github.com/deepashearaion/manage-crm/routes"
	"github.com/deepashearaion/manage-crm/utils"
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
	tokens := utils.NewJWT(d.Config.JWTSecret, d.Config.AccessTokenTTL)

	router := routes.SetupRoutes(
		&handlers.AuthHandler{
			DB:                         d.DB,
			Tokens:                     tokens,
			Sessions:                   &auth.Sessions{DB: d.DB, TTL: d.Config.RefreshTokenTTL},
			Mailer:                     d.Mailer,
			Logger:                     d.Logger,
			AccessTokenTTL:             d.Config.AccessTokenTTL,
			CookieSecure:               d.Config.RefreshCookieSecure,
			FrontendURL:                d.Config.FrontendURL,
			ReturnResetTokenInResponse: d.Config.ReturnResetTokenInResponse,
		},
		&handlers.SalesDashboardHandler{DB: d.DB},
		tokens,
	)

	requireUser := func(next http.Handler) http.Handler {
		return middleware.JWTValidation(tokens, next.ServeHTTP)
	}

	contacts.Routes(router,
		contacts.NewHandler(contacts.NewService(contacts.NewRepository(d.DB)), d.Logger),
		requireUser)

	return httpx.Chain(router,
		httpx.RequestID,
		httpx.AccessLog(d.Logger),
		httpx.Recover(d.Logger),
		httpx.CORS(d.Config.CORSAllowedOrigins),
		httpx.MaxBodyBytes(maxRequestBodyBytes),
	)
}
