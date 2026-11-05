# Blog Content Service – Design & Implementation Plan

> The single design document for the **Backend Engineering / Technical Assignment – Blog Content Service**.
>
> This document covers: requirement analysis, assumptions, architecture, database design, concurrency strategy, transaction boundaries, testing, performance/load testing, the implementation roadmap and technical decisions.
>
> Labels:
>
> - **[REQUIRED]**: a direct requirement from the assignment.
> - **[BONUS]**: earns extra credit.
> - **[OPTIMIZATION]**: implement only when a benchmark proves it is needed.
>
> Guiding principles:
>
> 1. **Correctness before performance.**
> 2. PostgreSQL is the **source of truth**.
> 3. Do not use a Go mutex to guarantee correctness across multiple instances.
> 4. Redis is not on the critical correctness path in the baseline.
> 5. Do not claim to meet a TPS target before benchmarking.
> 6. Do not add abstractions or infrastructure without a real need.

---

# Table of contents

1. Requirement analysis
2. Assumptions
3. Overall architecture
4. Folder structure
5. Database design
6. View counting strategy
7. Transaction design
8. Concurrency analysis
9. Pagination
10. Soft delete
11. RBAC / Mock authentication
12. Trusted proxy / Client IP
13. CSV export
14. Performance design
15. Caching strategy
16. Testing strategy
17. Error response
18. Observability
19. Docker Compose
20. Load testing
21. Deliverables
22. Implementation plan
23. Design decision summary
24. Interview defense

---

# 1. Requirement analysis

## 1.1 Required features

The service must provide:

- List blogs with pagination.
- Blog detail.
- Create blog: `Editor`, `Admin`.
- Update blog: `Editor`, `Admin`.
- Soft delete blog: `Admin` only.
- Export CSV: `Admin`, `Reporter`.
- Anonymous users can view the list and details.
- Soft-deleted blogs must not appear in public APIs or regular reports.

API:

```text
GET    /api/v1/blogs
GET    /api/v1/blogs/{id}
POST   /api/v1/blogs
PUT    /api/v1/blogs/{id}
DELETE /api/v1/blogs/{id}
GET    /api/v1/reports/blogs
```

Every API must have:

- input validation;
- appropriate HTTP status codes;
- a consistent error response.

---

## 1.2 The hardest part of the assignment

### Unique daily view

A view is identified by:

```text
blog + UTC date + client IP
```

Invariants:

```text
same blog + same IP + same UTC date -> at most 1 view
same blog + same IP + next UTC date -> +1 view
same blog + different IP           -> +1 per IP
```

Listing blogs does not increase views.

Concurrent requests must not create duplicates.

This is the most important part because `GET detail` is no longer a pure read; it has a write side effect.

---

## 1.3 Concurrency/data consistency risks

Errors to avoid:

1. **Check-then-act race**

Wrong:

```text
SELECT to see whether it exists
if not -> INSERT
```

Two requests can both see "does not exist".

Correctness must rely on a unique constraint / atomic DB primitive.

2. **Lost counter update**

Wrong:

```text
SELECT views
views++
UPDATE views = newValue
```

Must use:

```sql
UPDATE blogs SET views = views + 1
```

3. **Dedup row and counter not atomic**

If the unique view insert succeeds but the counter does not increase, or vice versa, the data diverges.

4. **Update vs delete**

An update must not "revive" a soft-deleted blog.

5. **Stale cache**

The cache must not make public APIs return a deleted blog.

6. **Hot row**

If many new IPs read the same blog at once, they can all contend for the `blogs.views` row.

---

## 1.4 Performance target

Targets from the assignment:

| Operation | Target |
|---|---:|
| Create blog | 1,000 TPS |
| Update blog | 1,000 TPS |
| Read blog | 10,000 TPS |
| List blogs | 10,000 TPS |

These are **benchmark targets**, not guarantees that can be claimed in advance.

Must record:

- hardware / CPU / RAM;
- Docker resource limits;
- dataset size;
- concurrency/VUs;
- p95;
- p99;
- error rate;
- actual throughput.

If the target is not met:

- identify the bottleneck;
- provide measurements;
- propose an optimization;
- benchmark before/after.

---

## 1.5 Meaning of the `views` field

Chosen definition:

```text
views(blog)
= total number of distinct (UTC date, IP) pairs that have read the blog
= COUNT(*) FROM blog_daily_views WHERE blog_id = X
```

