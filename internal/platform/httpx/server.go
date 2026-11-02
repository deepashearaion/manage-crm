package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Serve runs handler on addr until ctx is cancelled, then shuts down
// gracefully, giving in-flight requests up to shutdownTimeout to finish.
func Serve(ctx context.Context, addr string, handler http.Handler, logger *slog.Logger, shutdownTimeout time.Duration) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	errc := make(chan error, 1)
	go func() {
		logger.Info("server listening", slog.String("addr", addr))
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
