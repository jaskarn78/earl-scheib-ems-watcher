package status_test

import (
	"bytes"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjagpal/earl-scheib-watcher/internal/config"
	"github.com/jjagpal/earl-scheib-watcher/internal/db"
	"github.com/jjagpal/earl-scheib-watcher/internal/status"
)

// discardLogger returns a slog.Logger that discards all output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestPrintNoDB: Print with a non-existent DB path → output contains "No database yet"
func TestPrintNoDB(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Config{
		WatchFolder: tmpDir,
		WebhookURL:  "https://example.com/webhook",
		LogLevel:    "INFO",
	}

	var buf bytes.Buffer
	// Pass nil for sqlDB to simulate no DB
	status.Print(cfg, tmpDir, nil, discardLogger(), &buf)

	out := buf.String()
	if !strings.Contains(out, "No database yet") {
		t.Errorf("expected 'No database yet' in output, got:\n%s", out)
	}
}

// TestPrintWithRuns: open real DB in tempdir, InitSchema, RecordRun → call Print → output contains "Last run"
func TestPrintWithRuns(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "ems_watcher.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()

	if err := db.InitSchema(sqlDB); err != nil {
		t.Fatalf("db.InitSchema: %v", err)
	}
	if err := db.RecordRun(sqlDB, 3, 0, "test run"); err != nil {
		t.Fatalf("db.RecordRun: %v", err)
	}

	cfg := config.Config{
		WatchFolder: tmpDir,
		WebhookURL:  "https://example.com/webhook",
		LogLevel:    "INFO",
	}

	var buf bytes.Buffer
	status.Print(cfg, tmpDir, sqlDB, discardLogger(), &buf)

	out := buf.String()
	if !strings.Contains(out, "Last run") {
		t.Errorf("expected 'Last run' in output, got:\n%s", out)
	}
}

// TestPrintFolderReachable: existing tempdir as WatchFolder → output contains "YES"
func TestPrintFolderReachable(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Config{
		WatchFolder: tmpDir,
		WebhookURL:  "https://example.com/webhook",
		LogLevel:    "INFO",
	}

	var buf bytes.Buffer
	status.Print(cfg, tmpDir, nil, discardLogger(), &buf)

	out := buf.String()
	if !strings.Contains(out, "YES") {
		t.Errorf("expected 'YES' in output for reachable folder, got:\n%s", out)
	}
}

// TestPrintFolderUnreachable: "/definitely/no/such/folder" → output contains "NO"
func TestPrintFolderUnreachable(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Config{
		WatchFolder: "/definitely/no/such/folder",
		WebhookURL:  "https://example.com/webhook",
		LogLevel:    "INFO",
	}

	var buf bytes.Buffer
	status.Print(cfg, tmpDir, nil, discardLogger(), &buf)

	out := buf.String()
	if !strings.Contains(out, "NO") {
		t.Errorf("expected 'NO' in output for unreachable folder, got:\n%s", out)
	}
}

// TestPrintTodayTotalCounts: DB with processed_files → output contains today/total counts
func TestPrintTodayTotalCounts(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "ems_watcher.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()

	if err := db.InitSchema(sqlDB); err != nil {
		t.Fatalf("db.InitSchema: %v", err)
	}
	if err := db.RecordRun(sqlDB, 2, 0, "test"); err != nil {
		t.Fatalf("db.RecordRun: %v", err)
	}
	// Insert a processed file
	if err := db.MarkProcessed(sqlDB, "/tmp/test.xml", 1234567890.0, 100, "abc123"); err != nil {
		t.Fatalf("db.MarkProcessed: %v", err)
	}

	cfg := config.Config{
		WatchFolder: tmpDir,
		WebhookURL:  "https://example.com/webhook",
		LogLevel:    "INFO",
	}

	var buf bytes.Buffer
	status.Print(cfg, tmpDir, sqlDB, discardLogger(), &buf)

	out := buf.String()
	if !strings.Contains(out, "Files sent today") {
		t.Errorf("expected 'Files sent today' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Files sent total") {
		t.Errorf("expected 'Files sent total' in output, got:\n%s", out)
	}
}

// TestPrintLogTail: log file with errors → output contains "Recent log errors"
func TestPrintLogTail(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "ems_watcher.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer sqlDB.Close()

	if err := db.InitSchema(sqlDB); err != nil {
		t.Fatalf("db.InitSchema: %v", err)
	}
	if err := db.RecordRun(sqlDB, 0, 1, "test error run"); err != nil {
		t.Fatalf("db.RecordRun: %v", err)
	}

	// Create a log file with some error lines
	logPath := filepath.Join(tmpDir, "ems_watcher.log")
	logContent := "2026-01-01 10:00:00 [INFO] Normal line\n" +
		"2026-01-01 10:00:01 [ERROR] Something went wrong\n" +
		"2026-01-01 10:00:02 [WARNING] Watch out\n"
	if err := os.WriteFile(logPath, []byte(logContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := config.Config{
		WatchFolder: tmpDir,
		WebhookURL:  "https://example.com/webhook",
		LogLevel:    "INFO",
	}

	var buf bytes.Buffer
	status.Print(cfg, tmpDir, sqlDB, discardLogger(), &buf)

	out := buf.String()
	if !strings.Contains(out, "Recent log errors") {
		t.Errorf("expected 'Recent log errors' in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Something went wrong") {
		t.Errorf("expected error line in output, got:\n%s", out)
	}
}

// TestPrintWithNilDB_DBFileAbsent: nil sqlDB and no DB file → "No database yet"
func TestPrintWithNilDB_DBFileAbsent(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := config.Config{
		WatchFolder: tmpDir,
		WebhookURL:  "https://example.com/webhook",
		LogLevel:    "INFO",
	}

	var buf bytes.Buffer
	// nil sqlDB simulates no database
	status.Print(cfg, tmpDir, nil, discardLogger(), &buf)

	out := buf.String()
	if !strings.Contains(out, "No database yet") {
		t.Errorf("expected 'No database yet' when sqlDB is nil, got:\n%s", out)
	}
}

// Compile-time check: ensure status.Print has the expected signature.
var _ = func() {
	var cfg config.Config
	var sqlDB *sql.DB
	var logger *slog.Logger
	var w io.Writer
	status.Print(cfg, "", sqlDB, logger, w)
}
