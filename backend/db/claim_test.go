package db

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// cutoffPast returns a backoff cutoff far enough in the past that every row is
// immediately claimable ("no retry backoff in effect").
func cutoffPast() time.Time { return time.Now().UTC().Add(-time.Hour) }

func TestDequeueClaimsAtomically(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, p := range []string{"/a.jpg", "/b.jpg", "/c.jpg"} {
		if err := d.EnqueueFile(p); err != nil {
			t.Fatal(err)
		}
	}

	// Two concurrent dequeues must never return the same row. This guards the
	// double-upload bug: processNext() is re-entered from both the ticker and
	// every completion notification.
	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			items, err := d.DequeueNextPending(3, cutoffPast())
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, it := range items {
				seen[it.ID]++
				if it.Status != "uploading" {
					t.Errorf("dequeued row %d has status %q, want uploading", it.ID, it.Status)
				}
			}
		}()
	}
	wg.Wait()
	for id, n := range seen {
		if n != 1 {
			t.Errorf("row %d dequeued %d times, want exactly 1", id, n)
		}
	}
	if len(seen) != 3 {
		t.Errorf("claimed %d rows, want 3", len(seen))
	}

	// Rows left in 'uploading' must not be re-dequeued (that was the double-upload).
	again, err := d.DequeueNextPending(3, cutoffPast())
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Errorf("re-dequeued %d already-claimed rows, want 0", len(again))
	}
}

// TestMarkFailedAfterClaim is the regression guard for a stuck-in-'uploading'
// bug: the claim pinned the pool's single connection, so the worker's
// MarkFailed write was lost and the row never surfaced as failed in the UI.
func TestMarkFailedAfterClaim(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.EnqueueFile("/boom.jpg"); err != nil {
		t.Fatal(err)
	}

	items, err := d.DequeueNextPending(1, cutoffPast())
	if err != nil || len(items) != 1 {
		t.Fatalf("dequeue: %+v %v", items, err)
	}
	if items[0].Status != "uploading" {
		t.Fatalf("claimed status = %q, want uploading", items[0].Status)
	}
	if err := d.MarkFailed(items[0].ID, "injected failure"); err != nil {
		t.Fatalf("MarkFailed after claim: %v", err)
	}
	q, err := d.GetQueue()
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 1 || q[0].Status != "failed" {
		t.Fatalf("want 1 failed row (not stuck in uploading), got %+v", q)
	}
	if q[0].Error != "injected failure" {
		t.Errorf("error = %q, want injected failure", q[0].Error)
	}

	// A failed row becomes claimable again once its backoff has elapsed. The
	// row's last_attempt is ~now, so the elapsed check needs a cutoff that is at
	// or after that instant (the worker passes now-retryDelay).
	again, err := d.DequeueNextPending(1, time.Now().UTC())
	if err != nil || len(again) != 1 {
		t.Fatalf("re-claim failed row: %+v %v", again, err)
	}
	if again[0].RetryCount != 1 {
		t.Errorf("retry count = %d, want 1", again[0].RetryCount)
	}
}

// TestBackoffDefersClaim is the regression guard for the claim/release loop: a
// failed row whose backoff has NOT elapsed must not be claimed at all. Judging
// this after the claim failed, because the claim rewrites last_attempt -- the
// post-claim check compared against the timestamp it had just written and
// concluded the row was still backing off forever.
func TestBackoffDefersClaim(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.EnqueueFile("/slow.jpg"); err != nil {
		t.Fatal(err)
	}
	items, err := d.DequeueNextPending(1, cutoffPast())
	if err != nil || len(items) != 1 {
		t.Fatalf("dequeue: %+v %v", items, err)
	}
	if err := d.MarkFailed(items[0].ID, "boom"); err != nil {
		t.Fatal(err)
	}

	// last_attempt is "now", so a cutoff of now-1m must exclude the row.
	recent := time.Now().UTC().Add(-time.Minute)
	got, err := d.DequeueNextPending(1, recent)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("claimed %d backing-off rows, want 0 (claim/release loop)", len(got))
	}
	// It must still be 'failed', not stuck as 'uploading'.
	q, err := d.GetQueue()
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 1 || q[0].Status != "failed" {
		t.Errorf("row should stay failed while backing off, got %+v", q)
	}
}
