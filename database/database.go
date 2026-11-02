package database

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// DSN loads .env and builds the database URL from DB_HOST, DB_PORT,
// DB_USER, DB_PASSWORD and DB_NAME.
func DSN() (string, error) {

	err := godotenv.Load()
	if err != nil {
		return "", err
	}

	dsn := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(os.Getenv("DB_USER"), os.Getenv("DB_PASSWORD")),
		Host:   fmt.Sprintf("%s:%s", os.Getenv("DB_HOST"), os.Getenv("DB_PORT")),
		Path:   "/" + os.Getenv("DB_NAME"),
	}

	return dsn.String(), nil
}

// Connect opens a connection pool; unlike a single pgx.Conn it is safe for
// the concurrent use that serving HTTP requests requires.
func Connect() (*pgxpool.Pool, error) {

	dsn, err := DSN()
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return nil, err
	}

	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		return nil, err
	}

	return pool, nil
}
