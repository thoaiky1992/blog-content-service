package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/sky/blog-service/internal/domain"
)

// BlogService implements create, update, delete, and list. Authorization happens here,
// not in handlers.
type BlogService struct {
	repo  BlogRepository
	clock Clock
}

func NewBlogService(repo BlogRepository, clock Clock) *BlogService {
	return &BlogService{repo: repo, clock: clock}
}

func (s *BlogService) Create(ctx context.Context, actor domain.Actor, in domain.BlogInput) (domain.Blog, error) {
	if err := actor.Require(domain.PermBlogCreate); err != nil {
		return domain.Blog{}, err
	}
	in, err := in.Validate()
	if err != nil {
		return domain.Blog{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return domain.Blog{}, err
	}
	now := s.clock.Now()
	return s.repo.Create(ctx, domain.Blog{
		ID: id, Title: in.Title, Author: in.Author,
		Content: in.Content, Description: in.Description,
		Version:   1,
		CreatedAt: now, UpdatedAt: now,
		CreatedBy: actor.ID, UpdatedBy: actor.ID,
	})
}

// Update replaces every editable field. Any Editor or Admin may update any blog because
// there is no ownership rule. expectedVersion is an optional optimistic lock.
func (s *BlogService) Update(ctx context.Context, actor domain.Actor, id uuid.UUID, in domain.BlogInput, expectedVersion *int) (domain.Blog, error) {
	if err := actor.Require(domain.PermBlogUpdate); err != nil {
		return domain.Blog{}, err
	}
	in, err := in.Validate()
	if err != nil {
		return domain.Blog{}, err
	}
	return s.repo.UpdateActive(ctx, BlogUpdate{
		ID: id, Input: in, At: s.clock.Now(), By: actor.ID, ExpectedVersion: expectedVersion,
	})
}

func (s *BlogService) Delete(ctx context.Context, actor domain.Actor, id uuid.UUID) error {
	if err := actor.Require(domain.PermBlogDelete); err != nil {
		return err
	}
	return s.repo.DeleteActive(ctx, id, s.clock.Now(), actor.ID)
}

// List returns one page of active blogs and never records views.
func (s *BlogService) List(ctx context.Context, actor domain.Actor, page, limit int) (domain.BlogPage, error) {
	if err := actor.Require(domain.PermBlogRead); err != nil {
		return domain.BlogPage{}, err
	}
	req, err := domain.NewPageRequest(page, limit)
	if err != nil {
		return domain.BlogPage{}, err
	}
	rows, err := s.repo.ListActive(ctx, req.Limit+1, req.Offset())
	if err != nil {
		return domain.BlogPage{}, err
	}
	hasNext := len(rows) > req.Limit
	if hasNext {
		rows = rows[:req.Limit]
	}
	return domain.BlogPage{Items: rows, Page: req.Page, Limit: req.Limit, HasNext: hasNext}, nil
}
