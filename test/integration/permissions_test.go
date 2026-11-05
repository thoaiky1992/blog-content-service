package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sky/blog-service/internal/domain"
)

func TestRolePermissionValidation(t *testing.T) {
	a := newIntegrationAPI(t)

	for name, tc := range map[string]struct {
		actor domain.Actor
		want  int
	}{
		"admin": {a.admin, http.StatusCreated}, "editor": {a.editor, http.StatusCreated},
		"reader": {a.reader, http.StatusForbidden}, "reporter": {a.report, http.StatusForbidden},
		"anonymous": {domain.Anonymous(), http.StatusUnauthorized},
	} {
		t.Run("create/"+name, func(t *testing.T) {
			w := a.do(http.MethodPost, "/api/v1/blogs", integrationBlogInput("permission-"+name), tc.actor)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
			}
			if w.Code == http.StatusCreated {
				var body struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				a.track(body.ID)
			}
		})
	}

	id := a.create("permission target")
	if w := a.do(http.MethodPut, "/api/v1/blogs/"+id, integrationBlogInput("editor updated"), a.editor); w.Code != http.StatusOK {
		t.Fatalf("editor update returned status=%d body=%s", w.Code, w.Body.String())
	}
	if w := a.do(http.MethodPut, "/api/v1/blogs/"+id, integrationBlogInput("reader updated"), a.reader); w.Code != http.StatusForbidden {
		t.Fatalf("reader update returned status=%d body=%s", w.Code, w.Body.String())
	}
	if w := a.do(http.MethodDelete, "/api/v1/blogs/"+id, nil, a.editor); w.Code != http.StatusForbidden {
		t.Fatalf("editor delete returned status=%d body=%s", w.Code, w.Body.String())
	}
	if w := a.do(http.MethodDelete, "/api/v1/blogs/"+id, nil, a.admin); w.Code != http.StatusNoContent {
		t.Fatalf("admin delete returned status=%d body=%s", w.Code, w.Body.String())
	}
}
