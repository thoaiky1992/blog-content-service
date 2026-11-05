package app

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/domain"
	"github.com/sky/blog-service/internal/platform/clock"
)

// fakeRepo is an in-memory BlogRepository.
type fakeRepo struct {
	blogs     map[uuid.UUID]*domain.Blog
	order     []uuid.UUID
	streamErr error
}

func newFakeRepo() *fakeRepo { return &fakeRepo{blogs: map[uuid.UUID]*domain.Blog{}} }

func (r *fakeRepo) Create(_ context.Context, b domain.Blog) (domain.Blog, error) {
	r.blogs[b.ID] = &b
	r.order = append(r.order, b.ID)
	return b, nil
}

func (r *fakeRepo) GetActive(_ context.Context, id uuid.UUID) (domain.Blog, error) {
	b, ok := r.blogs[id]
	if !ok || b.DeletedAt != nil {
		return domain.Blog{}, domain.ErrNotFound
	}
	return *b, nil
}

func (r *fakeRepo) ListActive(_ context.Context, limit, offset int) ([]domain.BlogSummary, error) {
	var out []domain.BlogSummary
	i := 0
	for _, id := range r.order {
		b := r.blogs[id]
		if b.DeletedAt != nil {
			continue
		}
		if i >= offset && len(out) < limit {
			out = append(out, domain.BlogSummary{ID: b.ID, Title: b.Title, Description: b.Description})
		}
		i++
	}
	return out, nil
}

func (r *fakeRepo) UpdateActive(_ context.Context, u BlogUpdate) (domain.Blog, error) {
	b, ok := r.blogs[u.ID]
	if !ok || b.DeletedAt != nil {
		return domain.Blog{}, domain.ErrNotFound
	}
	if u.ExpectedVersion != nil && *u.ExpectedVersion != b.Version {
		return domain.Blog{}, domain.ErrVersionConflict
	}
	b.Title, b.Author, b.Content, b.Description = u.Input.Title, u.Input.Author, u.Input.Content, u.Input.Description
	b.UpdatedAt, b.UpdatedBy, b.Version = u.At, u.By, b.Version+1
	return *b, nil
}

func (r *fakeRepo) DeleteActive(_ context.Context, id uuid.UUID, at time.Time, by uuid.UUID) error {
	b, ok := r.blogs[id]
	if !ok || b.DeletedAt != nil {
		return domain.ErrNotFound
	}
	b.DeletedAt, b.DeletedBy = &at, &by
	return nil
}

func (r *fakeRepo) StreamActive(_ context.Context, fn func(ReportRow) error) error {
	if r.streamErr != nil {
		return r.streamErr
	}
	for _, id := range r.order {
		b := r.blogs[id]
		if b.DeletedAt != nil {
			continue
		}
		if err := fn(ReportRow{ID: b.ID, Title: b.Title}); err != nil {
			return err
		}
	}
	return nil
}

// fakeViews dedups on the same key the real PK uses.
type fakeViews struct {
	repo *fakeRepo
	seen map[domain.ViewKey]bool
	err  error
}

func (v *fakeViews) RecordUniqueView(_ context.Context, key domain.ViewKey, _ time.Time) (bool, error) {
	if v.err != nil {
		return false, v.err
	}
	b, ok := v.repo.blogs[key.BlogID]
	if !ok || b.DeletedAt != nil {
		return false, domain.ErrNotFound
	}
	if v.seen[key] {
		return false, nil
	}
	v.seen[key] = true
	b.Views++
	return true, nil
}

// fakeHasher maps each IP to a distinct hash.
type fakeHasher struct{}

func (fakeHasher) Hash(ip netip.Addr) domain.IPHash {
	var h domain.IPHash
	copy(h[:], ip.AsSlice())
	return h
}

var (
	admin    = domain.Actor{ID: uuid.New(), Role: domain.RoleAdmin}
	editor   = domain.Actor{ID: uuid.New(), Role: domain.RoleEditor}
	reader   = domain.Actor{ID: uuid.New(), Role: domain.RoleReader}
	reporter = domain.Actor{ID: uuid.New(), Role: domain.RoleReporter}
	valid    = domain.BlogInput{Title: "T", Author: "A", Content: "C", Description: "D"}
	t0       = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
)

func TestCreateAudit(t *testing.T) {
	repo := newFakeRepo()
	svc := NewBlogService(repo, clock.NewFake(t0))
	b, err := svc.Create(context.Background(), editor, valid)
	if err != nil {
		t.Fatal(err)
	}
	if b.CreatedBy != editor.ID || b.UpdatedBy != editor.ID || !b.CreatedAt.Equal(t0) || !b.UpdatedAt.Equal(t0) || b.Version != 1 || b.Views != 0 {
		t.Errorf("bad audit fields: %+v", b)
	}
}

