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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/duynhlab/review-service/internal/core/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// newTestDB starts a throwaway Postgres, applies the migrations, and returns a
// pool for the repository under test. Everything is torn down via t.Cleanup.
func newTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("review"),
		postgres.WithUsername("review"),
		postgres.WithPassword("secret"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	applyMigrations(t, ctx, dsn)

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// applyMigrations runs every db/migrations/sql/*.up.sql in lexical order using a
// simple-protocol connection (so multi-statement files execute in one round).
func applyMigrations(t *testing.T, ctx context.Context, dsn string) {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect for migrations: %v", err)
	}
	defer conn.Close(ctx)

	applySQLDir(t, ctx, conn, filepath.Join("..", "..", "..", "db", "migrations", "sql"))

	// Apply the dev seed too — it lives outside the migration chain (db/seed/sql),
	// so read-path tests must load it explicitly here.
	applySQLDir(t, ctx, conn, filepath.Join("..", "..", "..", "db", "seed", "sql"))
}

// applySQLDir executes every *.up.sql file in dir in lexical order.
func applySQLDir(t *testing.T, ctx context.Context, conn *pgx.Conn, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".up.sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	for _, f := range files {
		sqlBytes, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		if _, err := conn.Exec(ctx, string(sqlBytes)); err != nil {
			t.Fatalf("apply %s: %v", f, err)
		}
	}
}

func TestReviewRepository_Integration(t *testing.T) {
	pool := newTestDB(t)
	repo := NewReviewRepository(pool)
	ctx := context.Background()

	// Seed (000002) loads product_id=1 with 3 reviews (users 1, 3, 4).
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
		got, err := repo.GetReviewByProductAndUser(ctx, 1, 1) // seeded (1,1)
		if err != nil {
			t.Fatalf("GetReviewByProductAndUser: %v", err)
		}
		if got == nil {
			t.Fatal("want existing review for (product 1, user 1), got nil")
		}
		missing, err := repo.GetReviewByProductAndUser(ctx, 999, 999)
		if err != nil {
			t.Fatalf("GetReviewByProductAndUser(missing): %v", err)
		}
		if missing != nil {
			t.Errorf("want nil for missing pair, got %+v", missing)
		}
	})

	t.Run("CreateReview inserts and returns id + created_at", func(t *testing.T) {
		got, err := repo.CreateReview(ctx, domain.Review{
			ProductID: "42", UserID: "8", Rating: 5, Title: "New", Comment: "Fresh review",
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

	t.Run("CreateReview maps unique violation to ErrDuplicateReview", func(t *testing.T) {
		// (product 1, user 1) already exists in the seed; the V3 unique
		// constraint must trip and be translated.
		_, err := repo.CreateReview(ctx, domain.Review{
			ProductID: "1", UserID: "1", Rating: 3, Title: "Dup", Comment: "again",
		})
		if !errors.Is(err, domain.ErrDuplicateReview) {
			t.Errorf("err = %v, want ErrDuplicateReview", err)
		}
	})
}
