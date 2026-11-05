# Blog Content Service

A Go REST API for blog content with role-based access, soft delete, CSV export and
**unique daily view counting** (one view per blog + client IP + UTC day), backed by
PostgreSQL, with Redis caches for authenticated actors and committed view markers.

- Go, Gin, PostgreSQL 18, Redis 8, pgx/v5, Goose migrations, hand-written SQL, `log/slog` JSON
- Hexagonal layout: `transport/http` → `app` → `domain` ← `adapter/{postgres,redis}`
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

This starts PostgreSQL, Redis, migrations, then the API on
http://localhost:8080.

```bash
curl localhost:8080/healthz     # {"status":"ok"}
curl localhost:8080/readyz      # {"status":"ready"}  (503 if PostgreSQL is unreachable)
```

Stop with `docker compose down` (add `-v` to also delete the database volume).

### Run without Docker for the API

```bash
cp .env.example .env            # edit values if needed
docker compose up -d postgres redis migrate
make run                        # sources .env, then go run ./cmd/api
```

## Configuration

Environment variables (see [.env.example](.env.example)):

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | – (required) | PostgreSQL connection string |
| `HTTP_ADDR` | `:8080` | Listen address |
| `TRUSTED_PROXIES` | empty | Comma-separated CIDRs of reverse proxies whose `X-Forwarded-For` is trusted. Empty = never trust forwarded headers |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `SHUTDOWN_TIMEOUT` | `15s` | Graceful shutdown deadline |
| `DB_MAX_CONNS` | `20` | pgx pool size |
| `REDIS_ADDR` | empty (disabled) | Redis address, for example `localhost:6379` |
| `REDIS_PASSWORD` | empty | Redis password |
| `REDIS_DB` | `0` | Redis logical database |
| `REDIS_ACTOR_TTL` | `5m` | TTL for successful actor lookups |
| `REDIS_VIEW_TTL` | `48h` | TTL for committed unique-view markers |

Redis is a derived cache, not the source of truth. Actor cache misses/errors query
PostgreSQL. The view Lua script checks only markers written after PostgreSQL commits;
Redis misses/errors execute the original atomic PostgreSQL view statement.

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
go test ./test/unit/...          # six APIs with in-memory repositories
go test ./test/integration/...   # six APIs against the currently running PostgreSQL
TEST_DATABASE_URL='postgres://user:pass@host/db?sslmode=disable' go test ./test/integration/...
go test ./...                    # run both suites; integration needs PostgreSQL
go test -race ./...              # run both suites with the race detector
```

- **Unit tests** (`test/unit`) use six endpoint-specific files with in-memory repositories
  behind the real services and router. Each endpoint covers its valid response, invalid
  inputs and the role permission matrix.
- **Integration tests** (`test/integration`) run the same six APIs through the production
  PostgreSQL adapters. They connect to `TEST_DATABASE_URL`, then `DATABASE_URL`, or the
  default Compose database at `localhost:5432`. Tests use the existing schema directly,
  never truncate tables, and delete only blog IDs created by the tests themselves.

## Load tests and performance

The load tests use a local k6 installation. Each script contains its own
arrival-rate stages and thresholds:

| Script | Operation |
|---|---|
| `loadtest/k6/read_blogs.js` | `GET /api/v1/blogs` |
| `loadtest/k6/read_detail_blog.js` | `GET /api/v1/blogs/:id` |
| `loadtest/k6/create_blog.js` | `POST /api/v1/blogs` |
| `loadtest/k6/update_blog.js` | `PUT /api/v1/blogs/:id` |

Start the service, seed the deterministic blog dataset, and get an editor ID for the
authenticated create/update requests:

```bash
docker compose up -d --build
make seed
EDITOR_ID="$(docker exec postgres psql -U blog -d blog -Atc \
  "SELECT id FROM users WHERE role = 'editor' ORDER BY id LIMIT 1")"
```

Run each script separately:

```bash
# List blogs
k6 run loadtest/k6/read_blogs.js

# Blog detail; DETAIL_POOL defaults to 1,000 seeded blogs
k6 run -e DETAIL_POOL=1000 loadtest/k6/read_detail_blog.js

# Blog detail with the k6 Web Dashboard
K6_WEB_DASHBOARD=true k6 run loadtest/k6/read_detail_blog.js

# Create blogs; EDITOR_ID must have blog:create permission
k6 run -e EDITOR_ID="$EDITOR_ID" loadtest/k6/create_blog.js

# Update seeded blogs; ROWS must not exceed the number passed to make seed
k6 run -e EDITOR_ID="$EDITOR_ID" -e ROWS=1000000 loadtest/k6/update_blog.js
```

When the k6 Web Dashboard is enabled, view it in a browser at
[http://127.0.0.1:5665](http://127.0.0.1:5665).

All scripts default to `http://localhost:8080/api/v1`. Use
`-e BASE_URL=http://host:port/api/v1` to target another service instance. For detail
testing, pass `-e BLOG_ID=<uuid>` to target one blog; for update testing, the same
option overrides random selection from the seeded rows.

The seed is additive and idempotent. Change its size with `ROWS=10000 make seed`; this
adds deterministic blogs through `blog-10000` and does not remove existing rows.

Reset the local database with `docker compose down -v` if a clean dataset is required.

## Make targets

`make build | run | test-unit | test-integration | test-race | vet | up | down | seed`
