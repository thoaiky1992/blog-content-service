-- Deterministic dataset for load testing.
--
-- Blog i (1 <= i <= :rows) has ID md5('blog-' || i)::uuid so k6 can derive the
-- same IDs without shipping an ID file.
--
-- The script is idempotent and additive: rerunning with a larger :rows value adds only
-- missing rows. Views start at zero with no blog_daily_views rows, so the invariant
-- blogs.views == COUNT(blog_daily_views) holds from the beginning.
--
-- Usage:
--   docker exec -i postgres psql -v ON_ERROR_STOP=1 -v rows=1000000 \
--     -U blog -d blog -q < loadtest/seed/seed_blogs.sql

WITH author AS (
    SELECT id FROM users WHERE role = 'admin' ORDER BY id LIMIT 1
),
rows_to_seed AS (
    SELECT md5('blog-' || i)::uuid AS id,
           i,
           timestamptz '2026-01-01 00:00:00+00' + make_interval(secs => i) AS ts
    FROM generate_series(1, :rows) AS i
)
INSERT INTO blogs (id, title, author, content, description,
                   views, version, created_at, updated_at, created_by, updated_by)
SELECT r.id,
       'Load test blog ' || r.i,
       'Author ' || (r.i % 1000),
       repeat('Lorem ipsum dolor sit amet, consectetur adipiscing elit. ', 16),
       'Load test blog description ' || r.i,
       0,
       1,
       r.ts,
       r.ts,
       a.id,
       a.id
FROM rows_to_seed r
CROSS JOIN author a
ON CONFLICT (id) DO NOTHING;

ANALYZE blogs;