Not:

- raw HTTP hit count;
- all-time unique visitors.

`blog_daily_views` is the data source that can be used for reconciliation.

`blogs.views` is a denormalized counter for fast reads.

Baseline invariant:

```text
blogs.views == COUNT(blog_daily_views WHERE blog_id = X)
```

---

# 2. Assumptions

The points below are decisions for things the assignment does not specify clearly.

## 2.1 Time

### A-01 – Audit timestamps

The columns:

```text
created_at
updated_at
deleted_at
```

use PostgreSQL:

```sql
TIMESTAMPTZ
```

The service processes and returns timestamps in UTC.


Responses use RFC3339 UTC, for example:

```text
2026-11-05T03:15:20Z
```

### A-02 – Date used for view counting

`view_date` uses:

```sql
DATE
```

The value is computed from:

```go
now.UTC()
```

Reasons for choosing `DATE`:

- the actual business concept is a calendar date;
- the schema is easy to understand;
- no need to force the timestamp to 00:00;
- the unique key is clear;

### A-03 – Clock

The application has a small `Clock` abstraction for testing day boundaries.

Production clock:

```go
time.Now().UTC()
```


---

## 2.2 ID

Blogs and Users use UUIDs.

Prefer UUIDv7 if the chosen library is stable; otherwise UUIDv4 fully satisfies the requirement.

The design does not depend on a specific UUID version.

---

## 2.3 `author` and `created_by`

`author`:

```text
display name / business field of the post
```

`created_by`:

```text
user ID of the actor who created the record
```

The two fields serve different purposes and coexist.

---

## 2.4 Views when the blog is missing/deleted

No view is recorded.

Flow:

```text
blog missing/deleted
-> 404
-> no insert into blog_daily_views
```

---

## 2.5 Database error during RecordView

The baseline prioritizes **correctness**.

If PostgreSQL/the source of truth cannot record the view:

```text
GET detail -> 503 Service Unavailable
```

or `500` if the error cannot be classified.

The baseline does not return 200 while silently losing the view.

This helps guarantee that:

> a detail request is only considered successful once the required view side effect has completed.

If a real product wants to prioritize availability over analytics accuracy, this policy can be changed, but that is a trade-off outside the baseline assignment.

---

## 2.6 Client disconnect

The baseline does not need to keep trying to record the view after the request context has been cancelled.

Keep the lifecycle simple:

```text
request context cancel -> DB operation cancel
```

What matters in this assignment is concurrent correctness, not a detached analytics job.

---

## 2.7 Roles and ownership

An Editor can update a blog created by another Editor because the assignment does not define ownership.

---

## 2.8 List response

The list returns:

```json
{
  "id": "...",
  "title": "...",
  "description": "..."
}
```

The assignment requires title + description; `id` is added so the client can call detail.

---

## 2.9 Pagination

Baseline:

```text
page >= 1
limit default = 20
limit max = 100
```

The response includes:

```text
items
page
limit
has_next
```

The baseline does not return an exact `total`, to avoid an unnecessary `COUNT(*)`.

---

## 2.10 PUT semantics

`PUT /blogs/{id}` replaces all editable fields:

```text
title
author
content
description
```

An optional optimistic `version` can be used to demonstrate lost-update protection, but it is not required for the core requirement.

---

## 2.11 Validation

Proposed:

```text
title        1..255
author       1..100
content      1..100000
description  0..500
request body <= 1 MB
```

Unknown JSON fields: reject with `400`.

---

## 2.12 Trusted proxy

Configuration:

```text
TRUSTED_PROXIES=<CIDR list>
```

Empty by default.

Forwarded headers are trusted only if the direct peer belongs to a trusted proxy CIDR.

---

## 2.13 Redis

Redis is an **optional bonus optimization**.

The baseline application does not depend on Redis.

Redis is not the source of truth for unique views.

---

# 3. Overall architecture

## 3.1 Stack

| Component | Choice |
|---|---|
| Language | Go |
| HTTP | Gin |
| Database | PostgreSQL |
| Driver | pgx/v5 + pgxpool |
| Migration | Goose |
| SQL | Hand-written SQL |
| Logging | `log/slog` JSON |
| Integration test | testcontainers-go |
| Load test | k6 |
| Redis | Optional / Bonus |

Reasons for hand-written SQL:

