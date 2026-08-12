# AGENTS.md

Agent-focused guide for `review-service`. Keep changes minimal, verified against
the code, and consistent with existing patterns.

## Authority and scope

This repository implements the service. It does **not** define the contract.

- **Canonical contract:** [`homelab/docs/api/review.md`](https://github.com/duynhlab/homelab/blob/main/docs/api/review.md)
- **Shared API rules:** [`homelab/docs/api/api.md`](https://github.com/duynhlab/homelab/blob/main/docs/api/api.md)

Implement against those files. When this repository and the contract disagree,
**stop and classify the mismatch** using
[Resolving a mismatch](https://github.com/duynhlab/homelab/blob/main/docs/api/README.md#resolving-a-mismatch)
before changing either side. One class — an implementation that violates the
intended contract — **blocks the release tag**.

No route, RPC, payload or error inventory belongs in this file. Manifests,
gateway routing, NetworkPolicy and platform observability belong to
[duynhlab/homelab](https://github.com/duynhlab/homelab).

## Contribution workflow

- Never push to `main`. Branch, then open a PR.
- Branch names use conventional prefixes: `feat/`, `fix/`, `docs/`, `chore/`,
  `refactor/`, `test/`.
- One logical change per PR. PRs are merged with squash.
- Commit subject: imperative mood, capitalised, no trailing period, ≤ 50 chars
  (`Add gRPC reflection`, not `Added` / `Adds`).
- Commit body (only when non-trivial): explain *what* and *why*, wrap at 72 chars,
  one blank line after the subject.
- No attribution trailers (`Signed-off-by`, `Co-authored-by`, `Generated-by`, …).
- No issue references (`Fixes #123`) and no `@`-mentions in commit messages.
  Put issue links in the PR description.

## Code quality

- Go 1.26 (`go.mod` pins the toolchain; `GOTOOLCHAIN=auto` selects it).
- Idiomatic Go: small interfaces, constructor injection, wrapped errors
  (`fmt.Errorf("...: %w", err)`), sentinel errors in `internal/logic/v1/errors.go`.
- Always check error returns (or explicit `_ = fn()`).
- `errors.New` when there are no format verbs; `net.JoinHostPort` over
  `fmt.Sprintf("%s:%s", …)`; `http.NewRequestWithContext` over `http.NewRequest`.
- Extract repeated string literals to constants; split helpers to keep
  cognitive complexity down.
- Lint must pass (`golangci-lint`, v2 config in `.golangci.yml`). PRs with lint
  errors are not merged; CI runs it on every PR (`go-check`).
- Before pushing or opening a PR, verify Sonar new-code coverage ≥80%: run
  `go test -race -coverprofile=coverage.out ./...` and confirm changed lines are
  covered, including BOTH branches of any new conditional. `**/cmd/**`,
  `**/db/migrations/**`, `**/core/repository/**` are coverage-excluded;
  everything else counts.

## Build, test, lint

These are the commands CI runs, so a green local run means a green pipeline.

```bash
go build ./...
go vet ./...
go test -race ./...
go test -tags=integration ./internal/core/repository/...   # needs Docker (testcontainers)
golangci-lint run
```

Local development against an unreleased `pkg`: `pkg` is one module per package,
so its root has no `go.mod` and a single `replace github.com/duynhlab/pkg` can no
longer resolve. Use one commented `replace` line per module — the trailer in
`go.mod` shows the shape, and
[`docs/api/pkg.md`](https://github.com/duynhlab/homelab/blob/main/docs/api/pkg.md)
explains why.

## Architecture boundaries

**3-layer, dependencies flow one way only: transport → logic → core.**

- **Transport** — `internal/web/v1/` (HTTP) and `internal/grpc/v1/` (gRPC). Both
  validate, map and delegate; neither may touch the database or hold business
  rules.
- **Logic** — `internal/logic/v1/` holds the rules and calls repository
  interfaces. No SQL, no transport types.
- **Core** — `internal/core/` owns the domain model, the repository interface and
  the Postgres implementation. It imports nothing from transport or logic.

Both transports always run. Observability is wired once through
`github.com/duynhlab/pkg/obsx`; the pool comes from `github.com/duynhlab/pkg/dbx`;
the gRPC server is built by `github.com/duynhlab/pkg/grpcx`; HTTP responses use the
shared `github.com/duynhlab/pkg/httpx` envelope.

## Invariants

Rules an implementer can violate at the keyboard.

- **Identity comes from the JWT, never the request body.** The handler overwrites
  `req.UserID` with `c.GetString("user_id")`, and the request struct deliberately
  leaves that field non-required so it *can* be overwritten. Accepting a user id
  from the body would let anyone review as anyone.
- **`user_id` is the OIDC token subject — an opaque string, never an integer**
  (ADR-042). The insert used to run it through `strconv.Atoi` and discard the
  error, silently storing `0` for every non-numeric subject; the conversion is
  gone and a regression test pins the round-trip. Do not reintroduce a parse.
- **The database is the uniqueness authority, not the pre-check.** One review per
  (product, user) is enforced by a unique constraint; the pre-check is a nicety
  that a concurrent insert can slip past. The repository maps SQLSTATE `23505` to
  a duplicate error, which becomes a 409.
- **Both duplicate paths must record the same metric.** The rejection counter is
  called from the pre-check *and* from the constraint-violation path. A third
  rejection path added without it silently breaks the signal.
- **Rating is validated three times on purpose** — binding tag, logic guard, and a
  `CHECK` constraint. The metric histogram assumes a bounded 1–5 value, so
  loosening any one of the three puts values in undefined buckets.
- **`seed` is development-only** and refuses to run in production. It is invoked
  explicitly — never from `migrate` or the serve path — and it must not use
  golang-migrate: seeds are idempotent `ON CONFLICT` statements and must not share
  the `schema_migrations` version table.
- **Pooler-safe database settings live in `pkg/dbx`, not here.** Simple protocol
  and disabled statement caches are required by the pooler in front of Postgres.
  Do not re-add local pgx tuning.
- **One DSN for the app and for migrations.** `BuildDSN()` is the single source;
  pool sizing is applied to the parsed config, never to the DSN string, because
  the stdlib driver used by migrations rejects `pool_*` parameters.
- **Graceful-shutdown ordering is load-bearing:** flag not-ready → readiness drain
  delay → HTTP shutdown → gRPC `GracefulStop` → pool close → OTel shutdown last,
  so pending spans, metrics and logs still flush.
- **The gRPC read is capped at 10,000 reviews** because the proto has no
  pagination fields. Truncation must log and increment its counter. The counter
  knowingly over-counts the exact-cap case — a full page is indistinguishable from
  a truncated one — and that is preferred to under-reporting truncation.
- **Metrics carry no labels.** No ids, no free-form text. A label added here
  becomes cardinality in the platform's metric store.
- **Probe suppression is one contract across logs and traces.** Successful
  `/health` and `/ready` requests are excluded from spans *and* from access logs
  through the same skip list; a **failing** probe is still recorded. Changing one
  side without the other breaks the contract silently. 4xx logs at warn, 5xx at
  error.

## Repository map

- `cmd/main.go` — wiring, subcommand dispatch, HTTP + gRPC bootstrap, graceful shutdown
- `config/config.go` — env config, `Validate()`, `BuildDSN()`
- `db/migrations/` — forward-only golang-migrate SQL, embedded
- `db/seed/` — development-only demo seed, embedded
- `internal/web/v1/` — HTTP adapter
- `internal/grpc/v1/` — gRPC adapter, including the read cap and truncation counter
- `internal/logic/v1/` — business rules, sentinel errors, metrics
- `internal/core/` — database wiring, domain model, repository interface and Postgres implementation
- `middleware/` — tracing and logging only

## Gotchas

- Kyverno admission rejects bad images. The published image is
  `ghcr.io/duynhlab/review-service/review-service:<tag>` — the repository path
  repeats, and the tag carries no `v` prefix. There is no separate migration
  image; the init container reuses the app image with `args: ["migrate"]`. Never
  `:latest`.
- Metrics leave over OTLP. There is no `/metrics` endpoint and nothing scrapes
  this service.

## API change synchronization

An API change is not done when the code compiles.

- The contract in homelab and this repository move **together** — same change,
  and either the same PR pair or an immediate follow-up.
- Behaviour that is designed but not deployed is marked **`Planned`** in the
  contract; it is never described as current.
- A material mismatch between the contract and this implementation **blocks the
  release tag** until it is reconciled or explicitly accepted.
