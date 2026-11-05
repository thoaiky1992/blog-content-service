// Package redis implements the optional cache ports with Redis.
package redis

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// NewClient creates a Redis client and verifies the configured endpoint.
func NewClient(ctx context.Context, addr, password string, db int) (*goredis.Client, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr:          addr,
		Password:      password,
		DB:            db,
		MaxRetries:    -1,
		DialerRetries: 1,
		DialTimeout:   250 * time.Millisecond,
		ReadTimeout:   250 * time.Millisecond,
		WriteTimeout:  250 * time.Millisecond,
		PoolTimeout:   300 * time.Millisecond,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return client, nil
}
