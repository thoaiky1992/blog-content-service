// Package httpx holds HTTP helpers shared by middleware and handlers: context
// accessors and the unified error response.
package httpx

import (
	"net/netip"

	"github.com/gin-gonic/gin"

	"github.com/sky/blog-service/internal/domain"
)

const (
	keyRequestID = "request_id"
	keyActor     = "actor"
	keyClientIP  = "client_ip"
	keyErrorCode = "error_code"
)

func SetRequestID(c *gin.Context, id string) { c.Set(keyRequestID, id) }
func RequestID(c *gin.Context) string        { return c.GetString(keyRequestID) }

func SetActor(c *gin.Context, a domain.Actor) { c.Set(keyActor, a) }

// ActorFrom returns the authenticated actor, or Anonymous if none was set.
func ActorFrom(c *gin.Context) domain.Actor {
	if v, ok := c.Get(keyActor); ok {
		if a, ok := v.(domain.Actor); ok {
			return a
		}
	}
	return domain.Anonymous()
}

func SetClientIP(c *gin.Context, ip netip.Addr) { c.Set(keyClientIP, ip) }

// ClientIPFrom returns the resolved client IP. ok is false if unresolved.
func ClientIPFrom(c *gin.Context) (netip.Addr, bool) {
	if v, ok := c.Get(keyClientIP); ok {
		if ip, ok := v.(netip.Addr); ok && ip.IsValid() {
			return ip, true
		}
	}
	return netip.Addr{}, false
}

// ErrorCode returns the error code written for this request ("" if none).
func ErrorCode(c *gin.Context) string { return c.GetString(keyErrorCode) }
