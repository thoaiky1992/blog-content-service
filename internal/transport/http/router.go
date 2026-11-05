// Package http wires the Gin router.
package http

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/sky/blog-service/internal/transport/http/handler"
	"github.com/sky/blog-service/internal/transport/http/middleware"
)

// NewRouter builds the engine with the Phase 1 middleware chain and probes.
func NewRouter(log *slog.Logger, db handler.Pinger) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(middleware.Recover(log), middleware.RequestID(), middleware.AccessLog(log))

	health := handler.NewHealth(db)
	r.GET("/healthz", health.Healthz)
	r.GET("/readyz", health.Readyz)
	return r
}
