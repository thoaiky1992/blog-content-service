package integration

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/adapter/postgres"
	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/platform/clientip"
	"github.com/sky/blog-service/internal/platform/clock"
	"github.com/sky/blog-service/internal/platform/iphash"
	transport "github.com/sky/blog-service/internal/transport/http"
)

type api struct {
	t      *testing.T
	router http.Handler
	clk    *clock.Fake
	admin  string
	editor string
	reader string
	report string
}

// newAPI wires the full production stack (real repos, real HMAC hasher, real
// mock-auth lookup) against the container database.
func newAPI(t *testing.T, trusted ...string) *api {
	t.Helper()
	if skipReason != "" {
		t.Skip(skipReason)
	}
	hasher, err := iphash.New([]byte("integration-test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	var prefixes []netip.Prefix
	for _, c := range trusted {
		prefixes = append(prefixes, netip.MustParsePrefix(c))
	}
	clk := clock.NewFake(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC))
	blogs := postgres.NewBlogRepo(pool)
	return &api{
		t:   t,
		clk: clk,
		router: transport.NewRouter(transport.Deps{
			Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			DB:       pool,
			Users:    postgres.NewUserRepo(pool),
			Blogs:    app.NewBlogService(blogs, clk),
			Views:    app.NewViewService(blogs, postgres.NewViewStore(pool), hasher, clk),
			Reports:  app.NewReportService(blogs),
			ClientIP: clientip.New(prefixes),
			Clock:    clk,
		}),
		admin:  userID(t, "admin").String(),
		editor: userID(t, "editor").String(),
		reader: userID(t, "reader").String(),
		report: userID(t, "reporter").String(),
	}
}

func (a *api) do(method, path string, body any, user, remote string, xff ...string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = remote
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if user != "" {
		req.Header.Set("X-User-ID", user)
	}
	for _, v := range xff {
		req.Header.Add("X-Forwarded-For", v)
	}
	w := httptest.NewRecorder()
	a.router.ServeHTTP(w, req)
	return w
}

func (a *api) createBlog(title string) string {
	a.t.Helper()
	a.clk.Advance(time.Second)
	w := a.do("POST", "/api/v1/blogs",
		map[string]any{"title": title, "author": "Au", "content": "Co", "description": "De"}, a.editor, "192.0.2.1:1")
	if w.Code != 201 {
		a.t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var b struct{ ID string }
	json.Unmarshal(w.Body.Bytes(), &b)
	return b.ID
}

func blogViews(t *testing.T, w *httptest.ResponseRecorder) int {
	t.Helper()
	var b struct{ Views int }
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &b)
	return b.Views
}

func TestHTTPFullLifecycleWithAudit(t *testing.T) {
	a := newAPI(t)
	id := a.createBlog("Lifecycle")
	url := "/api/v1/blogs/" + id

	var got map[string]any
	json.Unmarshal(a.do("GET", url, nil, "", "192.0.2.5:1").Body.Bytes(), &got)
	if got["created_by"] != a.editor || got["views"].(float64) != 1 {
		t.Errorf("detail: %v", got)
	}

	a.clk.Advance(time.Hour)
	w := a.do("PUT", url, map[string]any{"title": "Changed", "author": "B", "content": "C"}, a.admin, "192.0.2.1:1")
	json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != 200 || got["updated_by"] != a.admin || got["created_by"] != a.editor ||
		got["version"].(float64) != 2 || got["views"].(float64) != 1 {
		t.Errorf("update: %d %v", w.Code, got)
	}

	if w := a.do("DELETE", url, nil, a.editor, "192.0.2.1:1"); w.Code != 403 {
		t.Errorf("editor delete: %d", w.Code)
	}
	if w := a.do("DELETE", url, nil, a.admin, "192.0.2.1:1"); w.Code != 204 {
		t.Errorf("admin delete: %d", w.Code)
	}
	var deletedBy uuid.UUID
	pool.QueryRow(ctx, `SELECT deleted_by FROM blogs WHERE id = $1`, id).Scan(&deletedBy)
	if deletedBy.String() != a.admin {
		t.Errorf("deleted_by = %s", deletedBy)
	}
	for _, c := range []struct{ method, user string }{{"GET", ""}, {"PUT", a.editor}, {"DELETE", a.admin}} {
		var body any
		if c.method == "PUT" {
			body = map[string]any{"title": "t", "author": "a", "content": "c"}
		}
		if w := a.do(c.method, url, body, c.user, "192.0.2.9:1"); w.Code != 404 {
			t.Errorf("%s after delete: %d", c.method, w.Code)
		}
	}
}

func TestHTTPAuthAgainstRealUsers(t *testing.T) {
	a := newAPI(t)
	if w := a.do("GET", "/api/v1/blogs", nil, uuid.NewString(), "192.0.2.1:1"); w.Code != 401 {
		t.Errorf("unknown user: %d", w.Code)
	}
	if w := a.do("POST", "/api/v1/blogs", map[string]any{"title": "t", "author": "a", "content": "c"}, a.reader, "192.0.2.1:1"); w.Code != 403 {
		t.Errorf("reader create: %d", w.Code)
	}
	if w := a.do("GET", "/api/v1/reports/blogs", nil, a.report, "192.0.2.1:1"); w.Code != 200 {
		t.Errorf("reporter export: %d", w.Code)
	}
	if w := a.do("GET", "/readyz", nil, "", "192.0.2.1:1"); w.Code != 200 {
		t.Errorf("readyz: %d", w.Code)
	}
}

func TestHTTPConcurrentSameIPCountsOnce(t *testing.T) {
	a := newAPI(t)
	id := a.createBlog("same-ip-http")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(port int) {
			defer wg.Done()
			// Same client IP, different source ports = same visitor.
			if w := a.do("GET", "/api/v1/blogs/"+id, nil, "", fmt.Sprintf("203.0.113.7:%d", 10000+port)); w.Code != 200 {
				t.Errorf("status %d: %s", w.Code, w.Body.String())
			}
		}(i)
	}
	wg.Wait()
	bid := uuid.MustParse(id)
	if viewRows(t, bid) != 1 || viewsCounter(t, bid) != 1 {
		t.Errorf("rows=%d views=%d, want 1/1", viewRows(t, bid), viewsCounter(t, bid))
	}
	assertInvariant(t, bid)
}

