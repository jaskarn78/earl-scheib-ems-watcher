// Package status provides the --status output for the EMS Watcher CLI.
// Port of run_status() from ems_watcher.py (lines 463–556).
package status

import (
	"bufio"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jjagpal/earl-scheib-watcher/internal/config"
)

// Print writes --status output to w (use os.Stdout in production).
// Port of run_status() from ems_watcher.py.
//
// If sqlDB is nil, Print prints the header block and "No database yet" message only.
// All filesystem and database errors are caught and printed inline (no panic).
func Print(cfg config.Config, dataDir string, sqlDB *sql.DB, logger *slog.Logger, w io.Writer) {
	configFile := filepath.Join(dataDir, "config.ini")
	dbFile := filepath.Join(dataDir, "ems_watcher.db")
	logFile := filepath.Join(dataDir, "ems_watcher.log")

	// Header block (matches Python run_status lines 464–470)
	fmt.Fprintf(w, "EMS Watcher status — Earl Scheib Auto Body Concord\n")
	fmt.Fprintf(w, "%s\n", strings.Repeat("=", 56))
	fmt.Fprintf(w, "Watch folder : %s\n", cfg.WatchFolder)
	fmt.Fprintf(w, "Webhook URL  : %s\n", cfg.WebhookURL)
	fmt.Fprintf(w, "Config file  : %s\n", configFile)
	fmt.Fprintf(w, "Database     : %s\n", dbFile)
	fmt.Fprintf(w, "Log file     : %s\n", logFile)
	fmt.Fprintf(w, "\n")

	// Watch folder reachability (Python lines 473–489)
	_, statErr := os.Stat(cfg.WatchFolder)
	reachable := statErr == nil
	if !reachable && statErr != nil && !os.IsNotExist(statErr) {
		fmt.Fprintf(w, "Watch folder check error: %v\n", statErr)
	}
	if reachable {
		fmt.Fprintf(w, "Watch folder reachable : YES\n")
		// Count .xml and .ems files
		entries, readErr := os.ReadDir(cfg.WatchFolder)
		if readErr != nil {
			fmt.Fprintf(w, "Folder iteration error: %v\n", readErr)
		} else {
			count := 0
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				ext := strings.ToLower(filepath.Ext(e.Name()))
				if ext == ".xml" || ext == ".ems" {
					count++
				}
			}
			fmt.Fprintf(w, "Files currently present: %d\n", count)
		}
	} else {
		fmt.Fprintf(w, "Watch folder reachable : NO\n")
	}

	// DB stats — if sqlDB is nil, no DB exists yet
	if sqlDB == nil {
		fmt.Fprintf(w, "\nNo database yet — watcher has not run.\n")
		return
	}

	// Last run info (Python lines 499–512)
	var runAt, note string
	var runProcessed, runErrors int
	rowErr := sqlDB.QueryRow(
		"SELECT run_at, processed, errors, note FROM runs ORDER BY rowid DESC LIMIT 1",
	).Scan(&runAt, &runProcessed, &runErrors, &note)

	if rowErr == nil {
		fmt.Fprintf(w, "\nLast run        : %s UTC\n", runAt)
		fmt.Fprintf(w, "  processed     : %d\n", runProcessed)
		fmt.Fprintf(w, "  errors        : %d\n", runErrors)
		fmt.Fprintf(w, "  note          : %s\n", note)
	} else if rowErr == sql.ErrNoRows {
		fmt.Fprintf(w, "\nNo run history recorded yet.\n")
	} else {
		fmt.Fprintf(w, "\nCould not read run history: %v\n", rowErr)
	}

	// Today/total counts (Python lines 514–525)
	var todayCount, totalCount int
	todayErr := sqlDB.QueryRow(
		"SELECT COUNT(*) FROM processed_files WHERE date(sent_at) = date('now')",
	).Scan(&todayCount)
	if todayErr == nil {
		fmt.Fprintf(w, "Files sent today (UTC): %d\n", todayCount)
	}
	totalErr := sqlDB.QueryRow(
		"SELECT COUNT(*) FROM processed_files",
	).Scan(&totalCount)
	if totalErr == nil {
		fmt.Fprintf(w, "Files sent total      : %d\n", totalCount)
	}

	// Recent 5 files (Python lines 527–537)
	rows, queryErr := sqlDB.Query(
		"SELECT filepath, sent_at FROM processed_files ORDER BY sent_at DESC LIMIT 5",
	)
	if queryErr == nil {
		defer rows.Close()
		hasRecent := false
		for rows.Next() {
			var fp, sentAt string
			if scanErr := rows.Scan(&fp, &sentAt); scanErr == nil {
				if !hasRecent {
					fmt.Fprintf(w, "\nRecent files:\n")
					hasRecent = true
				}
				fmt.Fprintf(w, "  %s  %s\n", sentAt, filepath.Base(fp))
			}
		}
	}

	// Log tail — last 10 lines containing [ERROR] or [WARNING] (Python lines 541–555)
	logData, readErr := os.ReadFile(logFile)
	if readErr == nil {
		fmt.Fprintf(w, "\nRecent log errors (last 10):\n")
		scanner := bufio.NewScanner(strings.NewReader(string(logData)))
		var errLines []string
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "[ERROR]") || strings.Contains(line, "[WARNING]") {
				errLines = append(errLines, line)
			}
		}
		// Print last 10
		start := 0
		if len(errLines) > 10 {
			start = len(errLines) - 10
		}
		for _, line := range errLines[start:] {
			fmt.Fprintf(w, "  %s\n", line)
		}
		if len(errLines) == 0 {
			fmt.Fprintf(w, "  (none)\n")
		}
	}
}
