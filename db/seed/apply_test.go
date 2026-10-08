package seed

import (
	"context"
	"strings"
	"testing"
)

// The guards that run before any connection. The database path itself is
// exercised by the repository integration suite, as the real migrator.
func TestApply_RefusesBeforeConnecting(t *testing.T) {
	ctx := context.Background()
	if err := Apply(ctx, "postgres://u:p@127.0.0.1:1/db", ""); err == nil || !strings.Contains(err.Error(), "DB_MIGRATION_ROLE") {
		t.Fatalf("Apply(empty role) = %v, want DB_MIGRATION_ROLE error", err)
	}
	if err := Apply(ctx, "postgres://u:p@h:notaport/db", "owner"); err == nil || !strings.Contains(err.Error(), "parse seed DSN") {
		t.Fatalf("Apply(bad dsn) = %v, want parse error", err)
	}
}