func TestHTTPConcurrentDifferentIPsCountEach(t *testing.T) {
	a := newAPI(t)
	id := a.createBlog("diff-ip-http")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			a.do("GET", "/api/v1/blogs/"+id, nil, "", fmt.Sprintf("198.51.100.%d:1", n+1))
		}(i)
	}
	wg.Wait()
	bid := uuid.MustParse(id)
	if viewRows(t, bid) != 100 || viewsCounter(t, bid) != 100 {
		t.Errorf("rows=%d views=%d, want 100/100", viewRows(t, bid), viewsCounter(t, bid))
	}
	assertInvariant(t, bid)
}

func TestHTTPConcurrentBehindTrustedProxy(t *testing.T) {
	a := newAPI(t, "10.0.0.0/8")
	id := a.createBlog("proxy-http")
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ { // 20 real clients, each sending 3 requests; left-most XFF is forged noise
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			client := fmt.Sprintf("198.51.100.%d", n%20+1)
			a.do("GET", "/api/v1/blogs/"+id, nil, "", fmt.Sprintf("10.0.0.%d:443", n%5+1),
				fmt.Sprintf("6.6.6.%d, %s, 10.0.0.9", n, client))
		}(i)
	}
	wg.Wait()
	bid := uuid.MustParse(id)
	if viewsCounter(t, bid) != 20 {
		t.Errorf("views=%d, want 20 distinct real clients", viewsCounter(t, bid))
	}
	assertInvariant(t, bid)
}

