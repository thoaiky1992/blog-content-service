package unit

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sky/blog-service/internal/domain"
)

func TestListBlogsSuccess(t *testing.T) {
	a := newUnitAPI(t)
	a.create("First")
	a.create("Second")
	w := a.do(http.MethodGet, "/api/v1/blogs?page=1&limit=20", nil, domain.Anonymous())
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Items []struct{ Title string } `json:"items"`
		Page  int                      `json:"page"`
		Limit int                      `json:"limit"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 || body.Items[0].Title != "Second" || body.Page != 1 || body.Limit != 20 {
		t.Fatalf("body=%+v", body)
	}
}

func TestListBlogsInputValidation(t *testing.T) {
	a := newUnitAPI(t)
	tests := []struct{ name, path string }{
		{"page below minimum", "/api/v1/blogs?page=0"},
		{"page is not an integer", "/api/v1/blogs?page=abc"},
		{"limit below minimum", "/api/v1/blogs?limit=0"},
		{"limit above maximum", "/api/v1/blogs?limit=101"},
		{"limit is not an integer", "/api/v1/blogs?limit=abc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertAPIError(t, a.do(http.MethodGet, tc.path, nil, domain.Anonymous()),
				http.StatusBadRequest, "VALIDATION_FAILED")
		})
	}
}

func TestListBlogsPermissions(t *testing.T) {
	a := newUnitAPI(t)
	for _, tc := range roleCases(a) {
		t.Run(tc.name, func(t *testing.T) {
			w := a.do(http.MethodGet, "/api/v1/blogs", nil, tc.actor)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
