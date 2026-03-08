package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	chimw "github.com/go-chi/chi/v5/middleware"
)

// Recoverer returns a chi-compatible middleware that catches panics from
// downstream handlers, logs the recovered value with structured fields via
// the provided slog.Logger, and responds with a bare 500 status (no body).
//
// The logged entry includes:
//   - "panic":      the recovered value (formatted with %+v)
//   - "stack":      the goroutine stack trace at the point of the panic
//   - "request_id": the chi-assigned request ID, if present
//   - "method":     the HTTP method
//   - "path":       the URL path
//
// http.ErrAbortHandler is re-panicked so the net/http server can tear down
// the connection without writing a response, matching the stdlib convention.
func Recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// http.ErrAbortHandler is a sentinel used by the
					// stdlib to silently abort the connection. Re-panic
					// so net/http handles it properly.
					if rec == http.ErrAbortHandler {
						panic(rec)
					}

					stack := debug.Stack()

					logger.ErrorContext(r.Context(), "panic recovered",
						slog.Any("panic", rec),
						slog.String("stack", string(stack)),
						slog.String("request_id", chimw.GetReqID(r.Context())),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
					)

					w.WriteHeader(http.StatusInternalServerError)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
