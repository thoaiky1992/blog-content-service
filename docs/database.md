# Database

PostgreSQL is the single source of truth. Schema changes are Goose migrations in
[migrations/](../migrations), applied by the `migrate` service (or `goose up`). Constraints
mirror the validation rules, so the database rejects bad data even if the application
had a bug.

## Relationships

```text
users 1 ──< N blogs        (created_by, updated_by, deleted_by)
blogs 1 ──< N blog_daily_views
```

## Tables

### `users` — `00001`

| Column | Type | Notes |
|---|---|---|
| `id` | `uuid` PK | `DEFAULT uuidv7()` |
| `name` | `text` NOT NULL | |
| `role` | `text` NOT NULL | `CHECK (role IN ('admin','editor','reader','reporter'))` |
| `created_at` | `timestamptz` NOT NULL | `DEFAULT now()` |

Anonymous callers have no row. Migration `00004` seeds one user per role.

### `blogs` — `00002`

| Column | Type | Notes |
|---|---|---|
| `id` | `uuid` PK | `DEFAULT uuidv7()`; the service generates a UUIDv7 itself |
| `title` | `text` NOT NULL | 1–255 characters |
| `author` | `text` NOT NULL | 1–100 characters (display name) |
| `content` | `text` NOT NULL | 1–100000 characters |
| `description` | `text` NOT NULL | default `''`, ≤ 500 characters |
| `views` | `bigint` NOT NULL | default 0, `CHECK (views >= 0)`; denormalized counter |
| `version` | `integer` NOT NULL | default 1; incremented on every update (optimistic locking) |
| `created_at`, `updated_at` | `timestamptz` NOT NULL | UTC |
| `deleted_at` | `timestamptz` NULL | soft-delete marker |
| `created_by`, `updated_by` | `uuid` NOT NULL → `users(id)` | actor IDs |
| `deleted_by` | `uuid` NULL → `users(id)` | |

Table constraints:

- `blogs_deleted_pair`: `(deleted_at IS NULL) = (deleted_by IS NULL)`, so a delete always
  records who did it
- `blogs_updated_after_created`: `updated_at >= created_at`
- `blogs_deleted_after_created`: `deleted_at IS NULL OR deleted_at >= created_at`

### `blog_daily_views` — `00003`

| Column | Type | Notes |
|---|---|---|
| `blog_id` | `uuid` NOT NULL → `blogs(id)` | |
| `view_date` | `date` NOT NULL | the **UTC** calendar day, computed by the app |
| `ip_address` | `inet` NOT NULL | Canonical client IP used for daily view deduplication |
| `created_at` | `timestamptz` NOT NULL | |

**Primary key `(blog_id, view_date, ip_address)`** is the uniqueness guarantee: one row per
blog + UTC day + client. Everything else about view counting depends on this constraint.

## Indexes

| Index | Definition | Serves |
|---|---|---|
| `blogs_pkey` | `(id)` | detail, update, delete |
| `blogs_active_created_idx` | `(created_at DESC, id DESC) WHERE deleted_at IS NULL` | list and CSV: ordered scan over active blogs, deleted rows are not in the index |
| `blog_daily_views_pkey` | `(blog_id, view_date, ip_address)` | dedup on insert; also lookups by blog |

There is deliberately no index on `deleted_at` (the partial index covers it) and none on
`views` (it is updated constantly, so an index would only add write cost).

## Invariants

```text
blogs.views == COUNT(*) FROM blog_daily_views WHERE blog_id = X
```

The integration tests assert this after every concurrent scenario. To check a live
database:

```sql
SELECT b.id, b.views, count(v.*) AS rows
FROM blogs b LEFT JOIN blog_daily_views v ON v.blog_id = b.id
GROUP BY b.id HAVING b.views <> count(v.*);   -- must return no rows
```

Soft delete: every public query and the report filter `deleted_at IS NULL`, and
`UPDATE`/`DELETE` carry the same predicate, so a deleted blog is neither visible nor
revivable.

## Time

All timestamps are `timestamptz`, produced from an injectable UTC clock and returned as
RFC3339 UTC. `view_date` is a plain `date` derived from `now.UTC()` in the application,
never from the database's `now()` or session time zone.

## Migrations

| File | Purpose |
|---|---|
| `00001_create_users.sql` | `users` |
| `00002_create_blogs.sql` | `blogs`, constraints, partial index |
| `00003_create_blog_daily_views.sql` | dedup table with the unique PK |
| `00004_seed_users.sql` | one user per role |

Each has a working `Down`. `goose down-to 0` followed by `up` is clean.

## Capacity note

`blog_daily_views` grows by one row per unique (blog, day, visitor). That is the cost of
exact de-duplication. If it ever becomes large, partition it by `view_date` and archive or
roll up old days. That is only worth doing after measuring, and old rows may only be
removed once their counts are safe to fold into `blogs.views`.
