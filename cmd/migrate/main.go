// Command migrate applies the embedded SQL migrations to the database
// configured in .env (DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME).
//
//	go run ./cmd/migrate [up|down|status|version]
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"authentication-backend/database"
	"authentication-backend/migrations"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		log.Fatal("migrate: ", err)
	}
}

func run(ctx context.Context, args []string) error {
	command := "up"
	if len(args) > 0 {
		command = args[0]
	}

	dsn, err := database.DSN()
	if err != nil {
		return err
	}
	provider, db, err := migrations.Open(dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	switch command {
	case "up":
		results, err := provider.Up(ctx)
		for _, r := range results {
			fmt.Println(r)
		}
		if err != nil {
			return err
		}
		if len(results) == 0 {
			fmt.Println("no pending migrations")
		}
	case "down":
		result, err := provider.Down(ctx)
		if result != nil {
			fmt.Println(result)
		}
		return err
	case "status":
		statuses, err := provider.Status(ctx)
		if err != nil {
			return err
		}
		for _, s := range statuses {
			applied := ""
			if !s.AppliedAt.IsZero() {
				applied = s.AppliedAt.Format("2006-01-02 15:04:05")
			}
			fmt.Printf("%-8s %-40s %s\n", s.State, s.Source.Path, applied)
		}
	case "version":
		version, err := provider.GetDBVersion(ctx)
		if err != nil {
			return err
		}
		fmt.Println(version)
	default:
		return fmt.Errorf("unknown command %q (want up, down, status or version)", command)
	}
	return nil
}
