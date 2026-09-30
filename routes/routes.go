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

	// Register
	http.HandleFunc(
		"/api/auth/register",
		authHandler.Register,
	)

	// Login
	http.HandleFunc(
		"/api/auth/login",
		authHandler.Login,
	)

	// Logout
	http.HandleFunc(
		"/api/auth/logout",
		authHandler.Logout,
	)

	// Forgot Password
	http.HandleFunc(
		"/api/auth/forgot-password",
		authHandler.ForgotPassword,
	)

	// Reset Password
	http.HandleFunc(
		"/api/auth/reset-password",
		authHandler.ResetPassword,
	)

	// =========================
	// Protected Profile API
	// =========================

	http.HandleFunc(
		"/api/profile",
		middleware.JWTValidation(
			authHandler.Profile,
		),
	)

	// =========================
	// Protected Contact APIs
	// =========================

	// Create a contact and get all contacts
	http.HandleFunc(
		"/api/contacts",
		middleware.JWTValidation(
			http.HandlerFunc(func(
				w http.ResponseWriter,
				r *http.Request,
			) {

				switch r.Method {

				case http.MethodPost:
					// Create a new contact
					contactHandler.CreateContact(w, r)

				case http.MethodGet:
					// Get all contacts
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

	// =========================
	// Get, Update or Delete Contact by ID
	// =========================

	http.HandleFunc(
		"/api/contacts/",
		middleware.JWTValidation(
			http.HandlerFunc(func(
				w http.ResponseWriter,
				r *http.Request,
			) {

				switch r.Method {

				case http.MethodGet:
					// Get contact by ID
					contactHandler.GetContactByID(w, r)

				case http.MethodPatch:
					// Update contact by ID
					contactHandler.UpdateContact(w, r)

				case http.MethodDelete:
					// Delete contact by ID
					contactHandler.DeleteContact(w, r)

				default:
					w.Header().Set(
						"Allow",
						"GET, PATCH, DELETE",
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
}
