package http_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
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

func nil2ctx() context.Context { return context.Background() }

type harness struct {
	t                               *testing.T
	repo                            *apptest.Repo
	views                           *apptest.Views
	users                           *apptest.Users
	clk                             *clock.Fake
	router                          http.Handler
	admin, editor, reader, reporter domain.Actor
}

func newHarness(t *testing.T, trusted ...string) *harness {
	t.Helper()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := &harness{
		t:        t,
		repo:     apptest.NewRepo(),
		clk:      clock.NewFake(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)),
		admin:    domain.Actor{ID: uuid.New(), Role: domain.RoleAdmin},
		editor:   domain.Actor{ID: uuid.New(), Role: domain.RoleEditor},
		reader:   domain.Actor{ID: uuid.New(), Role: domain.RoleReader},
		reporter: domain.Actor{ID: uuid.New(), Role: domain.RoleReporter},
	}
	h.views = apptest.NewViews(h.repo)
	h.users = apptest.NewUsers(h.admin, h.editor, h.reader, h.reporter)

	var prefixes []netip.Prefix
	for _, c := range trusted {
		prefixes = append(prefixes, netip.MustParsePrefix(c))
	}
	h.router = transport.NewRouter(transport.Deps{
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:       okDB{},
		Users:    h.users,
		Blogs:    app.NewBlogService(h.repo, h.clk),
		Views:    app.NewViewService(h.repo, h.views, apptest.Hasher{}, h.clk),
		Reports:  app.NewReportService(h.repo),
		ClientIP: clientip.New(prefixes),
		Clock:    h.clk,
	})
	return h
}

type reqOpt func(*http.Request)

func as(a domain.Actor) reqOpt {
	return func(r *http.Request) { r.Header.Set("X-User-ID", a.ID.String()) }
}
func userHeader(v string) reqOpt { return func(r *http.Request) { r.Header.Set("X-User-ID", v) } }
func from(addr string) reqOpt    { return func(r *http.Request) { r.RemoteAddr = addr } }
func xff(v string) reqOpt        { return func(r *http.Request) { r.Header.Add("X-Forwarded-For", v) } }
func contentType(v string) reqOpt {
	return func(r *http.Request) {
		if v == "" {
			r.Header.Del("Content-Type")
		} else {
			r.Header.Set("Content-Type", v)
		}
	}
}

// do sends a request. body may be nil, a string (raw), or any JSON-able value.
func (h *harness) do(method, path string, body any, opts ...reqOpt) *httptest.ResponseRecorder {
	h.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			h.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "192.0.2.10:5555"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, o := range opts {
		o(req)
	}
	w := httptest.NewRecorder()
	h.router.ServeHTTP(w, req)
	return w
}

func (h *harness) seed(title string) domain.Blog {
	h.t.Helper()
	h.clk.Advance(time.Second)
	b, err := app.NewBlogService(h.repo, h.clk).Create(nil2ctx(), h.editor,
		domain.BlogInput{Title: title, Author: "Author", Content: "Content", Description: "Desc"})
	if err != nil {
		h.t.Fatal(err)
	}
	return b
}

var validBody = map[string]any{"title": "T", "author": "A", "content": "C", "description": "D"}

