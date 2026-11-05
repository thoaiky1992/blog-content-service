package redis

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/domain"
)

const viewKeyPrefix = "blog-service:view-recorded:"

// The Lua script groups the check into one server-side operation. It observes only
// committed markers and never claims a view before PostgreSQL does.
var wasRecordedScript = goredis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 1 then
  return 1
end
return 0
`)

// ViewCache stores markers derived from committed PostgreSQL deduplication rows.
type ViewCache struct {
	client *goredis.Client
	ttl    time.Duration
}

func NewViewCache(client *goredis.Client, ttl time.Duration) *ViewCache {
	return &ViewCache{client: client, ttl: ttl}
}

var _ app.ViewCache = (*ViewCache)(nil)

func (c *ViewCache) WasRecorded(ctx context.Context, key domain.ViewKey) (bool, error) {
	result, err := wasRecordedScript.Run(ctx, c.client, []string{viewKey(key)}).Int64()
	if err != nil {
		return false, fmt.Errorf("evaluate view cache script: %w", err)
	}
	return result == 1, nil
}

func (c *ViewCache) MarkRecorded(ctx context.Context, key domain.ViewKey) error {
	if err := c.client.Set(ctx, viewKey(key), "1", c.ttl).Err(); err != nil {
		return fmt.Errorf("mark view cache: %w", err)
	}
	return nil
}

func viewKey(key domain.ViewKey) string {
	// Do not expose the client IP in Redis keys.
	ipHash := sha256.Sum256([]byte(key.IP.String()))
	return fmt.Sprintf("%s%s:%s:%x", viewKeyPrefix, key.BlogID, key.Date.UTC().Format("20060102"), ipHash[:16])
}
