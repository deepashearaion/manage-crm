package routes

import (
	"net/http"

	"github.com/deepashearaion/manage-crm/handlers"
	"github.com/deepashearaion/manage-crm/middleware"
	"github.com/deepashearaion/manage-crm/utils"

	"github.com/gorilla/mux"
)

func SetupRoutes(
	authHandler *handlers.AuthHandler,
	salesDashboardHandler *handlers.SalesDashboardHandler,
	tokens *utils.JWT,
) *mux.Router {

	router := mux.NewRouter()

	// =========================
	// AUTHENTICATION ROUTES
	// =========================

	router.HandleFunc(
		"/api/auth/register",
		authHandler.Register,
	).Methods(http.MethodPost)

	router.HandleFunc(
		"/api/auth/login",
		authHandler.Login,
	).Methods(http.MethodPost)

	router.HandleFunc(
		"/api/auth/logout",
		authHandler.Logout,
	).Methods(http.MethodPost)

	router.HandleFunc(
		"/api/auth/refresh",
		authHandler.Refresh,
	).Methods(http.MethodPost)

	router.HandleFunc(
		"/api/auth/forgot-password",
		authHandler.ForgotPassword,
	).Methods(http.MethodPost)

	router.HandleFunc(
		"/api/auth/reset-password",
		authHandler.ResetPassword,
	).Methods(http.MethodPost)

	// =========================
	// PROFILE ROUTE
	// =========================

	router.Handle(
		"/api/profile",
		middleware.JWTValidation(
			tokens,
			http.HandlerFunc(authHandler.Profile),
		),
	).Methods(http.MethodGet)

	// =========================
	// SALES DASHBOARD
	// =========================

	router.Handle(
		"/api/dashboard/sales",
		middleware.JWTValidation(
			tokens,
			http.HandlerFunc(
				salesDashboardHandler.GetSalesDashboard,
			),
		),
	).Methods(http.MethodGet)

	return router
}
