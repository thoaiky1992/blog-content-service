// Package app holds use cases. Ports are declared where they are consumed, and adapters
// implement them.
package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/domain"
)

// Clock supplies the current UTC time.
type Clock interface {
	Now() time.Time
}

// BlogRepository is the blog persistence port. Except for Create, every method operates
// only on active, non-deleted blogs.
type BlogRepository interface {
	Create(ctx context.Context, b domain.Blog) (domain.Blog, error)
	// GetActive returns domain.ErrNotFound when the blog is missing or deleted.
	GetActive(ctx context.Context, id uuid.UUID) (domain.Blog, error)
	// ListActive returns up to limit+1 rows so the caller can compute has_next.
	ListActive(ctx context.Context, limit, offset int) ([]domain.BlogSummary, error)
	// UpdateActive replaces editable fields and increments version. It returns
	// domain.ErrNotFound when the blog is missing or deleted, and
	// domain.ErrVersionConflict when expectedVersion is set but does not match.
	UpdateActive(ctx context.Context, u BlogUpdate) (domain.Blog, error)
	// DeleteActive soft-deletes a blog and returns domain.ErrNotFound if it is missing or deleted.
	DeleteActive(ctx context.Context, id uuid.UUID, at time.Time, by uuid.UUID) error
	// StreamActive calls fn for each active blog, newest first, without loading the
	// entire result into memory. It stops at the first fn error.
	StreamActive(ctx context.Context, fn func(ReportRow) error) error
}

// BlogUpdate carries a PUT operation. ExpectedVersion is an optional optimistic lock.
type BlogUpdate struct {
	ID              uuid.UUID
	Input           domain.BlogInput
	At              time.Time
	By              uuid.UUID
	ExpectedVersion *int
}

// ReportRow is one CSV export row.
type ReportRow struct {
	ID          uuid.UUID
	Title       string
	Author      string
	Description string
	Views       int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CreatedBy   uuid.UUID
	UpdatedBy   uuid.UUID
}

// ViewStore records unique daily views.
type ViewStore interface {
	// RecordUniqueView atomically inserts the deduplication row and increments the
	// counter in one SQL statement. counted is false for a duplicate key. It returns
	// domain.ErrNotFound when the blog is missing or deleted.
	RecordUniqueView(ctx context.Context, key domain.ViewKey, at time.Time) (counted bool, err error)
}

// ViewCache stores derived markers for views committed to PostgreSQL. A cache miss or
// failure must always fall back to ViewStore.
type ViewCache interface {
	WasRecorded(ctx context.Context, key domain.ViewKey) (bool, error)
	MarkRecorded(ctx context.Context, key domain.ViewKey) error
}

// UserRepository resolves users for mock authentication.
type UserRepository interface {
	// GetByID returns domain.ErrNotFound for an unknown user.
	GetByID(ctx context.Context, id uuid.UUID) (domain.Actor, error)
}

// ActorCache caches successful user lookups. Unknown users are not cached.
type ActorCache interface {
	Get(ctx context.Context, id uuid.UUID) (actor domain.Actor, found bool, err error)
	Set(ctx context.Context, actor domain.Actor) error
}
