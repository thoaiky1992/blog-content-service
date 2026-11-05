package middleware

import (
	"errors"
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/domain"
	"github.com/sky/blog-service/internal/transport/http/httpx"
)

const userIDHeader = "X-User-ID"

// MockAuth implements the mock authentication from the design (section 11):
//
//	header missing    -> Anonymous
//	header malformed  -> 401
//	user not found    -> 401
//	user found        -> Actor{ID, Role}
//
// The role comes from PostgreSQL (directly or through its bounded Redis cache),
// never from the client.
func MockAuth(users app.UserRepository, cache app.ActorCache) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader(userIDHeader)
		if raw == "" {
			httpx.SetActor(c, domain.Anonymous())
			c.Next()
			return
		}
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			httpx.WriteError(c, domain.ErrUnauthenticated)
			return
		}
		ctx := c.Request.Context()
		if cache != nil {
			actor, found, err := cache.Get(ctx, id)
			if err != nil {
				slog.DebugContext(ctx, "actor cache lookup failed; falling back to postgres", "error", err)
			} else if found {
				httpx.SetActor(c, actor)
				c.Next()
				return
			}
		}

		actor, err := users.GetByID(ctx, id)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				err = domain.ErrUnauthenticated
			}
			httpx.WriteError(c, err)
			return
		}
		if cache != nil {
			if err := cache.Set(ctx, actor); err != nil {
				slog.DebugContext(ctx, "actor cache write failed", "error", err)
			}
		}
		httpx.SetActor(c, actor)
		c.Next()
	}
}
