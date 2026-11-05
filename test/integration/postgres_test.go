package integration

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/adapter/postgres"
	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/domain"
	"github.com/sky/blog-service/internal/platform/clock"
)

type env struct {
	blogs  *postgres.BlogRepo
	views  *postgres.ViewStore
	users  *postgres.UserRepo
	admin  uuid.UUID
	editor uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	if skipReason != "" {
		t.Skip(skipReason)
	}
	e := &env{
		blogs: postgres.NewBlogRepo(pool),
		views: postgres.NewViewStore(pool),
		users: postgres.NewUserRepo(pool),
	}
	e.admin = userID(t, "admin")
	e.editor = userID(t, "editor")
	return e
}

func userID(t *testing.T, role string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT id FROM users WHERE role = $1`, role).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", role, err)
	}
	return id
}

func resetBlogs(t *testing.T) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `TRUNCATE blog_daily_views, blogs`); err != nil {
		t.Fatal(err)
	}
}

var ctx = context.Background()

func us(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

func (e *env) newBlog(t *testing.T, title string, at time.Time) domain.Blog {
	t.Helper()
	id, _ := uuid.NewV7()
	b, err := e.blogs.Create(ctx, domain.Blog{
		ID: id, Title: title, Author: "Author", Content: "Content", Description: "Desc",
		CreatedAt: us(at), UpdatedAt: us(at), CreatedBy: e.editor, UpdatedBy: e.editor,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return b
}

func ipHash(n int) domain.IPHash {
	var h domain.IPHash
	h[0], h[1], h[2], h[3] = byte(n>>24), byte(n>>16), byte(n>>8), byte(n)
	return h
}

func viewRows(t *testing.T, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM blog_daily_views WHERE blog_id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func viewsCounter(t *testing.T, id uuid.UUID) int64 {
	t.Helper()
	var v int64
	if err := pool.QueryRow(ctx, `SELECT views FROM blogs WHERE id = $1`, id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// assertInvariant checks blogs.views == COUNT(blog_daily_views) for the blog.
func assertInvariant(t *testing.T, id uuid.UUID) {
	t.Helper()
	if c, r := viewsCounter(t, id), viewRows(t, id); c != int64(r) {
		t.Errorf("invariant broken: blogs.views=%d, dedup rows=%d", c, r)
	}
}

func TestCRUDAndAudit(t *testing.T) {
	e := newEnv(t)
	t0 := time.Now()
	b := e.newBlog(t, "First", t0)

	if b.Version != 1 || b.Views != 0 || b.CreatedBy != e.editor || b.UpdatedBy != e.editor || b.DeletedAt != nil {
		t.Fatalf("bad created blog: %+v", b)
	}
	got, err := e.blogs.GetActive(ctx, b.ID)
	if err != nil || got.Title != "First" || !got.CreatedAt.Equal(us(t0)) {
		t.Fatalf("get: %+v %v", got, err)
	}

	t1 := t0.Add(time.Minute)
	upd, err := e.blogs.UpdateActive(ctx, app.BlogUpdate{
		ID: b.ID, At: us(t1), By: e.admin,
		Input: domain.BlogInput{Title: "Second", Author: "B", Content: "C2", Description: ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	if upd.Title != "Second" || upd.Author != "B" || upd.Description != "" || upd.Version != 2 ||
		upd.UpdatedBy != e.admin || upd.CreatedBy != e.editor ||
		!upd.UpdatedAt.Equal(us(t1)) || !upd.CreatedAt.Equal(us(t0)) {
		t.Errorf("bad update audit: %+v", upd)
	}

	t2 := t1.Add(time.Minute)
	if err := e.blogs.DeleteActive(ctx, b.ID, us(t2), e.admin); err != nil {
		t.Fatal(err)
	}
	var delAt time.Time
	var delBy uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT deleted_at, deleted_by FROM blogs WHERE id=$1`, b.ID).Scan(&delAt, &delBy); err != nil {
		t.Fatal(err)
	}
	if !delAt.Equal(us(t2)) || delBy != e.admin {
		t.Errorf("bad delete audit: %v %v", delAt, delBy)
	}

	if _, err := e.blogs.GetActive(ctx, b.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get after delete: %v", err)
	}
	if err := e.blogs.DeleteActive(ctx, b.ID, us(t2), e.admin); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("double delete: %v", err)
	}
	// An update must never revive a soft-deleted blog.
	_, err = e.blogs.UpdateActive(ctx, app.BlogUpdate{
		ID: b.ID, At: us(t2), By: e.editor,
		Input: domain.BlogInput{Title: "Zombie", Author: "x", Content: "x"},
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("update after delete: %v", err)
	}
	var title string
	pool.QueryRow(ctx, `SELECT title FROM blogs WHERE id=$1`, b.ID).Scan(&title)
	if title != "Second" {
		t.Errorf("deleted row was modified: title=%q", title)
	}
}

