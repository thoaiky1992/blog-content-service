package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sky/blog-service/internal/domain"
)

// Error codes returned in the response envelope.
const (
	CodeValidationFailed   = "VALIDATION_FAILED"
	CodeInvalidJSON        = "INVALID_JSON"
	CodeUnauthenticated    = "UNAUTHENTICATED"
	CodeForbidden          = "FORBIDDEN"
	CodeNotFound           = "NOT_FOUND"
	CodeMethodNotAllowed   = "METHOD_NOT_ALLOWED"
	CodeVersionConflict    = "VERSION_CONFLICT"
	CodePayloadTooLarge    = "PAYLOAD_TOO_LARGE"
	CodeUnsupportedMedia   = "UNSUPPORTED_MEDIA_TYPE"
	CodeServiceUnavailable = "SERVICE_UNAVAILABLE"
	CodeInternal           = "INTERNAL_ERROR"
)

// Detail describes one invalid field.
type Detail struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type errorBody struct {
	Code      string   `json:"code"`
	Message   string   `json:"message"`
	Details   []Detail `json:"details,omitempty"`
	RequestID string   `json:"request_id"`
}

type envelope struct {
	Error errorBody `json:"error"`
}

// APIError is an error that already knows its HTTP representation.
type APIError struct {
	Status  int
	Code    string
	Message string
	Details []Detail
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// WriteError maps err to the unified envelope and aborts the request.
// Unclassified errors are logged and returned as a generic 500 so internals
// never leak to clients.
func WriteError(c *gin.Context, err error) {
	var api *APIError
	var ve *domain.ValidationError
	var tooBig *http.MaxBytesError

	switch {
	case errors.As(err, &api):
	case errors.As(err, &ve):
		details := make([]Detail, len(ve.Fields))
		for i, f := range ve.Fields {
			details[i] = Detail{Field: f.Field, Message: f.Message}
		}
		api = &APIError{http.StatusBadRequest, CodeValidationFailed, "invalid request", details}
	case errors.As(err, &tooBig):
		api = &APIError{http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body too large", nil}
	case errors.Is(err, domain.ErrUnauthenticated):
		api = &APIError{http.StatusUnauthorized, CodeUnauthenticated, "authentication required or invalid user", nil}
	case errors.Is(err, domain.ErrForbidden):
		api = &APIError{http.StatusForbidden, CodeForbidden, "you do not have permission to perform this action", nil}
	case errors.Is(err, domain.ErrNotFound):
		api = &APIError{http.StatusNotFound, CodeNotFound, "resource not found", nil}
	case errors.Is(err, domain.ErrVersionConflict):
		api = &APIError{http.StatusConflict, CodeVersionConflict, "the blog was modified by someone else", nil}
	case errors.Is(err, domain.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		slog.Error("dependency unavailable", "request_id", RequestID(c), "error", err)
		api = &APIError{http.StatusServiceUnavailable, CodeServiceUnavailable, "service temporarily unavailable", nil}
	default:
		slog.Error("unhandled error", "request_id", RequestID(c), "error", err)
		api = &APIError{http.StatusInternalServerError, CodeInternal, "internal server error", nil}
	}

	c.Set(keyErrorCode, api.Code)
	c.AbortWithStatusJSON(api.Status, envelope{errorBody{
		Code: api.Code, Message: api.Message, Details: api.Details, RequestID: RequestID(c),
	}})
}

// Validation builds a single-field 400 error.
func Validation(field, msg string) error {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Message: msg}}}
}
