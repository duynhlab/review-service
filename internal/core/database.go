package database

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DatabaseConfig holds database connection configuration
type DatabaseConfig struct {
	Host           string `json:"host"`            // DB_HOST - PostgreSQL host
	Port           string `json:"port"`            // DB_PORT - PostgreSQL port (default: 5432)
	Name           string `json:"name"`            // DB_NAME - Database name
	User           string `json:"user"`            // DB_USER - Database user
	Password       string `json:"-"`               // DB_PASSWORD - Database password
	SSLMode        string `json:"sslMode"`         // DB_SSLMODE - SSL mode
	MaxConnections int    `json:"maxConnections"`  // DB_POOL_MAX_CONNECTIONS - Max pool connections
}

var globalPool *pgxpool.Pool

// LoadConfig loads database configuration from environment variables.
func LoadConfig() (*DatabaseConfig, error) {
	cfg := &DatabaseConfig{
		Host:           getEnv("DB_HOST", ""),
		Port:           getEnv("DB_PORT", "5432"),
		Name:           getEnv("DB_NAME", ""),
		User:           getEnv("DB_USER", ""),
		Password:       getEnv("DB_PASSWORD", ""),
		SSLMode:        getEnv("DB_SSLMODE", "disable"),
		MaxConnections: getEnvInt("DB_POOL_MAX_CONNECTIONS", 25),
	}

	if cfg.Host == "" {
		return nil, errors.New("DB_HOST environment variable is required")
	}
	if cfg.Name == "" {
		return nil, errors.New("DB_NAME environment variable is required")
	}
	if cfg.User == "" {
		return nil, errors.New("DB_USER environment variable is required")
	}
	if cfg.Password == "" {
		return nil, errors.New("DB_PASSWORD environment variable is required")
	}

	return cfg, nil
}

// BuildDSN constructs PostgreSQL connection string (DSN) from config.
func (c *DatabaseConfig) BuildDSN() string {
	hostPort := net.JoinHostPort(c.Host, c.Port)
	return fmt.Sprintf("postgresql://%s:%s@%s/%s?sslmode=%s&pool_max_conns=%d",
		c.User, c.Password, hostPort, c.Name, c.SSLMode, c.MaxConnections,
	)
}

// Connect establishes database connection pool using pgx/v5.
// pgx is used instead of lib/pq for PgBouncer/PgCat compatibility.
//
// IMPORTANT: We use SimpleProtocol mode and disable statement caching to work correctly
// with transaction-mode connection poolers (PgCat/PgBouncer). Without this, you may see:
//   "prepared statement stmtcache_* does not exist"
func Connect(ctx context.Context) (*pgxpool.Pool, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load database config: %w", err)
	}

	// Parse DSN into pool config
	poolCfg, err := pgxpool.ParseConfig(cfg.BuildDSN())
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

	// Create connection pool with the configured settings
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	globalPool = pool
	return pool, nil
}

// GetPool returns the global connection pool.
func GetPool() *pgxpool.Pool {
	return globalPool
}

// GetDB is an alias for GetPool() - provided for backward compatibility.
//
// Deprecated: Use GetPool() for new code.
func GetDB() *pgxpool.Pool {
	return globalPool
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if val := os.Getenv(key); val != "" {
		if intVal, err := strconv.Atoi(val); err == nil {
			return intVal
		}
	}
	return defaultValue
}
