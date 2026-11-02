package main

import (
	"fmt"
	"log"
	"net/http"

	"authentication-backend/database"
	"authentication-backend/handlers"
	"authentication-backend/middleware"
	"authentication-backend/routes"
)

func main() {

	conn, err := database.Connect()

	if err != nil {
		log.Fatal("Database connection failed:", err)
	}

	defer conn.Close()

	authHandler := &handlers.AuthHandler{
		DB: conn,
	}

	contactHandler := &handlers.ContactHandler{
		DB: conn,
	}

	salesDashboardHandler := &handlers.SalesDashboardHandler{
		DB: conn,
	}

	router := routes.SetupRoutes(
		authHandler,
		contactHandler,
		salesDashboardHandler,
	)

	fmt.Println("Server running on http://localhost:8080")

	err = http.ListenAndServe(":8080", middleware.CORS(router)) //nolint:gosec // G114: replaced by a configured http.Server in the platform step

	if err != nil {
		log.Fatal("Server failed:", err)
	}
}
