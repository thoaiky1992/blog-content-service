package middleware

import (
	"errors"

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
// The role is always read from the database, never from the client.
func MockAuth(users app.UserRepository) gin.HandlerFunc {
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
		actor, err := users.GetByID(c.Request.Context(), id)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				err = domain.ErrUnauthenticated
			}
			httpx.WriteError(c, err)
			return
		}
		httpx.SetActor(c, actor)
		c.Next()
	}
}
