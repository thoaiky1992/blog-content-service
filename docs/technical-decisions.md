# Technical decisions

Why the service is built the way it is, the trade-offs, and the places where the
behavior is a judgement call. For the layout see [architecture.md](architecture.md); for
the schema see [database.md](database.md).

## 1. Unique daily view

**Definition.** `views(blog)` = the number of distinct `(UTC date, client)` pairs that have
read the blog. It is not a raw hit count and not an all-time unique-visitor count.

### The statement

Counting a view is **one SQL statement** (`adapter/postgres/view_store.go`):

```sql
WITH target AS (
    SELECT id FROM blogs WHERE id = $1 AND deleted_at IS NULL
),
inserted AS (
    INSERT INTO blog_daily_views (blog_id, view_date, ip_address, created_at)
    SELECT id, $2::date, $3::bytea, $4::timestamptz FROM target
    ON CONFLICT (blog_id, view_date, ip_address) DO NOTHING
    RETURNING blog_id
),
bumped AS (
    UPDATE blogs SET views = views + 1
    WHERE id IN (SELECT blog_id FROM inserted)
    RETURNING id
)
SELECT (SELECT count(*) FROM target)   AS blog_exists,
       (SELECT count(*) FROM inserted) AS counted;
```

Why it is correct:

- **No check-then-act race.** The code never does "SELECT to see if it exists, then
  INSERT". The primary key arbitrates: of N concurrent identical requests, exactly one
  insert succeeds and the rest hit `ON CONFLICT DO NOTHING`.
- **No lost update.** The counter is `views = views + 1` inside the database, never
  read-modify-write in Go.
- **Row and counter are atomic.** The dedup row and the increment live in one statement,
  so they commit or fail together. A crash cannot leave a row without its increment.
- **The counter moves only for a new row.** `bumped` is driven by `inserted`, so
  duplicates change nothing.
- **Deleted blogs get no views.** `target` re-checks `deleted_at IS NULL`.
- **Works across instances.** The shared state is the database constraint, not a Go mutex
  or any in-process memory.

### Alternatives compared

| | A. PostgreSQL PK + atomic CTE (chosen) | B. Redis `SADD` + async DB aggregation |
|---|---|---|
| Consistency | Exact, immediately. `views` always equals the row count | Eventual. Counter lags until flushed; Redis and PostgreSQL cannot be updated atomically |
| Performance | One write round trip per *new* view; duplicates cost an index probe. Different visitors of one blog serialize on that blog's `views` row | Very high: a Redis op per request, DB sees batches |
| Complexity | Low: one statement, no extra infrastructure | High: flusher, batching, crash-safe hand-off, reconciliation |
| Scalability | Scales with PostgreSQL; hot-row contention for one very popular blog | Scales further; the hot row is hit once per batch |
| Failure recovery | Nothing to recover: it is all in one transactional store. PostgreSQL down → request fails (503) | Redis down: either fail open (risk duplicates until it returns) or lose un-flushed counts; needs a recovery story |
| Database load | One insert + one update per new view | Greatly reduced |

Decision: **A remains the source of truth** because correctness is checkable and there is
nothing to reconcile. A Redis committed-marker cache now sits in front of A for duplicate
reads. Its Lua script only checks for a marker; a miss or Redis error falls back to the
atomic PostgreSQL statement. The marker is written only after PostgreSQL succeeds, so
Redis downtime increases database load but does not lose or double-count views. This is
not option B: there is no Redis-owned counter or asynchronous aggregation.

### Details that matter

- **Date** is `DATE` computed from `now.UTC()` in the app through an injectable `Clock`, so
  the midnight boundary is unit-testable. Reads at `23:59:59` and `00:00:00` UTC are
  different days.
- **Client identity** is the normalized IP stored in PostgreSQL as `inet`.
  IPv4-mapped IPv6 (`::ffff:1.2.3.4`) equals `1.2.3.4`.
- **PostgreSQL failure** while recording the view fails the request (`503`, or `500` if
  unclassified). The detail endpoint never returns `200` with a silently dropped view.
- **Client disconnect** cancels the request context, which cancels the query. There is no
  detached "finish recording anyway" job, so a view is not recorded for a request that was
  abandoned before the statement ran.

## 2. Transactions and consistency

Isolation level is **READ COMMITTED** everywhere. The unique constraint and single-statement
writes are enough for the races that matter, so there are no explicit multi-statement
transactions.

