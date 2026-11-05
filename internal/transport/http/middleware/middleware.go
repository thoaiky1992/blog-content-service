// Package middleware holds Gin middleware: recovery, request IDs, access logging, CORS,
// client IP resolution, and mock authentication.
package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/sky/blog-service/internal/transport/http/httpx"
)

const requestIDHeader = "X-Request-ID"

// CORS allows API calls from any origin. Preflight requests stop here so they do not run
// authentication or application handlers.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		headers := c.Writer.Header()
		headers.Set("Access-Control-Allow-Origin", "*")
		headers.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		headers.Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-Request-ID, X-User-ID")
		headers.Set("Access-Control-Expose-Headers", "Content-Disposition, Location, X-Request-ID")
		headers.Set("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// Recover turns panics into a 500 envelope and logs the stack. A deliberate
// http.ErrAbortHandler, used to abort a partially written CSV stream, is re-raised so
// net/http closes the connection instead of sending a body that appears complete.
func Recover(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if err, ok := r.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(r)
			}
			log.Error("panic recovered",
				"request_id", httpx.RequestID(c),
				"panic", r,
				"stack", string(debug.Stack()))
			if c.Writer.Written() {
				c.Abort()
				return
			}
			httpx.WriteError(c, errors.New("panic"))
		}()
		c.Next()
	}
}

// RequestID assigns an ID to every request and reuses a valid incoming ID.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestIDHeader)
		if !validRequestID(id) {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		httpx.SetRequestID(c, id)
		c.Header(requestIDHeader, id)
		c.Next()
	}
}

// validRequestID accepts only short printable tokens so a client cannot inject newlines
// or huge values into logs.
func validRequestID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		ok := r == '-' || r == '_' || r == '.' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !ok {
			return false
		}
	}
	return true
}

// AccessLog writes one structured log entry per request and never logs raw IPs.
func AccessLog(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		size := c.Writer.Size()
		if size < 0 { // Gin reports -1 when nothing was written, for example a 204 response.
			size = 0
		}
		attrs := []any{
			"request_id", httpx.RequestID(c),
			"method", c.Request.Method,
			"route", route,
			"status", c.Writer.Status(),
			"latency_ms", float64(time.Since(start).Microseconds()) / 1000,
			"bytes", size,
		}
		if a := httpx.ActorFrom(c); !a.IsAnonymous() {
			attrs = append(attrs, "user_id", a.ID.String())
		}
		if code := httpx.ErrorCode(c); code != "" {
			attrs = append(attrs, "error_code", code)
		}
		log.Info("HTTP request", attrs...)
	}
}
