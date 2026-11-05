# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

This is a technical assignment: a Go **Blog Content Service**. Right now the repo has only specs, no code yet:

- [requirement.md](requirement.md): the original assignment. It lists functional rules, required tests, deliverables, grading weights and bonus challenges.
- [docs/design.md](docs/design.md): the frozen design and phased implementation plan (Phase 0–11). **This is the source of truth.** When the design and the code disagree, follow the design or update it on purpose. Labels used there: `[REQUIRED]`, `[BONUS]`, and `[OPTIMIZATION]` (only after a benchmark shows the need).

Build the phases in order (design.md §22). Phase 6 (unique view) is the most important one.

## Commands

Phase 1 (bootstrap) is done: config, Gin server, slog JSON, `/healthz` + `/readyz` (pings PostgreSQL), graceful shutdown, Dockerfile, compose, Makefile. Required env vars are in `.env.example` (only `DATABASE_URL` is mandatory). `make build|run|test|test-race|vet|up|down|seed` wrap the commands below. Phase 2 (database) is done too: Goose migrations in `migrations/` (users, blogs + partial index, blog_daily_views, seed users). Compose runs postgres → `migrate` (built from `Dockerfile.migrate`, goose only) → api. Seed users (admin, editor, reader, reporter) get DB-generated `uuidv7()` IDs, so look them up (`SELECT id, role FROM users`) to use as `X-User-ID`. Phase 3 (domain/app) is done: domain types, permission matrix, validation, ViewKey, ports in `internal/app/ports.go`, BlogService/ViewService/ReportService, `platform/clock`, and in-memory fakes in `internal/app/apptest`. Anonymous lacking a permission gets `ErrUnauthenticated` (401), an authenticated user gets `ErrForbidden` (403). `limit` outside 1..100 or `page` < 1 is a validation error, not clamped. Phase 4 (Postgres adapter) is done: `internal/adapter/postgres` (BlogRepo, ViewStore, UserRepo, `mapErr` turning connectivity errors into `ErrUnavailable`). All test files are centralized in `test/unit` and `test/integration`; each suite covers the six public APIs. Integration tests connect directly to the existing PostgreSQL schema, never truncate it, and clean up only rows they create. Phases 5–8 are done too: Gin handlers, mock-auth/client-ip middleware, `platform/clientip`, streamed CSV, README, `docs/architecture.md`, `docs/database.md`, `docs/technical-decisions.md`, and a validated `openapi.yaml`. Phase 9 is done: four standalone k6 scenarios in `loadtest/k6/` and deterministic seed data in `loadtest/seed/`. Only Phases 10–11 remain (benchmark-driven optimizations, final verification). Any new TPS number needs a k6 run that records the environment, dataset, VUs, p95/p99, error rate and commit SHA. Error mapping and context accessors live in `transport/http/httpx` (not `transport/http/errors.go`) to avoid an import cycle. When you change an endpoint, update `openapi.yaml` and README by hand: nothing checks them against the code. Commands marked "not yet" depend on later phases.

```bash
docker compose up --build                            # postgres (healthy) -> migrate (goose) -> api
go test ./test/unit/...                              # six APIs with in-memory repositories
go test ./test/integration/...                       # six APIs against current PostgreSQL
go test ./...                                        # both suites
go test -race ./...                                  # required in final verification
```

Load tests use k6 with arrival-rate scenarios. They live in `loadtest/k6/`, and the seed scripts live in `loadtest/seed/`.

## Stack

Go, Gin, PostgreSQL, Redis, pgx/v5 + pgxpool, Goose migrations, **hand-written SQL (no ORM)**, `log/slog` JSON, k6. Redis caches actors and committed view markers, but PostgreSQL remains the source of truth and the app can run with `REDIS_ADDR` empty.

## Architecture (hexagonal)

`transport/http` (Gin handlers, DTOs, middleware) → `app` (use cases: BlogService, ViewService, ReportService; **authorization lives here**, not in handlers) → `domain` (Blog, Actor/Role/Permission, ViewKey, domain errors) ← `adapter/postgres` implements the interfaces declared in `app/ports.go`.

