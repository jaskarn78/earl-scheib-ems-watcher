package scanner

import (
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jjagpal/earl-scheib-watcher/internal/db"
)

// testLogger returns an slog.Logger backed by testing.T for output capture.
func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// testWriter bridges slog to testing.T.Log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// ---- Settle tests ----------------------------------------------------------

// TestSettleStable: a file that never changes should settle after 2 consecutive stable samples.
func TestSettleStable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "stable.xml")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := SettleOptions{Samples: 4, Interval: 1 * time.Millisecond}
	info, settled := SettleCheck(path, opts, t.Logf)
	if !settled {
		t.Fatal("expected settled=true for a stable file")
	}
	if info == nil {
		t.Fatal("expected non-nil FileInfo for settled file")
	}
}

// TestSettleChanging: a file that keeps growing should not settle within sample limit.
func TestSettleChanging(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "growing.xml")
	if err := os.WriteFile(path, []byte("start"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Keep writing to the file during the settle check.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
				if err != nil {
					return
				}
				_, _ = f.WriteString("x")
				_ = f.Close()
				time.Sleep(200 * time.Microsecond)
			}
		}
	}()

	opts := SettleOptions{Samples: 6, Interval: 2 * time.Millisecond}
	_, settled := SettleCheck(path, opts, t.Logf)
	close(stop)
	wg.Wait()

	if settled {
		t.Fatal("expected settled=false for a continuously growing file")
	}
}

// TestSettleResets: file changes once then stabilises — stable_count resets to 0, eventually settles.
func TestSettleResets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "reset.xml")
	if err := os.WriteFile(path, []byte("initial"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write once after a very short delay to cause a change, then leave it stable.
	go func() {
		time.Sleep(3 * time.Millisecond)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		_, _ = f.WriteString("extra")
		_ = f.Close()
	}()

	// Use enough samples: initial stat + 1 stable + 1 change + 2 consecutive stable = settled.
	// With 10 samples at 1ms intervals, there's plenty of room.
	opts := SettleOptions{Samples: 10, Interval: 1 * time.Millisecond}
	info, settled := SettleCheck(path, opts, t.Logf)

	if !settled {
		t.Fatal("expected settled=true after reset + stabilisation")
	}
	if info == nil {
		t.Fatal("expected non-nil FileInfo")
	}
}

// TestSettleFileGone: file is deleted during settle check — should return (nil, false).
func TestSettleFileGone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.xml")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(2 * time.Millisecond)
		_ = os.Remove(path)
	}()

	opts := SettleOptions{Samples: 6, Interval: 3 * time.Millisecond}
	info, settled := SettleCheck(path, opts, t.Logf)
	if settled {
		t.Fatal("expected settled=false when file is removed")
	}
	if info != nil {
		t.Fatal("expected nil FileInfo when file is removed")
	}
}

// ---- Candidates tests ------------------------------------------------------

// TestCandidatesEmpty: empty dir → empty slice.
func TestCandidatesEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got := Candidates(dir, testLogger(t))
	if len(got) != 0 {
		t.Fatalf("expected 0 candidates, got %d: %v", len(got), got)
	}
}

// TestCandidatesFilters: only .xml and .ems (case-insensitive) returned.
func TestCandidatesFilters(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"a.xml", "b.ems", "c.txt", "d.XML", "e.EMS", "f.pdf"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := Candidates(dir, testLogger(t))
	if len(got) != 4 {
		t.Fatalf("expected 4 candidates (xml/ems case-insensitive), got %d: %v", len(got), got)
	}
	for _, p := range got {
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".xml" && ext != ".ems" {
			t.Fatalf("unexpected extension in candidates: %s", p)
		}
	}
}

// TestCandidatesMissingDir: Candidates on a non-existent path returns [] without panic.
func TestCandidatesMissingDir(t *testing.T) {
	t.Parallel()
	got := Candidates("/definitely/does/not/exist/12345", testLogger(t))
	if len(got) != 0 {
		t.Fatalf("expected 0 candidates for missing dir, got %d", len(got))
	}
}

