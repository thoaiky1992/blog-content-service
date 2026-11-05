# Blog Content Service

A Go REST API for blog content with role-based access, soft delete, CSV export and
**unique daily view counting** (one view per blog + client IP + UTC day), backed by
PostgreSQL.

- Go, Gin, PostgreSQL 18, pgx/v5, Goose migrations, hand-written SQL, `log/slog` JSON
- Hexagonal layout: `transport/http` → `app` → `domain` ← `adapter/postgres`
- Correctness of the view counter comes from a PostgreSQL primary key, not from
  process memory, so it holds across any number of instances

More detail: [architecture](docs/architecture.md) · [database](docs/database.md) ·
[technical decisions](docs/technical-decisions.md) · [OpenAPI](openapi.yaml) ·
[design](docs/design.md) · [assignment](requirement.md)

## Quick start

Requires Docker with Compose.

```bash
docker compose up --build
```

This starts `postgres` → `migrate` (goose applies `migrations/`) → `api` on
http://localhost:8080.

```bash
curl localhost:8080/healthz     # {"status":"ok"}
curl localhost:8080/readyz      # {"status":"ready"}  (503 if PostgreSQL is unreachable)
```

Stop with `docker compose down` (add `-v` to also delete the database volume).

### Run without Docker for the API

```bash
cp .env.example .env            # edit values if needed
docker compose up -d postgres migrate
make run                        # sources .env, then go run ./cmd/api
```

## Configuration

Environment variables (see [.env.example](.env.example)):

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | – (required) | PostgreSQL connection string |
| `IP_HASH_SECRET` | – (required) | Secret for the HMAC of client IPs. Raw IPs are never stored |
| `HTTP_ADDR` | `:8080` | Listen address |
| `TRUSTED_PROXIES` | empty | Comma-separated CIDRs of reverse proxies whose `X-Forwarded-For` is trusted. Empty = never trust forwarded headers |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown deadline |
| `DB_MAX_CONNS` | `20` | pgx pool size |

> If you run behind Nginx or a load balancer, set `TRUSTED_PROXIES` to its address
> range. Otherwise every visitor looks like the proxy's IP and views are
> under-counted.

## Authentication (mock)

There is no login. Send the header `X-User-ID: <uuid>` of a seeded user:

- no header → **Anonymous** (can read blogs)
- malformed or unknown ID → `401`
- the role always comes from the database, never from the client

The migrations seed one user per role. Their IDs are generated, so look them up:

```bash
docker exec postgres psql -U blog -d blog -c "select id, name, role from users order by role"
```

| Role | Read | Create | Update | Delete | CSV export |
|---|:-:|:-:|:-:|:-:|:-:|
| Admin | ✓ | ✓ | ✓ | ✓ | ✓ |
| Editor | ✓ | ✓ | ✓ | | |
| Reader | ✓ | | | | |
| Reporter | ✓ | | | | ✓ |
| Anonymous | ✓ | | | | |

An anonymous caller lacking a permission gets `401`; an authenticated one gets `403`.

## API

Base path `/api/v1`. Full spec: [openapi.yaml](openapi.yaml).

| Method | Path | Description |
|---|---|---|
| GET | `/blogs?page=1&limit=20` | List (`id`, `title`, `description`). Never counts views |
| GET | `/blogs/{id}` | Detail. Records the unique daily view |
| POST | `/blogs` | Create (Editor, Admin) → `201` + `Location` |
| PUT | `/blogs/{id}` | Replace title/author/content/description (Editor, Admin) |
| DELETE | `/blogs/{id}` | Soft delete (Admin) → `204` |
| GET | `/reports/blogs` | Streamed CSV (Admin, Reporter) |

```bash
EDITOR=<editor id>; ADMIN=<admin id>; REPORTER=<reporter id>

# Create
curl -i -X POST localhost:8080/api/v1/blogs \
  -H "X-User-ID: $EDITOR" -H 'Content-Type: application/json' \
  -d '{"title":"Hello","author":"Sky","content":"First post","description":"Intro"}'

# List, then read one blog (repeat it: views stays 1 for the same IP on the same UTC day)
curl 'localhost:8080/api/v1/blogs?page=1&limit=5'
curl localhost:8080/api/v1/blogs/<id>

# Update (optional "version" enables optimistic locking → 409 on mismatch)
curl -X PUT localhost:8080/api/v1/blogs/<id> \
  -H "X-User-ID: $EDITOR" -H 'Content-Type: application/json' \
  -d '{"title":"Hello v2","author":"Sky","content":"Edited","description":"","version":1}'

# Soft delete, then it is gone from detail, list and the report
curl -X DELETE localhost:8080/api/v1/blogs/<id> -H "X-User-ID: $ADMIN"

# CSV report
curl localhost:8080/api/v1/reports/blogs -H "X-User-ID: $REPORTER" -o blogs.csv
```

Rules worth knowing:

- `page` ≥ 1, `limit` 1..100 (default 20). Out-of-range values are `400`, not clamped.
  The list returns `has_next` instead of a total count.
- Request bodies: `Content-Type: application/json`, max 1 MB (`413`), unknown fields
  rejected (`400`), title 1–255, author 1–100, content 1–100000, description ≤ 500
  characters.
- Timestamps are RFC3339 in UTC.
- If PostgreSQL cannot record a view, detail returns `503` rather than `200` with a
  silently lost view.

Every error uses one envelope:

```json
{"error":{"code":"VALIDATION_FAILED","message":"invalid request",
          "details":[{"field":"title","message":"is required"}],
          "request_id":"a80dba855a5f03ec"}}
```

| Status | Code |
|---|---|
| 400 | `VALIDATION_FAILED`, `INVALID_JSON` |
| 401 | `UNAUTHENTICATED` |
| 403 | `FORBIDDEN` |
| 404 | `NOT_FOUND` |
| 405 | `METHOD_NOT_ALLOWED` |
| 409 | `VERSION_CONFLICT` |
| 413 | `PAYLOAD_TOO_LARGE` |
| 415 | `UNSUPPORTED_MEDIA_TYPE` |
| 500 / 503 | `INTERNAL_ERROR` / `SERVICE_UNAVAILABLE` |

## Tests

```bash
go test ./...                  # unit + handler tests; integration tests need Docker
go test -race ./...            # also run this before finishing a change
go test ./test/integration/... -run TestHTTPConcurrentSameIPCountsOnce -v   # one test
```

- **Unit / handler tests** use in-memory fakes (`internal/app/apptest`) behind the real
  services and router: the permission matrix for all 6 endpoints × 5 roles, validation,
  pagination, UUID and body errors, client-IP resolution, CSV format and injection
  defense.
- **Integration tests** (`test/integration`) start a real `postgres:18` with
  testcontainers-go, apply the real migrations, and cover CRUD and audit fields,
  soft delete, pagination, and the concurrency cases: 100 concurrent requests from the
  same IP → 1 view; from 100 IPs → 100 views; across the UTC midnight boundary; behind a
  trusted proxy; a 1200-row CSV export. Without Docker they skip themselves.

## Load tests and performance

Not done yet. Phase 9 of the [design](docs/design.md) adds k6 scenarios and
`docs/performance-report.md`. No throughput figure should be quoted before that run.

## Make targets

`make build | run | test | test-race | vet | up | down`
