// Package config loads the [watcher] INI section and resolves the data directory.
// The secret key is intentionally absent from Config — it is baked into the binary
// via build-time ldflags (SCAF-04) and never stored in config files.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/ini.v1"
)

// Config holds the configuration values parsed from [watcher] section.
// SecretKey is deliberately excluded — it is injected at build time via ldflags.
type Config struct {
	WatchFolder string
	WebhookURL  string
	LogLevel    string
}

// defaults mirrors the Python DEFAULTS dict in ems_watcher.py exactly.
var defaults = Config{
	WatchFolder: `C:\CCC\EMS_Export`,
	WebhookURL:  "https://support.jjagpal.me/earlscheibconcord",
	LogLevel:    "INFO",
}

// LoadConfig reads the [watcher] section from the INI file at path.
// If path does not exist, defaults are returned without error (matching Python behaviour).
// If the file is malformed, a warning is written to stderr and defaults are returned.
func LoadConfig(path string) (Config, error) {
	cfg := defaults

	// If file does not exist, return defaults silently (matches Python load_config behaviour).
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}

	f, err := ini.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: config.ini parse error: %v\n", err)
		return cfg, nil
	}

	section, sErr := f.GetSection("watcher")
	if sErr != nil {
		// No [watcher] section — return defaults (no error).
		return cfg, nil
	}

	if key, kErr := section.GetKey("watch_folder"); kErr == nil {
		cfg.WatchFolder = strings.TrimSpace(key.Value())
	}
	if key, kErr := section.GetKey("webhook_url"); kErr == nil {
		cfg.WebhookURL = strings.TrimSpace(key.Value())
	}
	if key, kErr := section.GetKey("log_level"); kErr == nil {
		cfg.LogLevel = strings.TrimSpace(key.Value())
	}

	return cfg, nil
}

// DataDir returns the directory where program data (DB, log file, config) lives.
// Priority: EARLSCHEIB_DATA_DIR env var > platform default.
//   - Windows: C:\EarlScheibWatcher\
//   - Non-Windows: $HOME/.earlscheib-dev/
func DataDir() string {
	if override := os.Getenv("EARLSCHEIB_DATA_DIR"); override != "" {
		return override
	}
	if runtime.GOOS == "windows" {
		return `C:\EarlScheibWatcher\`
	}
	return filepath.Join(os.Getenv("HOME"), ".earlscheib-dev") + string(os.PathSeparator)
}
