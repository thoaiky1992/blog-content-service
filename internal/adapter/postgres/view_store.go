package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sky/blog-service/internal/app"
	"github.com/sky/blog-service/internal/domain"
)

// ViewStore implements app.ViewStore with the single-statement atomic CTE from
// the design (section 6.1).
type ViewStore struct {
	pool *pgxpool.Pool
}

func NewViewStore(pool *pgxpool.Pool) *ViewStore { return &ViewStore{pool: pool} }

var _ app.ViewStore = (*ViewStore)(nil)

// recordViewSQL runs as ONE statement, so the dedup row and the counter
// increment commit or fail together (READ COMMITTED is enough):
//
//   - target:   the active blog (deleted_at re-checked here, closing the race
//     with a delete between the caller's read and this write)
//   - inserted: INSERT ... ON CONFLICT DO NOTHING. The PK
//     (blog_id, view_date, ip_address) is the correctness primitive, so
//     concurrent identical requests produce exactly one row
//   - bumped:   views = views + 1 only for a freshly inserted row. Data-modifying
//     CTEs always run to completion even though the final SELECT
//     does not read them
//
// The final SELECT reports whether the blog existed (to tell "missing/deleted"
// from "duplicate view") and whether this call counted a new view.
const recordViewSQL = `
WITH target AS (
    SELECT id
    FROM blogs
    WHERE id = $1
      AND deleted_at IS NULL
),
inserted AS (
    INSERT INTO blog_daily_views (blog_id, view_date, ip_address, created_at)
    SELECT id, $2::date, $3::inet, $4::timestamptz
    FROM target
    ON CONFLICT (blog_id, view_date, ip_address) DO NOTHING
    RETURNING blog_id
),
bumped AS (
    UPDATE blogs
    SET views = views + 1
    WHERE id IN (SELECT blog_id FROM inserted)
    RETURNING id
)
SELECT (SELECT count(*) FROM target)   AS blog_exists,
       (SELECT count(*) FROM inserted) AS counted`

func (s *ViewStore) RecordUniqueView(ctx context.Context, key domain.ViewKey, at time.Time) (bool, error) {
	var exists, counted int64
	err := s.pool.QueryRow(ctx, recordViewSQL,
		key.BlogID, key.Date, key.IP.String(), at).Scan(&exists, &counted)
	if err != nil {
		return false, mapErr("record view", err)
	}
	if exists == 0 {
		return false, domain.ErrNotFound
	}
	return counted > 0, nil
}
