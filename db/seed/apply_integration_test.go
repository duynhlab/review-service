//go:build integration

package seed

import (
	"context"
	"testing"

	"github.com/duynhlab/pkg/migratex"
	"github.com/duynhlab/review-service/db/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Apply after migrate: the seed lands, its setval moves the sequence past the
// seeded ids, and a second run is a no-op (ON CONFLICT).
func TestApply_Integration(t *testing.T) {
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("review"),
		postgres.WithUsername("review"),
		postgres.WithPassword("secret"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}

	if err := migratex.Run(migrations.FS, "sql", dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for i := range 2 {
		if err := Apply(ctx, dsn); err != nil {
			t.Fatalf("Apply run %d: %v", i+1, err)
		}
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var rows, next int64
	if err := conn.QueryRow(ctx, `SELECT count(*), (SELECT last_value FROM reviews_id_seq) FROM reviews`).Scan(&rows, &next); err != nil {
		t.Fatalf("read seed: %v", err)
	}
	if rows == 0 || next < rows {
		t.Fatalf("seed rows=%d, sequence last_value=%d; want rows and a sequence moved past them", rows, next)
	}
}