| Operation | Where the transaction starts and ends | What is inside |
|---|---|---|
| Create blog | One `INSERT ... RETURNING` (implicit transaction, committed when the statement ends) | the blog row, `created_*`/`updated_*` audit |
| Update blog | One `UPDATE ... WHERE id = $1 AND deleted_at IS NULL [AND version = $n] RETURNING` | fields, `updated_at`, `updated_by`, `version + 1` |
| Soft delete | One `UPDATE ... SET deleted_at, deleted_by WHERE id = $1 AND deleted_at IS NULL` | the delete marker and who/when |
| Record view | The single CTE above | dedup row + counter increment |
| Read detail | Two separate statements: `SELECT` the blog, then the view CTE | **not** one transaction (see below) |
| List / CSV | One `SELECT` (CSV streams from one cursor) | read only |

**Eventual consistency:** none in the baseline. `blogs.views` is exact at all times.

**Known, accepted gaps:**

- *Detail reads are two statements.* If an admin soft-deletes the blog between the
  `SELECT` and the view statement, the view statement sees the blog as deleted and the
  request returns `404`. If the delete lands *during* the view statement's snapshot window,
  one view may be recorded for a blog that is deleted a moment later. The counter and
  the rows stay consistent (the invariant holds), and the blog is deleted either way. Row
  locking would close this window but would make every repeat visitor take a row lock, so
  it was not added.
- *`UpdateActive` with `version`* needs a second query to tell "not found" from "version
  mismatch". That follow-up is only a diagnosis; the update itself is atomic.

## 3. Soft delete

Every public query and the CSV report filter `deleted_at IS NULL`; the repository methods
are named `GetActive`, `ListActive`, `UpdateActive`, `DeleteActive`, `StreamActive` so a
missing filter is hard to write by accident. `UPDATE`/`DELETE` repeat the predicate, so an
update racing a delete cannot revive a blog. There is no detail or list cache, so a deleted
blog cannot leak from a stale cache.

## 4. Authentication and authorization

- Mock auth: `X-User-ID`; the role is read from PostgreSQL on a cache miss and successful
  lookups are cached in Redis for a bounded TTL. Redis errors fall back to PostgreSQL, and
  a client still cannot claim a role (verified by a test that sends fake role headers).
- The permission matrix is one table in `domain/auth.go`; every use case calls
  `actor.Require(...)`. Anonymous with a missing permission → `401`; authenticated → `403`.
- Editors may update any blog; the assignment defines no ownership rule.

## 5. API behavior choices

| Choice | Reason |
|---|---|
| Pagination `page`/`limit`, out-of-range is `400` | Clients learn about bad input instead of silently getting a different page. Defaults: page 1, limit 20, max 100 |
| `has_next` instead of `total` | Avoids a `COUNT(*)` on every list call; fetches `limit + 1` rows to know if another page exists |
| OFFSET pagination | Requirement-first and simple. Ordering is `(created_at DESC, id DESC)` so pages are stable even when `created_at` ties. Keyset pagination is the upgrade if deep pages become slow |
| `PUT` replaces all editable fields | Predictable semantics; optional `version` adds optimistic locking → `409` |
| Unknown JSON fields → `400`, body ≤ 1 MB → `413`, wrong `Content-Type` → `415` | Fail loudly and early |
| Title/author are trimmed before validation | `"   "` is not a title. Limits count characters, not bytes |
| `Cache-Control: no-store` on detail and CSV | Every read must reach the view counter; intermediaries must not serve a cached copy |
| One error envelope with `request_id` | Uniform for clients; unclassified errors are logged server-side and returned as a generic 500, so internals do not leak |

## 6. CSV export

Streams from the database cursor to the response with constant memory; flushes every 500
rows. If a failure happens after the first byte, the status is already `200`, so the handler
aborts the connection instead of finishing normally: the client sees a failed transfer
rather than a plausible-looking truncated file. Failures before the first row return a
normal JSON error. Formula-injection defense prefixes `'` to cells starting with
`= + - @`, tab or CR.

## 7. Trusted proxies

`TRUSTED_PROXIES` is empty by default, so forwarded headers are ignored. When set,
`X-Forwarded-For` is trusted only from those peers and read right to left. This blocks the
common attack of sending `X-Forwarded-For: <random>` on every request to inflate the view
counter (covered by unit and integration tests).

## 8. Testing approach

- Pure logic (permissions, validation, UTC boundary, IP resolver, CSV sanitizing)
  is unit-tested.
- The real services run behind the real router with in-memory fakes for the endpoint ×
  role matrix and error cases.
- Integration tests run directly against the currently configured **real PostgreSQL**.
  They never truncate tables and clean up only blog IDs created by the tests.

## 9. Not done / future work

- Load tests and the performance report (design Phase 9). Throughput targets are
  unverified, so none is claimed.
- Optimizations only worth doing after measuring: async counter aggregation for a hot
  blog, keyset pagination, partitioning `blog_daily_views`.
- The `uuidv7()` column default needs PostgreSQL 18 (the service itself generates UUIDv7
  values, so only the default depends on the version).
