package db

import (
	"path/filepath"
	"testing"
)

// TestBusyTimeoutComesFromDSN guards the fix for a busy timeout that was set
// once on one pooled connection and lost when that connection was replaced.
func TestBusyTimeoutComesFromDSN(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	var got int
	if err := d.conn.QueryRow(`PRAGMA busy_timeout`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", got)
	}
}