- `domain` must not import Gin, pgx, Redis or HTTP DTOs.
- Interfaces are defined where they are consumed (in `app`). Do not add a generic repository, UnitOfWork, event bus, CQRS or distributed locks.
- `platform/` holds small helpers: `clientip` (trusted-proxy resolver), `clock` (injectable UTC clock for date-boundary tests).
- Middleware order: recover → request-id → access log → client-ip → mock auth.

## Core invariants (don't break these)

**Unique daily view = (blog_id, UTC `DATE`, ip_address).** Correctness comes from the PostgreSQL PK on `blog_daily_views`, never from a Go mutex, a SELECT-then-INSERT check or Redis. The dedup insert and the counter increment run in **one SQL statement**: a CTE that targets the active blog, then `INSERT ... ON CONFLICT DO NOTHING RETURNING`, then `UPDATE blogs SET views = views + 1 WHERE id = (SELECT blog_id FROM inserted)`. Redis only caches markers written after that statement succeeds; misses/errors fall back to PostgreSQL. The invariant is `blogs.views == COUNT(blog_daily_views WHERE blog_id = X)`.

- Only `GET /blogs/{id}` records a view. `GET /blogs` (list) never does.
- If the blog is missing or deleted, return 404 and record no view. The view statement re-checks `deleted_at IS NULL` to close the race with a concurrent delete.
- If the DB view write fails, return 503 (or 500). Never return 200 while silently dropping the view.
- `view_date` is computed in the app from `clock.Now().UTC()`, not by DB `now()`.
- Raw IPs are never stored or logged. Normalize with `netip.Addr.Unmap()`, then HMAC-SHA256 with a secret, truncated to 16 bytes.

**Soft delete:** every public query and the CSV report filter on `deleted_at IS NULL`. Repo methods are named for this (`GetActive`, `ListActive`, `StreamActive`, `UpdateActive`, `DeleteActive`). Update and delete include `AND deleted_at IS NULL` so a deleted blog is never revived. The baseline has no detail/list cache, so deleted blogs can't leak through a stale cache.

**Client IP:** forwarded headers are trusted only when the direct peer is in the `TRUSTED_PROXIES` CIDR list (empty by default). Walk XFF from right to left, skip trusted hops, and take the first untrusted one. Never trust the left-most XFF blindly.

**Mock auth:** the `X-User-ID: <uuid>` header is looked up in the `users` table. Missing header → Anonymous. Malformed or unknown ID → 401. The role always comes from the DB, never from the client. The permission matrix is in design.md §11. Editors may update any blog (there is no ownership rule).

## API conventions

- List uses `page` (≥1) and `limit` (default 20, max 100). It fetches `limit+1` rows to compute `has_next` and returns no `total` (no `COUNT(*)`). Items contain `id, title, description`. Order is `created_at DESC, id DESC`, backed by the partial index `blogs_active_created_idx`.
- PUT replaces all editable fields (title, author, content, description). `version` gets incremented; optional optimistic checking returns 409.
- JSON: unknown fields → 400. The body limit is 1 MB (→ 413). Validation limits are in design.md §2.11 and are mirrored as DB CHECK constraints.
- Timestamps are `TIMESTAMPTZ` and are returned as RFC3339 UTC.
- Error envelope: `{"error":{"code","message","details":[{"field","message"}],"request_id"}}`. The status mapping is in design.md §17.
- CSV export streams pgx rows → `csv.Writer` → the response, without loading everything into memory. It sanitizes cells that start with `= + - @`.
- Isolation level is READ COMMITTED throughout.

## Performance work

Correctness comes before performance. Don't claim a TPS number without a k6 run that records the environment, dataset, VUs, p95/p99, error rate and commit SHA. Optimizations (keyset pagination, pool tuning, async counter) are added one at a time, only after a benchmark shows the bottleneck, with before/after numbers. Don't turn off PostgreSQL durability settings to improve benchmark numbers.
