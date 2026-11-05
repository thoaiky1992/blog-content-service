package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/domain"
	"github.com/sky/blog-service/internal/transport/http/httpx"
)

// Blog serves the blog endpoints. Handlers only translate HTTP <-> use cases;
// authorization and business rules live in the app layer.
type Blog struct {
	blogs *app.BlogService
	views *app.ViewService
}

func NewBlog(blogs *app.BlogService, views *app.ViewService) *Blog {
	return &Blog{blogs: blogs, views: views}
}

// List: GET /api/v1/blogs. Never records a view.
func (h *Blog) List(c *gin.Context) {
	page, limit, err := parsePagination(c)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	res, err := h.blogs.List(c.Request.Context(), httpx.ActorFrom(c), page, limit)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, newListResponse(res))
}

// Get: GET /api/v1/blogs/{id}. Records the unique daily view.
func (h *Blog) Get(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	ip, ok := httpx.ClientIPFrom(c)
	if !ok {
		httpx.WriteError(c, errors.New("client address could not be determined"))
		return
	}
	b, err := h.views.Read(c.Request.Context(), httpx.ActorFrom(c), id, ip)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store") // every read must reach the view counter
	c.JSON(http.StatusOK, newBlogResponse(b))
}

// Create: POST /api/v1/blogs.
func (h *Blog) Create(c *gin.Context) {
	actor := httpx.ActorFrom(c)
	// Reject unauthorized callers before parsing a (possibly large) body.
	if err := actor.Require(domain.PermBlogCreate); err != nil {
		httpx.WriteError(c, err)
		return
	}
	var req createRequest
	if err := decodeJSON(c, &req); err != nil {
		httpx.WriteError(c, err)
		return
	}
	b, err := h.blogs.Create(c.Request.Context(), actor, req.input())
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.Header("Location", "/api/v1/blogs/"+b.ID.String())
	c.JSON(http.StatusCreated, newBlogResponse(b))
}

// Update: PUT /api/v1/blogs/{id}.
func (h *Blog) Update(c *gin.Context) {
	actor := httpx.ActorFrom(c)
	if err := actor.Require(domain.PermBlogUpdate); err != nil {
		httpx.WriteError(c, err)
		return
	}
	id, err := parseID(c)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	var req updateRequest
	if err := decodeJSON(c, &req); err != nil {
		httpx.WriteError(c, err)
		return
	}
	if req.Version != nil && *req.Version < 1 {
		httpx.WriteError(c, httpx.Validation("version", "must be >= 1"))
		return
	}
	b, err := h.blogs.Update(c.Request.Context(), actor, id, req.input(), req.Version)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, newBlogResponse(b))
}

// Delete: DELETE /api/v1/blogs/{id} (soft delete).
func (h *Blog) Delete(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	if err := h.blogs.Delete(c.Request.Context(), httpx.ActorFrom(c), id); err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
