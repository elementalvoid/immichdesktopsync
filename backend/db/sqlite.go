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

func (d *DB) DequeueNextPending(n int) ([]models.UploadQueueItem, error) {
	rows, err := d.conn.Query(`
		SELECT id, file_path, status, retry_count, last_attempt, error
		FROM upload_queue
		WHERE status IN ('pending', 'failed')
		ORDER BY id ASC
		LIMIT ?`, n)
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
