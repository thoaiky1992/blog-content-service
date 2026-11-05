package unit

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/domain"
)

func TestGetBlogSuccessAndUniqueView(t *testing.T) {
	a := newUnitAPI(t)
	id := a.create("Detail")
	for i := 0; i < 2; i++ {
		w := a.do(http.MethodGet, "/api/v1/blogs/"+id, nil, domain.Anonymous())
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var body struct {
			Title string `json:"title"`
			Views int64  `json:"views"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Title != "Detail" || body.Views != 1 {
			t.Fatalf("body=%+v", body)
		}
	}
}

func TestGetBlogInputValidation(t *testing.T) {
	a := newUnitAPI(t)
	assertAPIError(t, a.do(http.MethodGet, "/api/v1/blogs/not-a-uuid", nil, domain.Anonymous()),
		http.StatusBadRequest, "VALIDATION_FAILED")
	assertAPIError(t, a.do(http.MethodGet, "/api/v1/blogs/"+uuid.NewString(), nil, domain.Anonymous()),
		http.StatusNotFound, "NOT_FOUND")
}

func TestGetBlogPermissions(t *testing.T) {
	a := newUnitAPI(t)
	id := a.create("Readable")
	for _, tc := range roleCases(a) {
		t.Run(tc.name, func(t *testing.T) {
			w := a.do(http.MethodGet, "/api/v1/blogs/"+id, nil, tc.actor)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
