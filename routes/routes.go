package routes

import (
	"net/http"

	"authentication-backend/handlers"
	"authentication-backend/middleware"
	"authentication-backend/utils"

	"github.com/gorilla/mux"
)

func SetupRoutes(
	authHandler *handlers.AuthHandler,
	contactHandler *handlers.ContactHandler,
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
	// CONTACT ROUTES
	// =========================

	// Get all contacts
	router.Handle(
		"/api/contacts",
		middleware.JWTValidation(
			tokens,
			http.HandlerFunc(contactHandler.GetContacts),
		),
	).Methods(http.MethodGet)

	// Create contact
	router.Handle(
		"/api/contacts",
		middleware.JWTValidation(
			tokens,
			http.HandlerFunc(contactHandler.CreateContact),
		),
	).Methods(http.MethodPost)

	// Update contact
	router.Handle(
		"/api/contacts/{id}",
		middleware.JWTValidation(
			tokens,
			http.HandlerFunc(contactHandler.UpdateContact),
		),
	).Methods(http.MethodPatch)

	// Delete contact
	router.Handle(
		"/api/contacts/{id}",
		middleware.JWTValidation(
			tokens,
			http.HandlerFunc(contactHandler.DeleteContact),
		),
	).Methods(http.MethodDelete)

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