func TestHTTPForgedXFFFromUntrustedPeerIgnored(t *testing.T) {
	a := newAPI(t, "10.0.0.0/8")
	id := a.createBlog("forged-http")
	for i := 0; i < 10; i++ {
		a.do("GET", "/api/v1/blogs/"+id, nil, "", "203.0.113.77:1", fmt.Sprintf("1.2.3.%d", i))
	}
	if v := viewsCounter(t, uuid.MustParse(id)); v != 1 {
		t.Errorf("views=%d, want 1", v)
	}
}

func TestHTTPNextUTCDayCountsAgain(t *testing.T) {
	a := newAPI(t)
	id := a.createBlog("days-http")
	a.clk.Set(time.Date(2026, 11, 5, 23, 59, 59, 0, time.UTC))
	url := "/api/v1/blogs/" + id
	if v := blogViews(t, a.do("GET", url, nil, "", "203.0.113.1:1")); v != 1 {
		t.Errorf("day1: %d", v)
	}
	if v := blogViews(t, a.do("GET", url, nil, "", "203.0.113.1:1")); v != 1 {
		t.Errorf("day1 repeat: %d", v)
	}
	a.clk.Set(time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC))
	if v := blogViews(t, a.do("GET", url, nil, "", "203.0.113.1:1")); v != 2 {
		t.Errorf("day2: %d", v)
	}
	assertInvariant(t, uuid.MustParse(id))
}

func TestHTTPListDoesNotIncreaseViews(t *testing.T) {
	a := newAPI(t)
	id := a.createBlog("list-http")
	for i := 0; i < 5; i++ {
		a.do("GET", "/api/v1/blogs", nil, "", "203.0.113.1:1")
	}
	if v := viewsCounter(t, uuid.MustParse(id)); v != 0 {
		t.Errorf("views=%d, want 0", v)
	}
}

func TestHTTPCSVExportRealDatabase(t *testing.T) {
	a := newAPI(t)
	resetBlogs(t)
	const n = 1200 // spans more than two internal flushes
	a.clk.Set(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC))
	var ids []string
	for i := 0; i < n; i++ {
		ids = append(ids, a.createBlog(fmt.Sprintf("Blog %04d", i)))
	}
	special := a.do("POST", "/api/v1/blogs", map[string]any{
		"title": "=SUM(1,2)", "author": "O'Brien, \"Pat\"", "content": "c", "description": "line1\nline2"},
		a.editor, "192.0.2.1:1")
	var sp struct{ ID string }
	json.Unmarshal(special.Body.Bytes(), &sp)

	deleted := ids[7]
	if w := a.do("DELETE", "/api/v1/blogs/"+deleted, nil, a.admin, "192.0.2.1:1"); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	a.do("GET", "/api/v1/blogs/"+ids[0], nil, "", "203.0.113.1:1") // views = 1

	w := a.do("GET", "/api/v1/reports/blogs", nil, a.report, "192.0.2.1:1")
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Content-Type"))
	}
	rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("invalid CSV: %v", err)
	}
	if len(rows) != 1+n { // header + (n + 1 special - 1 deleted)
		t.Fatalf("rows = %d, want %d", len(rows)-1, n)
	}
	seen := map[string]bool{}
	for _, r := range rows[1:] {
		seen[r[0]] = true
		if len(r) != 9 {
			t.Fatalf("columns = %d: %v", len(r), r)
		}
	}
	if seen[deleted] {
		t.Error("soft-deleted blog leaked into the CSV report")
	}
	if !seen[ids[0]] || !seen[sp.ID] {
		t.Error("active blogs missing from report")
	}
	if rows[1][0] != sp.ID { // newest first
		t.Errorf("first row = %v, want newest blog", rows[1])
	}
	first := rows[1]
	if first[1] != "'=SUM(1,2)" || first[2] != "O'Brien, \"Pat\"" || first[3] != "line1\nline2" {
		t.Errorf("escaping/sanitizing: %q", first)
	}
	for _, r := range rows[1:] {
		if r[0] == ids[0] && r[4] != "1" {
			t.Errorf("views column = %q, want 1", r[4])
		}
	}
}
