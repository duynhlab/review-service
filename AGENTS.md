# review-service

> AI Agent context for understanding this repository

## 📋 Overview

Product review microservice. Manages product reviews and ratings.

Module path: `github.com/duynhlab/review-service`.

### Transport roles

- **HTTP server** (`:8080`) — north-south, browser + in-cluster callers (Variant A naming).
- **gRPC server** (`:9090`) — east-west. Exposes `review.v1.ReviewService/GetProductReviews`,
  called by `product-service` for product-details aggregation. Always runs alongside HTTP.
- **gRPC client** — dials the auth service (`auth.v1.AuthService/GetMe`, `AUTH_GRPC_ADDR`)
  to validate JWTs on `private` routes via `pkg/authmw`.

gRPC is the official east-west transport. Server + client are built via `pkg/grpcx`
(OpenTelemetry stats handler, gRPC health service, server reflection).

## 🏗️ Architecture Guidelines

### 3-Layer Architecture

| Layer | Location | Responsibility |
|-------|----------|----------------|
| **Web** | `internal/web/v1/handler.go` | HTTP, validation, error translation |
| **gRPC** | `internal/grpc/v1/server.go` | gRPC transport — thin adapter over Logic (peer of Web) |
| **Logic** | `internal/logic/v1/service.go` | Business rules (❌ NO SQL) |
| **Core** | `internal/core/` | Domain models, repositories, DB connection |

Both Web (`internal/web/v1`) and gRPC (`internal/grpc/v1`) are transport adapters
that call the same Logic service; neither contains business logic. The gRPC server
depends on Logic through a narrow `ReviewLister` interface.

### 3-Layer Coding Rules

**CRITICAL**: Strict layer boundaries. Violations will be rejected in code review.

#### Layer Boundaries

| Layer | Location | ALLOWED | FORBIDDEN |
|-------|----------|---------|-----------|
| **Web** | `internal/web/v1/` | HTTP handling, JSON binding, DTO mapping, call Logic, aggregation | SQL queries, direct DB access, business rules |
| **Logic** | `internal/logic/v1/` | Business rules, call repository interfaces, domain errors | SQL queries, `database.GetPool()`, HTTP handling, `*gin.Context` |
| **Core** | `internal/core/` | Domain models, repository implementations, SQL queries, DB connection | HTTP handling, business orchestration |

#### Dependency Direction

```
Web -> Logic -> Core (one-way only, never reverse)
```

- Web imports Logic and Core/domain
- Logic imports Core/domain and Core/repository interfaces
- Core imports nothing from Web or Logic

#### DO

- Put HTTP handlers, request validation, error-to-status mapping in `web/`
- Put business rules, orchestration, transaction logic in `logic/`
- Put SQL queries in `core/repository/` implementations
- Use repository interfaces (defined in `core/domain/`) for data access in Logic layer
- Use dependency injection (constructor parameters) for all service dependencies

#### DO NOT

- Write SQL or call `database.GetPool()` in Logic layer
- Import `gin` or handle HTTP in Logic layer
- Put business rules in Web layer (Web only translates and delegates)
- Call Logic functions directly from another service (use HTTP aggregation in Web layer)
- Skip the Logic layer (Web must not call Core/repository directly)

### Directory Structure

```
review-service/
├── cmd/main.go
├── config/config.go
├── db/migrations/sql/
├── internal/
│   ├── core/
│   │   ├── database.go
│   │   ├── domain/
│   │   └── repository/
│   ├── grpc/v1/server.go
│   ├── logic/v1/service.go
│   └── web/v1/handler.go
├── middleware/
└── Dockerfile
```

## 🛠️ Development Workflow

### Code Quality

**MANDATORY**: All code changes MUST pass lint before committing.

- Linter: `golangci-lint` v2+ with `.golangci.yml` config (60+ linters enabled)
- Zero tolerance: PRs with lint errors will NOT be merged
- CI enforces: `go-check` job runs lint on every PR

#### Commands (run in order)

```bash
go mod tidy              # Clean dependencies
go build ./...           # Verify compilation
go test ./...            # Run tests
golangci-lint run --timeout=10m  # Lint (MUST pass)
```

