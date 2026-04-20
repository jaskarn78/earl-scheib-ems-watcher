// Package logging configures slog with a rotating file handler and console output,
// matching the Python ems_watcher.py setup_logging() behaviour exactly:
//   - 2 MB × 5 backup rotation via lumberjack (same as RotatingFileHandler)
//   - UTC timestamps in "2006-01-02 15:04:05" format (same as Python datefmt)
//   - Format: "YYYY-MM-DD HH:MM:SS [LEVEL] message"
//   - Output to both rotating file AND os.Stdout (matches Python StreamHandler)
//   - Unknown log level falls back to INFO
//
// BMS XML content is NEVER logged through this package — callers must log only
// filename and byte size (PII protection, SCAN-14).
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

// emsHandler is a custom slog.Handler that formats log lines as:
//
//	"2026-04-20 14:32:01 [INFO] message text here"
//
// The format matches Python's: "%(asctime)s [%(levelname)s] %(message)s"
// with datefmt="%Y-%m-%d %H:%M:%S" and UTC timestamps.
type emsHandler struct {
	w     io.Writer
	level slog.Level
}

// Enabled returns true if the record's level is at or above the configured level.
func (h *emsHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

// Handle formats and writes the log record.
func (h *emsHandler) Handle(_ context.Context, r slog.Record) error {
	levelStr := levelLabel(r.Level)
	timestamp := r.Time.UTC().Format("2006-01-02 15:04:05")
	line := fmt.Sprintf("%s [%s] %s\n", timestamp, levelStr, r.Message)
	_, err := fmt.Fprint(h.w, line)
	return err
}

// WithAttrs returns a new handler with added attributes (no-op for our minimal format).
func (h *emsHandler) WithAttrs(_ []slog.Attr) slog.Handler {
	return h
}

// WithGroup returns a new handler with a group prefix (no-op for our minimal format).
func (h *emsHandler) WithGroup(_ string) slog.Handler {
	return h
}

// levelLabel maps slog levels to Python-compatible bracket label strings.
// Python uses "INFO", "DEBUG", "WARNING", "ERROR" — we match these exactly.
func levelLabel(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARNING"
	case l >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}

// parseLevel converts a string level name to slog.Level.
// Matches Python: "DEBUG", "INFO", "WARNING"/"WARN", "ERROR".
// Unknown values fall back to INFO.
func parseLevel(level string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO":
		return slog.LevelInfo
	case "WARNING", "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// SetupLogging configures slog with:
//   - A lumberjack rotating file at {dataDir}/ems_watcher.log (2 MB, 5 backups)
//   - A console handler writing to os.Stdout
//   - Custom line format matching Python: "YYYY-MM-DD HH:MM:SS [LEVEL] message"
//   - UTC timestamps
//
// Parameters:
//   - dataDir: directory where ems_watcher.log is created
//   - level: log level string ("DEBUG", "INFO", "WARNING", "ERROR"); unknown → INFO
func SetupLogging(dataDir string, level string) *slog.Logger {
	logPath := filepath.Join(dataDir, "ems_watcher.log")

	jack := &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    2, // megabytes — matches Python maxBytes=2*1024*1024
		MaxBackups: 5, // matches Python backupCount=5
		Compress:   false,
	}

	// Write to both rotating file and stdout (matches Python StreamHandler + FileHandler)
	multi := io.MultiWriter(jack, os.Stdout)

	parsedLevel := parseLevel(level)
	handler := &emsHandler{
		w:     multi,
		level: parsedLevel,
	}

	return slog.New(handler)
}

// ensure emsHandler satisfies the slog.Handler interface at compile time.
var _ slog.Handler = (*emsHandler)(nil)
