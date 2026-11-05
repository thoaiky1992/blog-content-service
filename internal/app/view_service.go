package app

import (
	"context"
	"log/slog"
	"net/netip"

	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/domain"
)

// ViewService serves blog detail and records the unique daily view.
type ViewService struct {
	repo  BlogRepository
	views ViewStore
	cache ViewCache
	clock Clock
}

func NewViewService(repo BlogRepository, views ViewStore, cache ViewCache, clock Clock) *ViewService {
	return &ViewService{repo: repo, views: views, cache: cache, clock: clock}
}

// Read implements design section 7.5:
//  1. canonicalize the client IP and compute the UTC view date
//  2. return the active blog immediately for a committed Redis marker
//  3. on a cache miss, load the active blog and record the view atomically
//  4. mark Redis only after PostgreSQL succeeds
//
// On a cache miss, RecordUniqueView re-checks deleted_at after GetActive, so a
// racing delete yields ErrNotFound instead of a view on a deleted blog.
func (s *ViewService) Read(ctx context.Context, actor domain.Actor, id uuid.UUID, clientIP netip.Addr) (domain.Blog, error) {
	if err := actor.Require(domain.PermBlogRead); err != nil {
		return domain.Blog{}, err
	}

	now := s.clock.Now()
	key := domain.NewViewKey(id, now, clientIP)
	if s.cache != nil {
		recorded, err := s.cache.WasRecorded(ctx, key)
		if err != nil {
			slog.DebugContext(ctx, "view cache check failed; falling back to postgres", "error", err)
		} else if recorded {
			// The marker is written only after PostgreSQL commits. Load the blog
			// after the cache check so soft-deleted blogs still return 404.
			return s.repo.GetActive(ctx, id)
		}
	}

	blog, err := s.repo.GetActive(ctx, id)
	if err != nil {
		return domain.Blog{}, err
	}
	counted, err := s.views.RecordUniqueView(ctx, key, now)
	if err != nil {
		return domain.Blog{}, err
	}
	if s.cache != nil {
		// RecordUniqueView returning nil means the dedup row exists, whether it
		// was inserted by this request or an earlier concurrent request.
		if err := s.cache.MarkRecorded(ctx, key); err != nil {
			slog.DebugContext(ctx, "view cache mark failed", "error", err)
		}
	}
	if counted {
		blog.Views++ // reflect the view this request just added
	}
	return blog, nil
}
