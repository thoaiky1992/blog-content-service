-- Deterministic load-test dataset (design.md section 20, Phase 9).
--
-- Blog i (1 <= i <= :rows) gets id md5('blog-' || i)::uuid, so k6 derives the
-- same IDs without shipping an ID file.
--
-- Idempotent and additive: rerunning with a larger :rows adds only the missing
-- rows. Views start at 0 with no blog_daily_views rows, so the invariant
-- blogs.views == COUNT(blog_daily_views) holds from the start.
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
       'Description of load test blog ' || r.i,
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
