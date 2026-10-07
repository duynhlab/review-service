//go:build integration

// Integration tests for the PostgreSQL review repository. They run a real
// Postgres via testcontainers-go and apply the service's migrations (incl. the
// seed + unique constraint), so they exercise the actual SQL, not a mock. Run:
//
//	go test -tags=integration ./internal/core/repository/...
//
// Requires a reachable Docker daemon. Excluded from the default `go test ./...`
// unit run by the `integration` build tag.
package repository

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/duynhlab/pkg/migratex"
	"github.com/duynhlab/review-service/db/migrations"
	"github.com/duynhlab/review-service/db/seed"
	"github.com/duynhlab/review-service/internal/core/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// testDB holds the three logins of the RFC-0029 shape: a superuser that plays
// the platform (creates the roles, as CNPG does), the migrator, and the
// runtime pool the repository runs on.
type testDB struct {
	adminDSN    string
	migratorDSN string
	runtimeDSN  string
	runtime     *pgxpool.Pool
}

// startWithRoles starts a throwaway Postgres and creates review_owner /
// review_migrator / review_runtime the way the platform does. Everything is
// torn down via t.Cleanup.
func startWithRoles(t *testing.T) *testDB {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("review"),
		postgres.WithUsername("platform"),
		postgres.WithPassword("secret"),
		// Ready twice (initdb restarts the server once), then the published
		// port: the module's own strategy, so a test never races the restart.
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	adminDSN, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	db := &testDB{
		adminDSN:    adminDSN,
		migratorDSN: withUser(t, adminDSN, "review_migrator", "migrator"),
		runtimeDSN:  withUser(t, adminDSN, "review_runtime", "runtime"),
	}

	execAll(t, ctx, adminDSN,
		`CREATE ROLE review_owner NOLOGIN`,
		`CREATE ROLE review_migrator LOGIN NOINHERIT PASSWORD 'migrator'`,
		`CREATE ROLE review_runtime LOGIN PASSWORD 'runtime'`,
		`GRANT review_owner TO review_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE`,
		// The platform makes the owner own the database; on PG15+ that is what
		// gives it CREATE on the public schema (owned by pg_database_owner).
		`ALTER DATABASE review OWNER TO review_owner`,
	)
	return db
}

// newBareDB is newTestDB without migrations or seed; it returns the
// migrator's DSN.
func newBareDB(t *testing.T) string {
	t.Helper()
	return startWithRoles(t).migratorDSN
}

// newTestDB starts a throwaway Postgres with the three roles, migrates and
// seeds as the migrator after SET ROLE review_owner, and returns it with a
// pool connected as review_runtime.
func newTestDB(t *testing.T) *testDB {
	t.Helper()
	ctx := context.Background()
	db := startWithRoles(t)

	if err := migratex.Run(migrations.FS, "sql", db.migratorDSN, migratex.WithSetRole(ownerRole)); err != nil {
		t.Fatalf("migrate as the migrator: %v", err)
	}
	if err := seed.Apply(ctx, db.migratorDSN, ownerRole); err != nil {
		t.Fatalf("seed as the migrator: %v", err)
	}

	pool, err := pgxpool.New(ctx, db.runtimeDSN)
	if err != nil {
		t.Fatalf("new runtime pool: %v", err)
	}
	t.Cleanup(pool.Close)
	db.runtime = pool
	return db
}

const ownerRole = "review_owner"

func withUser(t *testing.T, dsn, user, password string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}