func TestUpdateMissingAndVersionConflict(t *testing.T) {
	e := newEnv(t)
	b := e.newBlog(t, "V", time.Now())
	in := domain.BlogInput{Title: "n", Author: "a", Content: "c"}

	_, err := e.blogs.UpdateActive(ctx, app.BlogUpdate{ID: uuid.New(), Input: in, At: us(time.Now()), By: e.editor})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	ver := 1
	if _, err := e.blogs.UpdateActive(ctx, app.BlogUpdate{ID: b.ID, Input: in, At: us(time.Now()), By: e.editor, ExpectedVersion: &ver}); err != nil {
		t.Fatalf("matching version: %v", err)
	}
	if _, err := e.blogs.UpdateActive(ctx, app.BlogUpdate{ID: b.ID, Input: in, At: us(time.Now()), By: e.editor, ExpectedVersion: &ver}); !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("stale version: %v", err)
	}
	// Missing blog with an expected version is still NotFound, not Conflict.
	if _, err := e.blogs.UpdateActive(ctx, app.BlogUpdate{ID: uuid.New(), Input: in, At: us(time.Now()), By: e.editor, ExpectedVersion: &ver}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing with version: %v", err)
	}
}

func TestUpdateConcurrentVersionOptimistic(t *testing.T) {
	e := newEnv(t)
	b := e.newBlog(t, "Race", time.Now())
	in := domain.BlogInput{Title: "n", Author: "a", Content: "c"}
	ver := 1
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, conflict := 0, 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.blogs.UpdateActive(ctx, app.BlogUpdate{ID: b.ID, Input: in, At: us(time.Now()), By: e.editor, ExpectedVersion: &ver})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, domain.ErrVersionConflict):
				conflict++
			default:
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || conflict != 19 {
		t.Errorf("exactly one writer must win: ok=%d conflict=%d", ok, conflict)
	}
}

func TestListActiveOrderPaginationAndDeleted(t *testing.T) {
	e := newEnv(t)
	resetBlogs(t)
	base := time.Now().Add(-time.Hour)
	var ids []uuid.UUID
	for i := 0; i < 5; i++ {
		ids = append(ids, e.newBlog(t, fmt.Sprintf("b%d", i), base.Add(time.Duration(i)*time.Second)).ID)
	}
	if err := e.blogs.DeleteActive(ctx, ids[4], us(time.Now()), e.admin); err != nil { // newest, deleted
		t.Fatal(err)
	}

	all, err := e.blogs.ListActive(ctx, 10, 0)
	if err != nil || len(all) != 4 {
		t.Fatalf("list: %d %v", len(all), err)
	}
	for i, want := range []uuid.UUID{ids[3], ids[2], ids[1], ids[0]} { // newest first
		if all[i].ID != want {
			t.Errorf("order[%d] = %s, want %s", i, all[i].Title, want)
		}
	}
	page2, _ := e.blogs.ListActive(ctx, 2, 2)
	if len(page2) != 2 || page2[0].ID != ids[1] || page2[1].ID != ids[0] {
		t.Errorf("page2: %+v", page2)
	}
	empty, err := e.blogs.ListActive(ctx, 2, 100)
	if err != nil || len(empty) != 0 {
		t.Errorf("beyond end: %v %v", empty, err)
	}
}