- the concurrency part of the assignment depends directly on SQL semantics;
- CTE + `ON CONFLICT` must be easy to review;
- avoid an ORM hiding transaction/locking behavior.

---

## 3.2 Architecture

```text
                    Client
                      |
              Trusted Proxy / LB
                      |
        +-------------v-------------+
        |       HTTP / Gin          |
        |---------------------------|
        | recover                   |
        | request-id                |
        | access log                |
        | client-ip                 |
        | mock authentication       |
        +-------------+-------------+
                      |
                      v
        +---------------------------+
        |      Application          |
        |---------------------------|
        | BlogService               |
        | ViewService               |
        | ReportService             |
        | authorization             |
        +-------------+-------------+
                      |
                      v
        +---------------------------+
        |          Domain           |
        |---------------------------|
        | Blog                      |
        | Actor / Role / Permission |
        | ViewKey                   |
        | Domain errors             |
        +-------------+-------------+
                      |
              ports/interfaces
                      |
          +-----------+-----------+
          |                       |
          v                       v
 +----------------+       +----------------+
 | PostgreSQL     |       | Redis          |
 | Adapter        |       | [BONUS]        |
 | SOURCE OF TRUTH|       | optimization   |
 +----------------+       +----------------+
```

---

## 3.3 Dependency rule

The domain does not import:

- Gin;
- pgx;
- Redis;
- HTTP DTOs.

The application defines interfaces where it needs them.

Infrastructure implements those interfaces.

---

## 3.4 No over-engineering

Not needed:

- generic repository;
- event bus;
- CQRS framework;
- UnitOfWork abstraction from the start;
- message broker;
- distributed lock;
- Kubernetes;
- microservices.

One service + PostgreSQL is enough for the baseline.

---

# 4. Folder structure

```text
blog-service/
├── cmd/
│   └── api/
│       └── main.go
├── internal/
│   ├── config/
│   │   └── config.go
│   ├── domain/
│   │   ├── blog.go
│   │   ├── auth.go
│   │   ├── view.go
│   │   └── errors.go
│   ├── app/
│   │   ├── ports.go
│   │   ├── blog_service.go
│   │   ├── view_service.go
│   │   └── report_service.go
│   ├── adapter/
│   │   ├── postgres/
│   │   │   ├── pool.go
│   │   │   ├── blog_repo.go
│   │   │   ├── view_store.go
│   │   │   └── user_repo.go
│   │   └── redis/                 # BONUS
│   │       └── seen_cache.go
│   ├── transport/
│   │   └── http/
│   │       ├── router.go
│   │       ├── dto.go
│   │       ├── errors.go
│   │       ├── middleware/
│   │       │   ├── recover.go
│   │       │   ├── request_id.go
│   │       │   ├── access_log.go
│   │       │   ├── client_ip.go
│   │       │   └── auth.go
│   │       └── handler/
│   │           ├── blog.go
│   │           ├── report.go
│   │           └── health.go
│   └── platform/
│       ├── clientip/
│       ├── iphash/
│       └── clock/
├── migrations/
├── test/
│   └── integration/
├── loadtest/
│   ├── k6/
│   └── seed/
├── docs/
│   ├── architecture.md
│   ├── database.md
│   ├── technical-decisions.md
│   └── performance-report.md
├── openapi.yaml
├── docker-compose.yml
├── Dockerfile
├── Makefile
├── .env.example
├── README.md
└── go.mod
```

---

# 5. Database design

## 5.1 `users`

```sql
CREATE TABLE users (
    id          uuid        PRIMARY KEY,
    name        text        NOT NULL,
    role        text        NOT NULL
                            CHECK (role IN ('admin', 'editor', 'reader', 'reporter')),
    created_at  timestamptz NOT NULL DEFAULT now()
);
```

Anonymous users do not need a row in the DB.

---

## 5.2 `blogs`

