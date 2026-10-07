package seed

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Apply executes the embedded seed files in order against dsn. It does NOT use
// golang-migrate: seeds are idempotent (ON CONFLICT) and must not share the
// schema_migrations version table with the schema migrations. Simple query
// protocol lets each multi-statement file run in one Exec.
//
// Like migrate, it logs in as the migrator and switches to role, the schema
// owner, on every connection: the seed's setval needs UPDATE on the sequence,
// which the runtime login does not have. An empty role fails before connecting,
// and a denied SET ROLE fails the run; there is no fallback to the login.
func Apply(ctx context.Context, dsn, role string) error {
	if role == "" {
		return errors.New("seed: DB_MIGRATION_ROLE is not set")
	}
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse seed DSN: %w", err)
	}
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	setRole := "SET ROLE " + pgx.Identifier{role}.Sanitize()
	poolCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, setRole); err != nil {
			return fmt.Errorf("seed: SET ROLE %s: %w", role, err)
		}
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return fmt.Errorf("connect for seed: %w", err)
	}
	defer pool.Close()

	entries, err := fs.ReadDir(FS, "sql")
	if err != nil {
		return fmt.Errorf("read seed dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		b, readErr := fs.ReadFile(FS, "sql/"+name)
		if readErr != nil {
			return fmt.Errorf("read seed %s: %w", name, readErr)
		}
		if _, execErr := pool.Exec(ctx, string(b)); execErr != nil {
			return fmt.Errorf("apply seed %s: %w", name, execErr)
		}
	}
	return nil
}
