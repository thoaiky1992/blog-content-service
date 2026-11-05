// Package app holds the use cases. Ports (interfaces) are declared here, where
// they are consumed; adapters implement them.
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

// BlogRepository is the persistence port for blogs. Every method only sees
// active (not soft-deleted) blogs, except Create.
type BlogRepository interface {
	Create(ctx context.Context, b domain.Blog) (domain.Blog, error)
	// GetActive returns domain.ErrNotFound if missing or deleted.
	GetActive(ctx context.Context, id uuid.UUID) (domain.Blog, error)
	// ListActive returns up to limit+1 rows so the caller can compute has_next.
	ListActive(ctx context.Context, limit, offset int) ([]domain.BlogSummary, error)
	// UpdateActive replaces the editable fields and bumps version. It returns
	// domain.ErrNotFound if missing/deleted, and domain.ErrVersionConflict if
	// expectedVersion is set and does not match.
	UpdateActive(ctx context.Context, u BlogUpdate) (domain.Blog, error)
	// DeleteActive soft-deletes; domain.ErrNotFound if missing or already deleted.
	DeleteActive(ctx context.Context, id uuid.UUID, at time.Time, by uuid.UUID) error
	// StreamActive calls fn for each active blog, newest first, without
	// loading the whole set into memory. It stops on the first fn error.
	StreamActive(ctx context.Context, fn func(ReportRow) error) error
}

// BlogUpdate carries a PUT. ExpectedVersion is optional optimistic locking.
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
	// RecordUniqueView atomically inserts the dedup row and increments the
	// counter (one SQL statement). counted is false for a duplicate key. It
	// returns domain.ErrNotFound if the blog is missing or deleted.
	RecordUniqueView(ctx context.Context, key domain.ViewKey, at time.Time) (counted bool, err error)
}

// ViewCache stores derived markers for views already committed to PostgreSQL.
// A miss or cache failure must always fall back to ViewStore.
type ViewCache interface {
	WasRecorded(ctx context.Context, key domain.ViewKey) (bool, error)
	MarkRecorded(ctx context.Context, key domain.ViewKey) error
}

// UserRepository resolves mock-auth users.
type UserRepository interface {
	// GetByID returns domain.ErrNotFound for an unknown user.
	GetByID(ctx context.Context, id uuid.UUID) (domain.Actor, error)
}

// ActorCache caches successful user lookups. Unknown users are not cached.
type ActorCache interface {
	Get(ctx context.Context, id uuid.UUID) (actor domain.Actor, found bool, err error)
	Set(ctx context.Context, actor domain.Actor) error
}
