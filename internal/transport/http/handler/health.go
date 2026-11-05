// Package handler contains HTTP handlers.
package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Pinger reports whether a dependency is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Health serves liveness and readiness probes.
type Health struct {
	db Pinger
}

func NewHealth(db Pinger) *Health { return &Health{db: db} }

// Healthz is the liveness probe and reports that the process is running.
func (h *Health) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Readyz is the readiness probe and requires PostgreSQL to be reachable.
func (h *Health) Readyz(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	if err := h.db.Ping(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
