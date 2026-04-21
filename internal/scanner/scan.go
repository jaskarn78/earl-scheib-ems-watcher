package scanner

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jjagpal/earl-scheib-watcher/internal/db"
)

// RunConfig holds the dependencies for a single scan run.
// Sender and SettleOpts are injectable for unit tests.
type RunConfig struct {
	WatchFolder string
	// WebhookURL is logged in the per-cycle "scan start" INFO line so ops can
	// grep a single log line to see which webhook the client was pointed at
	// on any given scan. Empty string is acceptable (rendered as empty).
	WebhookURL string
	// AppVersion is logged in the per-cycle "scan start" INFO line so a log
	// tail immediately reveals which binary version produced it. Empty string
	// is acceptable (rendered as empty).
	AppVersion string
	DB         *sql.DB
	Logger     *slog.Logger
	// Sender is called with (filePath, raw XML bytes); returns true on success.
	// In production use webhook.Send. In tests, provide a mock.
	Sender     func(filePath string, body []byte) bool
	SettleOpts SettleOptions // zero value → DefaultSettleOptions used
}

// Candidates returns the full paths of .xml and .ems files in dir
// (case-insensitive extension match). On any OS or permission error,
// it logs a warning and returns an empty slice without panicking.
// This matches _list_candidates() in ems_watcher.py.
func Candidates(dir string, logger *slog.Logger) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// "path" (not "dir") is the contract ops rely on to grep for the bad
		// folder; "err" carries the verbatim OS error (ENOENT / permission /
		// network-share timeout) so Marco's 5-minute cycles produce actionable
		// log lines without a round-trip.
		logger.Warn("Cannot read watch folder", "path", dir, "err", err)
		return []string{}
	}

	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext == ".xml" || ext == ".ems" {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// Run performs a single scan cycle: list candidates, dedup, settle, re-dedup,
// read bytes, sha256, send, mark processed, record run. Returns (processed, errors).
// This matches scan_and_send() in ems_watcher.py.
func Run(cfg RunConfig) (int, int) {
	opts := cfg.SettleOpts
	if opts.Samples == 0 {
		opts = DefaultSettleOptions
	}

	logger := cfg.Logger

	// Per-cycle startup line: one record with the three values ops most
	// commonly needs when debugging live (which folder, which webhook, which
	// binary). Emitted BEFORE the folder probe so it fires even when the
	// scan bails out on a missing directory.
	logger.Info("scan start",
		"watch_folder", cfg.WatchFolder,
		"webhook", cfg.WebhookURL,
		"version", cfg.AppVersion,
	)

	candidates := Candidates(cfg.WatchFolder, logger)
	if len(candidates) == 0 {
		logger.Debug("No .xml/.ems files found", "folder", cfg.WatchFolder)
		_ = db.RecordRun(cfg.DB, 0, 0, "no files")
		return 0, 0
	}

	processed := 0
	errors := 0

	for _, fpath := range candidates {
		stat, err := os.Stat(fpath)
		if err != nil {
			logger.Debug("stat failed", "file", filepath.Base(fpath), "err", err)
			continue
		}

		mtime := float64(stat.ModTime().UnixNano()) / 1e9

		// Pre-settle dedup check.
		already, err := db.IsProcessed(cfg.DB, fpath, mtime)
		if err != nil {
			logger.Error("IsProcessed failed", "file", filepath.Base(fpath), "err", err)
		}
		if already {
			logger.Debug("Already processed", "file", filepath.Base(fpath), "mtime", mtime)
			continue
		}

		logger.Info("Detected: "+filepath.Base(fpath)+" — checking for write-settle")

		// Settle check.
		logFn := func(msg string, args ...any) {
			logger.Debug(msg, args...)
		}
		settledInfo, settled := SettleCheck(fpath, opts, logFn)
		if !settled || settledInfo == nil {
			logger.Info("File not settled — will retry next cycle", "file", filepath.Base(fpath))
			continue
		}

		// Re-read mtime from post-settle stat (mtime may have changed during settling).
		mtime = float64(settledInfo.ModTime().UnixNano()) / 1e9
		size := settledInfo.Size()

		// Post-settle dedup check (re-check with updated mtime).
		already, err = db.IsProcessed(cfg.DB, fpath, mtime)
		if err != nil {
			logger.Error("IsProcessed (post-settle) failed", "file", filepath.Base(fpath), "err", err)
		}
		if already {
			logger.Debug("Already processed (post-settle)", "file", filepath.Base(fpath))
			continue
		}

		// Read bytes — log filename + size only (PII protection: no XML body logged).
		xmlBytes, err := os.ReadFile(fpath)
		if err != nil {
			logger.Error("Cannot read file", "file", filepath.Base(fpath), "size", size, "err", err)
			errors++
			continue
		}

		// Compute SHA-256 over raw bytes.
		sum := sha256.Sum256(xmlBytes)
		shaHex := hex.EncodeToString(sum[:])

		// Send via injected Sender.
		if cfg.Sender(fpath, xmlBytes) {
			if mErr := db.MarkProcessed(cfg.DB, fpath, mtime, size, shaHex); mErr != nil {
				logger.Error("MarkProcessed failed", "file", filepath.Base(fpath), "err", mErr)
				errors++
				continue
			}
			processed++
		} else {
			errors++
		}
	}

	note := "scan of " + cfg.WatchFolder + " (" + itoa(len(candidates)) + " candidates)"
	_ = db.RecordRun(cfg.DB, processed, errors, note)
	return processed, errors
}

// itoa converts an int to its decimal string representation without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := [20]byte{}
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
