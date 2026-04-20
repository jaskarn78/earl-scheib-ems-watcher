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

// Merge atomically patches the INI file at path with values from remote,
// writing ONLY keys present in the allowed slice. Keys not in allowed are
// silently discarded (OPS-04 whitelist enforcement).
// Uses temp-file + rename for atomicity so a crash mid-write cannot corrupt config.ini.
// Returns changed=true if at least one key was written.
func Merge(path string, remote map[string]string, allowed []string) (changed bool, err error) {
	if len(remote) == 0 {
		return false, nil
	}

	// Build allowSet for O(1) lookup.
	allowSet := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		allowSet[k] = true
	}

	// Load existing file (or start with defaults if absent).
	var f *ini.File
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		f = ini.Empty()
	} else {
		f, err = ini.Load(path)
		if err != nil {
			return false, fmt.Errorf("config.Merge load: %w", err)
		}
	}

	section, sErr := f.GetSection("watcher")
	if sErr != nil {
		// Create the section if it doesn't exist.
		section, err = f.NewSection("watcher")
		if err != nil {
			return false, fmt.Errorf("config.Merge new section: %w", err)
		}
	}

	for k, v := range remote {
		if !allowSet[k] {
			continue // OPS-04: never write non-whitelisted keys
		}
		// Update existing key or create new one.
		if section.HasKey(k) {
			section.Key(k).SetValue(v)
		} else {
			if _, newKeyErr := section.NewKey(k, v); newKeyErr != nil {
				return false, fmt.Errorf("config.Merge new key %q: %w", k, newKeyErr)
			}
		}
		changed = true
	}

	if !changed {
		return false, nil
	}

	// Atomic write: write to temp file in same dir, then rename.
	dir := filepath.Dir(path)
	tmp, tmpErr := os.CreateTemp(dir, "config-*.ini.tmp")
	if tmpErr != nil {
		return false, fmt.Errorf("config.Merge tempfile: %w", tmpErr)
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			os.Remove(tmpPath) // clean up on failure
		}
	}()

	if _, writeErr := f.WriteTo(tmp); writeErr != nil {
		tmp.Close()
		err = fmt.Errorf("config.Merge write: %w", writeErr)
		return false, err
	}
	if closeErr := tmp.Close(); closeErr != nil {
		err = fmt.Errorf("config.Merge close: %w", closeErr)
		return false, err
	}
	if renameErr := os.Rename(tmpPath, path); renameErr != nil {
		err = fmt.Errorf("config.Merge rename: %w", renameErr)
		return false, err
	}
	return true, nil
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
