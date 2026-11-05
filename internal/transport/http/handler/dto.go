package handler

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/domain"
	"github.com/sky/blog-service/internal/transport/http/httpx"
)

// MaxBodyBytes is the 1 MB request body limit.
const MaxBodyBytes = 1 << 20

// createRequest is the POST body. Unknown fields are rejected.
type createRequest struct {
	Title       string `json:"title"`
	Author      string `json:"author"`
	Content     string `json:"content"`
	Description string `json:"description"`
}

// updateRequest is the PUT body and replaces every editable field. Version is an
// optional optimistic lock.
type updateRequest struct {
	Title       string `json:"title"`
	Author      string `json:"author"`
	Content     string `json:"content"`
	Description string `json:"description"`
	Version     *int   `json:"version"`
}

func (r createRequest) input() domain.BlogInput {
	return domain.BlogInput{Title: r.Title, Author: r.Author, Content: r.Content, Description: r.Description}
}

func (r updateRequest) input() domain.BlogInput {
	return domain.BlogInput{Title: r.Title, Author: r.Author, Content: r.Content, Description: r.Description}
}

type blogResponse struct {
	ID          uuid.UUID `json:"id"`
	Title       string    `json:"title"`
	Author      string    `json:"author"`
	Content     string    `json:"content"`
	Description string    `json:"description"`
	Views       int64     `json:"views"`
	Version     int       `json:"version"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
	CreatedBy   uuid.UUID `json:"created_by"`
	UpdatedBy   uuid.UUID `json:"updated_by"`
}

func newBlogResponse(b domain.Blog) blogResponse {
	return blogResponse{
		ID: b.ID, Title: b.Title, Author: b.Author, Content: b.Content,
		Description: b.Description, Views: b.Views, Version: b.Version,
		CreatedAt: formatTime(b.CreatedAt), UpdatedAt: formatTime(b.UpdatedAt),
		CreatedBy: b.CreatedBy, UpdatedBy: b.UpdatedBy,
	}
}

type summaryResponse struct {
	ID          uuid.UUID `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
}

type listResponse struct {
	Items   []summaryResponse `json:"items"`
	Page    int               `json:"page"`
	Limit   int               `json:"limit"`
	HasNext bool              `json:"has_next"`
}

func newListResponse(p domain.BlogPage) listResponse {
	items := make([]summaryResponse, len(p.Items))
	for i, s := range p.Items {
		items[i] = summaryResponse{ID: s.ID, Title: s.Title, Description: s.Description}
	}
	return listResponse{Items: items, Page: p.Page, Limit: p.Limit, HasNext: p.HasNext}
}

// formatTime renders RFC3339 in UTC, including fractional seconds only when non-zero.
func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// parseID accepts only the canonical 36-character UUID form.
func parseID(c *gin.Context) (uuid.UUID, error) {
	raw := c.Param("id")
	id, err := uuid.Parse(raw)
	if err != nil || len(raw) != 36 {
		return uuid.Nil, httpx.Validation("id", "must be a valid UUID")
	}
	return id, nil
}

// parsePagination reads page and limit and applies defaults for absent parameters.
// The domain checks page >= 1 and 1 <= limit <= 100.
func parsePagination(c *gin.Context) (page, limit int, err error) {
	ve := &domain.ValidationError{}
	page, limit = domain.DefaultPage, domain.DefaultLimit
	if raw, ok := c.GetQuery("page"); ok {
		if page, err = strconv.Atoi(raw); err != nil {
			ve.Fields = append(ve.Fields, domain.FieldError{Field: "page", Message: "must be an integer"})
		}
	}
	if raw, ok := c.GetQuery("limit"); ok {
		if limit, err = strconv.Atoi(raw); err != nil {
			ve.Fields = append(ve.Fields, domain.FieldError{Field: "limit", Message: "must be an integer"})
		}
	}
	if len(ve.Fields) > 0 {
		return 0, 0, ve
	}
	return page, limit, nil
}

// decodeJSON enforces Content-Type, body size, unknown-field rejection, and a single
// JSON value. Returned errors are ready for httpx.WriteError.
func decodeJSON(c *gin.Context, dst any) error {
	mt, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mt != "application/json" {
		return &httpx.APIError{
			Status: http.StatusUnsupportedMediaType, Code: httpx.CodeUnsupportedMedia,
			Message: "Content-Type must be application/json",
		}
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodyBytes)
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return translateDecodeError(err)
	}
	// Reject trailing data such as `{"a":1}{"b":2}`.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return err
		}
		return invalidJSON("request body must contain a single JSON object")
	}
	return nil
}

func translateDecodeError(err error) error {
	var (
		tooBig  *http.MaxBytesError
		syntax  *json.SyntaxError
		typeErr *json.UnmarshalTypeError
	)
	switch {
	case errors.As(err, &tooBig):
		return err
	case errors.Is(err, io.EOF):
		return invalidJSON("request body is required")
	case errors.Is(err, io.ErrUnexpectedEOF), errors.As(err, &syntax):
		return invalidJSON("malformed JSON body")
	case errors.As(err, &typeErr):
		field := typeErr.Field
		if field == "" {
			return invalidJSON("request body must be a JSON object")
		}
		return httpx.Validation(field, "must be of type "+typeErr.Type.String())
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		name := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
		return httpx.Validation(name, "is not an allowed field")
	}
	return invalidJSON("malformed JSON body")
}

func invalidJSON(msg string) error {
	return &httpx.APIError{Status: http.StatusBadRequest, Code: httpx.CodeInvalidJSON, Message: msg}
}
