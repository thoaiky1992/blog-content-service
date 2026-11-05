package unit

import (
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/domain"
)

func TestDeleteBlogSuccess(t *testing.T) {
	a := newUnitAPI(t)
	id := a.create("Delete")
	w := a.do(http.MethodDelete, "/api/v1/blogs/"+id, nil, a.admin)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	assertAPIError(t, a.do(http.MethodGet, "/api/v1/blogs/"+id, nil, domain.Anonymous()),
		http.StatusNotFound, "NOT_FOUND")
}

func TestDeleteBlogInputValidation(t *testing.T) {
	a := newUnitAPI(t)
	assertAPIError(t, a.do(http.MethodDelete, "/api/v1/blogs/not-a-uuid", nil, a.admin),
		http.StatusBadRequest, "VALIDATION_FAILED")
	assertAPIError(t, a.do(http.MethodDelete, "/api/v1/blogs/"+uuid.NewString(), nil, a.admin),
		http.StatusNotFound, "NOT_FOUND")
}

func TestDeleteBlogPermissions(t *testing.T) {
	tests := []struct {
		name   string
		role   domain.Role
		status int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"admin", domain.RoleAdmin, http.StatusNoContent},
		{"editor", domain.RoleEditor, http.StatusForbidden},
		{"reader", domain.RoleReader, http.StatusForbidden},
		{"reporter", domain.RoleReporter, http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newUnitAPI(t)
			id := a.create("Delete")
			w := a.do(http.MethodDelete, "/api/v1/blogs/"+id, nil, actorByRole(a, tc.role))
			if w.Code != tc.status {
				t.Fatalf("status=%d, want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}
