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

	routes.SetupRoutes(authHandler, contactHandler)

	fmt.Println("Server running on http://localhost:8080")

	err = http.ListenAndServe(":8080", nil)

	if err != nil {
		log.Fatal("Server failed:", err)
	}
}
