package routes

import (
	"net/http"

	"authentication-backend/handlers"
	"authentication-backend/middleware"
)

func SetupRoutes(
	authHandler *handlers.AuthHandler,
	contactHandler *handlers.ContactHandler,
) {

	// =========================
	// Public Authentication APIs
	// =========================

	http.HandleFunc(
		"/api/auth/register",
		authHandler.Register,
	)

	http.HandleFunc(
		"/api/auth/login",
		authHandler.Login,
	)

	http.HandleFunc(
		"/api/auth/logout",
		authHandler.Logout,
	)

	http.HandleFunc(
		"/api/auth/forgot-password",
		authHandler.ForgotPassword,
	)

	http.HandleFunc(
		"/api/auth/reset-password",
		authHandler.ResetPassword,
	)

	// =========================
	// Protected APIs
	// =========================

	http.HandleFunc(
		"/api/profile",
		middleware.JWTValidation(authHandler.Profile),
	)

	// =========================
	// Contact APIs
	// =========================

	http.HandleFunc(
		"/api/contacts",
		middleware.JWTValidation(contactHandler.CreateContact),
	)
}