func execAll(t *testing.T, ctx context.Context, dsn string, stmts ...string) {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, stmt := range stmts {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// Fixed Keycloak realm subjects used by the dev seed (ADR-042).
const (
	seedAlice = "a11ce000-0000-4000-8000-000000000001"
)

func TestReviewRepository_Integration(t *testing.T) {
	db := newTestDB(t)
	repo := NewReviewRepository(db.runtime)
	ctx := context.Background()

	// Seed loads product_id=1 with 3 reviews (alice, charlie, david subjects).
	t.Run("ListReviewsByProduct returns seeded reviews newest-first", func(t *testing.T) {
		reviews, err := repo.ListReviewsByProduct(ctx, 1, 10, 0)
		if err != nil {
			t.Fatalf("ListReviewsByProduct: %v", err)
		}
		if len(reviews) != 3 {
			t.Fatalf("len = %d, want 3 (seed product 1)", len(reviews))
		}
		for i := 1; i < len(reviews); i++ {
			if reviews[i-1].CreatedAt == nil || reviews[i].CreatedAt == nil {
				t.Fatal("created_at missing")
			}
			if reviews[i-1].CreatedAt.Before(*reviews[i].CreatedAt) {
				t.Error("results not ordered created_at DESC")
			}
		}
	})

	t.Run("ListReviewsByProduct paginates", func(t *testing.T) {
		page, err := repo.ListReviewsByProduct(ctx, 1, 2, 0)
		if err != nil {
			t.Fatalf("ListReviewsByProduct: %v", err)
		}
		if len(page) != 2 {
			t.Errorf("limit 2 returned %d", len(page))
		}
	})

	t.Run("CountReviewsByProduct", func(t *testing.T) {
		n, err := repo.CountReviewsByProduct(ctx, 1)
		if err != nil {
			t.Fatalf("CountReviewsByProduct: %v", err)
		}
		if n != 3 {
			t.Errorf("count = %d, want 3", n)
		}
	})

	t.Run("GetReviewByProductAndUser found / not found", func(t *testing.T) {
		got, err := repo.GetReviewByProductAndUser(ctx, 1, seedAlice) // seeded (1, alice)
		if err != nil {
			t.Fatalf("GetReviewByProductAndUser: %v", err)
		}
		if got == nil {
			t.Fatal("want existing review for (product 1, alice), got nil")
		}
		missing, err := repo.GetReviewByProductAndUser(ctx, 999, "no-such-subject")
		if err != nil {
			t.Fatalf("GetReviewByProductAndUser(missing): %v", err)
		}
		if missing != nil {
			t.Errorf("want nil for missing pair, got %+v", missing)
		}
	})

	t.Run("CreateReview inserts and returns id + created_at", func(t *testing.T) {
		got, err := repo.CreateReview(ctx, domain.Review{
			ProductID: "42", UserID: seedAlice, Rating: 5, Title: "New", Comment: "Fresh review",
		})
		if err != nil {
			t.Fatalf("CreateReview: %v", err)
		}
		if got.ID == "" {
			t.Error("returned review has empty ID")
		}
		if got.CreatedAt == nil {
			t.Error("returned review has nil CreatedAt")
		}
	})

	t.Run("CreateReview stores a non-numeric OIDC subject verbatim", func(t *testing.T) {
		// Regression for the swallowed conversion (`userID, _ := strconv.Atoi(...)`):
		// a non-numeric subject must round-trip to the DB intact. Under the old
		// code it was silently written as user_id=0.
		const subject = "f47ac10b-58cc-4372-a567-0e02b2c3d479"
		created, err := repo.CreateReview(ctx, domain.Review{
			ProductID: "77", UserID: subject, Rating: 4, Title: "Opaque", Comment: "subject round-trip",
		})
		if err != nil {
			t.Fatalf("CreateReview: %v", err)
		}
		if created.UserID != subject {
			t.Errorf("created.UserID = %q, want %q", created.UserID, subject)
		}

		stored, err := repo.ListReviewsByProduct(ctx, 77, 10, 0)
		if err != nil {
			t.Fatalf("ListReviewsByProduct: %v", err)
		}
		if len(stored) != 1 {
			t.Fatalf("len = %d, want 1", len(stored))
		}
		if stored[0].UserID != subject {
			t.Errorf("stored user_id = %q, want the exact subject %q (old code stored 0)", stored[0].UserID, subject)
		}

		// And the duplicate pre-check must find it by the exact string.
		found, err := repo.GetReviewByProductAndUser(ctx, 77, subject)
		if err != nil {
			t.Fatalf("GetReviewByProductAndUser: %v", err)
		}
		if found == nil {
			t.Error("want the stored review found by its exact subject, got nil")
		}
	})

	t.Run("CreateReview maps unique violation to ErrDuplicateReview", func(t *testing.T) {
		// (product 1, alice) already exists in the seed; the V3 unique
		// constraint must trip and be translated.
		_, err := repo.CreateReview(ctx, domain.Review{
			ProductID: "1", UserID: seedAlice, Rating: 3, Title: "Dup", Comment: "again",
		})
		if !errors.Is(err, domain.ErrDuplicateReview) {
			t.Errorf("err = %v, want ErrDuplicateReview", err)
		}
	})
}

// The RFC-0029 authorization contract, checked as the real logins: the owner
// owns every object, the runtime can serve traffic and nothing more, and the
// migration refuses to run without its role.
func TestAuthorization_Integration(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	t.Run("every relation belongs to review_owner", func(t *testing.T) {
		rows, err := db.runtime.Query(ctx, `
			SELECT c.relname || ':' || pg_get_userbyid(c.relowner)
			  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
			 WHERE n.nspname = 'public' AND pg_get_userbyid(c.relowner) <> 'review_owner'`)
		if err != nil {
			t.Fatalf("query owners: %v", err)
		}
		others, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("scan owners: %v", err)
		}
		if len(others) != 0 {
			t.Fatalf("relations not owned by review_owner: %v", others)
		}
	})

	t.Run("the runtime cannot change the schema or reach the migration table", func(t *testing.T) {
		for _, stmt := range []string{
			`CREATE TABLE public.evil (i int)`,
			`ALTER TABLE public.reviews ADD COLUMN evil int`,
			`DROP TABLE public.reviews`,
			`SELECT version FROM public.schema_migrations`,
			`SET ROLE review_owner`,
		} {
			_, err := db.runtime.Exec(ctx, stmt)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || (pgErr.Code != "42501" && pgErr.Code != "42704") {
				t.Errorf("%s as review_runtime: got %v, want permission denied", stmt, err)
			}
		}
		// Without GRANT OPTION, PostgreSQL only warns "no privileges were
		// granted"; the assertion is the effect, not an error code.
		if _, err := db.runtime.Exec(ctx, `GRANT SELECT ON public.reviews TO PUBLIC`); err != nil {
			t.Fatalf("grant attempt: %v", err)
		}
		var leaked bool
		if err := db.runtime.QueryRow(ctx,
			`SELECT has_table_privilege('public', 'public.reviews', 'SELECT')`).Scan(&leaked); err != nil {
			t.Fatalf("check PUBLIC access: %v", err)
		}
		if leaked {
			t.Fatal("review_runtime handed SELECT on reviews to PUBLIC")
		}
	})

	t.Run("migrate and seed refuse an empty DB_MIGRATION_ROLE", func(t *testing.T) {
		if err := migratex.Run(migrations.FS, "sql", db.migratorDSN, migratex.WithSetRole("")); err == nil {
			t.Fatal("migrate with an empty role succeeded")
		}
		if err := seed.Apply(ctx, db.migratorDSN, ""); err == nil {
			t.Fatal("seed with an empty role succeeded")
		}
	})

	t.Run("the migrator creates nothing as itself", func(t *testing.T) {
		// No SET ROLE at all: the NOINHERIT migrator has no right on the
		// owner's schema, so even golang-migrate's version table is refused.
		fresh := newBareDB(t)
		err := migratex.Run(migrations.FS, "sql", fresh)
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Fatalf("migrate without SET ROLE = %v, want permission denied", err)
		}
	})

	t.Run("seed as a role the login cannot switch to fails", func(t *testing.T) {
		err := seed.Apply(ctx, db.runtimeDSN, ownerRole)
		if err == nil || !strings.Contains(err.Error(), "SET ROLE") {
			t.Fatalf("seed as review_runtime = %v, want SET ROLE error", err)
		}
	})
}
