package unit

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/app/apptest"
	"github.com/sky/blog-service/internal/domain"
	"github.com/sky/blog-service/internal/platform/clientip"
	"github.com/sky/blog-service/internal/platform/clock"
	transport "github.com/sky/blog-service/internal/transport/http"
)

type okDB struct{}

func (okDB) Ping(context.Context) error { return nil }

type unitAPI struct {
	t      *testing.T
	router http.Handler
	editor domain.Actor
	admin  domain.Actor
	reader domain.Actor
	report domain.Actor
}

func newUnitAPI(t *testing.T) *unitAPI {
	t.Helper()
	repo := apptest.NewRepo()
	views := apptest.NewViews(repo)
	clk := clock.NewFake(time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC))
	a := &unitAPI{
		t:      t,
		editor: domain.Actor{ID: uuid.New(), Role: domain.RoleEditor},
		admin:  domain.Actor{ID: uuid.New(), Role: domain.RoleAdmin},
		reader: domain.Actor{ID: uuid.New(), Role: domain.RoleReader},
		report: domain.Actor{ID: uuid.New(), Role: domain.RoleReporter},
	}
	users := apptest.NewUsers(a.editor, a.admin, a.reader, a.report)
	a.router = transport.NewRouter(transport.Deps{
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:       okDB{},
		Users:    users,
		Blogs:    app.NewBlogService(repo, clk),
		Views:    app.NewViewService(repo, views, nil, clk),
		Reports:  app.NewReportService(repo),
		ClientIP: clientip.New(nil),
		Clock:    clk,
	})
	return a
}

func (a *unitAPI) do(method, path string, body any, actor domain.Actor) *httptest.ResponseRecorder {
	a.t.Helper()
	userID := ""
	if !actor.IsAnonymous() {
		userID = actor.ID.String()
	}
	return a.doWithUserID(method, path, body, userID)
}

func (a *unitAPI) doWithUserID(method, path string, body any, userID string) *httptest.ResponseRecorder {
	a.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			a.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "192.0.2.10:1234"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if userID != "" {
		req.Header.Set("X-User-ID", userID)
	}
	w := httptest.NewRecorder()
	a.router.ServeHTTP(w, req)
	return w
}

func blogInput(title string) map[string]any {
	return map[string]any{
		"title": title, "author": "Author", "content": "Content", "description": "Description",
	}
}

func (a *unitAPI) create(title string) string {
	a.t.Helper()
	w := a.do(http.MethodPost, "/api/v1/blogs", blogInput(title), a.editor)
	if w.Code != http.StatusCreated {
		a.t.Fatalf("create setup: status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		a.t.Fatal(err)
	}
	return body.ID
}

func roleCases(a *unitAPI) []struct {
	name  string
	actor domain.Actor
} {
	return []struct {
		name  string
		actor domain.Actor
	}{
		{"anonymous", domain.Anonymous()},
		{"admin", a.admin},
		{"editor", a.editor},
		{"reader", a.reader},
		{"reporter", a.report},
	}
}

func actorByRole(a *unitAPI, role domain.Role) domain.Actor {
	switch role {
	case domain.RoleAdmin:
		return a.admin
	case domain.RoleEditor:
		return a.editor
	case domain.RoleReader:
		return a.reader
	case domain.RoleReporter:
		return a.report
	default:
		return domain.Anonymous()
	}
}

func assertAPIError(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d, want=%d body=%s", w.Code, status, w.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != code {
		t.Fatalf("error code=%q, want=%q body=%s", body.Error.Code, code, w.Body.String())
	}
}

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
