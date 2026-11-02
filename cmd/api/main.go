// Command api serves the CRM HTTP API.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/deepashearaion/manage-crm/internal/app"
	"github.com/deepashearaion/manage-crm/internal/platform/config"
	"github.com/deepashearaion/manage-crm/internal/platform/database"
	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/logging"
	"github.com/deepashearaion/manage-crm/internal/platform/mail"
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

	openCtx, cancelOpen := context.WithTimeout(ctx, 15*time.Second)
	db, err := database.Open(openCtx, cfg.DatabaseURL)
	cancelOpen()
	if err != nil {
		return err
	}
	defer db.Close()

	var sender mail.Mailer = mail.Log{Logger: logger} // development only; config requires SMTP elsewhere
	if cfg.SMTP.Enabled() {
		sender = mail.NewSMTP(mail.SMTPConfig{
			Host: cfg.SMTP.Host,
			Port: cfg.SMTP.Port,
			User: cfg.SMTP.User,
			Pass: cfg.SMTP.Pass,
			From: cfg.SMTP.From,
		})
	}
	mailer := mail.NewAsync(sender, logger, 30*time.Second)
	defer mailer.Wait() // let queued emails finish after the server stops

	handler := app.NewHandler(app.Deps{Config: cfg, DB: db, Logger: logger, Mailer: mailer})

	logger.Info("starting", slog.String("env", cfg.Env), slog.Bool("smtp", cfg.SMTP.Enabled()))
	if cfg.ReturnResetTokenInResponse {
		logger.Warn("RETURN_RESET_TOKEN_IN_RESPONSE is on: forgot-password responses include the reset token")
	}
	return httpx.Serve(ctx, ":"+cfg.Port, handler, logger, 15*time.Second)
}
