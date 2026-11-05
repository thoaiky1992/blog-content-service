// Package domain holds business types and rules. It must not import Gin, pgx, Redis,
// or HTTP DTOs.
package domain

import (
	"errors"
	"strings"
)

// Sentinel errors are mapped to HTTP statuses by the transport layer.
var (
	ErrNotFound        = errors.New("not found")
	ErrForbidden       = errors.New("forbidden")
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrVersionConflict = errors.New("version conflict")
	// ErrUnavailable indicates that the source of truth (PostgreSQL) is unreachable.
	ErrUnavailable = errors.New("storage unavailable")
)

// FieldError describes one invalid field.
type FieldError struct {
	Field   string
	Message string
}

// ValidationError aggregates field errors from value-object validation and pagination
// checks.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, len(e.Fields))
	for i, f := range e.Fields {
		parts[i] = f.Field + " " + f.Message
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

func (e *ValidationError) add(field, msg string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Message: msg})
}

func (e *ValidationError) orNil() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}