func TestCreateAuthAndValidation(t *testing.T) {
	svc := NewBlogService(newFakeRepo(), clock.NewFake(t0))
	ctx := context.Background()
	if _, err := svc.Create(ctx, reader, valid); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("reader create: %v", err)
	}
	if _, err := svc.Create(ctx, reporter, valid); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("reporter create: %v", err)
	}
	if _, err := svc.Create(ctx, domain.Anonymous(), valid); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Errorf("anonymous create: %v", err)
	}
	if _, err := svc.Create(ctx, admin, valid); err != nil {
		t.Errorf("admin create: %v", err)
	}
	var ve *domain.ValidationError
	if _, err := svc.Create(ctx, editor, domain.BlogInput{}); !errors.As(err, &ve) {
		t.Errorf("invalid input: %v", err)
	}
}

func TestUpdate(t *testing.T) {
	repo := newFakeRepo()
	clk := clock.NewFake(t0)
	svc := NewBlogService(repo, clk)
	ctx := context.Background()
	b, _ := svc.Create(ctx, editor, valid)

	clk.Advance(time.Hour)
	in := valid
	in.Title = "New"
	got, err := svc.Update(ctx, admin, b.ID, in, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "New" || got.UpdatedBy != admin.ID || got.CreatedBy != editor.ID || got.Version != 2 || !got.UpdatedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("bad update audit: %+v", got)
	}
	if _, err := svc.Update(ctx, reader, b.ID, in, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("reader update: %v", err)
	}
	if _, err := svc.Update(ctx, editor, uuid.New(), in, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing blog: %v", err)
	}
	stale := 1
	if _, err := svc.Update(ctx, editor, b.ID, in, &stale); !errors.Is(err, domain.ErrVersionConflict) {
		t.Errorf("stale version: %v", err)
	}
}

func TestDelete(t *testing.T) {
	repo := newFakeRepo()
	svc := NewBlogService(repo, clock.NewFake(t0))
	ctx := context.Background()
	b, _ := svc.Create(ctx, editor, valid)

	if err := svc.Delete(ctx, editor, b.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("editor delete: %v", err)
	}
	if err := svc.Delete(ctx, admin, b.ID); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
	if got := repo.blogs[b.ID]; got.DeletedBy == nil || *got.DeletedBy != admin.ID || !got.DeletedAt.Equal(t0) {
		t.Errorf("bad delete audit: %+v", got)
	}
	if err := svc.Delete(ctx, admin, b.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("double delete: %v", err)
	}
	if _, err := svc.Update(ctx, editor, b.ID, valid, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("update after delete must not revive: %v", err)
	}
}

func TestListPaginationAndHasNext(t *testing.T) {
	repo := newFakeRepo()
	svc := NewBlogService(repo, clock.NewFake(t0))
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		svc.Create(ctx, editor, valid)
	}

	p, err := svc.List(ctx, domain.Anonymous(), 1, 2)
	if err != nil || len(p.Items) != 2 || !p.HasNext {
		t.Errorf("page1: %+v %v", p, err)
	}
	p, _ = svc.List(ctx, domain.Anonymous(), 3, 2)
	if len(p.Items) != 1 || p.HasNext {
		t.Errorf("last page: %+v", p)
	}
	p, _ = svc.List(ctx, domain.Anonymous(), 9, 2)
	if len(p.Items) != 0 || p.HasNext {
		t.Errorf("empty page: %+v", p)
	}
	p, _ = svc.List(ctx, domain.Anonymous(), 1, 5)
	if len(p.Items) != 5 || p.HasNext {
		t.Errorf("exact fit must have no next: %+v", p)
	}
	var ve *domain.ValidationError
	if _, err := svc.List(ctx, domain.Anonymous(), 0, 20); !errors.As(err, &ve) {
		t.Errorf("page 0: %v", err)
	}
	if _, err := svc.List(ctx, domain.Anonymous(), 1, 101); !errors.As(err, &ve) {
		t.Errorf("limit 101: %v", err)
	}
}

func TestListHidesDeletedAndDoesNotCountViews(t *testing.T) {
	repo := newFakeRepo()
	clk := clock.NewFake(t0)
	blogs := NewBlogService(repo, clk)
	ctx := context.Background()
	a, _ := blogs.Create(ctx, editor, valid)
	b, _ := blogs.Create(ctx, editor, valid)
	blogs.Delete(ctx, admin, a.ID)

	p, _ := blogs.List(ctx, domain.Anonymous(), 1, 20)
	if len(p.Items) != 1 || p.Items[0].ID != b.ID {
		t.Errorf("deleted blog leaked into list: %+v", p.Items)
	}
	if repo.blogs[b.ID].Views != 0 {
		t.Error("list must not increase views")
	}
}

func newViewSvc() (*ViewService, *fakeRepo, *clock.Fake, domain.Blog) {
	repo := newFakeRepo()
	clk := clock.NewFake(t0)
	b, _ := NewBlogService(repo, clk).Create(context.Background(), editor, valid)
	views := &fakeViews{repo: repo, seen: map[domain.ViewKey]bool{}}
	return NewViewService(repo, views, fakeHasher{}, clk), repo, clk, b
}

