package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/domain"
)

const actorKeyPrefix = "blog-service:actor:"

// ActorCache stores the role returned by PostgreSQL for a bounded period.
type ActorCache struct {
	client *goredis.Client
	ttl    time.Duration
}

func NewActorCache(client *goredis.Client, ttl time.Duration) *ActorCache {
	return &ActorCache{client: client, ttl: ttl}
}

var _ app.ActorCache = (*ActorCache)(nil)

func (c *ActorCache) Get(ctx context.Context, id uuid.UUID) (domain.Actor, bool, error) {
	role, err := c.client.Get(ctx, actorKey(id)).Result()
	if err == goredis.Nil {
		return domain.Actor{}, false, nil
	}
	if err != nil {
		return domain.Actor{}, false, fmt.Errorf("get actor cache: %w", err)
	}
	if !validRole(domain.Role(role)) {
		return domain.Actor{}, false, fmt.Errorf("get actor cache: invalid role %q", role)
	}
	return domain.Actor{ID: id, Role: domain.Role(role)}, true, nil
}

func (c *ActorCache) Set(ctx context.Context, actor domain.Actor) error {
	if actor.IsAnonymous() || !validRole(actor.Role) {
		return fmt.Errorf("set actor cache: invalid actor")
	}
	if err := c.client.Set(ctx, actorKey(actor.ID), string(actor.Role), c.ttl).Err(); err != nil {
		return fmt.Errorf("set actor cache: %w", err)
	}
	return nil
}

func actorKey(id uuid.UUID) string { return actorKeyPrefix + id.String() }

func validRole(role domain.Role) bool {
	switch role {
	case domain.RoleAdmin, domain.RoleEditor, domain.RoleReader, domain.RoleReporter:
		return true
	default:
		return false
	}
}
