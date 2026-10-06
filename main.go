package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"authentication-backend/database"
	"authentication-backend/handlers"
	"authentication-backend/routes"
)

func main() {

	conn, err := database.Connect()

	if err != nil {
		log.Fatal("Database connection failed:", err)
	}

	defer conn.Close(context.Background())

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

	err = http.ListenAndServe(":8080", router)

	if err != nil {
		log.Fatal("Server failed:", err)
	}
}