type errBody struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
		Details   []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"details"`
	} `json:"error"`
}

func decodeErr(t *testing.T, w *httptest.ResponseRecorder) errBody {
	t.Helper()
	var e errBody
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Error.Code == "" {
		t.Fatalf("not an error envelope (status %d): %s", w.Code, w.Body.String())
	}
	return e
}

func hasDetail(e errBody, field string) bool {
	for _, d := range e.Error.Details {
		if d.Field == field {
			return true
		}
	}
	return false
}

// ---- RBAC matrix: every endpoint x every role ----

func TestPermissionMatrixAllEndpoints(t *testing.T) {
	type who struct {
		name string
		opt  func(h *harness) []reqOpt
	}
	roles := []who{
		{"anonymous", func(*harness) []reqOpt { return nil }},
		{"reader", func(h *harness) []reqOpt { return []reqOpt{as(h.reader)} }},
		{"reporter", func(h *harness) []reqOpt { return []reqOpt{as(h.reporter)} }},
		{"editor", func(h *harness) []reqOpt { return []reqOpt{as(h.editor)} }},
		{"admin", func(h *harness) []reqOpt { return []reqOpt{as(h.admin)} }},
	}
	endpoints := []struct {
		name   string
		method string
		path   func(id uuid.UUID) string
		body   any
		want   map[string]int
	}{
		{"list", "GET", func(uuid.UUID) string { return "/api/v1/blogs" }, nil,
			map[string]int{"anonymous": 200, "reader": 200, "reporter": 200, "editor": 200, "admin": 200}},
		{"detail", "GET", func(id uuid.UUID) string { return "/api/v1/blogs/" + id.String() }, nil,
			map[string]int{"anonymous": 200, "reader": 200, "reporter": 200, "editor": 200, "admin": 200}},
		{"create", "POST", func(uuid.UUID) string { return "/api/v1/blogs" }, validBody,
			map[string]int{"anonymous": 401, "reader": 403, "reporter": 403, "editor": 201, "admin": 201}},
		{"update", "PUT", func(id uuid.UUID) string { return "/api/v1/blogs/" + id.String() }, validBody,
			map[string]int{"anonymous": 401, "reader": 403, "reporter": 403, "editor": 200, "admin": 200}},
		{"delete", "DELETE", func(id uuid.UUID) string { return "/api/v1/blogs/" + id.String() }, nil,
			map[string]int{"anonymous": 401, "reader": 403, "reporter": 403, "editor": 403, "admin": 204}},
		{"export", "GET", func(uuid.UUID) string { return "/api/v1/reports/blogs" }, nil,
			map[string]int{"anonymous": 401, "reader": 403, "reporter": 200, "editor": 403, "admin": 200}},
	}
	for _, ep := range endpoints {
		for _, r := range roles {
			h := newHarness(t)
			b := h.seed("seed")
			w := h.do(ep.method, ep.path(b.ID), ep.body, r.opt(h)...)
			if w.Code != ep.want[r.name] {
				t.Errorf("%s as %s: status %d, want %d (%s)", ep.name, r.name, w.Code, ep.want[r.name], w.Body.String())
			}
			if w.Code == 401 || w.Code == 403 {
				want := map[int]string{401: "UNAUTHENTICATED", 403: "FORBIDDEN"}[w.Code]
				if e := decodeErr(t, w); e.Error.Code != want {
					t.Errorf("%s as %s: code %s, want %s", ep.name, r.name, e.Error.Code, want)
				}
			}
		}
	}
}

func TestForbiddenCallerCannotMutate(t *testing.T) {
	h := newHarness(t)
	b := h.seed("keep")
	h.do("PUT", "/api/v1/blogs/"+b.ID.String(), map[string]any{"title": "X", "author": "A", "content": "C"}, as(h.reader))
	h.do("DELETE", "/api/v1/blogs/"+b.ID.String(), nil, as(h.editor))
	got, err := h.repo.GetActive(nil2ctx(), b.ID)
	if err != nil || got.Title != "keep" {
		t.Errorf("blog changed by unauthorized caller: %+v %v", got, err)
	}
}

// ---- Mock authentication ----

func TestMockAuth(t *testing.T) {
	h := newHarness(t)
	for _, v := range []string{"not-a-uuid", uuid.NewString(), uuid.Nil.String(), "  "} {
		w := h.do("GET", "/api/v1/blogs", nil, userHeader(v))
		if w.Code != 401 || decodeErr(t, w).Error.Code != "UNAUTHENTICATED" {
			t.Errorf("X-User-ID %q: status %d body %s, want 401", v, w.Code, w.Body.String())
		}
	}
	if w := h.do("GET", "/api/v1/blogs", nil); w.Code != 200 {
		t.Errorf("missing header must be anonymous: %d", w.Code)
	}
}

func TestMockAuthStorageFailureIs503(t *testing.T) {
	h := newHarness(t)
	h.users.Err = domain.ErrUnavailable
	w := h.do("GET", "/api/v1/blogs", nil, as(h.admin))
	if w.Code != 503 || decodeErr(t, w).Error.Code != "SERVICE_UNAVAILABLE" {
		t.Errorf("status %d: %s", w.Code, w.Body.String())
	}
}

func TestClientCannotChooseRole(t *testing.T) {
	h := newHarness(t)
	w := h.do("POST", "/api/v1/blogs", validBody, as(h.reader), func(r *http.Request) {
		r.Header.Set("X-User-Role", "admin")
		r.Header.Set("X-Role", "admin")
	})
	if w.Code != 403 {
		t.Errorf("role headers must be ignored: %d", w.Code)
	}
}

// ---- Create / update / delete behavior ----

func TestCreate(t *testing.T) {
	h := newHarness(t)
	w := h.do("POST", "/api/v1/blogs", validBody, as(h.editor))
	if w.Code != 201 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var b map[string]any
	json.Unmarshal(w.Body.Bytes(), &b)
	if loc := w.Header().Get("Location"); loc != "/api/v1/blogs/"+b["id"].(string) {
		t.Errorf("Location = %q", loc)
	}
	if b["created_by"] != h.editor.ID.String() || b["updated_by"] != h.editor.ID.String() ||
		b["views"].(float64) != 0 || b["version"].(float64) != 1 ||
		b["created_at"] != "2026-11-05T10:00:00Z" || b["updated_at"] != "2026-11-05T10:00:00Z" {
		t.Errorf("bad body: %v", b)
	}
	if _, leaked := b["deleted_at"]; leaked {
		t.Error("deleted_at must not be exposed")
	}
}

func TestCreateValidation(t *testing.T) {
	h := newHarness(t)
	tests := []struct {
		name  string
		body  any
		field string
	}{
		{"missing title", map[string]any{"author": "a", "content": "c"}, "title"},
		{"blank title", map[string]any{"title": "  ", "author": "a", "content": "c"}, "title"},
		{"long title", map[string]any{"title": strings.Repeat("x", 256), "author": "a", "content": "c"}, "title"},
		{"missing author", map[string]any{"title": "t", "content": "c"}, "author"},
		{"missing content", map[string]any{"title": "t", "author": "a"}, "content"},
		{"long description", map[string]any{"title": "t", "author": "a", "content": "c", "description": strings.Repeat("x", 501)}, "description"},
		{"wrong type", map[string]any{"title": 123, "author": "a", "content": "c"}, "title"},
		{"unknown field", map[string]any{"title": "t", "author": "a", "content": "c", "views": 99}, "views"},
		{"version not allowed on create", map[string]any{"title": "t", "author": "a", "content": "c", "version": 1}, "version"},
	}
	for _, tt := range tests {
		w := h.do("POST", "/api/v1/blogs", tt.body, as(h.editor))
		e := decodeErr(t, w)
		if w.Code != 400 || e.Error.Code != "VALIDATION_FAILED" || !hasDetail(e, tt.field) {
			t.Errorf("%s: status %d %s", tt.name, w.Code, w.Body.String())
		}
	}
	if len(h.repo.Order) != 0 {
		t.Error("invalid requests must not create blogs")
	}
}

func TestMalformedBodies(t *testing.T) {
	h := newHarness(t)
	tests := []struct {
		name string
		body string
	}{
		{"malformed JSON", `{"title":`},
		{"empty body", ``},
		{"trailing data", `{"title":"t","author":"a","content":"c"}{"x":1}`},
		{"JSON array", `[1,2]`},
		{"plain text", `hello`},
	}
	for _, tt := range tests {
		body := tt.body
		if body == "" { // empty string would be treated as nil body by the helper
			w := h.do("POST", "/api/v1/blogs", nil, as(h.editor), contentType("application/json"))
			if w.Code != 400 || decodeErr(t, w).Error.Code != "INVALID_JSON" {
				t.Errorf("%s: %d %s", tt.name, w.Code, w.Body.String())
			}
			continue
		}
		w := h.do("POST", "/api/v1/blogs", body, as(h.editor))
		if w.Code != 400 {
			t.Errorf("%s: status %d %s", tt.name, w.Code, w.Body.String())
		}
	}
}

func TestContentType(t *testing.T) {
	h := newHarness(t)
	for _, ct := range []string{"", "text/plain", "application/xml"} {
		w := h.do("POST", "/api/v1/blogs", validBody, as(h.editor), contentType(ct))
		if w.Code != 415 || decodeErr(t, w).Error.Code != "UNSUPPORTED_MEDIA_TYPE" {
			t.Errorf("Content-Type %q: %d %s", ct, w.Code, w.Body.String())
		}
	}
	if w := h.do("POST", "/api/v1/blogs", validBody, as(h.editor), contentType("application/json; charset=utf-8")); w.Code != 201 {
		t.Errorf("charset param must be accepted: %d", w.Code)
	}
}

func TestPayloadTooLarge(t *testing.T) {
	h := newHarness(t)
	big := fmt.Sprintf(`{"title":"t","author":"a","content":"%s"}`, strings.Repeat("x", 1<<20+10))
	for _, path := range []struct{ method, url string }{
		{"POST", "/api/v1/blogs"},
		{"PUT", "/api/v1/blogs/" + h.seed("x").ID.String()},
	} {
		w := h.do(path.method, path.url, big, as(h.editor))
		if w.Code != 413 || decodeErr(t, w).Error.Code != "PAYLOAD_TOO_LARGE" {
			t.Errorf("%s: status %d %.200s", path.method, w.Code, w.Body.String())
		}
	}
}

func TestInvalidUUID(t *testing.T) {
	h := newHarness(t)
	for _, id := range []string{"abc", "123", "urn:uuid:" + uuid.NewString(), uuid.NewString()[:35], strings.ReplaceAll(uuid.NewString(), "-", "")} {
		for _, c := range []struct {
			method string
			actor  domain.Actor
			body   any
		}{
			{"GET", domain.Anonymous(), nil},
			{"PUT", h.editor, validBody},
			{"DELETE", h.admin, nil},
		} {
			opts := []reqOpt{}
			if !c.actor.IsAnonymous() {
				opts = append(opts, as(c.actor))
			}
			w := h.do(c.method, "/api/v1/blogs/"+id, c.body, opts...)
			e := decodeErr(t, w)
			if w.Code != 400 || e.Error.Code != "VALIDATION_FAILED" || !hasDetail(e, "id") {
				t.Errorf("%s %q: status %d %s", c.method, id, w.Code, w.Body.String())
			}
		}
	}
}

func TestUpdate(t *testing.T) {
	h := newHarness(t)
	b := h.seed("orig")
	h.clk.Advance(time.Hour)
	w := h.do("PUT", "/api/v1/blogs/"+b.ID.String(),
		map[string]any{"title": "New", "author": "N", "content": "NC", "description": ""}, as(h.admin))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	json.Unmarshal(w.Body.Bytes(), &got)
	if got["title"] != "New" || got["author"] != "N" || got["description"] != "" ||
		got["updated_by"] != h.admin.ID.String() || got["created_by"] != h.editor.ID.String() ||
		got["version"].(float64) != 2 || got["updated_at"] == got["created_at"] {
		t.Errorf("bad update: %v", got)
	}
}

func TestUpdateMissingAndOptimisticVersion(t *testing.T) {
	h := newHarness(t)
	if w := h.do("PUT", "/api/v1/blogs/"+uuid.NewString(), validBody, as(h.editor)); w.Code != 404 {
		t.Errorf("missing: %d", w.Code)
	}
	b := h.seed("v")
	path := "/api/v1/blogs/" + b.ID.String()
	withVersion := func(v any) map[string]any {
		return map[string]any{"title": "t", "author": "a", "content": "c", "version": v}
	}
	if w := h.do("PUT", path, withVersion(1), as(h.editor)); w.Code != 200 {
		t.Errorf("matching version: %d %s", w.Code, w.Body.String())
	}
	if w := h.do("PUT", path, withVersion(1), as(h.editor)); w.Code != 409 || decodeErr(t, w).Error.Code != "VERSION_CONFLICT" {
		t.Errorf("stale version: %d %s", w.Code, w.Body.String())
	}
	for _, bad := range []any{0, -3, "x"} {
		if w := h.do("PUT", path, withVersion(bad), as(h.editor)); w.Code != 400 {
			t.Errorf("version %v: %d", bad, w.Code)
		}
	}
}

func TestSoftDeleteFlow(t *testing.T) {
	h := newHarness(t)
	keep, gone := h.seed("keep"), h.seed("gone")
	goneURL := "/api/v1/blogs/" + gone.ID.String()

	if w := h.do("DELETE", goneURL, nil, as(h.admin)); w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("delete: %d %q", w.Code, w.Body.String())
	}
	if w := h.do("GET", goneURL, nil); w.Code != 404 {
		t.Errorf("detail after delete: %d", w.Code)
	}
	if w := h.do("DELETE", goneURL, nil, as(h.admin)); w.Code != 404 {
		t.Errorf("double delete: %d", w.Code)
	}
	if w := h.do("PUT", goneURL, validBody, as(h.editor)); w.Code != 404 {
		t.Errorf("update after delete must not revive: %d", w.Code)
	}
	list := h.do("GET", "/api/v1/blogs", nil)
	if strings.Contains(list.Body.String(), gone.ID.String()) || !strings.Contains(list.Body.String(), keep.ID.String()) {
		t.Errorf("list: %s", list.Body.String())
	}
	csvResp := h.do("GET", "/api/v1/reports/blogs", nil, as(h.reporter))
	if strings.Contains(csvResp.Body.String(), gone.ID.String()) || !strings.Contains(csvResp.Body.String(), keep.ID.String()) {
		t.Errorf("csv: %s", csvResp.Body.String())
	}
	if w := h.do("DELETE", "/api/v1/blogs/"+uuid.NewString(), nil, as(h.admin)); w.Code != 404 {
		t.Errorf("delete missing: %d", w.Code)
	}
}

// ---- List / pagination ----

func TestListPagination(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 5; i++ {
		h.seed(fmt.Sprintf("b%d", i))
	}
	type page struct {
		Items []struct {
			ID, Title, Description string
			Content                *string `json:"content"`
		} `json:"items"`
		Page, Limit int
		HasNext     bool `json:"has_next"`
	}
	get := func(q string) (page, int) {
		w := h.do("GET", "/api/v1/blogs"+q, nil)
		var p page
		json.Unmarshal(w.Body.Bytes(), &p)
		return p, w.Code
	}
	if p, code := get(""); code != 200 || p.Page != 1 || p.Limit != 20 || len(p.Items) != 5 || p.HasNext {
		t.Errorf("defaults: %+v %d", p, code)
	}
	if p, _ := get("?page=1&limit=2"); len(p.Items) != 2 || !p.HasNext || p.Items[0].Title != "b4" {
		t.Errorf("page1: %+v", p)
	}
	if p, _ := get("?page=3&limit=2"); len(p.Items) != 1 || p.HasNext {
		t.Errorf("last page: %+v", p)
	}
	if p, code := get("?page=99&limit=2"); code != 200 || len(p.Items) != 0 || p.HasNext {
		t.Errorf("empty page: %+v %d", p, code)
	}
	if p, _ := get("?limit=100"); p.Limit != 100 {
		t.Errorf("limit 100 allowed: %+v", p)
	}
	if p, _ := get("?limit=5"); len(p.Items) != 5 || p.HasNext {
		t.Errorf("exact fit: %+v", p)
	}
	if p, _ := get("?limit=2"); p.Items[0].Content != nil {
		t.Error("list must not include content")
	}
	for _, q := range []string{"?page=0", "?page=-1", "?page=abc", "?limit=0", "?limit=101", "?limit=-5", "?limit=x", "?page=1.5"} {
		w := h.do("GET", "/api/v1/blogs"+q, nil)
		if w.Code != 400 || decodeErr(t, w).Error.Code != "VALIDATION_FAILED" {
			t.Errorf("%s: %d %s", q, w.Code, w.Body.String())
		}
	}
}

func TestListEmpty(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/api/v1/blogs", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Errorf("empty list must be [] not null: %d %s", w.Code, w.Body.String())
	}
}

// ---- Views over HTTP ----

func viewsOf(t *testing.T, w *httptest.ResponseRecorder) int {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var b struct{ Views int }
	json.Unmarshal(w.Body.Bytes(), &b)
	return b.Views
}

func TestDetailRecordsUniqueDailyView(t *testing.T) {
	h := newHarness(t)
	b := h.seed("v")
	url := "/api/v1/blogs/" + b.ID.String()

	if v := viewsOf(t, h.do("GET", url, nil, from("1.1.1.1:1000"))); v != 1 {
		t.Errorf("first view: %d", v)
	}
	for i := 0; i < 3; i++ {
		if v := viewsOf(t, h.do("GET", url, nil, from("1.1.1.1:"+fmt.Sprint(2000+i)))); v != 1 {
			t.Errorf("repeat (new source port) must not count: %d", v)
		}
	}
	if v := viewsOf(t, h.do("GET", url, nil, from("2.2.2.2:1"))); v != 2 {
		t.Errorf("different IP: %d", v)
	}
	h.clk.Advance(24 * time.Hour)
	if v := viewsOf(t, h.do("GET", url, nil, from("1.1.1.1:1"))); v != 3 {
		t.Errorf("next UTC day: %d", v)
	}
}

func TestListDoesNotCountViews(t *testing.T) {
	h := newHarness(t)
	b := h.seed("v")
	for i := 0; i < 5; i++ {
		h.do("GET", "/api/v1/blogs", nil, from("1.1.1.1:1"))
	}
	if h.repo.Blogs[b.ID].Views != 0 {
		t.Error("list must not increase views")
	}
}

func TestDetailOfMissingRecordsNoView(t *testing.T) {
	h := newHarness(t)
	if w := h.do("GET", "/api/v1/blogs/"+uuid.NewString(), nil); w.Code != 404 || decodeErr(t, w).Error.Code != "NOT_FOUND" {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
}

func TestDetailStorageFailureIs503NotSilent(t *testing.T) {
	h := newHarness(t)
	b := h.seed("v")
	h.views.Err = domain.ErrUnavailable
	w := h.do("GET", "/api/v1/blogs/"+b.ID.String(), nil)
	if w.Code != 503 || decodeErr(t, w).Error.Code != "SERVICE_UNAVAILABLE" {
		t.Errorf("must fail rather than return 200 with a lost view: %d %s", w.Code, w.Body.String())
	}
	h.views.Err = errors.New("boom")
	w = h.do("GET", "/api/v1/blogs/"+b.ID.String(), nil)
	if w.Code != 500 || strings.Contains(w.Body.String(), "boom") {
		t.Errorf("unclassified error: %d %s (must be generic 500)", w.Code, w.Body.String())
	}
}

func TestViewIPResolutionBehindProxy(t *testing.T) {
	h := newHarness(t, "10.0.0.0/8")
	b := h.seed("proxy")
	url := "/api/v1/blogs/" + b.ID.String()
	proxy := from("10.0.0.1:443")

	if v := viewsOf(t, h.do("GET", url, nil, proxy, xff("198.51.100.7"))); v != 1 {
		t.Errorf("client via proxy: %d", v)
	}
	// Same client through another proxy hop: still the same visitor.
	if v := viewsOf(t, h.do("GET", url, nil, from("10.0.0.2:443"), xff("198.51.100.7, 10.0.0.9"))); v != 1 {
		t.Errorf("same client, extra hop: %d", v)
	}
	// Forged left-most entry must not create a new visitor.
	if v := viewsOf(t, h.do("GET", url, nil, proxy, xff("6.6.6.6, 198.51.100.7"))); v != 1 {
		t.Errorf("forged left-most XFF created a view: %d", v)
	}
	if v := viewsOf(t, h.do("GET", url, nil, proxy, xff("203.0.113.1"))); v != 2 {
		t.Errorf("different client via proxy: %d", v)
	}
	// Untrusted peer: XFF is ignored, so rotating it cannot inflate views.
	for _, f := range []string{"1.0.0.1", "1.0.0.2", "1.0.0.3"} {
		h.do("GET", url, nil, from("203.0.113.50:1"), xff(f))
	}
	if h.repo.Blogs[b.ID].Views != 3 {
		t.Errorf("forged XFF from untrusted peer inflated views: %d, want 3", h.repo.Blogs[b.ID].Views)
	}
}

func TestXFFIgnoredWithoutTrustedProxies(t *testing.T) {
	h := newHarness(t)
	b := h.seed("v")
	url := "/api/v1/blogs/" + b.ID.String()
	for i := 0; i < 5; i++ {
		h.do("GET", url, nil, from("203.0.113.9:1"), xff(fmt.Sprintf("9.9.9.%d", i)))
	}
	if h.repo.Blogs[b.ID].Views != 1 {
		t.Errorf("views=%d, want 1", h.repo.Blogs[b.ID].Views)
	}
}

// ---- Error envelope & routing ----

func TestErrorEnvelopeAndRequestID(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/api/v1/blogs/bad", nil, func(r *http.Request) { r.Header.Set("X-Request-ID", "req-123") })
	e := decodeErr(t, w)
	if e.Error.RequestID != "req-123" || w.Header().Get("X-Request-ID") != "req-123" {
		t.Errorf("request id not propagated: %+v hdr=%q", e, w.Header().Get("X-Request-ID"))
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("Content-Type = %q", w.Header().Get("Content-Type"))
	}

	w = h.do("GET", "/api/v1/blogs", nil, func(r *http.Request) { r.Header.Set("X-Request-ID", "bad id\nwith newline") })
	if id := w.Header().Get("X-Request-ID"); id == "" || strings.ContainsAny(id, " \n") {
		t.Errorf("unsafe request id must be replaced, got %q", id)
	}
	a := h.do("GET", "/healthz", nil).Header().Get("X-Request-ID")
	b := h.do("GET", "/healthz", nil).Header().Get("X-Request-ID")
	if a == "" || a == b {
		t.Errorf("generated ids must be unique: %q %q", a, b)
	}
}

func TestUnknownRouteAndMethod(t *testing.T) {
	h := newHarness(t)
	if w := h.do("GET", "/nope", nil); w.Code != 404 || decodeErr(t, w).Error.Code != "NOT_FOUND" {
		t.Errorf("unknown route: %d %s", w.Code, w.Body.String())
	}
	if w := h.do("PATCH", "/api/v1/blogs", nil); w.Code != 405 || decodeErr(t, w).Error.Code != "METHOD_NOT_ALLOWED" {
		t.Errorf("method: %d %s", w.Code, w.Body.String())
	}
}

func TestHealthEndpointsNeedNoAuth(t *testing.T) {
	h := newHarness(t)
	for _, p := range []string{"/healthz", "/readyz"} {
		if w := h.do("GET", p, nil, userHeader("garbage")); w.Code != 200 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
}

// ---- CSV export ----

func parseCSV(t *testing.T, body string) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("invalid CSV: %v\n%s", err, body)
	}
	return rows
}

func TestCSVFormat(t *testing.T) {
	h := newHarness(t)
	a := h.seed("First")
	h.seed("Second")
	h.views.RecordUniqueView(nil2ctx(), domain.NewViewKey(a.ID, h.clk.Now(), domain.IPHash{1}), h.clk.Now())

	for _, actor := range []domain.Actor{h.admin, h.reporter} {
		w := h.do("GET", "/api/v1/reports/blogs", nil, as(actor))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", actor.Role, w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); ct != "text/csv; charset=utf-8" {
			t.Errorf("Content-Type = %q", ct)
		}
		if cd := w.Header().Get("Content-Disposition"); cd != `attachment; filename="blogs-20261105.csv"` {
			t.Errorf("Content-Disposition = %q", cd)
		}
		rows := parseCSV(t, w.Body.String())
		wantHeader := "id,title,author,description,views,created_at,updated_at,created_by,updated_by"
		if strings.Join(rows[0], ",") != wantHeader {
			t.Errorf("header = %v", rows[0])
		}
		if len(rows) != 3 {
			t.Fatalf("rows = %d, want header + 2", len(rows))
		}
		if rows[1][1] != "Second" || rows[2][1] != "First" { // newest first
			t.Errorf("order: %v / %v", rows[1], rows[2])
		}
		first := rows[2]
		if first[0] != a.ID.String() || first[2] != "Author" || first[3] != "Desc" || first[4] != "1" ||
			first[5] != "2026-11-05T10:00:01Z" || first[7] != h.editor.ID.String() || first[8] != h.editor.ID.String() {
			t.Errorf("row: %v", first)
		}
		for _, row := range rows {
			if len(row) != 9 {
				t.Errorf("row has %d columns: %v", len(row), row)
			}
		}
	}
}

func TestCSVEmptyReportHasHeader(t *testing.T) {
	h := newHarness(t)
	w := h.do("GET", "/api/v1/reports/blogs", nil, as(h.admin))
	rows := parseCSV(t, w.Body.String())
	if w.Code != 200 || len(rows) != 1 || rows[0][0] != "id" {
		t.Errorf("%d %v", w.Code, rows)
	}
}

func TestCSVInjectionAndEscaping(t *testing.T) {
	h := newHarness(t)
	svc := app.NewBlogService(h.repo, h.clk)
	for _, title := range []string{"=HYPERLINK(\"http://evil\")", "+1+1", "-2+3", "@SUM(A1)", "safe-title"} {
		h.clk.Advance(time.Second)
		if _, err := svc.Create(nil2ctx(), h.editor, domain.BlogInput{Title: title, Author: "=cmd", Content: "c", Description: "-x"}); err != nil {
			t.Fatal(err)
		}
	}
	h.clk.Advance(time.Second)
	svc.Create(nil2ctx(), h.editor, domain.BlogInput{Title: "has, comma \"quote\"\nnewline", Author: "a", Content: "c"})

	rows := parseCSV(t, h.do("GET", "/api/v1/reports/blogs", nil, as(h.admin)).Body.String())
	titles := map[string]bool{}
	for _, r := range rows[1:] {
		titles[r[1]] = true
		if strings.ContainsAny(r[1][:1], "=+-@\t\r") || strings.ContainsAny(r[2][:1], "=+-@\t\r") ||
			(r[3] != "" && strings.ContainsAny(r[3][:1], "=+-@\t\r")) {
			t.Errorf("formula-like cell not neutralized: %q %q %q", r[1], r[2], r[3])
		}
	}
	for _, want := range []string{"'=HYPERLINK(\"http://evil\")", "'+1+1", "'-2+3", "'@SUM(A1)", "safe-title", "has, comma \"quote\"\nnewline"} {
		if !titles[want] {
			t.Errorf("missing cell %q in %v", want, titles)
		}
	}
}

func TestSanitizedCSVUsesUTCRFC3339(t *testing.T) {
	h := newHarness(t)
	h.clk.Set(time.Date(2026, 11, 5, 23, 0, 0, 0, time.FixedZone("UTC-5", -5*3600))) // = 2026-11-06T04:00Z
	h.seed("tz")
	rows := parseCSV(t, h.do("GET", "/api/v1/reports/blogs", nil, as(h.admin)).Body.String())
	if !strings.HasSuffix(rows[1][5], "Z") || !strings.HasPrefix(rows[1][5], "2026-11-06T04:00:01") {
		t.Errorf("created_at = %q", rows[1][5])
	}
}

func TestCSVStreamFailureAbortsConnection(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 5; i++ {
		h.seed(fmt.Sprintf("b%d", i))
	}
	h.repo.StreamFailAfter = 2
	srv := httptest.NewServer(h.router)
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/reports/blogs", nil)
	req.Header.Set("X-User-ID", h.admin.ID.String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return // connection aborted before any byte: the client clearly sees a failure
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Error("a failed export must not look like a complete, successful transfer")
	}
}

func TestCSVStorageFailureBeforeFirstRowIsJSON503(t *testing.T) {
	h := newHarness(t)
	h.seed("x")
	h.repo.StreamErr = domain.ErrUnavailable
	w := h.do("GET", "/api/v1/reports/blogs", nil, as(h.admin))
	if w.Code != 503 || decodeErr(t, w).Error.Code != "SERVICE_UNAVAILABLE" || strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
}

// ---- Misc safety ----

func TestPanicIsRecoveredAs500Envelope(t *testing.T) {
	h := newHarness(t)
	// Nil services make the handler panic; Recover must turn it into a 500 envelope.
	r := transport.NewRouter(transport.Deps{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: okDB{}, Users: h.users,
		Blogs: nil, Views: nil, Reports: nil, ClientIP: clientip.New(nil), Clock: h.clk,
	})
	req := httptest.NewRequest("GET", "/api/v1/blogs", nil)
	req.RemoteAddr = "192.0.2.1:1"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 500 || decodeErr(t, w).Error.Code != "INTERNAL_ERROR" {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
}
