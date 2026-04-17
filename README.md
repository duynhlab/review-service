# review-service

Product review microservice for ratings and comments.

## Features

- Create product reviews
- Get reviews by product
- Rating aggregation
- Duplicate prevention

## API Endpoints

> **Browser callers** hit `https://gateway.duynhne.me/review/v1/{public,private}/reviews…`; Kong rewrites to the cluster paths below. See [homelab naming convention](https://github.com/duynhlab/homelab/blob/main/docs/api/api-naming-convention.md).

| Method | Cluster path | Edge path (via gateway) |
|--------|--------------|-------------------------|
| `GET` | `/api/v1/reviews?product_id={id}` | `/review/v1/public/reviews?product_id={id}` |
| `POST` | `/api/v1/reviews` | `/review/v1/private/reviews` |

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
