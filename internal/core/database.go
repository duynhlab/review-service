package database

import (
	"context"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/duynhlab/review-service/config"
)

// Connect establishes a database connection pool using pgx/v5 from the parsed
// application config (config.DatabaseConfig is the single source of truth for
// connection settings; there is no separate env-loading path here).
//
// pgx is used instead of lib/pq for PgBouncer/PgCat compatibility.
//
// IMPORTANT: We use SimpleProtocol mode and disable statement caching to work correctly
// with transaction-mode connection poolers (PgCat/PgBouncer). Without this, you may see:
//
//	"prepared statement stmtcache_* does not exist"
func Connect(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	// Parse DSN into pool config
	poolCfg, err := pgxpool.ParseConfig(cfg.Database.BuildDSN())
	if err != nil {
		return nil, fmt.Errorf("failed to parse database config: %w", err)
	}

	// Configure for transaction-mode poolers (PgCat/PgBouncer):
	// - Use simple protocol to avoid server-side prepared statements
	// - Disable statement cache (prepared statements are connection-scoped)
	// - Disable description cache
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	poolCfg.ConnConfig.StatementCacheCapacity = 0
	poolCfg.ConnConfig.DescriptionCacheCapacity = 0

	// Pool sizing applied here (not in the DSN) so migrate can share the DSN.
	if maxConns := cfg.Database.MaxConnections; maxConns > 0 && maxConns <= math.MaxInt32 {
		poolCfg.MaxConns = int32(maxConns)
	}

	// Create connection pool with the configured settings
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return pool, nil
}
