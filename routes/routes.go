package routes

import (
	"net/http"

	"authentication-backend/handlers"
	"authentication-backend/middleware"

	"github.com/gorilla/mux"
)

func SetupRoutes(
	authHandler *handlers.AuthHandler,
	contactHandler *handlers.ContactHandler,
	dealHandler *handlers.DealHandler,
	salesDashboardHandler *handlers.SalesDashboardHandler,
) *mux.Router {

	router := mux.NewRouter()

	// Authentication routes
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

	// Profile route
	router.Handle(
		"/api/profile",
		middleware.JWTValidation(
			http.HandlerFunc(authHandler.Profile),
		),
	).Methods(http.MethodGet)

	// Contact routes
	router.Handle(
		"/api/contacts",
		middleware.JWTValidation(
			http.HandlerFunc(contactHandler.GetContacts),
		),
	).Methods(http.MethodGet)

	router.Handle(
		"/api/contacts",
		middleware.JWTValidation(
			http.HandlerFunc(contactHandler.CreateContact),
		),
	).Methods(http.MethodPost)

	router.Handle(
		"/api/contacts/{id}",
		middleware.JWTValidation(
			http.HandlerFunc(contactHandler.UpdateContact),
		),
	).Methods(http.MethodPatch)

	router.Handle(
		"/api/contacts/{id}",
		middleware.JWTValidation(
			http.HandlerFunc(contactHandler.DeleteContact),
		),
	).Methods(http.MethodDelete)

	// Deal routes
	router.Handle(
		"/api/deals",
		middleware.JWTValidation(
			http.HandlerFunc(dealHandler.GetDeals),
		),
	).Methods(http.MethodGet)

	router.Handle(
		"/api/deals",
		middleware.JWTValidation(
			http.HandlerFunc(dealHandler.CreateDeal),
		),
	).Methods(http.MethodPost)

	router.Handle(
		"/api/deals/{id}",
		middleware.JWTValidation(
			http.HandlerFunc(dealHandler.GetDealByID),
		),
	).Methods(http.MethodGet)

	// Sales dashboard
	router.Handle(
		"/api/dashboard/sales",
		middleware.JWTValidation(
			http.HandlerFunc(salesDashboardHandler.GetSalesDashboard),
		),
	).Methods(http.MethodGet)

	return router
}
