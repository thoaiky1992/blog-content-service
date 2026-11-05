# CLAUDE.md

This file guides coding agents working in this repository.

## Project status

This is a complete Go Blog Content Service with APIs, migrations, PostgreSQL and Redis
adapters, unit tests, integration tests, load tests, OpenAPI, and architecture
documentation. Do not reference the former files under `docs/` or `requirement.md`; they
are no longer part of the current repository structure.

When changing an endpoint, update `README.md`, `openapi.yaml`, and `ARCHITECTURE.md` when
the architectural flow changes. No automated check currently verifies that code and
documentation remain synchronized.

## Common commands

```bash
docker compose up --build
go test ./test/unit/...
go test ./test/integration/...
go test ./...
go test -race ./...
go vet ./...
```

Load tests use k6. Scenarios live in `loadtest/k6/`, and deterministic seed data lives
in `loadtest/seed/`.

## Technology and architecture

The stack is Go, Gin, PostgreSQL, Redis, pgx/v5 + pgxpool, Goose migrations,
hand-written SQL, `log/slog` JSON, and k6. PostgreSQL is the source of truth. Redis only
caches actors and committed view markers; the service remains correct when `REDIS_ADDR`
is empty.

Dependency direction:

```text
transport/http → app → domain ← adapter/postgres
                         ↑     ← adapter/redis
```

- `domain` must not import Gin, pgx, Redis, or HTTP DTOs.
- Interfaces are declared where they are consumed in `app/ports.go`.
- Do not introduce a generic repository, UnitOfWork, event bus, CQRS, or distributed
  lock without an explicit requirement.
- `internal/transport/http/clientip` handles trusted proxies;
  `internal/adapter/clock` implements the injectable UTC clock port.
- Middleware order is recover → request-id → access log → CORS → client-ip → mock auth.

## Core invariants

**Unique daily view = (`blog_id`, UTC date, `ip_address`).** The PostgreSQL primary key
on `blog_daily_views` prevents duplicates. One SQL statement uses a CTE to validate
the active blog, run
`INSERT ... ON CONFLICT DO NOTHING`, and increment `blogs.views` only when the insert
succeeds and the blog is still active (`deleted_at IS NULL`). Do not replace this
mechanism with a Go mutex, Redis lock, or SELECT-then-INSERT flow.

- Only `GET /blogs/{id}` records a view; list endpoints never do.
- A missing or soft-deleted blog returns `404` and records no view.
- A view-write failure returns `503` or `500`; never return `200` while silently losing
  the view.
- `view_date` comes from `clock.Now().UTC()` in the application layer.
- PostgreSQL stores the normalized address as `inet`; Redis keys contain only a
  truncated SHA-256 hash of the address. Access logs never contain raw IP addresses.
- Redis markers are written only after PostgreSQL succeeds. Cache misses and failures
  must fall back to PostgreSQL.

**Soft delete:** every public query and CSV report must filter on
`deleted_at IS NULL`. Update and delete statements must use the same condition so a
deleted blog cannot be revived.

**Client IP:** forwarded headers are trusted only when the direct peer belongs to
`TRUSTED_PROXIES`. Walk `X-Forwarded-For` from right to left, skip trusted hops, and use
the first untrusted hop.

**Mock authentication:** `X-User-ID` is resolved through the `users` table. A missing
header means Anonymous; a malformed or unknown UUID returns `401`. Roles always come
from the database, never from the client.

## API conventions

- Pagination uses `page` ≥ 1 and `limit` from 1 to 100, defaulting to 20. The repository
  fetches `limit+1` rows to compute `has_next` and does not run `COUNT(*)`.
- `PUT` replaces every editable field. `version` is an optional optimistic lock.
- JSON rejects unknown fields, has a 1 MB body limit, and accepts only
  `application/json`.
- Timestamps are returned as RFC3339 UTC.
- CSV is streamed directly from pgx rows through `csv.Writer`; cells beginning with
  `=`, `+`, `-`, `@`, tab, or CR must be neutralized.
- Error envelopes use
  `{"error":{"code","message","details":[{"field","message"}],"request_id"}}`.

## Performance work

Do not publish a TPS claim without a k6 run that records the environment, dataset, VUs,
p95/p99, error rate, and commit SHA. Add optimizations only after a benchmark identifies
the bottleneck, and never disable PostgreSQL durability to improve benchmark numbers.
