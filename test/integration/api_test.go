// Package integration exercises the six HTTP APIs directly against the
// currently configured PostgreSQL database.
package integration

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sky/blog-service/internal/adapter/postgres"
	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/domain"
	"github.com/sky/blog-service/internal/platform/clientip"
	"github.com/sky/blog-service/internal/platform/clock"
	transport "github.com/sky/blog-service/internal/transport/http"
)

var (
	pool       *pgxpool.Pool
	skipReason string
)

func TestMain(m *testing.M) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://blog:blog@localhost:5432/blog?sslmode=disable"
	}

	cleanup, err := setupPostgres(context.Background(), dsn)
	if err != nil {
		skipReason = err.Error()
		fmt.Fprintln(os.Stderr, "integration tests will be skipped; current database is unavailable:", skipReason)
	}
	code := m.Run()
	if cleanup != nil {
		cleanup()
	}
	os.Exit(code)
}

func setupPostgres(ctx context.Context, dsn string) (func(), error) {
	var err error
	pool, err = pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect current postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping current postgres: %w", err)
	}
	return pool.Close, nil
}

type integrationAPI struct {
	t       *testing.T
	router  http.Handler
	editor  domain.Actor
	admin   domain.Actor
	report  domain.Actor
	clk     *clock.Fake
	created []uuid.UUID
}

func newIntegrationAPI(t *testing.T) *integrationAPI {
	t.Helper()
	if skipReason != "" {
		t.Skip(skipReason)
	}
	ctx := context.Background()
	actor := func(role domain.Role) domain.Actor {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE role = $1`, role).Scan(&id); err != nil {
			t.Fatalf("load %s user: %v", role, err)
		}
		return domain.Actor{ID: id, Role: role}
	}
	// Future timestamps keep rows created by this test near the front of list
	// results without modifying or deleting existing application rows.
	clk := clock.NewFake(time.Now().UTC().Add(24 * time.Hour))
	blogs := postgres.NewBlogRepo(pool)
	a := &integrationAPI{
		t: t, editor: actor(domain.RoleEditor), admin: actor(domain.RoleAdmin),
		report: actor(domain.RoleReporter), clk: clk,
	}
	a.router = transport.NewRouter(transport.Deps{
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:       pool,
		Users:    postgres.NewUserRepo(pool),
		Blogs:    app.NewBlogService(blogs, clk),
		Views:    app.NewViewService(blogs, postgres.NewViewStore(pool), nil, clk),
		Reports:  app.NewReportService(blogs),
		ClientIP: clientip.New(nil),
		Clock:    clk,
	})
	t.Cleanup(a.cleanup)
	return a
}

func (a *integrationAPI) cleanup() {
	ctx := context.Background()
	for _, id := range a.created {
		if _, err := pool.Exec(ctx, `DELETE FROM blog_daily_views WHERE blog_id = $1`, id); err != nil {
			a.t.Errorf("cleanup views for %s: %v", id, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM blogs WHERE id = $1`, id); err != nil {
			a.t.Errorf("cleanup blog %s: %v", id, err)
		}
	}
}

func (a *integrationAPI) track(id string) {
	a.t.Helper()
	parsed, err := uuid.Parse(id)
	if err != nil {
		a.t.Fatalf("invalid blog id %q: %v", id, err)
	}
	a.created = append(a.created, parsed)
}

func (a *integrationAPI) do(method, path string, body any, actor domain.Actor) *httptest.ResponseRecorder {
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
	req.RemoteAddr = "198.51.100.10:4321"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if !actor.IsAnonymous() {
		req.Header.Set("X-User-ID", actor.ID.String())
	}
	w := httptest.NewRecorder()
	a.router.ServeHTTP(w, req)
	return w
}

func integrationBlogInput(title string) map[string]any {
	return map[string]any{
		"title": title, "author": "Author", "content": "Content", "description": "Description",
	}
}

func (a *integrationAPI) create(title string) string {
	a.t.Helper()
	a.clk.Advance(time.Second)
	w := a.do(http.MethodPost, "/api/v1/blogs", integrationBlogInput(title), a.editor)
	if w.Code != http.StatusCreated {
		a.t.Fatalf("create setup: status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		a.t.Fatal(err)
	}
	a.track(body.ID)
	return body.ID
}

func TestListBlogsAPI(t *testing.T) {
	a := newIntegrationAPI(t)
	a.create("First")
	a.create("Second")
	w := a.do(http.MethodGet, "/api/v1/blogs?page=1&limit=20", nil, domain.Anonymous())
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Items []struct{ Title string } `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) < 2 || body.Items[0].Title != "Second" || body.Items[1].Title != "First" {
		t.Fatalf("items=%+v", body.Items)
	}
}

func TestGetBlogAPI(t *testing.T) {
	a := newIntegrationAPI(t)
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

func TestCreateBlogAPI(t *testing.T) {
	a := newIntegrationAPI(t)
	w := a.do(http.MethodPost, "/api/v1/blogs", integrationBlogInput("Created"), a.editor)
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
	a.track(body.ID)
	if body.Title != "Created" || body.Version != 1 || w.Header().Get("Location") != "/api/v1/blogs/"+body.ID {
		t.Fatalf("body=%+v location=%q", body, w.Header().Get("Location"))
	}
}

func TestUpdateBlogAPI(t *testing.T) {
	a := newIntegrationAPI(t)
	id := a.create("Before")
	w := a.do(http.MethodPut, "/api/v1/blogs/"+id, integrationBlogInput("After"), a.editor)
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

func TestDeleteBlogAPI(t *testing.T) {
	a := newIntegrationAPI(t)
	id := a.create("Delete")
	w := a.do(http.MethodDelete, "/api/v1/blogs/"+id, nil, a.admin)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	w = a.do(http.MethodGet, "/api/v1/blogs/"+id, nil, domain.Anonymous())
	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete status=%d body=%s", w.Code, w.Body.String())
	}
}

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
