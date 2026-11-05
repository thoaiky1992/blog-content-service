// Package integration exercises the six HTTP APIs directly against the configured
// PostgreSQL database.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sky/blog-service/internal/adapter/clock"
	"github.com/sky/blog-service/internal/adapter/postgres"
	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/domain"
	transport "github.com/sky/blog-service/internal/transport/http"
	"github.com/sky/blog-service/internal/transport/http/clientip"
)

var (
	pool       *pgxpool.Pool
	skipReason string
)

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		dsn = "postgres://blog:blog@localhost:5432/blog?sslmode=disable"
	}

	cleanup, err := setupPostgres(context.Background(), dsn)
	if err != nil {
		skipReason = err.Error()
		fmt.Fprintln(os.Stderr, "integration tests will be skipped; the current database is unavailable:", skipReason)
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
		return nil, fmt.Errorf("connect to current PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping current PostgreSQL: %w", err)
	}
	return pool.Close, nil
}

type integrationAPI struct {
	t       *testing.T
	router  http.Handler
	editor  domain.Actor
	admin   domain.Actor
	report  domain.Actor
	reader  domain.Actor
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
			t.Fatalf("load user with role %s: %v", role, err)
		}
		return domain.Actor{ID: id, Role: role}
	}
	// Future timestamps keep rows created by this test near the front of list results
	// without modifying or deleting existing application rows.
	clk := clock.NewFake(time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	blogs := postgres.NewBlogRepo(pool)
	a := &integrationAPI{
		t: t, editor: actor(domain.RoleEditor), admin: actor(domain.RoleAdmin),
		report: actor(domain.RoleReporter), reader: actor(domain.RoleReader), clk: clk,
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
			a.t.Errorf("clean up views for %s: %v", id, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM blogs WHERE id = $1`, id); err != nil {
			a.t.Errorf("clean up blog %s: %v", id, err)
		}
	}
}

func (a *integrationAPI) track(id string) {
	a.t.Helper()
	parsed, err := uuid.Parse(id)
	if err != nil {
		a.t.Fatalf("invalid blog ID %q: %v", id, err)
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
	return a.doRequest(method, path, reader, body != nil, actor, "198.51.100.10:4321")
}

func (a *integrationAPI) doIP(method, path string, body any, actor domain.Actor, ip string) *httptest.ResponseRecorder {
	a.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			a.t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	return a.doRequest(method, path, reader, body != nil, actor, ip+":4321")
}

func (a *integrationAPI) doRequest(method, path string, body io.Reader, jsonBody bool, actor domain.Actor, remoteAddr string) *httptest.ResponseRecorder {
	a.t.Helper()
	req := httptest.NewRequest(method, path, body)
	req.RemoteAddr = remoteAddr
	if jsonBody {
		req.Header.Set("Content-Type", "application/json")
	}
	if !actor.IsAnonymous() {
		req.Header.Set("X-User-ID", actor.ID.String())
	}
	w := httptest.NewRecorder()
	a.router.ServeHTTP(w, req)
	return w
}

func responseErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, w.Body.String())
	}
	if body.Error.RequestID == "" {
		t.Fatalf("error response has no request_id: %s", w.Body.String())
	}
	return body.Error.Code
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
		a.t.Fatalf("create setup failed: status=%d body=%s", w.Code, w.Body.String())
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
