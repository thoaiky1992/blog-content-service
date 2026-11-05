package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/sky/blog-service/internal/platform/clientip"
	"github.com/sky/blog-service/internal/transport/http/httpx"
)

// ClientIP resolves the client IP once per request using the trusted-proxy
// rules and stores it in the context.
func ClientIP(r *clientip.Resolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		if ip, ok := r.Resolve(c.Request.RemoteAddr, c.Request.Header.Values("X-Forwarded-For")); ok {
			httpx.SetClientIP(c, ip)
		}
		c.Next()
	}
}
