package db

import (
	"path/filepath"
	"testing"
	"time"
)

// openCacheDB opens a DB with a small, explicit thumbnail-cache policy so the
// tests do not have to insert hundreds of megabytes.
func openCacheDB(t *testing.T, maxBytes int64, ttl time.Duration) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	d.cacheMaxBytes = maxBytes
	d.cacheTTL = ttl
	return d
}

// cacheBytes reads the stored cache size. The cache has no size accessor, and
// the byte budget is the policy under test, so this checks the stated
// invariant directly.
func cacheBytes(t *testing.T, d *DB) int64 {
	t.Helper()
	var n int64
	if err := d.conn.QueryRow(
		`SELECT COALESCE(SUM(LENGTH(data)), 0) FROM thumbnail_cache`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestThumbnailCacheKeepsEverythingUnderBudget(t *testing.T) {
	d := openCacheDB(t, 1<<20, time.Hour)
	for _, key := range []string{"a", "b", "c"} {
		if err := d.CacheThumbnail(key, make([]byte, 100)); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"a", "b", "c"} {
		got, err := d.GetThumbnail(key)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Errorf("%s was evicted, want kept", key)
		}
	}
}

// TestThumbnailCacheEvictsOldestOverBudget is the guard against unbounded
// growth: the cache must not hold more than its byte budget, and the oldest
// rows go first.
func TestThumbnailCacheEvictsOldestOverBudget(t *testing.T) {
	d := openCacheDB(t, 250, time.Hour)
	blob := make([]byte, 100)
	for _, key := range []string{"k1", "k2", "k3", "k4", "k5"} {
		if err := d.CacheThumbnail(key, blob); err != nil {
			t.Fatal(err)
		}
	}

	// A 250-byte budget holds the newest two 100-byte rows.
	for _, key := range []string{"k4", "k5"} {
		got, err := d.GetThumbnail(key)
		if err != nil {
			t.Fatal(err)
		}
		if got == nil {
			t.Errorf("%s was evicted, want kept (newest)", key)
		}
	}
	for _, key := range []string{"k1", "k2", "k3"} {
		got, err := d.GetThumbnail(key)
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Errorf("%s survived, want evicted (oldest)", key)
		}
	}
	if size := cacheBytes(t, d); size > 250 {
		t.Errorf("cache holds %d bytes, want <= 250", size)
	}
}

// TestThumbnailCacheEvictsExpired guards the TTL half of the policy.
func TestThumbnailCacheEvictsExpired(t *testing.T) {
	d := openCacheDB(t, 1<<20, 20*time.Millisecond)
	if err := d.CacheThumbnail("old", make([]byte, 50)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := d.CacheThumbnail("new", make([]byte, 50)); err != nil {
		t.Fatal(err)
	}

	if got, err := d.GetThumbnail("old"); err != nil {
		t.Fatal(err)
	} else if got != nil {
		t.Error("expired row survived, want evicted")
	}
	if got, err := d.GetThumbnail("new"); err != nil {
		t.Fatal(err)
	} else if got == nil {
		t.Error("fresh row was evicted, want kept")
	}
}

func TestClearCacheRemovesEverything(t *testing.T) {
	d := openCacheDB(t, 1<<20, time.Hour)
	if err := d.CacheThumbnail("a", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := d.ClearCache(); err != nil {
		t.Fatal(err)
	}
	if got, err := d.GetThumbnail("a"); err != nil {
		t.Fatal(err)
	} else if got != nil {
		t.Error("row survived ClearCache")
	}
}
