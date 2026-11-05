package unit

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/domain"
)

func TestUpdateBlogSuccess(t *testing.T) {
	a := newUnitAPI(t)
	id := a.create("Before")
	w := a.do(http.MethodPut, "/api/v1/blogs/"+id, blogInput("After"), a.editor)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Title   string `json:"title"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Title != "After" || body.Version != 2 {
		t.Fatalf("body=%+v", body)
	}
}

func TestUpdateBlogInputValidation(t *testing.T) {
	a := newUnitAPI(t)
	id := a.create("Before")
	tests := []struct {
		name string
		path string
		body map[string]any
		code string
	}{
		{"invalid ID", "/api/v1/blogs/not-a-uuid", blogInput("After"), "VALIDATION_FAILED"},
		{"missing title", "/api/v1/blogs/" + id, map[string]any{"author": "A", "content": "C"}, "VALIDATION_FAILED"},
		{"invalid version", "/api/v1/blogs/" + id, map[string]any{"title": "T", "author": "A", "content": "C", "version": 0}, "VALIDATION_FAILED"},
		{"missing blog", "/api/v1/blogs/" + uuid.NewString(), blogInput("After"), "NOT_FOUND"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status := http.StatusBadRequest
			if tc.code == "NOT_FOUND" {
				status = http.StatusNotFound
			}
			assertAPIError(t, a.do(http.MethodPut, tc.path, tc.body, a.editor), status, tc.code)
		})
	}
}

func TestUpdateBlogPermissions(t *testing.T) {
	tests := []struct {
		name   string
		role   domain.Role
		status int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"admin", domain.RoleAdmin, http.StatusOK},
		{"editor", domain.RoleEditor, http.StatusOK},
		{"reader", domain.RoleReader, http.StatusForbidden},
		{"reporter", domain.RoleReporter, http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newUnitAPI(t)
			id := a.create("Before")
			actor := actorByRole(a, tc.role)
			w := a.do(http.MethodPut, "/api/v1/blogs/"+id, blogInput("After"), actor)
			if w.Code != tc.status {
				t.Fatalf("status=%d, want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}
