# review-service

Product reviews and star ratings — the only writer of the `reviews` table, and
the read path that serves them to the storefront and to product-service.

## Responsibilities

- **Owns:** reviews and their star ratings, one per product per user, and the
  two read surfaces that expose them.
- **Does not own:** products (`product-service`), identity or token issuance
  (`auth-service`, `user-service`), and rating **aggregation** — no average is
  computed here. Reviews are also write-once: there is no update or delete path.

## Tech

| Area | Technology |
|------|------------|
| Runtime | Go 1.27 |
| Transports | HTTP (public read, private write) · gRPC (east-west read) |
| Data | PostgreSQL — one table, `reviews` |
| Platform libraries | `authmw`, `dbx`, `grpcx`, `httpx`, `logger/zapx`, `migratex`, `obsx`, `proto` |

## API

- **Canonical contract:** [`homelab/docs/api/review.md`](https://github.com/duynhlab/homelab/blob/main/docs/api/review.md)
- **Shared conventions:** [`homelab/docs/api/api.md`](https://github.com/duynhlab/homelab/blob/main/docs/api/api.md)
- **Surfaces:** a public HTTP read and a JWT-protected HTTP write, plus
  `review.v1.ReviewService` for east-west reads — product-service calls it to
  put reviews on a product page. HTTP `:8080` also carries `/health` and
  `/ready`.

Routes, payloads and error codes live in the contract, so there is one place to
change when they change.

## Run locally

Prefer the homelab **local-stack** — reviews are most useful with a product
catalog and a signed token in front of them.

Standalone you need PostgreSQL reachable through the `DB_*` variables and three
roles: `review_owner` (owns the schema, cannot log in), `review_migrator` (logs
in, may only `SET ROLE review_owner`) and `review_runtime` (serves traffic, CRUD
only). `migrate` and `seed` log in as the migrator and need
`DB_MIGRATION_ROLE=review_owner`; they refuse to run without it, and they must
reach PostgreSQL directly, not through a transaction pooler. The app logs in as
`review_runtime`. `review_owner` must own the `review` database: on PostgreSQL
15+ that is what lets it create objects in the `public` schema.

```bash
DB_USER=review_migrator DB_MIGRATION_ROLE=review_owner go run cmd/main.go migrate
DB_USER=review_migrator DB_MIGRATION_ROLE=review_owner go run cmd/main.go seed   # development only
DB_USER=review_runtime go run cmd/main.go   # serve HTTP :8080 + gRPC :9090
```

## Verify

The commands CI runs, so a green local run means a green pipeline:

```bash
go build ./...
go test -race ./...
go test -tags=integration ./internal/core/repository/...   # needs Docker (testcontainers)
golangci-lint run
```

## Docs

- [Canonical contract](https://github.com/duynhlab/homelab/blob/main/docs/api/review.md)
- [local-stack guide](https://github.com/duynhlab/homelab/blob/main/local-stack/README.md)

## License

MIT
