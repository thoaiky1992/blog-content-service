package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sky/blog-service/internal/adapter/postgres"
	redisadapter "github.com/sky/blog-service/internal/adapter/redis"
	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/config"
	"github.com/sky/blog-service/internal/platform/clientip"
	"github.com/sky/blog-service/internal/platform/clock"
	transport "github.com/sky/blog-service/internal/transport/http"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		level = slog.LevelInfo
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	clk := clock.System{}
	blogRepo := postgres.NewBlogRepo(pool)
	var actorCache app.ActorCache
	var viewCache app.ViewCache
	if cfg.RedisAddr != "" {
		redisClient, err := redisadapter.NewClient(ctx, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
		if err != nil {
			return err
		}
		defer redisClient.Close()
		actorCache = redisadapter.NewActorCache(redisClient, cfg.RedisActorTTL)
		viewCache = redisadapter.NewViewCache(redisClient, cfg.RedisViewTTL)
	}
	router := transport.NewRouter(transport.Deps{
		Log:      log,
		DB:       pool,
		Users:    postgres.NewUserRepo(pool),
		Actors:   actorCache,
		Blogs:    app.NewBlogService(blogRepo, clk),
		Views:    app.NewViewService(blogRepo, postgres.NewViewStore(pool), viewCache, clk),
		Reports:  app.NewReportService(blogRepo),
		ClientIP: clientip.New(cfg.TrustedProxies),
		Clock:    clk,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	log.Info("shutdown complete")
	return nil
}
