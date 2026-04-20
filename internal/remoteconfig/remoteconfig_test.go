package remoteconfig_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jjagpal/earl-scheib-watcher/internal/config"
	"github.com/jjagpal/earl-scheib-watcher/internal/remoteconfig"
	"github.com/jjagpal/earl-scheib-watcher/internal/webhook"
)

// ---------- config.Merge tests ----------

func TestMerge_UpdatesAllowedKey(t *testing.T) {
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "config.ini")
	// seed with a valid config
	seed := "[watcher]\nwatch_folder = C:\\My\\Folder\nwebhook_url = https://old.example.com\nlog_level = INFO\n"
	if err := os.WriteFile(iniPath, []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}

	changed, err := config.Merge(iniPath, map[string]string{"webhook_url": "https://new.example.com"}, []string{"webhook_url", "log_level"})
	if err != nil {
		t.Fatalf("Merge: unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("Merge: expected changed=true, got false")
	}

	// Reload and verify webhook_url was updated
	cfg, _ := config.LoadConfig(iniPath)
	if cfg.WebhookURL != "https://new.example.com" {
		t.Errorf("WebhookURL: got %q, want %q", cfg.WebhookURL, "https://new.example.com")
	}
	// watch_folder must be unchanged (AllowedKeys does not include it)
	if cfg.WatchFolder != `C:\My\Folder` {
		t.Errorf("WatchFolder should be unchanged, got %q", cfg.WatchFolder)
	}
}

func TestMerge_BlacklistedKeysNotWritten(t *testing.T) {
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "config.ini")
	seed := "[watcher]\nwatch_folder = C:\\Safe\\Folder\nwebhook_url = https://good.example.com\nlog_level = INFO\n"
	if err := os.WriteFile(iniPath, []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}

	// Try to inject secret_key and watch_folder — both are NOT in allowed
	changed, err := config.Merge(iniPath,
		map[string]string{"secret_key": "evil-secret", "watch_folder": "/evil/path"},
		[]string{"webhook_url", "log_level"},
	)
	if err != nil {
		t.Fatalf("Merge: unexpected error: %v", err)
	}
	if changed {
		t.Fatal("Merge: expected changed=false when only blacklisted keys supplied")
	}

	// Verify nothing changed on disk
	cfg, _ := config.LoadConfig(iniPath)
	if cfg.WatchFolder != `C:\Safe\Folder` {
		t.Errorf("WatchFolder must not be overwritten, got %q", cfg.WatchFolder)
	}
}

func TestMerge_EmptyRemote_NoWrite(t *testing.T) {
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "config.ini")
	seed := "[watcher]\nwebhook_url = https://original.example.com\n"
	if err := os.WriteFile(iniPath, []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}

	changed, err := config.Merge(iniPath, map[string]string{}, []string{"webhook_url", "log_level"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if changed {
		t.Fatal("Merge with empty map should return changed=false")
	}
}

func TestMerge_AtomicWrite(t *testing.T) {
	// Verify no temp files remain after a successful Merge.
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "config.ini")
	seed := "[watcher]\nwebhook_url = https://old.example.com\n"
	if err := os.WriteFile(iniPath, []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := config.Merge(iniPath, map[string]string{"log_level": "DEBUG"}, []string{"webhook_url", "log_level"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No .tmp files should remain
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file not cleaned up: %s", e.Name())
		}
	}

	// config.ini should exist and be valid
	if _, statErr := os.Stat(iniPath); statErr != nil {
		t.Errorf("config.ini should exist after merge: %v", statErr)
	}
}

// ---------- remoteconfig.Fetch tests ----------

func TestFetch_SuccessfulResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"webhook_url": "https://new.url",
			"log_level":   "DEBUG",
		})
	}))
	defer srv.Close()

	result, err := remoteconfig.Fetch(context.Background(), srv.URL, "test-secret", nil)
	if err != nil {
		t.Fatalf("Fetch: unexpected error: %v", err)
	}
	if result["webhook_url"] != "https://new.url" {
		t.Errorf("webhook_url: got %q", result["webhook_url"])
	}
	if result["log_level"] != "DEBUG" {
		t.Errorf("log_level: got %q", result["log_level"])
	}
}