```sql
CREATE TABLE blogs (
    id          uuid        PRIMARY KEY,
    title       text        NOT NULL
                            CHECK (char_length(title) BETWEEN 1 AND 255),
    author      text        NOT NULL
                            CHECK (char_length(author) BETWEEN 1 AND 100),
    content     text        NOT NULL
                            CHECK (char_length(content) BETWEEN 1 AND 100000),
    description text        NOT NULL DEFAULT ''
                            CHECK (char_length(description) <= 500),
    views       bigint      NOT NULL DEFAULT 0
                            CHECK (views >= 0),
    version     integer     NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    deleted_at  timestamptz,
    created_by  uuid        NOT NULL REFERENCES users(id),
    updated_by  uuid        NOT NULL REFERENCES users(id),
    deleted_by  uuid        REFERENCES users(id),
    CONSTRAINT blogs_deleted_pair
        CHECK ((deleted_at IS NULL) = (deleted_by IS NULL)),
    CONSTRAINT blogs_updated_after_created
        CHECK (updated_at >= created_at),
    CONSTRAINT blogs_deleted_after_created
        CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);
```

---

## 5.3 Active blog index

```sql
CREATE INDEX blogs_active_created_idx
ON blogs (created_at DESC, id DESC)
WHERE deleted_at IS NULL;
```

No separate index on `deleted_at` is needed.

Do not index `views` because the counter is updated frequently.

---

## 5.4 `blog_daily_views`

```sql
CREATE TABLE blog_daily_views (
    blog_id     uuid        NOT NULL REFERENCES blogs(id),
    view_date   date        NOT NULL,
    ip_hash     bytea       NOT NULL CHECK (octet_length(ip_hash) = 16),
    created_at  timestamptz NOT NULL,

    PRIMARY KEY (blog_id, view_date, ip_hash)
);
```

The uniqueness guarantee lives directly in the PK:

```text
(blog_id, UTC date, IP)
```

---

## 5.5 Why `DATE`

The business rule is:

```text
one IP / one blog / one UTC day
```

So `DATE` expresses the semantics more directly than `TIMESTAMPTZ 00:00 UTC`.

The app is responsible for deriving the date in UTC.

---

## 5.6 IP privacy

Raw IPs are not stored.

Flow:

```text
client IP
-> normalize
-> HMAC-SHA256(secret, canonical IP)
-> truncate 16 bytes
-> DB
```

Normalize with:

```go
netip.Addr.Unmap()
```

---

## 5.7 Relationships

```text
users 1 --- N blogs (created_by)
users 1 --- N blogs (updated_by)
users 1 --- N blogs (deleted_by)

blogs 1 --- N blog_daily_views
```

---

## 5.8 Capacity optimization [OPTIMIZATION]

Only do this when the dataset proves it is needed:

- partition `blog_daily_views` by `view_date`;
- monthly partitions;
- roll up historical counts;
- archive old dedup rows once it is certain this will not break the invariant.

---

# 6. View counting strategy

## 6.1 Approach A – PostgreSQL-only atomic [BASELINE]

```sql
WITH target AS (
    SELECT id
    FROM blogs
    WHERE id = $1
      AND deleted_at IS NULL
),
inserted AS (
    INSERT INTO blog_daily_views (
        blog_id,
        view_date,
        ip_hash,
        created_at
    )
    SELECT
        id,
        $2,
        $3,
        $4
    FROM target
    ON CONFLICT (blog_id, view_date, ip_hash)
    DO NOTHING
    RETURNING blog_id
)
UPDATE blogs
SET views = views + 1
WHERE id = (SELECT blog_id FROM inserted)
RETURNING views;
```

If it is a new unique view:

```text
INSERT -> 1 row
UPDATE views -> +1
```

If it is a duplicate:

```text
ON CONFLICT DO NOTHING
inserted = empty
UPDATE = 0 rows
```

Because everything is in a single SQL statement, the dedup row + counter increment are atomic.

---

## 6.2 Approach B – No stored counter

```sql
SELECT COUNT(*)
FROM blog_daily_views
WHERE blog_id = $1;
```

Not chosen because read cost grows with the number of views.

---

## 6.3 Approach C – Redis as the source of truth

Not chosen for the baseline because Redis/PG are not atomic together, and recovery and durability are more complex.

---

## 6.4 Approach D – PostgreSQL + Redis seen-cache [BONUS]

Only add after benchmarking.

```text
1. Redis EXISTS?
2. miss / Redis unavailable -> PostgreSQL atomic statement
3. PostgreSQL confirms state -> SET Redis cache
```

Redis does not decide correctness.

---

## 6.5 Approach E – Async counter aggregation [ADVANCED BONUS]

Only consider this if a hot-blog benchmark proves `blogs.views` is a hot row.

Trade-offs:

- eventual consistency;
- more complex code;
- requires crash-safe flushing.

---

## 6.6 Decision

```text
BASELINE = Approach A
```