func TestListTieBreakByIDIsStable(t *testing.T) {
	e := newEnv(t)
	resetBlogs(t)
	same := us(time.Now().Add(-time.Hour))
	for i := 0; i < 6; i++ {
		e.newBlog(t, fmt.Sprintf("t%d", i), same)
	}
	a, _ := e.blogs.ListActive(ctx, 6, 0)
	p1, _ := e.blogs.ListActive(ctx, 3, 0)
	p2, _ := e.blogs.ListActive(ctx, 3, 3)
	joined := append(p1, p2...)
	for i := range a {
		if a[i].ID != joined[i].ID {
			t.Fatalf("pagination unstable at %d with identical created_at", i)
		}
	}
}

func TestStreamActiveExcludesDeleted(t *testing.T) {
	e := newEnv(t)
	resetBlogs(t)
	base := time.Now().Add(-time.Hour)
	a := e.newBlog(t, "a", base)
	b := e.newBlog(t, "b", base.Add(time.Second))
	e.blogs.DeleteActive(ctx, a.ID, us(time.Now()), e.admin)

	var got []app.ReportRow
	if err := e.blogs.StreamActive(ctx, func(r app.ReportRow) error { got = append(got, r); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != b.ID || got[0].CreatedBy != e.editor || got[0].Author != "Author" {
		t.Errorf("stream: %+v", got)
	}

	stop := errors.New("stop")
	e.newBlog(t, "c", base.Add(2*time.Second))
	calls := 0
	err := e.blogs.StreamActive(ctx, func(app.ReportRow) error { calls++; return stop })
	if !errors.Is(err, stop) || calls != 1 {
		t.Errorf("callback error must stop the stream: calls=%d err=%v", calls, err)
	}
}

func TestRecordUniqueViewSameIPConcurrent(t *testing.T) {
	e := newEnv(t)
	b := e.newBlog(t, "same-ip", time.Now())
	key := domain.NewViewKey(b.ID, time.Now(), ipHash(1))

	var wg sync.WaitGroup
	var mu sync.Mutex
	counted := 0
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := e.views.RecordUniqueView(ctx, key, us(time.Now()))
			if err != nil {
				t.Errorf("record: %v", err)
				return
			}
			if c {
				mu.Lock()
				counted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if counted != 1 || viewRows(t, b.ID) != 1 || viewsCounter(t, b.ID) != 1 {
		t.Errorf("counted=%d rows=%d views=%d, want 1/1/1", counted, viewRows(t, b.ID), viewsCounter(t, b.ID))
	}
	assertInvariant(t, b.ID)
}

func TestRecordUniqueViewDifferentIPsConcurrent(t *testing.T) {
	e := newEnv(t)
	b := e.newBlog(t, "diff-ip", time.Now())
	now := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c, err := e.views.RecordUniqueView(ctx, domain.NewViewKey(b.ID, now, ipHash(n)), us(now))
			if err != nil || !c {
				t.Errorf("ip %d: counted=%v err=%v", n, c, err)
			}
		}(i)
	}
	wg.Wait()
	if viewRows(t, b.ID) != 100 || viewsCounter(t, b.ID) != 100 {
		t.Errorf("rows=%d views=%d, want 100/100", viewRows(t, b.ID), viewsCounter(t, b.ID))
	}
	assertInvariant(t, b.ID)
}

func TestRecordUniqueViewMixedConcurrentKeepsInvariant(t *testing.T) {
	e := newEnv(t)
	b := e.newBlog(t, "mixed", time.Now())
	now := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 300; i++ { // 30 distinct IPs, each hit 10 times
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if _, err := e.views.RecordUniqueView(ctx, domain.NewViewKey(b.ID, now, ipHash(n%30)), us(now)); err != nil {
				t.Errorf("record: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if viewsCounter(t, b.ID) != 30 {
		t.Errorf("views=%d, want 30", viewsCounter(t, b.ID))
	}
	assertInvariant(t, b.ID)
}

func TestRecordUniqueViewAcrossDays(t *testing.T) {
	e := newEnv(t)
	b := e.newBlog(t, "days", time.Now())
	ip := ipHash(7)
	d1 := time.Date(2026, 11, 5, 23, 59, 59, 0, time.UTC)
	d2 := d1.Add(time.Second) // 00:00:00 next UTC day

	for _, step := range []struct {
		at   time.Time
		want bool
	}{{d1, true}, {d1, false}, {d2, true}, {d2, false}} {
		got, err := e.views.RecordUniqueView(ctx, domain.NewViewKey(b.ID, step.at, ip), us(step.at))
		if err != nil || got != step.want {
			t.Fatalf("at %v: counted=%v err=%v, want %v", step.at, got, err, step.want)
		}
	}
	if viewsCounter(t, b.ID) != 2 {
		t.Errorf("views=%d, want 2", viewsCounter(t, b.ID))
	}
	assertInvariant(t, b.ID)
}

func TestRecordUniqueViewMissingAndDeleted(t *testing.T) {
	e := newEnv(t)
	now := time.Now()
	if _, err := e.views.RecordUniqueView(ctx, domain.NewViewKey(uuid.New(), now, ipHash(1)), us(now)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	b := e.newBlog(t, "gone", now)
	e.blogs.DeleteActive(ctx, b.ID, us(now), e.admin)
	if _, err := e.views.RecordUniqueView(ctx, domain.NewViewKey(b.ID, now, ipHash(1)), us(now)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deleted: %v", err)
	}
	if viewRows(t, b.ID) != 0 || viewsCounter(t, b.ID) != 0 {
		t.Error("a deleted blog must not receive views")
	}
}

func TestUpdateDoesNotTouchViews(t *testing.T) {
	e := newEnv(t)
	b := e.newBlog(t, "keep", time.Now())
	now := time.Now()
	e.views.RecordUniqueView(ctx, domain.NewViewKey(b.ID, now, ipHash(1)), us(now))
	upd, err := e.blogs.UpdateActive(ctx, app.BlogUpdate{
		ID: b.ID, At: us(now), By: e.editor,
		Input: domain.BlogInput{Title: "t", Author: "a", Content: "c"},
	})
	if err != nil || upd.Views != 1 {
		t.Errorf("views after update: %+v %v", upd, err)
	}
}

// Full path through the real services: ViewService + repositories.
func TestViewServiceEndToEnd(t *testing.T) {
	e := newEnv(t)
	b := e.newBlog(t, "e2e", time.Now())
	clk := clock.NewFake(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC))
	svc := app.NewViewService(e.blogs, e.views, hashByAddr{}, clk)
	ip := netip.MustParseAddr("203.0.113.9")

	got, err := svc.Read(ctx, domain.Anonymous(), b.ID, ip)
	if err != nil || got.Views != 1 {
		t.Fatalf("first read: %+v %v", got, err)
	}
	got, _ = svc.Read(ctx, domain.Anonymous(), b.ID, ip)
	if got.Views != 1 {
		t.Errorf("repeat read views=%d, want 1", got.Views)
	}
	clk.Advance(24 * time.Hour)
	got, _ = svc.Read(ctx, domain.Anonymous(), b.ID, ip)
	if got.Views != 2 {
		t.Errorf("next day views=%d, want 2", got.Views)
	}
	assertInvariant(t, b.ID)
}

// hashByAddr is a deterministic test hasher (real HMAC hasher arrives in Phase 6).
type hashByAddr struct{}

func (hashByAddr) Hash(ip netip.Addr) domain.IPHash {
	var h domain.IPHash
	copy(h[:], ip.AsSlice())
	return h
}

func TestUserRepo(t *testing.T) {
	e := newEnv(t)
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleEditor, domain.RoleReader, domain.RoleReporter} {
		id := userID(t, string(role))
		a, err := e.users.GetByID(ctx, id)
		if err != nil || a.Role != role || a.ID != id {
			t.Errorf("%s: %+v %v", role, a, err)
		}
	}
	if _, err := e.users.GetByID(ctx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}
}

func TestUnavailableMapsToErrUnavailable(t *testing.T) {
	if skipReason != "" {
		t.Skip(skipReason)
	}
	dead, err := postgres.NewPool(ctx, "postgres://x:x@127.0.0.1:1/x?sslmode=disable&connect_timeout=2", 2)
	if err != nil {
		t.Fatal(err)
	}
	defer dead.Close()
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err = postgres.NewBlogRepo(dead).GetActive(cctx, uuid.New())
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Errorf("GetActive: %v, want ErrUnavailable", err)
	}
	_, err = postgres.NewViewStore(dead).RecordUniqueView(cctx, domain.NewViewKey(uuid.New(), time.Now(), ipHash(1)), time.Now())
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Errorf("RecordUniqueView: %v, want ErrUnavailable", err)
	}
}
