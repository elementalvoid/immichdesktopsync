package db

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"immich-desktop-sync/backend/models"
)

// testDelays is a short backoff table for tests. The values are literals, not
// the production table in backend/queue.go, so each test states the wait it
// expects. The test does not recompute the wait from the code under test.
var testDelays = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

const testMaxRetries = 3

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
			items, err := d.DequeueNextPending(3, time.Now().UTC(), testDelays, testMaxRetries)
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
	again, err := d.DequeueNextPending(3, time.Now().UTC(), testDelays, testMaxRetries)
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

	base := time.Now().UTC()
	items, err := d.DequeueNextPending(1, base, testDelays, testMaxRetries)
	if err != nil || len(items) != 1 {
		t.Fatalf("dequeue: %+v %v", items, err)
	}
	if items[0].Status != "uploading" {
		t.Fatalf("claimed status = %q, want uploading", items[0].Status)
	}
	if err := d.MarkFailed(items[0].ID, "injected failure", testMaxRetries); err != nil {
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
	// first retry waits testDelays[0] after the failed attempt.
	again, err := d.DequeueNextPending(1, base.Add(testDelays[0]+time.Second), testDelays, testMaxRetries)
	if err != nil || len(again) != 1 {
		t.Fatalf("re-claim failed row: %+v %v", again, err)
	}
	if again[0].RetryCount != 1 {
		t.Errorf("retry count = %d, want 1", again[0].RetryCount)
	}
}

// TestBackoffDefersClaim is the regression guard for the claim/release loop: a
// failed row whose backoff has NOT elapsed must not be claimed at all. Judging
// this after the claim failed before, because the claim rewrites last_attempt --
// the post-claim check compared against the timestamp it had just written and
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
	base := time.Now().UTC()
	items, err := d.DequeueNextPending(1, base, testDelays, testMaxRetries)
	if err != nil || len(items) != 1 {
		t.Fatalf("dequeue: %+v %v", items, err)
	}
	if err := d.MarkFailed(items[0].ID, "boom", testMaxRetries); err != nil {
		t.Fatal(err)
	}

	// last_attempt is base and the first retry waits testDelays[0], so a claim
	// before base+testDelays[0] must not return the row.
	got, err := d.DequeueNextPending(1, base.Add(30*time.Second), testDelays, testMaxRetries)
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

// claimOne claims at most one row at the given time. It returns nil when the
// claim returns nothing.
func claimOne(t *testing.T, d *DB, now time.Time, maxRetries int) *models.UploadQueueItem {
	t.Helper()
	items, err := d.DequeueNextPending(1, now, testDelays, maxRetries)
	if err != nil {
		t.Fatalf("dequeue at %s: %v", now, err)
	}
	if len(items) == 0 {
		return nil
	}
	return &items[0]
}

// TestBackoffProgressesPerRetryCount is the regression guard for the flat
// one-minute backoff. A higher retry count must wait longer, so a claim inside
// the longer backoff must not return the row.
func TestBackoffProgressesPerRetryCount(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.EnqueueFile("/p.jpg"); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()

	first := claimOne(t, d, base, testMaxRetries)
	if first == nil {
		t.Fatal("pending row not claimed")
	}
	if err := d.MarkFailed(first.ID, "boom", testMaxRetries); err != nil {
		t.Fatal(err)
	}

	// First retry waits testDelays[0] (1 minute). The old flat cutoff claimed
	// the row after 1 minute no matter how often it had failed.
	if it := claimOne(t, d, base.Add(30*time.Second), testMaxRetries); it != nil {
		t.Fatalf("claimed retry_count=1 row before its 1m backoff")
	}
	second := claimOne(t, d, base.Add(time.Minute+time.Second), testMaxRetries)
	if second == nil {
		t.Fatal("retry_count=1 row not claimed after its 1m backoff")
	}
	if second.RetryCount != 1 {
		t.Fatalf("retry count = %d, want 1", second.RetryCount)
	}
	if err := d.MarkFailed(second.ID, "boom again", testMaxRetries); err != nil {
		t.Fatal(err)
	}

	// Second retry waits testDelays[1] (5 minutes) after the second attempt.
	secondAttempt := base.Add(time.Minute + time.Second)
	if it := claimOne(t, d, secondAttempt.Add(2*time.Minute), testMaxRetries); it != nil {
		t.Fatalf("claimed retry_count=2 row before its 5m backoff")
	}
	third := claimOne(t, d, secondAttempt.Add(5*time.Minute+time.Second), testMaxRetries)
	if third == nil {
		t.Fatal("retry_count=2 row not claimed after its 5m backoff")
	}
	if third.RetryCount != 2 {
		t.Fatalf("retry count = %d, want 2", third.RetryCount)
	}
}

// TestRetryCapMarksExhausted proves that uploads stop after maxRetries. The row
// must become 'exhausted', and no later claim may pick it up again.
func TestRetryCapMarksExhausted(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.EnqueueFile("/cap.jpg"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	for i := 0; i < testMaxRetries; i++ {
		it := claimOne(t, d, now, testMaxRetries)
		if it == nil {
			t.Fatalf("attempt %d: expected a claimable row, got none", i+1)
		}
		if err := d.MarkFailed(it.ID, "always fails", testMaxRetries); err != nil {
			t.Fatal(err)
		}
		// Advance past this attempt's backoff so the next attempt is due.
		now = now.Add(testDelays[i] + time.Second)
	}

	q, err := d.GetQueue()
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 1 || q[0].Status != "exhausted" {
		t.Fatalf("after %d failures: got %+v, want 1 exhausted row", testMaxRetries, q)
	}
	if q[0].RetryCount != testMaxRetries {
		t.Errorf("retry count = %d, want %d", q[0].RetryCount, testMaxRetries)
	}

	later := now.Add(24 * time.Hour)
	got, err := d.DequeueNextPending(1, later, testDelays, testMaxRetries)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("claimed %d exhausted rows, want 0", len(got))
	}
}

// TestResetFailedUploadsRestoresExhausted keeps a recovery path: a manual reset
// must make an exhausted row claimable again.
func TestResetFailedUploadsRestoresExhausted(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.EnqueueFile("/gone.jpg"); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC()

	// maxRetries=1 makes the first failure exhaust the row.
	it := claimOne(t, d, base, 1)
	if it == nil {
		t.Fatal("pending row not claimed")
	}
	if err := d.MarkFailed(it.ID, "fatal", 1); err != nil {
		t.Fatal(err)
	}
	q, err := d.GetQueue()
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 1 || q[0].Status != "exhausted" {
		t.Fatalf("want 1 exhausted row, got %+v", q)
	}

	if err := d.ResetFailedUploads(); err != nil {
		t.Fatal(err)
	}
	q, err = d.GetQueue()
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 1 || q[0].Status != "pending" || q[0].RetryCount != 0 {
		t.Fatalf("after reset: got %+v, want 1 pending row with retry count 0", q)
	}
	again := claimOne(t, d, base, 1)
	if again == nil {
		t.Fatal("row not claimable after reset")
	}
}