Optimizations are only done after benchmarking.

---

# 7. Transaction design

## 7.1 Isolation

```text
READ COMMITTED
```

A unique constraint + atomic SQL is enough for the important races.

---

## 7.2 Create blog

```sql
INSERT INTO blogs (
    id,title,author,content,description,
    created_at,updated_at,created_by,updated_by
)
VALUES ($1,$2,$3,$4,$5,$6,$6,$7,$7)
RETURNING *;
```

---

## 7.3 Update blog

```sql
UPDATE blogs
SET
    title       = $2,
    author      = $3,
    content     = $4,
    description = $5,
    updated_at  = $6,
    updated_by  = $7,
    version     = version + 1
WHERE id = $1
  AND deleted_at IS NULL
RETURNING *;
```

Optional optimistic concurrency:

```sql
AND version = $expected_version
```

---

## 7.4 Soft delete

```sql
UPDATE blogs
SET
    deleted_at = $2,
    deleted_by = $3
WHERE id = $1
  AND deleted_at IS NULL
RETURNING id;
```

---

## 7.5 Read detail + view

```text
1. SELECT active blog
2. missing -> 404
3. resolve client IP
4. calculate UTC view_date
5. RecordUniqueView with an atomic PostgreSQL statement
6. DB view-write error -> 503/500
7. return detail
```

`RecordView` must re-check `deleted_at IS NULL` to block a delete race between step 1 and step 5.

---

# 8. Concurrency analysis

## 8.1 Same IP

1000 requests:

```text
same blog
same IP
same UTC date
```

Expected:

```text
blog_daily_views = 1
views +1
```

The PK + `ON CONFLICT DO NOTHING` guarantee the invariant.

---

## 8.2 Different IPs

1000 different IPs have 1000 different dedup keys.

Correctness still holds, but updating `blogs.views` at the same time can cause hot-row contention.

This is a performance bottleneck that needs benchmarking.

---

## 8.3 Multi-replica

Do not use a Go mutex as a correctness primitive.

The database constraint is the shared state across all replicas.

---

# 9. Pagination

## 9.1 Baseline OFFSET

```sql
SELECT id, title, description
FROM blogs
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT $1 OFFSET $2;
```

Fetch `limit + 1` to compute `has_next`.

No exact `COUNT(*)`.

---

## 9.2 Keyset [OPTIMIZATION]

```sql
WHERE deleted_at IS NULL
  AND (created_at, id) < ($cursor_created_at, $cursor_id)
ORDER BY created_at DESC, id DESC
LIMIT $limit;
```

Only add this if a deep-pagination benchmark shows it is needed.

---

# 10. Soft delete

Every public query must include:

```sql
deleted_at IS NULL
```

The repository prefers explicit methods:

```go
GetActive
ListActive
StreamActive
UpdateActive
DeleteActive
```

The baseline does not cache detail/list, to avoid stale deleted data.

---

# 11. RBAC / Mock authentication

Header:

```text
X-User-ID: <uuid>
```

Flow:

```text
missing -> Anonymous
malformed -> 401
not found -> 401
found -> Actor{ID, Role}
```

Permission matrix:

| Permission | Admin | Editor | Reader | Reporter | Anonymous |
|---|---:|---:|---:|---:|---:|
| blog:read | ✓ | ✓ | ✓ | ✓ | ✓ |
| blog:create | ✓ | ✓ | | | |
| blog:update | ✓ | ✓ | | | |
| blog:delete | ✓ | | | | |
| blog:export | ✓ | | | ✓ | |

AuthZ lives in the application/use case layer, not scattered across handlers.

---

# 12. Trusted proxy / Client IP

If the direct peer is not trusted:

```text
client IP = RemoteAddr
```

If it is trusted:

- parse XFF;
- walk from right to left;
- skip trusted hops;
- take the first untrusted hop.

Do not blindly trust the left-most XFF.

The resolver must be tested for:

- no proxy;
- one proxy;
- multiple proxies;
- forged XFF;
- invalid XFF;
- IPv4/IPv6.

---

# 13. CSV export

Admin/Reporter only.

Query:

```sql
SELECT
    id,title,author,description,views,
    created_at,updated_at,created_by,updated_by
FROM blogs
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC;
```

Streaming:

```text
pgx rows
-> csv.Writer
-> HTTP response
```

Do not load the whole dataset into RAM.

