# review-service

Product review microservice for ratings and comments.

Module path: `github.com/duynhlab/review-service`.

## Features

- Create product reviews (one per user per product)
- List reviews by product
- Duplicate prevention (unique `(product_id, user_id)`, race-safe at DB level)

## Transport

review-service speaks both HTTP (north-south) and gRPC (east-west). gRPC is the
official east-west transport.

### HTTP API (`:8080`)

All routes follow Variant A naming — single path for browser and in-cluster callers. See [homelab naming convention](https://github.com/duynhlab/homelab/blob/main/docs/api/api-naming-convention.md).

| Method | Path | Audience |
|--------|------|----------|
| `GET` | `/review/v1/public/reviews?product_id={id}` | public (`product_id` query param required) |
| `POST` | `/review/v1/private/reviews` | private (JWT enforced) |

Infra endpoints: `GET /health`, `GET /ready` (503 while draining), `GET /metrics`.

JWT on `private` routes is validated by the shared `pkg/authmw` middleware, which
calls the auth service over gRPC (`auth.v1.AuthService/GetMe`). The authenticated
`user_id` is taken from context, never from the request body.

### gRPC (`:9090`)

review-service is both a gRPC server and a gRPC client:

- **Server** — exposes `review.v1.ReviewService/GetProductReviews`, called by
  `product-service` for the product-details aggregation. Mirrors
  `GET /review/v1/public/reviews` and returns identical data.
- **Client** — dials the auth service (`AUTH_GRPC_ADDR`,
  default `dns:///auth.auth.svc.cluster.local:9090`) to validate JWTs.

The gRPC server is built via the shared `pkg/grpcx` bootstrap (OpenTelemetry stats
handler, gRPC health service, server reflection) and always runs alongside the
HTTP listener.

## Tech Stack

- Go 1.26 + Gin
- PostgreSQL (`supporting-shared-db`, Zalando Postgres Operator) via `pgx/v5`
- gRPC (`pkg/grpcx`), JWT validation via `pkg/authmw`
- OpenTelemetry traces + metrics via `pkg/obsx`

## Observability

- **Tracing**: OpenTelemetry → OTel Collector (OTLP/HTTP). Spans per layer (`web`, `logic`).
- **Metrics**: `obsx.SetupMetrics()` bridges OpenTelemetry metrics into the default
  Prometheus registry, so gRPC RED metrics (`rpc_server_*` from the gRPC server and
  `rpc_client_*` from the auth client) surface on the **same** `/metrics` endpoint as
  the HTTP RED metrics (`request_duration_seconds`, …). No separate metrics port; the
  platform ServiceMonitor scrapes `/metrics`.
- **Logging**: structured Zap. The logging middleware uses `obsx.TraceIDFromContext`
  so each log line carries the active span's `trace_id`.
- **Profiling**: Pyroscope continuous profiling (when enabled).
- Middleware chain (HTTP): tracing → logging → metrics.

## Development

### Prerequisites

- Go 1.26+
- [golangci-lint](https://golangci-lint.run/welcome/install/) v2+

### Local Development

```bash
# Install dependencies
go mod tidy
go mod download

# Build
go build ./...

# Test
go test ./...

# Lint (must pass before PR merge)
golangci-lint run --timeout=10m

# Run locally (requires .env or env vars)
go run cmd/main.go
```

### Pre-push Checklist

```bash
go build ./... && go test ./... && golangci-lint run --timeout=10m
```

## License

MIT
