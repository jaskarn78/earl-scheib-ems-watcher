package db_test

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/jjagpal/earl-scheib-watcher/internal/db"
)

// TestInitSchema verifies that InitSchema is idempotent on a fresh DB.
func TestInitSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	// First call — must succeed.
	if err := db.InitSchema(sqlDB); err != nil {
		t.Fatalf("InitSchema (first): %v", err)
	}

	// Second call — must also succeed (IF NOT EXISTS).
	if err := db.InitSchema(sqlDB); err != nil {
		t.Fatalf("InitSchema (second): %v", err)
	}
}

// TestWALMode verifies that the DB is opened in WAL journal mode.
func TestWALMode(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	var mode string
	row := sqlDB.QueryRow("PRAGMA journal_mode")
	if err := row.Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode scan: %v", err)
	}
	if mode != "wal" {
		t.Errorf("expected journal_mode=wal, got %q", mode)
	}
}

// TestDedup exercises the core deduplication semantics.
//   - MarkProcessed once → IsProcessed returns true.
//   - MarkProcessed same (filepath, mtime) again → row count stays at 1 (INSERT OR IGNORE).
//   - MarkProcessed with different mtime → second row inserted.
func TestDedup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	if err := db.InitSchema(sqlDB); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	const path = "/data/CCC/BMS123.xml"
	const mtime1 = 1713657000.123
	const mtime2 = 1713657999.456

	// Not yet processed.
	ok, err := db.IsProcessed(sqlDB, path, mtime1)
	if err != nil {
		t.Fatalf("IsProcessed: %v", err)
	}
	if ok {
		t.Error("expected false before first insert")
	}

	// Insert.
	if err := db.MarkProcessed(sqlDB, path, mtime1, 4096, "abc123sha256"); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}

	// Now it's processed.
	ok, err = db.IsProcessed(sqlDB, path, mtime1)
	if err != nil {
		t.Fatalf("IsProcessed after insert: %v", err)
	}
	if !ok {
		t.Error("expected true after insert")
	}

	// Duplicate insert — should be silently ignored.
	if err := db.MarkProcessed(sqlDB, path, mtime1, 4096, "abc123sha256"); err != nil {
		t.Fatalf("MarkProcessed (duplicate): %v", err)
	}

	// Row count for this filepath must still be 1.
	var cnt int
	if err := sqlDB.QueryRow(
		"SELECT COUNT(*) FROM processed_files WHERE filepath = ? AND mtime = ?",
		path, mtime1,
	).Scan(&cnt); err != nil {
		t.Fatalf("row count scan: %v", err)
	}
	if cnt != 1 {
		t.Errorf("expected 1 row after duplicate INSERT OR IGNORE, got %d", cnt)
	}

	// New mtime for same filepath — must produce a separate row.
	if err := db.MarkProcessed(sqlDB, path, mtime2, 5000, "def456sha256"); err != nil {
		t.Fatalf("MarkProcessed (new mtime): %v", err)
	}

	ok, err = db.IsProcessed(sqlDB, path, mtime2)
	if err != nil {
		t.Fatalf("IsProcessed (new mtime): %v", err)
	}
	if !ok {
		t.Error("expected true for new mtime after insert")
	}

	// Total rows for path = 2.
	if err := sqlDB.QueryRow(
		"SELECT COUNT(*) FROM processed_files WHERE filepath = ?",
		path,
	).Scan(&cnt); err != nil {
		t.Fatalf("total row count scan: %v", err)
	}
	if cnt != 2 {
		t.Errorf("expected 2 rows for different mtimes, got %d", cnt)
	}
}

// TestRecordRun verifies that RecordRun inserts into the runs table.
func TestRecordRun(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	if err := db.InitSchema(sqlDB); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	if err := db.RecordRun(sqlDB, 3, 0, "scan note"); err != nil {
		t.Fatalf("RecordRun: %v", err)
	}

	var cnt int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM runs").Scan(&cnt); err != nil {
		t.Fatalf("runs count scan: %v", err)
	}
	if cnt != 1 {
		t.Errorf("expected 1 row in runs, got %d", cnt)
	}
}

// TestDBRetrySuccess verifies fn is called once and nil is returned on success.
func TestDBRetrySuccess(t *testing.T) {
	// Use a very short delay to keep tests fast.
	db.RetryBaseDelay = 1
	defer func() { db.RetryBaseDelay = 500_000_000 }() // restore 500ms

	calls := 0
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	err := db.DBRetry(func() error {
		calls++
		return nil
	}, logger)

	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected fn called once, called %d times", calls)
	}
}

// TestDBRetryLockedExhausted verifies that DBRetry retries 5 times on "locked" errors
// and returns an error after exhausting attempts.
func TestDBRetryLockedExhausted(t *testing.T) {
	db.RetryBaseDelay = 1
	defer func() { db.RetryBaseDelay = 500_000_000 }()

	calls := 0
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	lockedErr := errors.New("database is locked")

	err := db.DBRetry(func() error {
		calls++
		return lockedErr
	}, logger)

	if err == nil {
		t.Fatal("expected an error after exhausting retries, got nil")
	}
	if calls != 5 {
		t.Errorf("expected fn called 5 times, called %d times", calls)
	}
}

// TestDBRetryNonLockError verifies that DBRetry returns immediately on non-lock errors.
func TestDBRetryNonLockError(t *testing.T) {
	db.RetryBaseDelay = 1
	defer func() { db.RetryBaseDelay = 500_000_000 }()

	calls := 0
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	otherErr := errors.New("disk full")

	err := db.DBRetry(func() error {
		calls++
		return otherErr
	}, logger)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 1 {
		t.Errorf("expected fn called once for non-lock error, called %d times", calls)
	}
}

// TestPrimaryKey verifies that the processed_files table has PRIMARY KEY on (filepath, mtime).
func TestPrimaryKey(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sqlDB.Close()

	if err := db.InitSchema(sqlDB); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// Attempt a direct duplicate insert without OR IGNORE — should fail with
	// a UNIQUE constraint violation, proving the PRIMARY KEY is in place.
	_, err = sqlDB.Exec(
		"INSERT INTO processed_files (filepath, mtime, size, sha256) VALUES (?, ?, ?, ?)",
		"/file.xml", 1000.0, 100, "sha1",
	)
	if err != nil {
		t.Fatalf("first insert failed: %v", err)
	}

	_, err = sqlDB.Exec(
		"INSERT INTO processed_files (filepath, mtime, size, sha256) VALUES (?, ?, ?, ?)",
		"/file.xml", 1000.0, 100, "sha1",
	)
	if err == nil {
		t.Error("expected UNIQUE constraint error on duplicate (filepath, mtime), got nil")
	}
}
