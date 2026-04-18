# review-service

Product review microservice for ratings and comments.

## Features

- Create product reviews
- Get reviews by product
- Rating aggregation
- Duplicate prevention

## API Endpoints

All routes follow Variant A naming — single path for browser and in-cluster callers. See [homelab naming convention](https://github.com/duynhlab/homelab/blob/main/docs/api/api-naming-convention.md).

| Method | Path | Audience |
|--------|------|----------|
| `GET` | `/review/v1/public/reviews?product_id={id}` | public (also called by product-service for aggregation) |
| `POST` | `/review/v1/private/reviews` | private |

## Tech Stack

- Go + Gin framework
- PostgreSQL 16 (review-db cluster, single instance)
- Direct connection (no pooler)
- OpenTelemetry tracing

## Development

### Prerequisites

- Go 1.25+
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
