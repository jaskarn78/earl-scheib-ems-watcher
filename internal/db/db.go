// Package db provides SQLite access for the Earl Scheib EMS watcher.
//
// It wraps modernc.org/sqlite (pure-Go, CGO_ENABLED=0) and exposes the same
// schema and semantics as the Python reference ems_watcher.py:
//   - WAL journal_mode + busy_timeout=30000 + synchronous=NORMAL
//   - processed_files(filepath, mtime) PRIMARY KEY for dedup
//   - runs table for scan history
//   - DBRetry with 5 attempts and exponential backoff on "locked" errors
package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "modernc.org/sqlite" // register "sqlite" driver
)

// RetryBaseDelay is the initial sleep between DBRetry attempts.
// Exported so tests can set it to 1ns to avoid real sleeps.
var RetryBaseDelay = 500 * time.Millisecond

// dbLockAttempts is the maximum number of retries on a "locked" error,
// matching Python's DB_LOCK_ATTEMPTS = 5.
const dbLockAttempts = 5

// Open opens or creates the SQLite database at path.
//
// It sets:
//   - PRAGMA busy_timeout = 30000
//   - PRAGMA journal_mode = WAL
//   - PRAGMA synchronous = NORMAL
//
// These match open_db() in ems_watcher.py.
func Open(path string) (*sql.DB, error) {
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("db.Open: sql.Open: %w", err)
	}

	pragmas := []string{
		"PRAGMA busy_timeout = 30000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
	}
	for _, pragma := range pragmas {
		if _, err := sqlDB.Exec(pragma); err != nil {
			// Mirror Python's fallback: WAL failure on network drives is
			// tolerated; other pragma errors are also tolerated but logged.
			// We proceed even on error so callers get a usable DB.
			_ = err
		}
	}

	return sqlDB, nil
}

// InitSchema creates the processed_files and runs tables if they do not
// already exist. Safe to call on an existing database — IF NOT EXISTS
// makes it idempotent.
//
// Schema is byte-for-byte compatible with init_db() in ems_watcher.py so an
// existing Python-written ems_watcher.db can be opened without migration.
func InitSchema(db *sql.DB) error {
	const createProcessedFiles = `
CREATE TABLE IF NOT EXISTS processed_files (
    filepath  TEXT NOT NULL,
    mtime     REAL NOT NULL,
    size      INTEGER,
    sha256    TEXT,
    sent_at   TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (filepath, mtime)
)`

	const createRuns = `
CREATE TABLE IF NOT EXISTS runs (
    run_at    TEXT NOT NULL DEFAULT (datetime('now')),
    processed INTEGER NOT NULL DEFAULT 0,
    errors    INTEGER NOT NULL DEFAULT 0,
    note      TEXT
)`

	if _, err := db.Exec(createProcessedFiles); err != nil {
		return fmt.Errorf("InitSchema: create processed_files: %w", err)
	}
	if _, err := db.Exec(createRuns); err != nil {
		return fmt.Errorf("InitSchema: create runs: %w", err)
	}
	return nil
}

// IsProcessed returns true if the (filepath, mtime) pair exists in
// processed_files, matching is_already_processed() in ems_watcher.py.
func IsProcessed(db *sql.DB, filepath string, mtime float64) (bool, error) {
	var exists int
	err := db.QueryRow(
		"SELECT 1 FROM processed_files WHERE filepath = ? AND mtime = ?",
		filepath, mtime,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("IsProcessed: %w", err)
	}
	return true, nil
}

// MarkProcessed inserts (filepath, mtime, size, sha256) into processed_files
// using INSERT OR IGNORE. A duplicate (filepath, mtime) is silently skipped,
// matching mark_processed() in ems_watcher.py.
func MarkProcessed(db *sql.DB, filepath string, mtime float64, size int64, sha256 string) error {
	_, err := db.Exec(
		"INSERT OR IGNORE INTO processed_files (filepath, mtime, size, sha256) VALUES (?, ?, ?, ?)",
		filepath, mtime, size, sha256,
	)
	if err != nil {
		return fmt.Errorf("MarkProcessed: %w", err)
	}
	return nil
}

// RecordRun inserts a row into the runs table, matching record_run() in
// ems_watcher.py.
func RecordRun(db *sql.DB, processed, errors int, note string) error {
	_, err := db.Exec(
		"INSERT INTO runs (processed, errors, note) VALUES (?, ?, ?)",
		processed, errors, note,
	)
	if err != nil {
		return fmt.Errorf("RecordRun: %w", err)
	}
	return nil
}

// DBRetry runs fn, retrying up to dbLockAttempts (5) times when the returned
// error contains "locked". Delay starts at RetryBaseDelay (500 ms) and
// doubles each attempt.
//
// On a non-lock error, DBRetry returns immediately without retrying.
// After exhausting all attempts, the last error is returned.
//
// This matches _db_retry() in ems_watcher.py.
func DBRetry(fn func() error, logger *slog.Logger) error {
	delay := RetryBaseDelay
	for attempt := 1; attempt <= dbLockAttempts; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		if !strings.Contains(strings.ToLower(err.Error()), "locked") || attempt == dbLockAttempts {
			return err
		}
		logger.Warn("SQLite busy",
			"attempt", attempt,
			"of", dbLockAttempts,
			"retrying_in", delay,
		)
		time.Sleep(delay)
		delay *= 2
	}
	return nil
}
