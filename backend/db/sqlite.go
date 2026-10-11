package db

import (
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"immich-desktop-sync/backend/models"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB

	// Thumbnail cache policy. Open sets the defaults; tests override them.
	cacheMaxBytes int64
	cacheTTL      time.Duration

	// cacheBytes is the exact stored BLOB total. It lets eviction skip the
	// full-cache window scan on every insert; the scan runs only when over
	// budget. Atomic because UI thumbnail requests can run concurrently.
	cacheBytes atomic.Int64
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// The upload-queue worker goroutines AND the UI bindings hit the same DB
	// concurrently. A shared connection pool lets two writers collide instantly
	// with SQLITE_BUSY ("database is locked"), which made completed uploads stay
	// stuck as 'uploading' and caused the worker to re-upload files repeatedly.
	// Pin the pool to one connection so SQLite serializes writers itself, and
	// add a busy timeout as a second line of defense.
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		return nil, fmt.Errorf("sqlite busy_timeout: %w", err)
	}
	d := &DB{
		conn:          conn,
		cacheMaxBytes: 512 << 20, // 512 MiB
		cacheTTL:      30 * 24 * time.Hour,
	}
	if err := d.migrate(); err != nil {
		return nil, err
	}
	var stored int64
	if err := d.conn.QueryRow(
		`SELECT COALESCE(SUM(LENGTH(data)), 0) FROM thumbnail_cache`).Scan(&stored); err != nil {
		return nil, fmt.Errorf("sqlite cache size: %w", err)
	}
	d.cacheBytes.Store(stored)
	return d, nil
}

func (d *DB) Close() error {
	return d.conn.Close()
}

func (d *DB) migrate() error {
	_, err := d.conn.Exec(`
		CREATE TABLE IF NOT EXISTS upload_queue (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			file_path    TEXT    NOT NULL UNIQUE,
			status       TEXT    NOT NULL DEFAULT 'pending',
			retry_count  INTEGER NOT NULL DEFAULT 0,
			last_attempt TEXT,
			error        TEXT
		);

		CREATE TABLE IF NOT EXISTS upload_history (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			file_path    TEXT NOT NULL,
			asset_id     TEXT,
			uploaded_at  TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS folders (
			id   INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT NOT NULL UNIQUE
		);

		CREATE TABLE IF NOT EXISTS thumbnail_cache (
			asset_id TEXT PRIMARY KEY,
			data     BLOB,
			cached_at TEXT NOT NULL
		);

		CREATE INDEX IF NOT EXISTS idx_thumbnail_cache_cached_at
			ON thumbnail_cache(cached_at);
	`)
	return err
}

func (d *DB) ResetStuckUploads() error {
	_, err := d.conn.Exec(`UPDATE upload_queue SET status='pending' WHERE status='uploading'`)
	return err
}

func (d *DB) EnqueueFile(path string) error {
	_, err := d.conn.Exec(
		`INSERT OR IGNORE INTO upload_queue (file_path, status) VALUES (?, 'pending')`,
		path,
	)
	return err
}