// ---- Run tests -------------------------------------------------------------

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InitSchema(d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestRunDedup: first run processes file; second run skips it (mtime match).
func TestRunDedup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.xml")
	if err := os.WriteFile(path, []byte("<root/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := openTestDB(t)
	sendCalls := 0
	sender := func(_ string, _ []byte) bool {
		sendCalls++
		return true
	}

	cfg := RunConfig{
		WatchFolder: dir,
		DB:          d,
		Logger:      testLogger(t),
		Sender:      sender,
		SettleOpts:  SettleOptions{Samples: 2, Interval: 1 * time.Millisecond},
	}

	proc1, err1 := Run(cfg)
	if proc1 != 1 || err1 != 0 {
		t.Fatalf("first run: expected (1, 0), got (%d, %d)", proc1, err1)
	}
	if sendCalls != 1 {
		t.Fatalf("expected 1 send call, got %d", sendCalls)
	}

	proc2, err2 := Run(cfg)
	if proc2 != 0 || err2 != 0 {
		t.Fatalf("second run (dedup): expected (0, 0), got (%d, %d)", proc2, err2)
	}
	if sendCalls != 1 {
		t.Fatalf("expected still 1 send call after dedup, got %d", sendCalls)
	}
}

// TestRunSettleSkip: file that keeps changing fails to settle → skipped this cycle.
func TestRunSettleSkip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "growing.xml")
	if err := os.WriteFile(path, []byte("start"), 0o644); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
				if f != nil {
					_, _ = f.WriteString("x")
					_ = f.Close()
				}
				time.Sleep(100 * time.Microsecond)
			}
		}
	}()
	defer func() {
		close(stop)
		wg.Wait()
	}()

	d := openTestDB(t)
	sender := func(_ string, _ []byte) bool { return true }
	cfg := RunConfig{
		WatchFolder: dir,
		DB:          d,
		Logger:      testLogger(t),
		Sender:      sender,
		SettleOpts:  SettleOptions{Samples: 4, Interval: 2 * time.Millisecond},
	}

	proc, errs := Run(cfg)
	if proc != 0 {
		t.Fatalf("expected 0 processed for unsettled file, got %d", proc)
	}
	if errs != 0 {
		t.Fatalf("expected 0 errors for unsettled file, got %d", errs)
	}
}

// TestRunSenderFailure: sender returns false → errors=1, file NOT in DB, retry on next run.
func TestRunSenderFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "fail.xml")
	if err := os.WriteFile(path, []byte("<root/>"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := openTestDB(t)
	sender := func(_ string, _ []byte) bool { return false }
	cfg := RunConfig{
		WatchFolder: dir,
		DB:          d,
		Logger:      testLogger(t),
		Sender:      sender,
		SettleOpts:  SettleOptions{Samples: 2, Interval: 1 * time.Millisecond},
	}

	proc, errs := Run(cfg)
	if proc != 0 || errs != 1 {
		t.Fatalf("expected (0, 1), got (%d, %d)", proc, errs)
	}

	// File must NOT be in DB so it is retried next run.
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mtime := float64(stat.ModTime().UnixNano()) / 1e9
	already, err := db.IsProcessed(d, path, mtime)
	if err != nil {
		t.Fatal(err)
	}
	if already {
		t.Fatal("file should NOT be marked processed after sender failure")
	}
}

// TestRunMissingFolder: Run with non-existent WatchFolder returns (0, 0) without panic.
func TestRunMissingFolder(t *testing.T) {
	t.Parallel()
	d := openTestDB(t)
	sender := func(_ string, _ []byte) bool { return true }
	cfg := RunConfig{
		WatchFolder: "/no/such/folder/xyz",
		DB:          d,
		Logger:      testLogger(t),
		Sender:      sender,
		SettleOpts:  SettleOptions{Samples: 2, Interval: 1 * time.Millisecond},
	}
	proc, errs := Run(cfg)
	if proc != 0 || errs != 0 {
		t.Fatalf("expected (0, 0) for missing folder, got (%d, %d)", proc, errs)
	}
}
