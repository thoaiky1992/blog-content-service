package unit

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sky/blog-service/internal/domain"
)

func TestCreateBlogSuccess(t *testing.T) {
	a := newUnitAPI(t)
	w := a.do(http.MethodPost, "/api/v1/blogs", blogInput("Created"), a.editor)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Title != "Created" || body.Version != 1 || w.Header().Get("Location") != "/api/v1/blogs/"+body.ID {
		t.Fatalf("body=%+v location=%q", body, w.Header().Get("Location"))
	}
}

func TestCreateBlogInputValidation(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{"missing title", map[string]any{"author": "A", "content": "C"}},
		{"missing author", map[string]any{"title": "T", "content": "C"}},
		{"missing content", map[string]any{"title": "T", "author": "A"}},
		{"unknown field", map[string]any{"title": "T", "author": "A", "content": "C", "unknown": true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newUnitAPI(t)
			assertAPIError(t, a.do(http.MethodPost, "/api/v1/blogs", tc.body, a.editor),
				http.StatusBadRequest, "VALIDATION_FAILED")
		})
	}
}

func TestCreateBlogPermissions(t *testing.T) {
	tests := []struct {
		name   string
		role   domain.Role
		status int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"admin", domain.RoleAdmin, http.StatusCreated},
		{"editor", domain.RoleEditor, http.StatusCreated},
		{"reader", domain.RoleReader, http.StatusForbidden},
		{"reporter", domain.RoleReporter, http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newUnitAPI(t)
			w := a.do(http.MethodPost, "/api/v1/blogs", blogInput("Role "+tc.name), actorByRole(a, tc.role))
			if w.Code != tc.status {
				t.Fatalf("status=%d, want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}
