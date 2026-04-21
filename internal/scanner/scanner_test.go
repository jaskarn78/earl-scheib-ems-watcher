package scanner

import (
	"context"
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

// recordingHandler is a slog.Handler that captures every Record into a slice
// (protected by a mutex) so tests can assert on level, message, and attrs.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *recordingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(_ string) slog.Handler      { return h }

// snapshot returns a copy of the captured records.
func (h *recordingHandler) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]slog.Record, len(h.records))
	copy(out, h.records)
	return out
}

// findRecord returns the first captured record whose Message == msg, or nil.
func findRecord(recs []slog.Record, msg string) *slog.Record {
	for i := range recs {
		if recs[i].Message == msg {
			return &recs[i]
		}
	}
	return nil
}

// attrMap flattens a record's attrs into a map[string]any for easy assertions.
func attrMap(r slog.Record) map[string]any {
	m := make(map[string]any)
	r.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.Any()
		return true
	})
	return m
}

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

// ---- Debuggability tests (Task 1: 260421-shq) ------------------------------

// TestRunEmitsScanStartINFO: every Run() call emits exactly one INFO record
// at cycle start with keys watch_folder, webhook, version — for ops grep.
func TestRunEmitsScanStartINFO(t *testing.T) {
	t.Parallel()
	h := &recordingHandler{}
	logger := slog.New(h)
	d := openTestDB(t)
	dir := t.TempDir()

	cfg := RunConfig{
		WatchFolder: dir,
		WebhookURL:  "https://example.test/earlscheibconcord",
		AppVersion:  "0.4.0-test",
		DB:          d,
		Logger:      logger,
		Sender:      func(_ string, _ []byte) bool { return true },
		SettleOpts:  SettleOptions{Samples: 2, Interval: 1 * time.Millisecond},
	}

	_, _ = Run(cfg)

	rec := findRecord(h.snapshot(), "scan start")
	if rec == nil {
		t.Fatal(`expected a record with message "scan start"; got none`)
	}
	if rec.Level != slog.LevelInfo {
		t.Fatalf("scan start: expected level INFO, got %s", rec.Level)
	}
	attrs := attrMap(*rec)
	if got, want := attrs["watch_folder"], dir; got != want {
		t.Errorf("scan start: watch_folder=%v; want %q", got, want)
	}
	if got, want := attrs["webhook"], cfg.WebhookURL; got != want {
		t.Errorf("scan start: webhook=%v; want %q", got, want)
	}
	if got, want := attrs["version"], cfg.AppVersion; got != want {
		t.Errorf("scan start: version=%v; want %q", got, want)
	}
}

// TestCandidatesLogsPathAndError: Candidates on a non-existent directory
// logs a WARN record whose attrs include the exact path AND a non-nil err
// whose Error() string contains the path (proves the OS error is forwarded,
// not replaced with a generic message).
func TestCandidatesLogsPathAndError(t *testing.T) {
	t.Parallel()
	h := &recordingHandler{}
	logger := slog.New(h)

	missing := filepath.Join(t.TempDir(), "does-not-exist-9z")
	_ = Candidates(missing, logger)

	rec := findRecord(h.snapshot(), "Cannot read watch folder")
	if rec == nil {
		t.Fatal(`expected WARN record "Cannot read watch folder"; got none`)
	}
	if rec.Level != slog.LevelWarn {
		t.Fatalf("expected WARN, got %s", rec.Level)
	}
	attrs := attrMap(*rec)

	// Spec uses key "path" (renamed from the old "dir") so ops greps/dashboards
	// know exactly which directory the OS rejected.
	gotPath, ok := attrs["path"].(string)
	if !ok {
		t.Fatalf(`expected attr "path" to be a string; attrs=%v`, attrs)
	}
	if gotPath != missing {
		t.Errorf(`path attr=%q; want %q`, gotPath, missing)
	}

	// err must be the real underlying OS error — not nil, not a sanitized string.
	gotErr, ok := attrs["err"].(error)
	if !ok {
		// slog may surface the error via its String representation depending on
		// handler rendering; accept both but reject nil/empty.
		if s, isStr := attrs["err"].(string); isStr {
			if s == "" {
				t.Fatalf("err attr empty")
			}
			if !strings.Contains(s, missing) {
				t.Errorf("err string %q does not contain path %q — OS error was not forwarded", s, missing)
			}
			return
		}
		t.Fatalf(`expected attr "err" to be an error or string; got %T (%v)`, attrs["err"], attrs["err"])
	}
	if gotErr == nil {
		t.Fatal("err is nil — OS error swallowed")
	}
	if !strings.Contains(gotErr.Error(), missing) {
		t.Errorf("err %q does not contain path %q — OS error not forwarded verbatim", gotErr.Error(), missing)
	}
}

// TestRunEmptyFolderStillRecordsRun: regression — new INFO line does not change
// the (0, 0) + "no files" note semantics for an empty watch folder.
func TestRunEmptyFolderStillRecordsRun(t *testing.T) {
	t.Parallel()
	h := &recordingHandler{}
	logger := slog.New(h)
	d := openTestDB(t)
	dir := t.TempDir()

	cfg := RunConfig{
		WatchFolder: dir,
		WebhookURL:  "https://example.test/earlscheibconcord",
		AppVersion:  "0.4.0-test",
		DB:          d,
		Logger:      logger,
		Sender:      func(_ string, _ []byte) bool { return true },
		SettleOpts:  SettleOptions{Samples: 2, Interval: 1 * time.Millisecond},
	}

	proc, errs := Run(cfg)
	if proc != 0 || errs != 0 {
		t.Fatalf("empty folder: expected (0, 0), got (%d, %d)", proc, errs)
	}

	// "no files" note must be recorded in the runs table.
	var note string
	if err := d.QueryRow(
		"SELECT note FROM runs ORDER BY rowid DESC LIMIT 1",
	).Scan(&note); err != nil {
		t.Fatalf("query last run note: %v", err)
	}
	if note != "no files" {
		t.Errorf(`runs.note = %q; want "no files"`, note)
	}
}