Timestamps are output as UTC RFC3339.

Sanitize CSV injection for cells starting with `= + - @`.

---

# 14. Performance design

Baseline:

```text
Go + PostgreSQL
no Redis
no content cache
no async counter
```

Measure with:

- pprof;
- pg_stat_statements;
- PostgreSQL wait events;
- pgxpool stats;
- EXPLAIN ANALYZE BUFFERS;
- k6;
- docker/host metrics.

Do not turn off durability settings just to make the benchmark look better.

---

# 15. Caching strategy

## Baseline

No Redis, no list/detail cache.

## Bonus

Prefer a `seen-cache` over a content cache if a benchmark proves duplicate views cause DB load.

Only add a content cache with a benchmark, and its invalidation must be tested against soft delete.

---

# 16. Testing strategy

## Unit

- validation;
- RBAC matrix;
- UTC date boundary;
- client IP resolver;
- IP hash;
- application permission/error behavior.

## Handler

- 6 endpoints x roles;
- invalid UUID;
- invalid body;
- bad pagination;
- unknown JSON field;
- payload too large.

## Integration – real PostgreSQL

- migrations;
- CRUD;
- audit;
- soft delete;
- pagination;
- unique view;
- concurrency.

### Same IP concurrency

```text
100 goroutines
same blog
same IP
same date
=> rows 1, views +1
```

### Different IP concurrency

```text
100 goroutines
same blog
100 IPs
same date
=> rows 100, views +100
```

### Next day

```text
same blog
same IP
day 1 + day 2
=> +2
```

### List

```text
GET /blogs
=> does not increase views
```

Also run:

```bash
go test -race ./...
```

---

