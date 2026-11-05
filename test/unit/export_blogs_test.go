package unit

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/domain"
)

func TestExportBlogsSuccess(t *testing.T) {
	a := newUnitAPI(t)
	a.create("Report")
	w := a.do(http.MethodGet, "/api/v1/reports/blogs", nil, a.report)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("status=%d content-type=%q", w.Code, w.Header().Get("Content-Type"))
	}
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1][1] != "Report" {
		t.Fatalf("rows=%v", rows)
	}
}

func TestExportBlogsInputValidation(t *testing.T) {
	a := newUnitAPI(t)
	assertAPIError(t, a.doWithUserID(http.MethodGet, "/api/v1/reports/blogs", nil, "not-a-uuid"),
		http.StatusUnauthorized, "UNAUTHENTICATED")
	assertAPIError(t, a.doWithUserID(http.MethodGet, "/api/v1/reports/blogs", nil, uuid.NewString()),
		http.StatusUnauthorized, "UNAUTHENTICATED")
}

func TestExportBlogsPermissions(t *testing.T) {
	tests := []struct {
		name   string
		role   domain.Role
		status int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"admin", domain.RoleAdmin, http.StatusOK},
		{"editor", domain.RoleEditor, http.StatusForbidden},
		{"reader", domain.RoleReader, http.StatusForbidden},
		{"reporter", domain.RoleReporter, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newUnitAPI(t)
			a.create("Report")
			w := a.do(http.MethodGet, "/api/v1/reports/blogs", nil, actorByRole(a, tc.role))
			if w.Code != tc.status {
				t.Fatalf("status=%d, want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}