func TestFetch_EmptyJSONObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	result, err := remoteconfig.Fetch(context.Background(), srv.URL, "test-secret", nil)
	if err != nil {
		t.Fatalf("Fetch: unexpected error for empty JSON: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}

func TestFetch_404ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	result, err := remoteconfig.Fetch(context.Background(), srv.URL, "test-secret", nil)
	if err == nil {
		t.Fatal("Fetch: expected error for 404, got nil")
	}
	if result != nil {
		t.Errorf("Fetch: expected nil map on error, got %v", result)
	}
}

func TestFetch_HMACSignatureHeader(t *testing.T) {
	const secret = "my-secret"
	expectedSig := webhook.Sign(secret, []byte(""))

	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-EMS-Signature")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	_, err := remoteconfig.Fetch(context.Background(), srv.URL, secret, nil)
	if err != nil {
		t.Fatalf("Fetch: unexpected error: %v", err)
	}
	if gotSig != expectedSig {
		t.Errorf("X-EMS-Signature: got %q, want %q", gotSig, expectedSig)
	}
}

func TestFetch_TimeoutEnforced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the 5s Fetch timeout — will be cut off
		time.Sleep(7 * time.Second)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	_, err := remoteconfig.Fetch(ctx, srv.URL, "secret", nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Fetch: expected timeout error, got nil")
	}
	if elapsed >= 6*time.Second {
		t.Errorf("Fetch should have timed out in < 6s, took %v", elapsed)
	}
}

// ---------- remoteconfig.Apply tests ----------

func TestApply_OnlyAllowedKeysWritten(t *testing.T) {
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "config.ini")
	seed := "[watcher]\nwebhook_url = https://old.example.com\nwatch_folder = C:\\Safe\\Folder\n"
	if err := os.WriteFile(iniPath, []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}

	changed, err := remoteconfig.Apply(iniPath, map[string]string{
		"webhook_url": "https://new.example.com",
		"secret_key":  "should-be-dropped",
	}, nil)
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if !changed {
		t.Fatal("Apply: expected changed=true")
	}

	cfg, _ := config.LoadConfig(iniPath)
	if cfg.WebhookURL != "https://new.example.com" {
		t.Errorf("WebhookURL: got %q, want https://new.example.com", cfg.WebhookURL)
	}
	// secret_key must never appear in a loaded Config (it's not a Config field).
	// Verify watch_folder unchanged (not in AllowedKeys for Apply).
	if cfg.WatchFolder != `C:\Safe\Folder` {
		t.Errorf("WatchFolder should be unchanged, got %q", cfg.WatchFolder)
	}
}

func TestApply_EmptyRemote_NothingWritten(t *testing.T) {
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "config.ini")
	seed := "[watcher]\nwebhook_url = https://original.example.com\n"
	if err := os.WriteFile(iniPath, []byte(seed), 0600); err != nil {
		t.Fatal(err)
	}

	changed, err := remoteconfig.Apply(iniPath, map[string]string{}, nil)
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if changed {
		t.Fatal("Apply: expected changed=false for empty remote map")
	}
}

func TestAllowedKeys_Contents(t *testing.T) {
	// AllowedKeys must contain exactly webhook_url and log_level.
	// secret_key must NOT be present.
	allowed := remoteconfig.AllowedKeys
	allowedSet := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		allowedSet[k] = true
	}

	if !allowedSet["webhook_url"] {
		t.Error("AllowedKeys must contain webhook_url")
	}
	if !allowedSet["log_level"] {
		t.Error("AllowedKeys must contain log_level")
	}
	if allowedSet["secret_key"] {
		t.Error("AllowedKeys must NOT contain secret_key (OPS-04)")
	}
	if allowedSet["watch_folder"] {
		t.Error("AllowedKeys must NOT contain watch_folder (OPS-04)")
	}
	if len(allowed) != 2 {
		t.Errorf("AllowedKeys should have exactly 2 entries, got %d: %v", len(allowed), allowed)
	}
}
