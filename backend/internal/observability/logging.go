// Package observability provides structured logging, request IDs, request
// logging, panic recovery and Prometheus metrics.
package observability

import (
	"io"
	"log/slog"
	"strings"
)

// NewLogger returns a JSON logger writing to w at the given level
// (debug|info|warn|error; anything else means info).
func NewLogger(w io.Writer, level string) *slog.Logger {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: l}))
}
