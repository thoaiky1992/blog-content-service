package app

import (
	"context"
	"net/netip"

	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/domain"
)

// ViewService serves blog detail and records the unique daily view.
type ViewService struct {
	repo   BlogRepository
	views  ViewStore
	hasher IPHasher
	clock  Clock
}

func NewViewService(repo BlogRepository, views ViewStore, hasher IPHasher, clock Clock) *ViewService {
	return &ViewService{repo: repo, views: views, hasher: hasher, clock: clock}
}

// Read implements design section 7.5:
//  1. load the active blog (missing -> ErrNotFound, no view written)
//  2. hash the client IP and compute the UTC view date
//  3. record the view atomically; a storage error fails the request rather
//     than silently dropping the view
//
// RecordUniqueView re-checks deleted_at, so a delete racing between 1 and 3
// yields ErrNotFound instead of a view on a deleted blog.
func (s *ViewService) Read(ctx context.Context, actor domain.Actor, id uuid.UUID, clientIP netip.Addr) (domain.Blog, error) {
	if err := actor.Require(domain.PermBlogRead); err != nil {
		return domain.Blog{}, err
	}
	blog, err := s.repo.GetActive(ctx, id)
	if err != nil {
		return domain.Blog{}, err
	}
	now := s.clock.Now()
	key := domain.NewViewKey(id, now, s.hasher.Hash(clientIP.Unmap()))
	counted, err := s.views.RecordUniqueView(ctx, key, now)
	if err != nil {
		return domain.Blog{}, err
	}
	if counted {
		blog.Views++ // reflect the view this request just added
	}
	return blog, nil
}