// DequeueNextPending atomically CLAIMS up to n uploadable rows by flipping them
// to 'uploading' in the same statement that selects them.
//
// This must be atomic. The worker calls processNext() from both its ticker and
// every completion notification, so two calls can overlap: a plain SELECT would
// hand the same 'pending' row to two goroutines and upload the file twice.
// Rows are returned with Status already set to "uploading", matching the state
// they are in on disk.
//
// A 'failed' row is only claimable once its own retry backoff has elapsed. The
// wait for a row depends on its retry_count: retryDelays[retry_count-1] for the
// first entries, and the last entry for higher counts. Evaluating this in the
// claim (rather than after it) matters because the claim overwrites
// last_attempt: a post-claim check would compare against the time it had just
// written and conclude the row was still backing off forever. A row at
// maxRetries failures is 'exhausted' and is never claimed again.
//
// now is a parameter, not a call to time.Now(), so tests can move time.
func (d *DB) DequeueNextPending(n int, now time.Time, retryDelays []time.Duration, maxRetries int) ([]models.UploadQueueItem, error) {
	// Single auto-commit statement: the UPDATE claims the rows and RETURNING
	// hands them back, so there is no window between selecting and claiming.
	// Deliberately NOT wrapped in an explicit transaction: with MaxOpenConns(1)
	// an open tx pins the pool's only connection, which starves the concurrent
	// MarkFailed/MarkDone writes the worker goroutines make. The row set is also
	// fully drained before this function returns.
	nowStr := now.Format(time.RFC3339)

	// Map a row's retry count to the cutoff for its next attempt. WHEN 0 covers
	// a failed row with no retries. ELSE clamps counts past the table to the
	// longest delay.
	var backoff strings.Builder
	backoff.WriteString("CASE retry_count WHEN 0 THEN ?")
	args := []any{nowStr, maxRetries, nowStr}
	for i, delay := range retryDelays {
		fmt.Fprintf(&backoff, " WHEN %d THEN ?", i+1)
		args = append(args, now.Add(-delay).Format(time.RFC3339))
	}
	backoff.WriteString(" ELSE ? END")
	if len(retryDelays) > 0 {
		args = append(args, now.Add(-retryDelays[len(retryDelays)-1]).Format(time.RFC3339))
	} else {
		args = append(args, nowStr)
	}
	args = append(args, n)

	rows, err := d.conn.Query(`
		UPDATE upload_queue
		SET status='uploading', last_attempt=?
		WHERE id IN (
			SELECT id FROM upload_queue
			WHERE retry_count < ?
			   AND (
			        status = 'pending'
			        OR (status = 'failed' AND (last_attempt IS NULL OR last_attempt <= `+backoff.String()+`))
			   )
			ORDER BY id ASC
			LIMIT ?
		)
		RETURNING id, file_path, status, retry_count, last_attempt, error`,
		args...)
	if err != nil {
		return nil, err
	}

	var items []models.UploadQueueItem
	for rows.Next() {
		var item models.UploadQueueItem
		var lastAttempt, errStr sql.NullString
		if err := rows.Scan(&item.ID, &item.FilePath, &item.Status, &item.RetryCount, &lastAttempt, &errStr); err != nil {
			rows.Close() // release the pooled connection before returning
			return nil, err
		}
		item.LastAttempt = lastAttempt.String
		item.Error = errStr.String
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close() // must happen here: this frees the single pooled connection
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (d *DB) MarkDone(id int64, filePath, assetID string) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`DELETE FROM upload_queue WHERE id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO upload_history (file_path, asset_id, uploaded_at) VALUES (?,?,?)`,
		filePath, assetID, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkFailed marks a claimed row as failed and increments its retry count. A
// row that reaches maxRetries failures becomes 'exhausted', and
// DequeueNextPending never claims it again. Call ResetFailedUploads to make it
// claimable again after a manual retry.
func (d *DB) MarkFailed(id int64, errMsg string, maxRetries int) error {
	_, err := d.conn.Exec(
		`UPDATE upload_queue
		SET status = CASE WHEN retry_count + 1 >= ? THEN 'exhausted' ELSE 'failed' END,
		    retry_count = retry_count + 1,
		    error = ?
		WHERE id = ?`,
		maxRetries, errMsg, id,
	)
	return err
}

func (d *DB) ResetFailedUploads() error {
	_, err := d.conn.Exec(`UPDATE upload_queue SET status='pending', retry_count=0, error=NULL WHERE status IN ('failed','exhausted')`)
	return err
}

func (d *DB) GetQueue() ([]models.UploadQueueItem, error) {
	rows, err := d.conn.Query(`
		SELECT id, file_path, status, retry_count, last_attempt, error
		FROM upload_queue ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.UploadQueueItem
	for rows.Next() {
		var item models.UploadQueueItem
		var lastAttempt, errStr sql.NullString
		if err := rows.Scan(&item.ID, &item.FilePath, &item.Status, &item.RetryCount, &lastAttempt, &errStr); err != nil {
			return nil, err
		}
		item.LastAttempt = lastAttempt.String
		item.Error = errStr.String
		items = append(items, item)
	}
	return items, rows.Err()
}

func (d *DB) IsUploaded(path string) (bool, error) {
	var count int
	err := d.conn.QueryRow(`SELECT COUNT(*) FROM upload_history WHERE file_path=?`, path).Scan(&count)
	return count > 0, err
}

func (d *DB) AddFolder(path string) error {
	_, err := d.conn.Exec(`INSERT OR IGNORE INTO folders (path) VALUES (?)`, path)
	return err
}

func (d *DB) RemoveFolder(path string) error {
	_, err := d.conn.Exec(`DELETE FROM folders WHERE path=?`, path)
	return err
}

func (d *DB) GetFolders() ([]string, error) {
	rows, err := d.conn.Query(`SELECT path FROM folders ORDER BY path ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		folders = append(folders, p)
	}
	return folders, rows.Err()
}

func (d *DB) CacheThumbnail(key string, data []byte) error {
	now := time.Now().UTC()
	// INSERT OR REPLACE hides the replaced size, so read it first. A miss leaves
	// oldLen at 0; a stale count only makes eviction run slightly early.
	var oldLen int64
	_ = d.conn.QueryRow(`SELECT LENGTH(data) FROM thumbnail_cache WHERE asset_id=?`, key).Scan(&oldLen)
	if _, err := d.conn.Exec(
		`INSERT OR REPLACE INTO thumbnail_cache (asset_id, data, cached_at) VALUES (?,?,?)`,
		key, data, now.Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	d.cacheBytes.Add(int64(len(data)) - oldLen)
	return d.evictThumbnails(now)
}

// evictThumbnails drops expired rows, then the oldest rows that push the cache
// over its byte budget. The byte-budget window scan only runs when the in-memory
// total says the cache is over budget, so a normal insert does not scan every
// row. It runs as separate auto-commit statements rather than one transaction: a
// cached thumbnail may be briefly over budget, and an open transaction would pin
// the pool's single connection (see DequeueNextPending).
func (d *DB) evictThumbnails(now time.Time) error {
	freed, err := d.deleteThumbnailsReturningSize(
		`DELETE FROM thumbnail_cache WHERE cached_at < ?`,
		now.Add(-d.cacheTTL).Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	d.cacheBytes.Add(-freed)

	if d.cacheMaxBytes <= 0 || d.cacheBytes.Load() <= d.cacheMaxBytes {
		return nil
	}
	// Keep the newest rows while their running total fits the budget. rowid
	// breaks cached_at ties (RFC3339 has one-second resolution) and grows with
	// each INSERT OR REPLACE.
	freed, err = d.deleteThumbnailsReturningSize(`
		DELETE FROM thumbnail_cache
		WHERE rowid IN (
			SELECT rowid FROM (
				SELECT rowid,
				       SUM(LENGTH(data)) OVER (ORDER BY cached_at DESC, rowid DESC) AS running
				FROM thumbnail_cache
			)
			WHERE running > ?
		)`, d.cacheMaxBytes)
	if err != nil {
		return err
	}
	d.cacheBytes.Add(-freed)
	return nil
}

// deleteThumbnailsReturningSize runs a DELETE and sums the stored BLOB sizes of
// the rows it removed, so the caller can keep the byte count exact.
func (d *DB) deleteThumbnailsReturningSize(query string, args ...any) (int64, error) {
	rows, err := d.conn.Query(query+` RETURNING LENGTH(data)`, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var freed int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return 0, err
		}
		freed += n
	}
	return freed, rows.Err()
}

func (d *DB) GetThumbnail(key string) ([]byte, error) {
	var data []byte
	err := d.conn.QueryRow(`SELECT data FROM thumbnail_cache WHERE asset_id=?`, key).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return data, err
}

func (d *DB) ClearCache() error {
	if _, err := d.conn.Exec(`DELETE FROM thumbnail_cache`); err != nil {
		return err
	}
	d.cacheBytes.Store(0)
	return nil
}
