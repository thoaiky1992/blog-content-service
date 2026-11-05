package integration

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"

	"github.com/sky/blog-service/internal/domain"
)

func TestExportBlogsAPI(t *testing.T) {
	a := newIntegrationAPI(t)
	firstID := a.create("CSV First")
	secondID := a.create("CSV Second")
	// The view count must also be reflected in the CSV body.
	if w := a.do(http.MethodGet, "/api/v1/blogs/"+firstID, nil, domain.Anonymous()); w.Code != http.StatusOK {
		t.Fatalf("record view: status=%d body=%s", w.Code, w.Body.String())
	}

	w := a.do(http.MethodGet, "/api/v1/reports/blogs", nil, a.report)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("status=%d content-type=%q", w.Code, w.Header().Get("Content-Type"))
	}
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("invalid CSV body: %v\n%s", err, w.Body.String())
	}
	wantHeader := "id,title,author,description,views,created_at,updated_at,created_by,updated_by"
	if len(rows) == 0 || strings.Join(rows[0], ",") != wantHeader {
		t.Fatalf("CSV header=%v, want=%s", rows[0], wantHeader)
	}
	if len(rows) < 3 || len(rows) > 11 { // header + at most 10 rows
		t.Fatalf("CSV row count=%d, want 2..10 data rows", len(rows)-1)
	}

	want := map[string]struct {
		title string
		views string
	}{
		firstID:  {title: "CSV First", views: "1"},
		secondID: {title: "CSV Second", views: "0"},
	}
	for _, row := range rows[1:] {
		if len(row) != 9 {
			t.Fatalf("CSV columns=%d row=%v", len(row), row)
		}
		expected, ok := want[row[0]]
		if !ok {
			continue
		}
		if row[1] != expected.title || row[2] != "Author" || row[3] != "Description" || row[4] != expected.views {
			t.Errorf("CSV row for %s=%v", row[0], row)
		}
		delete(want, row[0])
	}
	if len(want) != 0 {
		t.Fatalf("CSV body is missing test blogs: %v\n%s", want, w.Body.String())
	}
}

func TestCSVExportPermissions(t *testing.T) {
	a := newIntegrationAPI(t)
	a.create("CSV permissions")
	for name, tc := range map[string]struct {
		actor domain.Actor
		want  int
	}{
		"admin": {a.admin, 200}, "reporter": {a.report, 200},
		"editor": {a.editor, 403}, "reader": {a.reader, 403}, "anonymous": {domain.Anonymous(), 401},
	} {
		t.Run(name, func(t *testing.T) {
			w := a.do(http.MethodGet, "/api/v1/reports/blogs", nil, tc.actor)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == 200 {
				if !strings.HasPrefix(w.Header().Get("Content-Disposition"), `attachment; filename="blogs-`) {
					t.Fatalf("content-disposition=%q", w.Header().Get("Content-Disposition"))
				}
				if _, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll(); err != nil {
					t.Fatalf("invalid CSV: %v", err)
				}
			} else if code := responseErrorCode(t, w); code != map[int]string{401: "UNAUTHENTICATED", 403: "FORBIDDEN"}[tc.want] {
				t.Fatalf("error code=%s", code)
			}
		})
	}
}
