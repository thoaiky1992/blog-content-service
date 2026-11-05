// Package http wires the Gin router.
package http

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/platform/clientip"
	"github.com/sky/blog-service/internal/transport/http/handler"
	"github.com/sky/blog-service/internal/transport/http/httpx"
	"github.com/sky/blog-service/internal/transport/http/middleware"
)

// Deps are the collaborators the router needs (dependency injection).
type Deps struct {
	Log      *slog.Logger
	DB       handler.Pinger
	Users    app.UserRepository
	Blogs    *app.BlogService
	Views    *app.ViewService
	Reports  *app.ReportService
	ClientIP *clientip.Resolver
	Clock    app.Clock
}

// NewRouter builds the engine. Middleware order (design section 3.2):
// recover -> request-id -> access log -> client-ip -> mock auth. Probes sit
// outside the API group so they need neither auth nor an IP.
func NewRouter(d Deps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	_ = r.SetTrustedProxies(nil) // client IP is resolved by our own resolver
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false
	r.HandleMethodNotAllowed = true
	r.Use(middleware.Recover(d.Log), middleware.RequestID(), middleware.AccessLog(d.Log))

	r.NoRoute(func(c *gin.Context) {
		httpx.WriteError(c, &httpx.APIError{
			Status: http.StatusNotFound, Code: httpx.CodeNotFound, Message: "route not found"})
	})
	r.NoMethod(func(c *gin.Context) {
		httpx.WriteError(c, &httpx.APIError{
			Status: http.StatusMethodNotAllowed, Code: httpx.CodeMethodNotAllowed, Message: "method not allowed"})
	})

	health := handler.NewHealth(d.DB)
	r.GET("/healthz", health.Healthz)
	r.GET("/readyz", health.Readyz)

	blog := handler.NewBlog(d.Blogs, d.Views)
	report := handler.NewReport(d.Reports, d.Clock)

	api := r.Group("/api/v1", middleware.ClientIP(d.ClientIP), middleware.MockAuth(d.Users))
	api.GET("/blogs", blog.List)
	api.GET("/blogs/:id", blog.Get)
	api.POST("/blogs", blog.Create)
	api.PUT("/blogs/:id", blog.Update)
	api.DELETE("/blogs/:id", blog.Delete)

	api.GET("/reports/blogs", report.Export)
	return r
}
