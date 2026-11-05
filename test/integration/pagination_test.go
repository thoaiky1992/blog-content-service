package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sky/blog-service/internal/domain"
)

func TestPagination(t *testing.T) {
	a := newIntegrationAPI(t)
	first := a.create("page first")
	second := a.create("page second")
	third := a.create("page third")

	type pageBody struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Page    int  `json:"page"`
		Limit   int  `json:"limit"`
		HasNext bool `json:"has_next"`
	}
	read := func(path string) pageBody {
		w := a.do(http.MethodGet, path, nil, domain.Anonymous())
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var body pageBody
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	p1 := read("/api/v1/blogs?page=1&limit=2")
	if p1.Page != 1 || p1.Limit != 2 || !p1.HasNext || len(p1.Items) != 2 || p1.Items[0].ID != third || p1.Items[1].ID != second {
		t.Fatalf("page 1=%+v", p1)
	}
	p2 := read("/api/v1/blogs?page=2&limit=2")
	if p2.Page != 2 || p2.Limit != 2 || len(p2.Items) == 0 || p2.Items[0].ID != first {
		t.Fatalf("page 2=%+v", p2)
	}
}
