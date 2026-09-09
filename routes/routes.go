package routes

import (
	"net/http"

	"authentication-backend/handlers"
)

func SetupRoutes(authHandler *handlers.AuthHandler) {

	http.HandleFunc("/api/auth/register", authHandler.Register)
	http.HandleFunc("/api/auth/login", authHandler.Login)
	http.HandleFunc("/api/auth/logout", authHandler.Logout)
	http.HandleFunc("/api/auth/forgot-password", authHandler.ForgotPassword)
	http.HandleFunc("/api/auth/reset-password", authHandler.ResetPassword)
}
