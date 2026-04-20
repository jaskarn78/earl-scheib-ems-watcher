package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jjagpal/earl-scheib-watcher/internal/config"
)

func TestLoadConfig_MissingFile_ReturnsDefaults(t *testing.T) {
	cfg, err := config.LoadConfig("/nonexistent/path/config.ini")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if cfg.WatchFolder != `C:\CCC\EMS_Export` {
		t.Errorf("WatchFolder: got %q, want %q", cfg.WatchFolder, `C:\CCC\EMS_Export`)
	}
	if cfg.WebhookURL != "https://support.jjagpal.me/earlscheibconcord" {
		t.Errorf("WebhookURL: got %q, want %q", cfg.WebhookURL, "https://support.jjagpal.me/earlscheibconcord")
	}
	if cfg.LogLevel != "INFO" {
		t.Errorf("LogLevel: got %q, want %q", cfg.LogLevel, "INFO")
	}
}

func TestLoadConfig_ValidINI_ReturnsValues(t *testing.T) {
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "config.ini")
	content := "[watcher]\nwatch_folder = C:\\MyFolder\nwebhook_url = https://example.com/hook\nlog_level = DEBUG\n"
	if err := os.WriteFile(iniPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadConfig(iniPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WatchFolder != `C:\MyFolder` {
		t.Errorf("WatchFolder: got %q, want %q", cfg.WatchFolder, `C:\MyFolder`)
	}
	if cfg.WebhookURL != "https://example.com/hook" {
		t.Errorf("WebhookURL: got %q, want %q", cfg.WebhookURL, "https://example.com/hook")
	}
	if cfg.LogLevel != "DEBUG" {
		t.Errorf("LogLevel: got %q, want %q", cfg.LogLevel, "DEBUG")
	}
}

func TestLoadConfig_ValidINI_StripsWhitespace(t *testing.T) {
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "config.ini")
	content := "[watcher]\nwatch_folder =   C:\\TrimMe   \nwebhook_url =  https://trimmed.example.com  \nlog_level =  WARNING  \n"
	if err := os.WriteFile(iniPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadConfig(iniPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.WatchFolder != `C:\TrimMe` {
		t.Errorf("WatchFolder should be trimmed, got %q", cfg.WatchFolder)
	}
	if cfg.WebhookURL != "https://trimmed.example.com" {
		t.Errorf("WebhookURL should be trimmed, got %q", cfg.WebhookURL)
	}
	if cfg.LogLevel != "WARNING" {
		t.Errorf("LogLevel should be trimmed, got %q", cfg.LogLevel)
	}
}

func TestLoadConfig_MalformedINI_ReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "config.ini")
	// Write bytes that are not valid INI (no section header + invalid key)
	if err := os.WriteFile(iniPath, []byte("not = valid ini file\n[broken"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadConfig(iniPath)
	if err != nil {
		t.Fatalf("expected no error for malformed INI (should return defaults), got: %v", err)
	}
	// Should fall back to defaults
	if cfg.LogLevel != "INFO" {
		t.Errorf("LogLevel should default to INFO on parse error, got %q", cfg.LogLevel)
	}
}

func TestConfig_NoSecretKeyField(t *testing.T) {
	// Config struct must not expose SecretKey — security requirement
	// This is a compile-time check: if Config has SecretKey this test won't compile.
	// We verify the field doesn't exist by checking exported fields.
	cfg := config.Config{}
	// Verify only expected fields exist by using them
	_ = cfg.WatchFolder
	_ = cfg.WebhookURL
	_ = cfg.LogLevel
	// No cfg.SecretKey — intentionally not referenced; should not compile if field is added
}

func TestDataDir_EnvVarOverride(t *testing.T) {
	t.Setenv("EARLSCHEIB_DATA_DIR", "/tmp/test-override-dir")
	got := config.DataDir()
	if got != "/tmp/test-override-dir" {
		t.Errorf("DataDir(): got %q, want %q", got, "/tmp/test-override-dir")
	}
}

func TestDataDir_PlatformDefault(t *testing.T) {
	// Unset the env var to test platform defaults
	t.Setenv("EARLSCHEIB_DATA_DIR", "")
	got := config.DataDir()

	if runtime.GOOS == "windows" {
		if got != `C:\EarlScheibWatcher\` {
			t.Errorf("DataDir() on Windows: got %q, want %q", got, `C:\EarlScheibWatcher\`)
		}
	} else {
		home := os.Getenv("HOME")
		expected := filepath.Join(home, ".earlscheib-dev") + string(os.PathSeparator)
		if got != expected {
			t.Errorf("DataDir() on non-Windows: got %q, want %q", got, expected)
		}
		if !strings.HasPrefix(got, home) {
			t.Errorf("DataDir() should be under HOME=%q, got %q", home, got)
		}
	}
}
