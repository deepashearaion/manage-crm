package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"authentication-backend/internal/app"
	"authentication-backend/internal/platform/config"
	"authentication-backend/internal/platform/database"
	"authentication-backend/internal/platform/httpx"
	"authentication-backend/internal/platform/logging"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := logging.New(os.Stdout, cfg.LogFormat, cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	handler := app.NewHandler(app.Deps{Config: cfg, DB: db, Logger: logger})

	logger.Info("starting", slog.String("env", cfg.Env))
	return httpx.Serve(ctx, ":"+cfg.Port, handler, logger, 15*time.Second)
}
