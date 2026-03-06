package apperror

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Error codes used across the API. Each code maps to a fixed HTTP status via
// StatusCode. See BACKEND_SPEC.md Section 11 for the canonical list.
const (
	CodeValidationError      = "VALIDATION_ERROR"
	CodeUnauthorized         = "UNAUTHORIZED"
	CodeForbidden            = "FORBIDDEN"
	CodeNotFound             = "NOT_FOUND"
	CodeConflict             = "CONFLICT"
	CodeRateLimited          = "RATE_LIMITED"
	CodeSubscriptionRequired = "SUBSCRIPTION_REQUIRED"
	CodeInternalError        = "INTERNAL_ERROR"
)

// internalErrorMessage is the fixed safe message returned for internal errors
// so that implementation details are never leaked to clients.
const internalErrorMessage = "an unexpected error occurred"

// ErrorBody is the inner object nested under the top-level "error" key.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// ErrorResponse is the top-level JSON envelope returned for all error
// responses: {"error":{"code","message","details"}}.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// FieldErrors is a map of field names to human-readable validation messages.
// It is the expected type for the "fields" key inside validation error details.
type FieldErrors map[string]string

// ValidationDetails wraps FieldErrors so the JSON output matches the spec:
// {"fields":{"email":"must be a valid email address"}}.
// Use NewValidationDetails to construct a value suitable for passing to WriteError.
type ValidationDetails struct {
	Fields FieldErrors `json:"fields"`
}

// NewValidationDetails constructs a ValidationDetails value suitable for
// passing as the details argument to WriteError.
func NewValidationDetails(fields FieldErrors) ValidationDetails {
	return ValidationDetails{Fields: fields}
}

// StatusCode returns the HTTP status code associated with the given error code.
// Unknown codes default to 500.
func StatusCode(code string) int {
	switch code {
	case CodeValidationError:
		return http.StatusUnprocessableEntity
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
		return http.StatusConflict
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeSubscriptionRequired:
		return http.StatusForbidden
	case CodeInternalError:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// WriteError writes a JSON error response using the standard envelope.
// Content-Type is set to application/json. If JSON encoding fails after
// headers are written, the error is logged with slog using key "err".
func WriteError(w http.ResponseWriter, code string, message string, details any) {
	resp := ErrorResponse{
		Error: ErrorBody{
			Code:    code,
			Message: message,
			Details: details,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(StatusCode(code))
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("failed to encode error response", "err", err)
	}
}

// ValidationError writes a 422 response with the given message and optional
// details (e.g. FieldErrors wrapped in ValidationDetails).
func ValidationError(w http.ResponseWriter, message string, details any) {
	WriteError(w, CodeValidationError, message, details)
}

// ValidationFieldErrors writes a 422 response whose details contain per-field
// validation messages in the shape expected by the spec:
// {"error":{"code":"VALIDATION_ERROR","message":"...","details":{"fields":{...}}}}.
func ValidationFieldErrors(w http.ResponseWriter, message string, fields FieldErrors) {
	WriteError(w, CodeValidationError, message, ValidationDetails{Fields: fields})
}

// Unauthorized writes a 401 response with the given message.
func Unauthorized(w http.ResponseWriter, message string) {
	WriteError(w, CodeUnauthorized, message, nil)
}

// Forbidden writes a 403 response with the given message.
func Forbidden(w http.ResponseWriter, message string) {
	WriteError(w, CodeForbidden, message, nil)
}

// NotFound writes a 404 response with the given message.
func NotFound(w http.ResponseWriter, message string) {
	WriteError(w, CodeNotFound, message, nil)
}

// Conflict writes a 409 response with the given message.
func Conflict(w http.ResponseWriter, message string) {
	WriteError(w, CodeConflict, message, nil)
}

// RateLimited writes a 429 response with the given message.
func RateLimited(w http.ResponseWriter, message string) {
	WriteError(w, CodeRateLimited, message, nil)
}

// SubscriptionRequired writes a 403 response indicating the caller needs an
// active premium subscription.
func SubscriptionRequired(w http.ResponseWriter, message string) {
	WriteError(w, CodeSubscriptionRequired, message, nil)
}

// InternalError writes a 500 response with a fixed safe message. Internal
// details are never exposed to the client.
func InternalError(w http.ResponseWriter) {
	WriteError(w, CodeInternalError, internalErrorMessage, nil)
}
