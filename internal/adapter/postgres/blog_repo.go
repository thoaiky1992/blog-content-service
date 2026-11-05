package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/domain"
)

// BlogRepo implements app.BlogRepository. All reads and updates filter on
// deleted_at IS NULL so a soft-deleted blog is never visible or revived.
type BlogRepo struct {
	pool *pgxpool.Pool
}

func NewBlogRepo(pool *pgxpool.Pool) *BlogRepo { return &BlogRepo{pool: pool} }

var _ app.BlogRepository = (*BlogRepo)(nil)

const blogColumns = `id, title, author, content, description, views, version,
	created_at, updated_at, deleted_at, created_by, updated_by, deleted_by`

func scanBlog(row pgx.Row) (domain.Blog, error) {
	var b domain.Blog
	err := row.Scan(&b.ID, &b.Title, &b.Author, &b.Content, &b.Description,
		&b.Views, &b.Version, &b.CreatedAt, &b.UpdatedAt, &b.DeletedAt,
		&b.CreatedBy, &b.UpdatedBy, &b.DeletedBy)
	if err == nil {
		b.CreatedAt, b.UpdatedAt = b.CreatedAt.UTC(), b.UpdatedAt.UTC()
		if b.DeletedAt != nil {
			t := b.DeletedAt.UTC()
			b.DeletedAt = &t
		}
	}
	return b, err
}

func (r *BlogRepo) Create(ctx context.Context, b domain.Blog) (domain.Blog, error) {
	const q = `
	INSERT INTO blogs (id, title, author, content, description,
					created_at, updated_at, created_by, updated_by)
	VALUES ($1, $2, $3, $4, $5, $6, $6, $7, $7)
	RETURNING ` + blogColumns
	out, err := scanBlog(r.pool.QueryRow(ctx, q,
		b.ID, b.Title, b.Author, b.Content, b.Description, b.CreatedAt, b.CreatedBy))
	if err != nil {
		return domain.Blog{}, mapErr("create blog", err)
	}
	return out, nil
}

func (r *BlogRepo) GetActive(ctx context.Context, id uuid.UUID) (domain.Blog, error) {
	const q = `SELECT ` + blogColumns + ` FROM blogs WHERE id = $1 AND deleted_at IS NULL`
	b, err := scanBlog(r.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Blog{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Blog{}, mapErr("get blog", err)
	}
	return b, nil
}

func (r *BlogRepo) ListActive(ctx context.Context, limit, offset int) ([]domain.BlogSummary, error) {
	const q = `
	SELECT id, title, description
	FROM blogs
	WHERE deleted_at IS NULL
	ORDER BY created_at DESC, id DESC
	LIMIT $1 OFFSET $2`
	rows, err := r.pool.Query(ctx, q, limit, offset)
	if err != nil {
		return nil, mapErr("list blogs", err)
	}
	defer rows.Close()
	out := make([]domain.BlogSummary, 0, limit)
	for rows.Next() {
		var s domain.BlogSummary
		if err := rows.Scan(&s.ID, &s.Title, &s.Description); err != nil {
			return nil, mapErr("scan blog summary", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr("list blogs", err)
	}
	return out, nil
}

func (r *BlogRepo) UpdateActive(ctx context.Context, u app.BlogUpdate) (domain.Blog, error) {
	const q = `
	UPDATE blogs
	SET title       = $2,
		author      = $3,
		content     = $4,
		description = $5,
		updated_at  = $6,
		updated_by  = $7,
		version     = version + 1
	WHERE id = $1
	AND deleted_at IS NULL
	AND ($8::int IS NULL OR version = $8::int)
	RETURNING ` + blogColumns
	b, err := scanBlog(r.pool.QueryRow(ctx, q, u.ID,
		u.Input.Title, u.Input.Author, u.Input.Content, u.Input.Description,
		u.At, u.By, u.ExpectedVersion))
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.Blog{}, mapErr("update blog", err)
	}
	// No row updated: either the blog is gone, or the version check failed.
	if u.ExpectedVersion != nil {
		var one int
		err := r.pool.QueryRow(ctx,
			`SELECT 1 FROM blogs WHERE id = $1 AND deleted_at IS NULL`, u.ID).Scan(&one)
		if err == nil {
			return domain.Blog{}, domain.ErrVersionConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.Blog{}, mapErr("update blog", err)
		}
	}
	return domain.Blog{}, domain.ErrNotFound
}

func (r *BlogRepo) DeleteActive(ctx context.Context, id uuid.UUID, at time.Time, by uuid.UUID) error {
	const q = `
	UPDATE blogs
	SET deleted_at = $2, deleted_by = $3
	WHERE id = $1 AND deleted_at IS NULL
	RETURNING id`
	var got uuid.UUID
	err := r.pool.QueryRow(ctx, q, id, at, by).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return mapErr("delete blog", err)
}

func (r *BlogRepo) StreamActive(ctx context.Context, fn func(app.ReportRow) error) error {
	const q = `
	SELECT id, title, author, description, views,
		created_at, updated_at, created_by, updated_by
	FROM blogs
	WHERE deleted_at IS NULL
	ORDER BY created_at DESC, id DESC
	LIMIT 10`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return mapErr("stream blogs", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row app.ReportRow
		if err := rows.Scan(&row.ID, &row.Title, &row.Author, &row.Description,
			&row.Views, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
			return mapErr("scan report row", err)
		}
		row.CreatedAt, row.UpdatedAt = row.CreatedAt.UTC(), row.UpdatedAt.UTC()
		if err := fn(row); err != nil {
			return err
		}
	}
	return mapErr("stream blogs", rows.Err())
}
