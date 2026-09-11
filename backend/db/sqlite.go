package db

import (
	"database/sql"
	"fmt"
	"time"

	"immich-desktop-sync/backend/models"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
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
	d := &DB{conn: conn}
	if err := d.migrate(); err != nil {
		return nil, err
	}
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
// A 'failed' row is only claimable once its retry backoff has elapsed, i.e.
// when its last_attempt is at or before cutoff. Evaluating that here (rather
// than after the claim) matters because the claim overwrites last_attempt: a
// post-claim check would compare against the time it had just written and
// conclude the row was still backing off forever. The caller owns the retry
// policy and passes the cutoff.
func (d *DB) DequeueNextPending(n int, cutoff time.Time) ([]models.UploadQueueItem, error) {
	// Single auto-commit statement: the UPDATE claims the rows and RETURNING
	// hands them back, so there is no window between selecting and claiming.
	// Deliberately NOT wrapped in an explicit transaction: with MaxOpenConns(1)
	// an open tx pins the pool's only connection, which starves the concurrent
	// MarkFailed/MarkDone writes the worker goroutines make. The row set is also
	// fully drained before this function returns.
	now := time.Now().UTC()
	rows, err := d.conn.Query(`
		UPDATE upload_queue
		SET status='uploading', last_attempt=?
		WHERE id IN (
			SELECT id FROM upload_queue
			WHERE status = 'pending'
			   OR (
			        status = 'failed'
			        AND (last_attempt IS NULL OR last_attempt <= ?)
			   )
			ORDER BY id ASC
			LIMIT ?
		)
		RETURNING id, file_path, status, retry_count, last_attempt, error`,
		now.Format(time.RFC3339), cutoff.Format(time.RFC3339), n)
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

func (d *DB) MarkUploading(id int64) error {
	_, err := d.conn.Exec(
		`UPDATE upload_queue SET status='uploading', last_attempt=? WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
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

func (d *DB) MarkFailed(id int64, errMsg string) error {
	_, err := d.conn.Exec(
		`UPDATE upload_queue SET status='failed', retry_count=retry_count+1, error=? WHERE id=?`,
		errMsg, id,
	)
	return err
}

func (d *DB) ResetFailedUploads() error {
	_, err := d.conn.Exec(`UPDATE upload_queue SET status='pending', retry_count=0, error=NULL WHERE status='failed'`)
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
	_, err := d.conn.Exec(
		`INSERT OR REPLACE INTO thumbnail_cache (asset_id, data, cached_at) VALUES (?,?,?)`,
		key, data, time.Now().UTC().Format(time.RFC3339),
	)
	return err
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
	_, err := d.conn.Exec(`DELETE FROM thumbnail_cache`)
	return err
}
