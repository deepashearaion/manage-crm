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
	// Protected Profile API
	// =========================

	http.HandleFunc(
		"/api/profile",
		middleware.JWTValidation(authHandler.Profile),
	)

	// =========================
	// Protected Contact APIs
	// =========================

	// Create a contact and get all contacts
	http.HandleFunc(
		"/api/contacts",
		middleware.JWTValidation(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

				switch r.Method {

				case http.MethodPost:
					contactHandler.CreateContact(w, r)

				case http.MethodGet:
					contactHandler.GetContacts(w, r)

				default:
					w.Header().Set(
						"Allow",
						"GET, POST",
					)

					http.Error(
						w,
						"Method not allowed",
						http.StatusMethodNotAllowed,
					)
				}
			}),
		),
	)

	// Get a contact by ID
	http.HandleFunc(
		"/api/contacts/",
		middleware.JWTValidation(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

				if r.Method != http.MethodGet {
					w.Header().Set(
						"Allow",
						"GET",
					)

					http.Error(
						w,
						"Method not allowed",
						http.StatusMethodNotAllowed,
					)
					return
				}

				contactHandler.GetContactByID(w, r)
			}),
		),
	)
}
