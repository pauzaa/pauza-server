package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/IsorilovA/pauza-server/internal/apperror"
)

// Recoverer returns a chi-compatible middleware that catches panics from
// downstream handlers, logs the recovered value with structured fields via
// the provided slog.Logger, and responds with the standard JSON internal-error
// envelope when the response has not yet been started. If headers have already
// been written, the middleware only logs the panic and does not attempt to
// write a response.
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
			rw := &recoverWriter{ResponseWriter: w}

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

					if !rw.written {
						apperror.InternalError(w)
					}
				}
			}()

			next.ServeHTTP(rw, r)
		})
	}
}

// recoverWriter wraps an http.ResponseWriter and tracks whether headers have
// been sent. The Recoverer middleware uses this to decide whether it can
// safely write a JSON error response after catching a panic.
type recoverWriter struct {
	http.ResponseWriter
	written bool
}

func (rw *recoverWriter) WriteHeader(code int) {
	rw.written = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *recoverWriter) Write(b []byte) (int, error) {
	rw.written = true
	return rw.ResponseWriter.Write(b)
}

// Unwrap returns the underlying ResponseWriter so that middleware higher in
// the stack (e.g. chi's WrapResponseWriter) can discover optional interfaces
// like http.Flusher and http.Hijacker.
func (rw *recoverWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
}
