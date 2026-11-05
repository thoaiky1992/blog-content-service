// Package config loads service configuration from environment variables.
package config

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings. Defaults suit local development.
type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	LogLevel        string
	ShutdownTimeout time.Duration
	TrustedProxies  []netip.Prefix
	DBMaxConns      int32
	RedisAddr       string
	RedisPassword   string
	RedisDB         int
	RedisActorTTL   time.Duration
	RedisViewTTL    time.Duration
}

// Load reads configuration from the environment and validates it.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:      getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
		LogLevel:      getenv("LOG_LEVEL", "info"),
		RedisAddr:     os.Getenv("REDIS_ADDR"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
	}

	var err error
	if cfg.ShutdownTimeout, err = time.ParseDuration(getenv("SHUTDOWN_TIMEOUT", "15s")); err != nil {
		return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT: %w", err)
	}

	maxConns, err := strconv.ParseInt(getenv("DB_MAX_CONNS", "20"), 10, 32)
	if err != nil || maxConns < 1 {
		return Config{}, fmt.Errorf("DB_MAX_CONNS must be a positive integer")
	}
	cfg.DBMaxConns = int32(maxConns)

	redisDB, err := strconv.Atoi(getenv("REDIS_DB", "0"))
	if err != nil || redisDB < 0 {
		return Config{}, fmt.Errorf("REDIS_DB must be a non-negative integer")
	}
	cfg.RedisDB = redisDB

	if cfg.RedisActorTTL, err = positiveDuration("REDIS_ACTOR_TTL", "5m"); err != nil {
		return Config{}, err
	}
	if cfg.RedisViewTTL, err = positiveDuration("REDIS_VIEW_TTL", "48h"); err != nil {
		return Config{}, err
	}

	if cfg.TrustedProxies, err = parseCIDRs(os.Getenv("TRUSTED_PROXIES")); err != nil {
		return Config{}, fmt.Errorf("TRUSTED_PROXIES: %w", err)
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	return cfg, nil
}

func positiveDuration(name, def string) (time.Duration, error) {
	d, err := time.ParseDuration(getenv(name, def))
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return d, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseCIDRs(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", part, err)
		}
		out = append(out, p)
	}
	return out, nil
}
