//go:build integration

package seed

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/duynhlab/pkg/migratex"
	"github.com/duynhlab/review-service/db/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Apply as the migrator: the seed lands, its setval works (it needs UPDATE on
// the sequence, which only the owner has), and a login that cannot switch to
// the owner is refused.
func TestApply_Integration(t *testing.T) {
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("review"),
		postgres.WithUsername("platform"),
		postgres.WithPassword("secret"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	admin, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("dsn: %v", err)
	}

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	for _, stmt := range []string{
		`CREATE ROLE review_owner NOLOGIN`,
		`CREATE ROLE review_migrator LOGIN NOINHERIT PASSWORD 'migrator'`,
		`CREATE ROLE review_runtime LOGIN PASSWORD 'runtime'`,
		`GRANT review_owner TO review_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE`,
		`ALTER DATABASE review OWNER TO review_owner`,
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	defer func() { _ = conn.Close(ctx) }()

	migrator := withUser(t, admin, "review_migrator", "migrator")
	if err := migratex.Run(migrations.FS, "sql", migrator, migratex.WithSetRole("review_owner")); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if err := Apply(ctx, migrator, "review_owner"); err != nil {
		t.Fatalf("Apply as the migrator: %v", err)
	}
	var rows, next int64
	if err := conn.QueryRow(ctx, `SELECT count(*), (SELECT last_value FROM reviews_id_seq) FROM reviews`).Scan(&rows, &next); err != nil {
		t.Fatalf("read seed: %v", err)
	}
	if rows == 0 || next < rows {
		t.Fatalf("seed rows=%d, sequence last_value=%d; want rows and a sequence moved past them", rows, next)
	}

	err = Apply(ctx, withUser(t, admin, "review_runtime", "runtime"), "review_owner")
	if err == nil || !strings.Contains(err.Error(), "SET ROLE") {
		t.Fatalf("Apply as review_runtime = %v, want SET ROLE error", err)
	}
}

func withUser(t *testing.T, dsn, user, password string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.User = url.UserPassword(user, password)
	return u.String()
}
