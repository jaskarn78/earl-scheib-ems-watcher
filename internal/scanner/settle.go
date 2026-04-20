// Package scanner implements the file scan loop: candidate listing, settle
// check, dedup, read, and sender hand-off. It is an exact port of
// _list_candidates, _wait_for_settle, and scan_and_send from ems_watcher.py.
package scanner

import (
	"io/fs"
	"os"
	"time"
)

// SettleOptions controls the settle check timing.
// Use {Samples: 4, Interval: 2*time.Second} for production (matching Python).
// Tests use {Samples: 4, Interval: 1*time.Millisecond} to avoid 8-second waits.
type SettleOptions struct {
	Samples  int           // SETTLE_CHECKS — number of stat samples (default 4)
	Interval time.Duration // SETTLE_INTERVAL — delay between samples (default 2s)
}

// DefaultSettleOptions matches the Python reference constants:
// SETTLE_CHECKS = 4, SETTLE_INTERVAL = 2.0 seconds.
var DefaultSettleOptions = SettleOptions{Samples: 4, Interval: 2 * time.Second}

// SettleCheck polls path at opts.Interval for opts.Samples iterations.
// It returns (info, true) when mtime+size are identical for 2 consecutive
// samples (stable_count >= 2), matching _wait_for_settle in ems_watcher.py.
// Returns (nil, false) if the file changes or becomes inaccessible.
//
// The log func is injected so callers can pass slog's Info/Debug/Warn method
// or t.Logf in tests.
func SettleCheck(path string, opts SettleOptions, log func(msg string, args ...any)) (fs.FileInfo, bool) {
	prev, err := os.Stat(path)
	if err != nil {
		log("stat failed", "path", path, "err", err)
		return nil, false
	}

	stableCount := 0
	for i := 0; i < opts.Samples; i++ {
		time.Sleep(opts.Interval)
		cur, err := os.Stat(path)
		if err != nil {
			log("stat failed during settle", "path", path, "err", err)
			return nil, false
		}
		if cur.ModTime().Equal(prev.ModTime()) && cur.Size() == prev.Size() {
			stableCount++
			if stableCount >= 2 {
				return cur, true
			}
		} else {
			stableCount = 0
			prev = cur
		}
	}
	return nil, false
}
