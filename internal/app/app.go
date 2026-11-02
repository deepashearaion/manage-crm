// Package app wires configuration, the database and every module into the
// HTTP handler served by cmd/api. Tests build the same handler.
package app

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"authentication-backend/handlers"
	"authentication-backend/internal/platform/config"
	"authentication-backend/internal/platform/httpx"
	"authentication-backend/middleware"
	"authentication-backend/routes"
)

const maxRequestBodyBytes = 1 << 20 // 1 MiB

type Deps struct {
	Config *config.Config
	DB     *pgxpool.Pool
	Logger *slog.Logger
}

// NewHandler returns the complete HTTP stack: middleware plus all routes.
func NewHandler(d Deps) http.Handler {
	router := routes.SetupRoutes(
		&handlers.AuthHandler{DB: d.DB},
		&handlers.ContactHandler{DB: d.DB},
		&handlers.SalesDashboardHandler{DB: d.DB},
	)

	return httpx.Chain(router,
		httpx.RequestID,
		httpx.AccessLog(d.Logger),
		httpx.Recover(d.Logger),
		func(h http.Handler) http.Handler { return middleware.CORS(d.Config.CORSAllowedOrigins, h) },
		httpx.MaxBodyBytes(maxRequestBodyBytes),
	)
}
