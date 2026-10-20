package observability

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const unmatchedRoute = "unmatched"

// routePattern returns the chi route pattern of a finished request. It must be
// called after the router ran, from a middleware registered with Router.Use.
func routePattern(r *http.Request) string {
	if ri := infoFrom(r.Context()); ri != nil && ri.unmatched {
		return unmatchedRoute
	}
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if p := rctx.RoutePattern(); p != "" {
			return p
		}
	}
	return unmatchedRoute
}

func isProbe(path string) bool { return path == "/healthz" || path == "/readyz" }

// RequestLogger writes one JSON log line per request. clientIP resolves the
// client address (honouring TRUSTED_PROXY_HOPS). Cookies, headers and bodies
// are never logged. Health probes are logged at debug level.
func RequestLogger(logger *slog.Logger, clientIP func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := wrap(w)
			next.ServeHTTP(sw, r)

			level := slog.LevelInfo
			if isProbe(r.URL.Path) {
				level = slog.LevelDebug
			}
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("route", routePattern(r)),
				slog.Int("status", sw.status),
				slog.Int64("bytes", sw.bytes),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
				slog.String("request_id", RequestID(r.Context())),
				slog.String("client_ip", clientIP(r)),
			}
			if ri := infoFrom(r.Context()); ri != nil && ri.userID != "" {
				attrs = append(attrs, slog.String("user_id", ri.userID))
			}
			logger.LogAttrs(r.Context(), level, "request", attrs...)
		})
	}
}

// Recoverer turns a panic into a logged stack trace and a JSON 500. The
// ErrAbortHandler sentinel is re-panicked as net/http expects.
func Recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				logger.ErrorContext(r.Context(), "panic recovered",
					"panic", sanitize(rec),
					"request_id", RequestID(r.Context()),
					"stack", string(debug.Stack()))
				if sw, ok := w.(*statusWriter); !ok || !sw.wrote {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"internal_error","message":"internal server error"}` + "\n"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func sanitize(v any) string {
	s, ok := v.(string)
	if !ok {
		if e, isErr := v.(error); isErr {
			s = e.Error()
		} else {
			return "non-string panic value"
		}
	}
	return strings.ReplaceAll(s, "\n", " ")
}