#### Pre-commit One-liner

```bash
go build ./... && go test ./... && golangci-lint run --timeout=10m
```

### Common Lint Fixes

- `perfsprint`: Use `errors.New()` instead of `fmt.Errorf()` when no format verbs
- `nosprintfhostport`: Use `net.JoinHostPort()` instead of `fmt.Sprintf("%s:%s", host, port)`
- `errcheck`: Always check error returns (or explicitly `_ = fn()`)
- `goconst`: Extract repeated string literals to constants
- `gocognit`: Extract helper functions to reduce complexity
- `noctx`: Use `http.NewRequestWithContext()` instead of `http.NewRequest()`

## 🔧 Tech Stack

| Component | Technology |
|-----------|------------|
| Language | Go 1.26 |
| HTTP framework | Gin |
| Database | PostgreSQL via pgx/v5 |
| East-west RPC | gRPC (`pkg/grpcx`) — server + auth client |
| AuthN | JWT validation via `pkg/authmw` (gRPC to auth) |
| Tracing | OpenTelemetry (`pkg/obsx`) |
| Metrics | OpenTelemetry → Prometheus default registry (`pkg/obsx`) |
| Profiling | Pyroscope |

## 📈 Observability

- **Middleware chain (HTTP)**: tracing → logging → metrics.
- **Metrics**: `obsx.SetupMetrics()` bridges OTel metrics into the default Prometheus
  registry. gRPC RED metrics (`rpc_server_*` and `rpc_client_*`) and HTTP RED metrics
  (`request_duration_seconds`, …) share the **single** `/metrics` endpoint — no
  separate port. The platform ServiceMonitor scrapes `/metrics`.
- **Logging**: Zap; the logging middleware uses `obsx.TraceIDFromContext` to stamp the
  active span's `trace_id` on every log line.

## 🏗️ Infrastructure Details

### Database

| Component | Value |
|-----------|-------|
| **Cluster** | `supporting-shared-db` (Zalando Postgres Operator) |
| **Pooler** | PgBouncer — `supporting-shared-db-pooler.user.svc.cluster.local` (`DB_HOST`) |
| **Migration host** | `supporting-shared-db.user.svc.cluster.local` (direct, no pooler) |
| **Driver** | pgx/v5 |

**pgx is configured for transaction-mode pooling** (`QueryExecModeSimpleProtocol`,
statement + description caches disabled) so server-side prepared statements don't
break behind PgBouncer.

Schema (`db/migrations/sql/`): `reviews` table keyed by SERIAL `id`, with a
`CHECK (rating BETWEEN 1 AND 5)` and a unique `(product_id, user_id)` constraint
(`user_id` references `auth.users.id` cross-cluster, no FK).

### Graceful Shutdown

1. `/ready` → 503 when shutting down
2. Drain delay (`READINESS_DRAIN_DELAY`, default 5s, max 30s)
3. Sequential: HTTP → gRPC (`GracefulStop`) → Database → Tracer → Profiling

## 🔌 API Reference

### HTTP (`:8080`)

| Method | Path | Audience | Description |
|--------|------|----------|-------------|
| `GET` | `/review/v1/public/reviews?product_id={id}` | public | List reviews for a product (`product_id` query param required). |
| `POST` | `/review/v1/private/reviews` | private | Create review — JWT enforced via `pkg/authmw`; `user_id` is taken from the authenticated context (not the body). 400 invalid rating, 409 duplicate. |

### gRPC (`:9090`)

| RPC | Caller | Description |
|-----|--------|-------------|
| `review.v1.ReviewService/GetProductReviews` | `product-service` | Returns all reviews for a product; mirrors `GET /review/v1/public/reviews`. |

The auth gRPC client (`auth.v1.AuthService/GetMe`, `AUTH_GRPC_ADDR`) backs JWT
validation on `private` HTTP routes.

Full convention + inventory: [`homelab/docs/api/api-naming-convention.md`](https://github.com/duynhlab/homelab/blob/main/docs/api/api-naming-convention.md).
