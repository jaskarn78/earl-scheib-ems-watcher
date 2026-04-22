package update

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testSecret = "unit-test-secret"

// computeHash16 returns the first 16 hex of SHA256(data). Mirrors production.
func computeHash16(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])[:16]
}

// testExeHash16 hashes the currently-running test binary via os.Executable().
func testExeHash16(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	f, err := os.Open(exe)
	if err != nil {
		t.Fatalf("open exe: %v", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("hash exe: %v", err)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// mockCounters tracks per-endpoint hits so tests can assert network was/wasn't touched.
type mockCounters struct {
	Version  int
	Download int
}

// newMockServer returns an httptest.Server that handles /version + /download.exe
// (both HMAC-gated) plus a counter for per-endpoint hit tracking.
func newMockServer(t *testing.T, versionResp map[string]any, installerBytes []byte) (*httptest.Server, *mockCounters) {
	t.Helper()
	counters := &mockCounters{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expect := hmacHex(testSecret, nil)
		got := r.Header.Get("X-EMS-Signature")
		if got != expect {
			http.Error(w, "bad sig", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/version":
			counters.Version++
			b, _ := json.Marshal(versionResp)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(b)
		case "/download.exe":
			counters.Download++
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(installerBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, counters
}

func hmacHex(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// newTestContext resets package-level hooks so each test is isolated.
func newTestContext(t *testing.T) (launcherCalls *int, launcherPath *string, exitCalls *int, launcher func(string) error) {
	t.Helper()
	t.Setenv("UPDATE_TEST_FORCE", "1")

	// Stub exitFn + sleepFn so tests don't actually exit or pause.
	prevExit := exitFn
	prevSleep := sleepFn
	exitCalls = new(int)
	exitFn = func(int) { *exitCalls++ }
	sleepFn = func(time.Duration) {}
	t.Cleanup(func() {
		exitFn = prevExit
		sleepFn = prevSleep
	})

	launcherCalls = new(int)
	launcherPath = new(string)
	launcher = func(p string) error {
		*launcherCalls++
		*launcherPath = p
		return nil
	}
	return
}

func readCooldownForTest(t *testing.T, dataDir string) (cooldownState, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dataDir, "update_last_check.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return cooldownState{}, false
		}
		t.Fatalf("read cooldown: %v", err)
	}
	var s cooldownState
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("unmarshal cooldown: %v", err)
	}
	return s, true
}

func writeCooldownForTest(t *testing.T, dataDir string, s cooldownState) {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "update_last_check.json"), b, 0o644); err != nil {
		t.Fatalf("write cooldown: %v", err)
	}
}

func TestCheck_SameHash_NoDownload(t *testing.T) {
	launches, _, exits, launcher := newTestContext(t)
	dataDir := t.TempDir()

	own := testExeHash16(t)
	srv, counters := newMockServer(t, map[string]any{
		"version":      own,
		"download_url": "/download.exe",
		"paused":       false,
	}, []byte("irrelevant"))

	err := Check(t.Context(), srv.URL, testSecret, dataDir, "test", slog.Default(), launcher)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if *launches != 0 {
		t.Fatalf("launcher should not run; called %d times", *launches)
	}
	if *exits != 0 {
		t.Fatalf("exitFn should not run; called %d times", *exits)
	}
	if counters.Version != 1 {
		t.Fatalf("expected 1 /version hit, got %d", counters.Version)
	}
	if counters.Download != 0 {
		t.Fatalf("expected 0 /download hits, got %d", counters.Download)
	}
	state, ok := readCooldownForTest(t, dataDir)
	if !ok {
		t.Fatalf("cooldown file not written")
	}
	if state.FailCount != 0 {
		t.Fatalf("expected FailCount=0, got %d", state.FailCount)
	}
	if state.Ts == 0 {
		t.Fatalf("expected Ts to be set")
	}
}

func TestCheck_DifferentHash_Match_LaunchesInstaller(t *testing.T) {
	launches, launchPath, exits, launcher := newTestContext(t)
	dataDir := t.TempDir()

	installerBytes := []byte("fresh-installer-bytes-xyz")
	installerHash := computeHash16(installerBytes)
	srv, counters := newMockServer(t, map[string]any{
		"version":      installerHash,
		"download_url": "/download.exe",
		"paused":       false,
	}, installerBytes)

	err := Check(t.Context(), srv.URL, testSecret, dataDir, "test", slog.Default(), launcher)
	if err != nil {
		t.Fatalf("Check returned err: %v", err)
	}
	if *launches != 1 {
		t.Fatalf("launcher calls: want 1, got %d", *launches)
	}
	if *launchPath == "" {
		t.Fatal("launcher path empty")
	}
	if filepath.Dir(*launchPath) != os.TempDir() {
		// Accept either TempDir equality or a longer temp path — some platforms
		// normalise symlinks. Just check the file exists at the launch path.
		if _, err := os.Stat(*launchPath); err != nil {
			t.Fatalf("launcher path %q not found: %v", *launchPath, err)
		}
	}
	if *exits != 1 {
		t.Fatalf("exitFn calls: want 1, got %d", *exits)
	}
	if counters.Version != 1 || counters.Download != 1 {
		t.Fatalf("expected 1 version + 1 download, got v=%d d=%d", counters.Version, counters.Download)
	}
	state, ok := readCooldownForTest(t, dataDir)
	if !ok {
		t.Fatal("cooldown not written")
	}
	if state.FailCount != 0 {
		t.Fatalf("FailCount should be 0 on success; got %d", state.FailCount)
	}
}

func TestCheck_DifferentHash_Mismatch_RejectsAndIncrementsFail(t *testing.T) {
	launches, _, exits, launcher := newTestContext(t)
	dataDir := t.TempDir()

	installerBytes := []byte("tampered-bytes-are-not-what-server-claims")
	srv, counters := newMockServer(t, map[string]any{
		"version":      "0000000000000000", // won't match installerBytes
		"download_url": "/download.exe",
		"paused":       false,
	}, installerBytes)

	err := Check(t.Context(), srv.URL, testSecret, dataDir, "test", slog.Default(), launcher)
	if err == nil {
		t.Fatal("expected error from hash mismatch; got nil")
	}
	if *launches != 0 {
		t.Fatalf("launcher should not run on mismatch; called %d", *launches)
	}
	if *exits != 0 {
		t.Fatalf("exitFn should not run on mismatch; called %d", *exits)
	}
	if counters.Version != 1 || counters.Download != 1 {
		t.Fatalf("expected 1 version + 1 download, got v=%d d=%d", counters.Version, counters.Download)
	}
	state, ok := readCooldownForTest(t, dataDir)
	if !ok {
		t.Fatal("cooldown not written")
	}
	if state.FailCount != 1 {
		t.Fatalf("FailCount should be 1 after mismatch; got %d", state.FailCount)
	}
}

func TestCheck_Paused_SkipsDownload(t *testing.T) {
	launches, _, exits, launcher := newTestContext(t)
	dataDir := t.TempDir()

	srv, counters := newMockServer(t, map[string]any{
		"version":      "differenthashxxx",
		"download_url": "/download.exe",
		"paused":       true,
	}, []byte("unused"))

	err := Check(t.Context(), srv.URL, testSecret, dataDir, "test", slog.Default(), launcher)
	if err != nil {
		t.Fatalf("Check returned err: %v", err)
	}
	if *launches != 0 || *exits != 0 {
		t.Fatalf("launcher=%d exits=%d; both should be 0", *launches, *exits)
	}
	if counters.Version != 1 {
		t.Fatalf("expected 1 /version hit, got %d", counters.Version)
	}
	if counters.Download != 0 {
		t.Fatalf("expected 0 /download hits when paused; got %d", counters.Download)
	}
	state, ok := readCooldownForTest(t, dataDir)
	if !ok {
		t.Fatal("cooldown not written")
	}
	if state.FailCount != 0 {
		t.Fatalf("FailCount should be 0 on paused; got %d", state.FailCount)
	}
}

func TestCheck_CooldownActive_BlocksPoll(t *testing.T) {
	launches, _, exits, launcher := newTestContext(t)
	dataDir := t.TempDir()

	// Cooldown active: recent ts, healthy fail_count.
	writeCooldownForTest(t, dataDir, cooldownState{
		Ts:        time.Now().Unix() - 60,
		FailCount: 0,
	})

	srv, counters := newMockServer(t, map[string]any{
		"version":      "does-not-matter",
		"download_url": "/download.exe",
		"paused":       false,
	}, []byte("unused"))

	err := Check(t.Context(), srv.URL, testSecret, dataDir, "test", slog.Default(), launcher)
	if err != nil {
		t.Fatalf("Check returned err: %v", err)
	}
	if counters.Version != 0 {
		t.Fatalf("expected 0 /version hits during cooldown, got %d", counters.Version)
	}
	if *launches != 0 || *exits != 0 {
		t.Fatalf("launcher=%d exits=%d; both should be 0", *launches, *exits)
	}
}

func TestCheck_FailLimit_BlocksEvenOffCooldown(t *testing.T) {
	launches, _, exits, launcher := newTestContext(t)
	dataDir := t.TempDir()

	// Cooldown expired (2h old) BUT fail_count at limit: still bail.
	writeCooldownForTest(t, dataDir, cooldownState{
		Ts:        time.Now().Unix() - 7200,
		FailCount: maxFailCount,
	})

	srv, counters := newMockServer(t, map[string]any{
		"version":      "does-not-matter",
		"download_url": "/download.exe",
		"paused":       false,
	}, []byte("unused"))

	err := Check(t.Context(), srv.URL, testSecret, dataDir, "test", slog.Default(), launcher)
	if err != nil {
		t.Fatalf("Check returned err: %v", err)
	}
	if counters.Version != 0 {
		t.Fatalf("expected 0 /version hits when fail_count at limit, got %d", counters.Version)
	}
	if *launches != 0 || *exits != 0 {
		t.Fatalf("launcher=%d exits=%d; both should be 0", *launches, *exits)
	}
}
