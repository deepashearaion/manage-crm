// Package app wires configuration, the database and every module into the
// HTTP handler served by cmd/api. Tests build the same handler.
package app

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepashearaion/manage-crm/handlers"
	"github.com/deepashearaion/manage-crm/internal/platform/config"
	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/mail"
	"github.com/deepashearaion/manage-crm/middleware"
	"github.com/deepashearaion/manage-crm/routes"
	"github.com/deepashearaion/manage-crm/utils"
)

const (
	maxRequestBodyBytes = 1 << 20 // 1 MiB

	// accessTokenTTL keeps the legacy 24h lifetime until short-lived access
	// tokens with refresh-token rotation replace it.
	accessTokenTTL = 24 * time.Hour
)

type Deps struct {
	Config *config.Config
	DB     *pgxpool.Pool
	Logger *slog.Logger
	Mailer mail.Mailer
}

// NewHandler returns the complete HTTP stack: middleware plus all routes.
func NewHandler(d Deps) http.Handler {
	tokens := utils.NewJWT(d.Config.JWTSecret, accessTokenTTL)

	router := routes.SetupRoutes(
		&handlers.AuthHandler{
			DB:                         d.DB,
			Tokens:                     tokens,
			Mailer:                     d.Mailer,
			Logger:                     d.Logger,
			FrontendURL:                d.Config.FrontendURL,
			ReturnResetTokenInResponse: d.Config.ReturnResetTokenInResponse,
		},
		&handlers.ContactHandler{DB: d.DB},
		&handlers.SalesDashboardHandler{DB: d.DB},
		tokens,
	)

	return httpx.Chain(router,
		httpx.RequestID,
		httpx.AccessLog(d.Logger),
		httpx.Recover(d.Logger),
		func(h http.Handler) http.Handler { return middleware.CORS(d.Config.CORSAllowedOrigins, h) },
		httpx.MaxBodyBytes(maxRequestBodyBytes),
	)
}
