# Architecture

The service follows a hexagonal (ports and adapters) layout. Dependencies point inward:
HTTP and PostgreSQL code depend on the application and domain layers, never the reverse.

```text
 Client ─▶ Trusted proxy / LB
              │
   ┌──────────▼───────────────────────────┐
   │ transport/http   (Gin)               │  HTTP <-> use cases
   │  middleware: recover, request-id,    │
   │   access log, client-ip, mock auth   │
   │  handler: blog, report, health       │
   │  httpx: error envelope, ctx helpers  │
   └──────────┬───────────────────────────┘
              │ calls
   ┌──────────▼───────────────────────────┐
   │ app   (use cases + ports)            │  authorization, orchestration
   │  BlogService  ViewService            │
   │  ReportService                       │
   │  ports.go: interfaces it needs       │
   └──────────┬───────────────────────────┘
              │ uses
   ┌──────────▼───────────────────────────┐
   │ domain   (no framework imports)      │  rules and types
   │  Blog, BlogInput.Validate, PageRequest│
   │  Actor/Role/Permission, ViewKey      │
   │  sentinel errors                     │
   └──────────────────────────────────────┘
              ▲ implements app ports
   ┌──────────┴───────────────────────────┐
   │ adapter/postgres  (pgx, SQL)         │  the source of truth
   │  BlogRepo, ViewStore, UserRepo       │
   └──────────────────────────────────────┘
```

## Layers and responsibilities

| Layer | Package | Responsibility | May import |
|---|---|---|---|
| Domain | `internal/domain` | Entities, value objects, validation, the permission matrix, `ViewKey`/`ViewDate`, sentinel errors | stdlib, `uuid` only |
| Application | `internal/app` | Use cases, **authorization**, orchestration; declares the ports (`BlogRepository`, `ViewStore`, `UserRepository`, `IPHasher`, `Clock`) | domain |
| Adapter | `internal/adapter/postgres` | Implements the ports with hand-written SQL; maps DB errors to domain errors | app, domain, pgx |
| Transport | `internal/transport/http` | Routing, JSON/DTOs, middleware, status codes and the error envelope | app, domain, Gin |
| Platform | `internal/platform/*` | Small infrastructure helpers: `clientip` (trusted-proxy resolver), `iphash` (HMAC), `clock` | domain |
| Wiring | `cmd/api` | Config, constructs everything, graceful shutdown | all |

Rules that keep the layers honest:

- `domain` never imports Gin, pgx, Redis or HTTP DTOs.
- Interfaces are declared in `app/ports.go`, where they are consumed. Adapters implement
  them. There is no generic repository, unit-of-work or event bus.
- **Authorization lives in the application layer** (`Actor.Require(permission)`), not in
  handlers. Handlers only turn the request into a call and the result into JSON. The
  one exception is deliberate: `POST`/`PUT` check the permission *before* decoding the
  body so an unauthorized caller cannot make the server parse a large payload.
- Dependency injection is plain constructors; `cmd/api/main.go` is the only place that
  knows the concrete types. `transport.Deps` is the router's dependency bundle.

Small deviation from the design's folder sketch: error mapping and context accessors
live in `transport/http/httpx` instead of `transport/http/errors.go`, because both the
middleware and the handlers need them and Go forbids the import cycle that
`handler → http → handler` would create. The DTOs live in `handler/dto.go`.

## Request flow

Middleware order (`transport/http/router.go`):

```text
recover → request-id → access log → client-ip → mock auth → handler
```

`/healthz` and `/readyz` sit outside the API group, so they need no auth or client IP.

1. **recover** turns a panic into a 500 envelope. A deliberate `http.ErrAbortHandler`
   (a CSV export that failed mid-stream) is re-raised so the connection is cut instead
   of ending in a truncated-but-"complete" file.
2. **request-id** reuses a safe incoming `X-Request-ID` (≤ 64 chars of `[A-Za-z0-9._-]`)
   or generates one, and echoes it in the response and every error body.
3. **access log** writes one JSON line: request id, method, route pattern, status,
   latency, bytes, `user_id`, `error_code`. It never logs an IP.
4. **client-ip** resolves the client address once (see below).
5. **mock auth** reads `X-User-ID`, loads the user (and role) from PostgreSQL.

### Client IP

`platform/clientip.Resolver`:

- Peer not in `TRUSTED_PROXIES` → use the TCP peer address; forwarded headers are ignored
  because a client can forge them.
- Peer trusted → walk `X-Forwarded-For` right to left, skip trusted hops, take the first
  untrusted one. The left-most entry is never trusted blindly.
- An invalid entry means a corrupt header → fall back to the peer address.
- Addresses are normalized (IPv4-mapped IPv6 → IPv4, zone dropped) so one client maps to
  one key.

### Detail read + view

`GET /blogs/{id}` → `ViewService.Read`:

```text
authorize blog:read
GetActive(id)                  missing/deleted → 404, nothing written
hash(client IP) + UTC view date
RecordUniqueView(key)          ONE SQL statement (see technical-decisions.md)
   storage error → 503/500     never "200 with a lost view"
return blog (views +1 if this call counted)
```

### CSV export

`GET /reports/blogs` → `ReportService.Export` streams rows from a pgx cursor through
`csv.Writer` into the response, flushing every 500 rows. Memory use is constant. The
permission check happens before the first byte, so a forbidden caller still gets JSON 403.
Cells starting with `= + - @` (or tab/CR) are prefixed with `'` to defuse spreadsheet
formula injection.

## Graceful shutdown

`SIGTERM`/`SIGINT` → `http.Server.Shutdown` with `SHUTDOWN_TIMEOUT` (in-flight requests
finish) → the pgx pool is closed. Request contexts are propagated to every database
call, so a disconnected client cancels its query.