func TestReadUniqueDailyView(t *testing.T) {
	svc, repo, clk, b := newViewSvc()
	ctx := context.Background()
	ip1, ip2 := netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("2.2.2.2")

	got, err := svc.Read(ctx, domain.Anonymous(), b.ID, ip1)
	if err != nil || got.Views != 1 {
		t.Fatalf("first read: views=%d err=%v", got.Views, err)
	}
	for i := 0; i < 3; i++ { // same IP, same day: no more views
		svc.Read(ctx, domain.Anonymous(), b.ID, ip1)
	}
	if repo.blogs[b.ID].Views != 1 {
		t.Errorf("same IP same day: views=%d, want 1", repo.blogs[b.ID].Views)
	}
	svc.Read(ctx, domain.Anonymous(), b.ID, ip2) // different IP: +1
	if repo.blogs[b.ID].Views != 2 {
		t.Errorf("different IP: views=%d, want 2", repo.blogs[b.ID].Views)
	}
	clk.Advance(24 * time.Hour) // next UTC day: +1 for same IP
	svc.Read(ctx, domain.Anonymous(), b.ID, ip1)
	if repo.blogs[b.ID].Views != 3 {
		t.Errorf("next day: views=%d, want 3", repo.blogs[b.ID].Views)
	}
}

func TestReadIPv4MappedIPv6SameAsIPv4(t *testing.T) {
	svc, repo, _, b := newViewSvc()
	ctx := context.Background()
	svc.Read(ctx, domain.Anonymous(), b.ID, netip.MustParseAddr("1.2.3.4"))
	svc.Read(ctx, domain.Anonymous(), b.ID, netip.MustParseAddr("::ffff:1.2.3.4"))
	if repo.blogs[b.ID].Views != 1 {
		t.Errorf("mapped IPv6 must dedup with IPv4: views=%d", repo.blogs[b.ID].Views)
	}
}

func TestReadMissingOrDeletedRecordsNoView(t *testing.T) {
	svc, repo, _, b := newViewSvc()
	ctx := context.Background()
	ip := netip.MustParseAddr("1.1.1.1")

	if _, err := svc.Read(ctx, domain.Anonymous(), uuid.New(), ip); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	now := t0
	repo.blogs[b.ID].DeletedAt = &now
	if _, err := svc.Read(ctx, domain.Anonymous(), b.ID, ip); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deleted: %v", err)
	}
	if repo.blogs[b.ID].Views != 0 {
		t.Error("deleted blog must not get views")
	}
}

func TestReadDeleteRaceBetweenGetAndRecord(t *testing.T) {
	svc, repo, _, b := newViewSvc()
	// GetActive succeeds, but the blog is deleted before RecordUniqueView runs.
	svc.repo = &deleteAfterGet{fakeRepo: repo}
	if _, err := svc.Read(context.Background(), domain.Anonymous(), b.ID, netip.MustParseAddr("1.1.1.1")); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("race: got %v, want ErrNotFound", err)
	}
}

type deleteAfterGet struct{ *fakeRepo }

func (d *deleteAfterGet) GetActive(ctx context.Context, id uuid.UUID) (domain.Blog, error) {
	b, err := d.fakeRepo.GetActive(ctx, id)
	now := t0
	d.fakeRepo.blogs[id].DeletedAt = &now
	return b, err
}

func TestReadStorageErrorFailsRequest(t *testing.T) {
	repo := newFakeRepo()
	clk := clock.NewFake(t0)
	b, _ := NewBlogService(repo, clk).Create(context.Background(), editor, valid)
	views := &fakeViews{repo: repo, seen: map[domain.ViewKey]bool{}, err: domain.ErrUnavailable}
	svc := NewViewService(repo, views, fakeHasher{}, clk)
	if _, err := svc.Read(context.Background(), domain.Anonymous(), b.ID, netip.MustParseAddr("1.1.1.1")); !errors.Is(err, domain.ErrUnavailable) {
		t.Errorf("got %v, want ErrUnavailable (never 200 with a lost view)", err)
	}
}

func TestExport(t *testing.T) {
	repo := newFakeRepo()
	blogs := NewBlogService(repo, clock.NewFake(t0))
	ctx := context.Background()
	a, _ := blogs.Create(ctx, editor, valid)
	b, _ := blogs.Create(ctx, editor, valid)
	blogs.Delete(ctx, admin, a.ID)
	svc := NewReportService(repo)

	for _, actor := range []domain.Actor{admin, reporter} {
		var ids []uuid.UUID
		err := svc.Export(ctx, actor, func(r ReportRow) error { ids = append(ids, r.ID); return nil })
		if err != nil || len(ids) != 1 || ids[0] != b.ID {
			t.Errorf("%s export: ids=%v err=%v (deleted blog must be excluded)", actor.Role, ids, err)
		}
	}
	noop := func(ReportRow) error { t.Error("rows must not be produced without permission"); return nil }
	for _, actor := range []domain.Actor{editor, reader} {
		if err := svc.Export(ctx, actor, noop); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s export: %v", actor.Role, err)
		}
	}
	if err := svc.Export(ctx, domain.Anonymous(), noop); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Errorf("anonymous export: %v", err)
	}
}
