package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sky/blog-service/internal/domain"
)

// mapErr classifies infrastructure errors. Connectivity and availability failures become
// domain.ErrUnavailable (HTTP 503); all other errors are wrapped and remain unclassified
// (HTTP 500). Context cancellation passes through unchanged.
func mapErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", op, err)
	}
	if isUnavailable(err) {
		return fmt.Errorf("%s: %w: %v", op, domain.ErrUnavailable, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}

func isUnavailable(err error) bool {
	var connErr *pgconn.ConnectError
	if errors.As(err, &connErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		// 08: connection exception; 53: insufficient resources;
		// 57P01-03: admin shutdown, crash shutdown, or cannot connect now.
		return strings.HasPrefix(pgErr.Code, "08") ||
			strings.HasPrefix(pgErr.Code, "53") ||
			pgErr.Code == "57P01" || pgErr.Code == "57P02" || pgErr.Code == "57P03"
	}
	// pgxpool returns "closed pool" and similar plain errors.
	msg := err.Error()
	return strings.Contains(msg, "closed pool") || strings.Contains(msg, "conn closed")
}
