// Package logging builds the application's structured logger (log/slog).
package logging

import (
	"io"
	"log/slog"
)

// New returns a logger writing text or JSON lines at the given level
// ("debug", "info", "warn" or "error"; anything else means info).
func New(w io.Writer, format, level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if format == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
