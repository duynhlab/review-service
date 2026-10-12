package seed

import (
	"context"
	"strings"
	"testing"
)

// The guard that runs before any connection. The database path is exercised
// by the integration test.
func TestApply_RefusesBadDSN(t *testing.T) {
	if err := Apply(context.Background(), "postgres://u:p@h:notaport/db"); err == nil || !strings.Contains(err.Error(), "parse seed DSN") {
		t.Fatalf("Apply(bad dsn) = %v, want parse error", err)
	}
}
