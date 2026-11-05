// Package apptest provides in-memory implementations of app ports for tests that need
// real services without a database, such as handler tests.
package apptest

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/domain"
)

// Repo is an in-memory app.BlogRepository.
type Repo struct {
	mu    sync.Mutex
	Blogs map[uuid.UUID]*domain.Blog
	Order []uuid.UUID
	// StreamFailAfter > 0 makes StreamActive fail after that many rows.
	StreamFailAfter int
	// StreamErr makes StreamActive fail before producing a row when set.
	StreamErr error
}

func NewRepo() *Repo { return &Repo{Blogs: map[uuid.UUID]*domain.Blog{}} }

func (r *Repo) Create(_ context.Context, b domain.Blog) (domain.Blog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Blogs[b.ID] = &b
	r.Order = append(r.Order, b.ID)
	return b, nil
}

func (r *Repo) GetActive(_ context.Context, id uuid.UUID) (domain.Blog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.Blogs[id]
	if !ok || b.DeletedAt != nil {
		return domain.Blog{}, domain.ErrNotFound
	}
	return *b, nil
}

// ListActive returns newest blogs first, like the real query.
func (r *Repo) ListActive(_ context.Context, limit, offset int) ([]domain.BlogSummary, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []domain.BlogSummary
	i := 0
	for n := len(r.Order) - 1; n >= 0; n-- {
		b := r.Blogs[r.Order[n]]
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

func (r *Repo) UpdateActive(_ context.Context, u app.BlogUpdate) (domain.Blog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.Blogs[u.ID]
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

func (r *Repo) DeleteActive(_ context.Context, id uuid.UUID, at time.Time, by uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.Blogs[id]
	if !ok || b.DeletedAt != nil {
		return domain.ErrNotFound
	}
	b.DeletedAt, b.DeletedBy = &at, &by
	return nil
}

func (r *Repo) StreamActive(_ context.Context, fn func(app.ReportRow) error) error {
	if r.StreamErr != nil {
		return r.StreamErr
	}
	r.mu.Lock()
	var rows []app.ReportRow
	for n := len(r.Order) - 1; n >= 0; n-- {
		b := r.Blogs[r.Order[n]]
		if b.DeletedAt != nil {
			continue
		}
		rows = append(rows, app.ReportRow{
			ID: b.ID, Title: b.Title, Author: b.Author, Description: b.Description, Views: b.Views,
			CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt, CreatedBy: b.CreatedBy, UpdatedBy: b.UpdatedBy,
		})
	}
	failAfter := r.StreamFailAfter
	r.mu.Unlock()

	for i, row := range rows {
		if failAfter > 0 && i >= failAfter {
			return errors.New("simulated database failure during streaming")
		}
		if err := fn(row); err != nil {
			return err
		}
	}
	return nil
}

// Views is an in-memory app.ViewStore that deduplicates on the same key as the primary key.
type Views struct {
	repo *Repo
	mu   sync.Mutex
	seen map[domain.ViewKey]bool
	// Err is returned by every call when set, simulating a PostgreSQL outage.
	Err error
}

func NewViews(repo *Repo) *Views { return &Views{repo: repo, seen: map[domain.ViewKey]bool{}} }

func (v *Views) RecordUniqueView(_ context.Context, key domain.ViewKey, _ time.Time) (bool, error) {
	if v.Err != nil {
		return false, v.Err
	}
	v.repo.mu.Lock()
	defer v.repo.mu.Unlock()
	b, ok := v.repo.Blogs[key.BlogID]
	if !ok || b.DeletedAt != nil {
		return false, domain.ErrNotFound
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.seen[key] {
		return false, nil
	}
	v.seen[key] = true
	b.Views++
	return true, nil
}

// Users is an in-memory app.UserRepository.
type Users struct {
	byID map[uuid.UUID]domain.Actor
	// Err is returned by every call when set.
	Err error
}

func NewUsers(actors ...domain.Actor) *Users {
	u := &Users{byID: map[uuid.UUID]domain.Actor{}}
	for _, a := range actors {
		u.byID[a.ID] = a
	}
	return u
}

func (u *Users) GetByID(_ context.Context, id uuid.UUID) (domain.Actor, error) {
	if u.Err != nil {
		return domain.Actor{}, u.Err
	}
	a, ok := u.byID[id]
	if !ok {
		return domain.Actor{}, domain.ErrNotFound
	}
	return a, nil
}