# 17. Error response

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "message": "invalid request body",
    "details": [
      {
        "field": "title",
        "message": "is required"
      }
    ],
    "request_id": "..."
  }
}
```

| Case | HTTP |
|---|---:|
| invalid input | 400 |
| unauthenticated | 401 |
| forbidden | 403 |
| missing/deleted | 404 |
| version conflict | 409 |
| payload too large | 413 |
| unsupported media | 415 |
| PostgreSQL unavailable | 503 |
| unexpected error | 500 |

Success:

```text
POST 201
GET 200
PUT 200
DELETE 204
CSV 200
```

---

# 18. Observability

`slog` JSON fields:

```text
timestamp
level
request_id
method
route
status
latency_ms
bytes
user_id
error_code
```

Do not log raw IPs.

Endpoints:

```text
/healthz
/readyz
```

Graceful shutdown:

```text
SIGTERM
-> stop new traffic
-> server shutdown
-> close dependencies
```

---

# 19. Docker Compose

Baseline:

```text
postgres
migrate
api
```

Optional profiles:

```text
redis
nginx
seed
k6
```

Startup:

```text
postgres healthy
-> migrate
-> api
```

The README must verify:

```bash
docker compose up --build
```

---

# 20. Load testing

Use k6 arrival-rate.

Scenarios:

| Scenario | Goal |
|---|---|
| List | target 10k/s |
| Detail repeated IP | target 10k/s |
| Detail unique IP | counter contention |
| Create | target 1k/s |
| Update | target 1k/s |
| Same-IP concurrent | correctness |
| Different-IP concurrent | correctness/contention |
| Mixed | realistic |
| Hot blog | stress |

Metrics:

```text
throughput
p95
p99
error rate
dropped iterations
```

Reproducibility:

- OS;
- CPU/RAM;
- Docker version;
- Go/PostgreSQL version;
- resource limits;
- dataset;
- k6 config;
- duration;
- warm-up;
- commit SHA.

Optimization loop:

```text
baseline
-> profile
-> bottleneck
-> one optimization
-> rerun
-> compare
```

---

# 21. Deliverables

Must include:

```text
source code
README.md
docs/architecture.md
docs/database.md
docs/technical-decisions.md
docs/performance-report.md
openapi.yaml
migrations
unit tests
integration tests
Docker Compose
```

README:

- setup;
- env;
- seed users;
- API examples;
- tests;
- load tests.

Performance report:

- environment;
- dataset;
- throughput;
- p95/p99;
- errors;
- bottlenecks;
- before/after optimization.

---

# 22. Implementation plan

## Phase 0 – Design freeze

Output:

```text
docs/design.md
```

## Phase 1 – Bootstrap

```text
config
HTTP server
slog
health/readiness
graceful shutdown
Dockerfile
compose
Makefile
```

## Phase 2 – Database

```text
users
blogs
blog_daily_views
indexes
constraints
seed users
migrations
```

## Phase 3 – Domain/Application

```text
Blog
Actor/Role/Permission
ViewKey
validation
errors
ports
Clock
```

## Phase 4 – PostgreSQL adapter

```text
GetActive
ListActive
Create
UpdateActive
DeleteActive
StreamActive
RecordUniqueView
```

Integration tests.

## Phase 5 – HTTP/Auth/Client IP

Middleware + CRUD handlers.

RBAC and validation tests.

## Phase 6 – Unique view

Atomic CTE + HMAC IP + concurrency tests.

This is the most important phase.

## Phase 7 – CSV

Streaming + permissions + deleted exclusion.

## Phase 8 – Docs/OpenAPI

```text
README
architecture
database
technical decisions
openapi
```

## Phase 9 – Performance baseline

k6 + seed + performance report.

## Phase 10 – Bonus optimization

Choose only based on benchmarks:

- Redis seen-cache;
- keyset pagination;
- pool tuning;
- async counter.

## Phase 11 – Final verification

```text
docker compose up works
tests pass
race pass
OpenAPI matches
README commands verified
performance report reproducible
view invariant verified
no deleted blog leak
```

---

# 23. Design decision summary

| Decision | Baseline | Reason |
|---|---|---|
| HTTP | Gin | Simple |
| DB | PostgreSQL | constraints + UPSERT |
| DB access | pgx + SQL | concurrency SQL explicit |
| ID | UUID | requirement-compatible |
| Timestamp | TIMESTAMPTZ UTC | absolute time |
| View date | DATE from UTC | correct semantic |
| View dedup | PostgreSQL PK | exact correctness |
| Counter | atomic `views+1` | fast reads |
| Redis | no baseline | benchmark first |
| Content cache | no baseline | avoid stale delete |
| Auth | X-User-ID | don't trust client role |
| AuthZ | permission matrix | centralized policy |
| Pagination | page/limit | requirement-first |
| IP | trusted-proxy resolver | security |
| IP persistence | HMAC | privacy |
| Isolation | READ COMMITTED | enough with constraints |
| Integration DB | testcontainers | real concurrency |
| Load test | k6 | p95/p99 + arrival-rate |
| PG failure during view | fail request | correctness-first |

---

# 24. Interview defense

## Why PostgreSQL for views?

A unique constraint is a shared correctness primitive across multiple app replicas.

## How are duplicates avoided?

```sql
INSERT ... ON CONFLICT DO NOTHING
```

and the counter only increases when the insert returns a row.

## Why not a mutex?

A mutex only works within one process.

## Why not Redis-only?

Redis + PG create an unnecessary consistency/recovery problem for the baseline.

## What if Redis goes down?

The baseline does not need Redis. The bonus cache fails open to PostgreSQL.

## What if PostgreSQL goes down during detail?

The baseline returns 503 instead of returning success while losing the view.

## 10k from the same IP?

One unique row, one increment.

## 10k from different IPs?

Correctness holds, but `blogs.views` can become a hot row; benchmark first, then optimize.

## Why DATE?

The business key is the UTC calendar day, so DATE is the most semantically correct choice.

## Why not cache detail?

Soft-delete correctness matters more than a bonus cache.

## How do we reach 10k TPS?

No guessing. Benchmark -> profile -> optimize -> benchmark again.

## What if we don't reach it?

Report actual throughput, p95/p99, the bottleneck, before/after, and next steps.

---

# Conclusion

The baseline should be:

```text
Go
+ Gin
+ PostgreSQL
+ pgx
+ Clean/Hexagonal boundaries
+ atomic PostgreSQL unique-view
+ Docker Compose
+ automated tests
+ k6 benchmark
```

Redis, content caching, keyset pagination and async aggregation are only **bonus/optimizations after benchmarking**.

The most important points when a reviewer evaluates the solution:

1. Unique-view correctness lives in PostgreSQL, not in process memory.
2. Dedup + counter increment are atomic.
3. Soft-deleted blogs do not leak into public APIs/reports.
4. Trusted proxies are whitelisted.
5. Concurrency tests use a real PostgreSQL.
6. Performance is measured, not guessed.
7. The architecture is just enough, with no over-engineering.
