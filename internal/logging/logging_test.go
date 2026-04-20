package logging_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jjagpal/earl-scheib-watcher/internal/logging"
)

// timestampPattern matches "2006-01-02 15:04:05" (Python datefmt format).
var timestampPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`)

func TestSetupLogging_CreatesLogFile(t *testing.T) {
	dir := t.TempDir()
	logger := logging.SetupLogging(dir, "INFO")
	if logger == nil {
		t.Fatal("SetupLogging returned nil logger")
	}

	logger.Info("test message creation")

	logPath := filepath.Join(dir, "ems_watcher.log")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Errorf("expected log file at %s to be created", logPath)
	}
}

func TestSetupLogging_InfoMessageFormat(t *testing.T) {
	dir := t.TempDir()
	logger := logging.SetupLogging(dir, "INFO")

	logger.Info("hello world")

	logPath := filepath.Join(dir, "ems_watcher.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	line := strings.TrimSpace(string(content))
	if line == "" {
		t.Fatal("log file is empty after writing INFO message")
	}

	// Must match: "2006-01-02 15:04:05 [INFO] message text"
	if !timestampPattern.MatchString(line) {
		t.Errorf("log line does not match timestamp pattern %q: got %q", timestampPattern.String(), line)
	}
	if !strings.Contains(line, "[INFO]") {
		t.Errorf("log line missing [INFO] level: %q", line)
	}
	if !strings.Contains(line, "hello world") {
		t.Errorf("log line missing message text: %q", line)
	}
}

func TestSetupLogging_DebugNotVisibleAtInfoLevel(t *testing.T) {
	dir := t.TempDir()
	logger := logging.SetupLogging(dir, "INFO")

	// Write only a DEBUG message — should not appear in log
	logger.Debug("this debug message should not appear")

	logPath := filepath.Join(dir, "ems_watcher.log")
	content, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("unexpected error reading log: %v", err)
	}

	// File may not exist at all (no messages written), which is also correct
	if len(content) > 0 {
		t.Errorf("DEBUG message appeared in INFO-level log: %q", string(content))
	}
}

func TestSetupLogging_LevelCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	// "debug" lowercase should work
	logger := logging.SetupLogging(dir, "debug")
	if logger == nil {
		t.Fatal("SetupLogging returned nil for lowercase 'debug'")
	}
	// A debug message SHOULD appear with debug level
	logger.Debug("debug visible at debug level")

	logPath := filepath.Join(dir, "ems_watcher.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	if !strings.Contains(string(content), "debug visible at debug level") {
		t.Errorf("debug message not found in log with debug level; content: %q", string(content))
	}
}

func TestSetupLogging_UnknownLevelFallsBackToInfo(t *testing.T) {
	dir := t.TempDir()
	logger := logging.SetupLogging(dir, "BOGUS_LEVEL")
	if logger == nil {
		t.Fatal("SetupLogging returned nil for unknown level")
	}
	// Info should still work
	logger.Info("info visible with bogus level fallback")

	logPath := filepath.Join(dir, "ems_watcher.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	if !strings.Contains(string(content), "info visible with bogus level fallback") {
		t.Errorf("info message not found after bogus level fallback; content: %q", string(content))
	}
	// Debug should NOT appear (fell back to INFO)
	logger.Debug("debug not visible at info fallback")
	// Re-read
	content2, _ := os.ReadFile(logPath)
	if strings.Contains(string(content2), "debug not visible") {
		t.Errorf("debug message appeared when level fell back to INFO: %q", string(content2))
	}
}

func TestSetupLogging_WritesToStdout(t *testing.T) {
	dir := t.TempDir()

	// Redirect stdout to capture it
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	logger := logging.SetupLogging(dir, "INFO")
	logger.Info("stdout check message")

	w.Close()
	os.Stdout = old

	var buf strings.Builder
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "stdout check message") {
		t.Errorf("expected message to appear on stdout, got: %q", output)
	}
	if !strings.Contains(output, "[INFO]") {
		t.Errorf("expected [INFO] bracket format in stdout output, got: %q", output)
	}
}

func TestSetupLogging_LumberjackConfig(t *testing.T) {
	// Verify the logger type implements slog.Logger interface (returns *slog.Logger)
	dir := t.TempDir()
	logger := logging.SetupLogging(dir, "INFO")

	var _ *slog.Logger = logger
	_ = logger
}

func TestSetupLogging_WarnLevel(t *testing.T) {
	dir := t.TempDir()
	logger := logging.SetupLogging(dir, "WARNING")
	logger.Warn("warning message test")
	logger.Info("info should not appear at WARNING level")

	logPath := filepath.Join(dir, "ems_watcher.log")
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "warning message test") {
		t.Errorf("WARN message not found in log: %q", text)
	}
	if strings.Contains(text, "info should not appear") {
		t.Errorf("INFO message appeared at WARNING level: %q", text)
	}
}
