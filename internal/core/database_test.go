package database

import (
	"context"
	"testing"
	"time"

	"github.com/duynhlab/review-service/config"
)

// TestConnect_ParseError covers the DSN-parse failure path: an invalid sslmode
// makes pgxpool.ParseConfig reject the DSN before any network I/O.
func TestConnect_ParseError(t *testing.T) {
	cfg := &config.Config{}
	cfg.Database = config.DatabaseConfig{
		Host: "localhost", Port: "5432", Name: "review",
		User: "review", Password: "secret", SSLMode: "bogus",
		MaxConnections: 25,
	}
	if _, err := Connect(context.Background(), cfg); err == nil {
		t.Fatal("Connect() with invalid sslmode = nil error, want parse error")
	}
}

// TestConnect_PingError covers the happy-parse / failed-connect path: the DSN is
// valid (so ParseConfig and the pool build succeed and the MaxConnections branch
// runs), but the host is unreachable so Ping fails and Connect returns the error.
func TestConnect_PingError(t *testing.T) {
	cfg := &config.Config{}
	cfg.Database = config.DatabaseConfig{
		Host: "127.0.0.1", Port: "1", Name: "review",
		User: "review", Password: "secret", SSLMode: "disable",
		MaxConnections: 25,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := Connect(ctx, cfg); err == nil {
		t.Fatal("Connect() to unreachable host = nil error, want ping error")
	}
}
