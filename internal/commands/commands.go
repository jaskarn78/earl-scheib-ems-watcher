// Package commands polls the webhook server for operator-initiated actions
// and executes them. Designed for NAT-traversal: the client polls (we can't
// push to Marco's PC from outside), and acts on whatever command appears.
//
// Supported command today:
//   upload_log: read the last N lines of ems_watcher.log and POST to /logs.
//
// Poll contract:
//   GET {webhookURL}/commands   with HMAC-signed empty body
//   → 200 + JSON map of commands, or 204 if nothing pending.
//
// Upload contract:
//   POST {webhookURL}/logs      with HMAC-signed body
//   body = {"host": "<win_host>", "log": "<tail of ems_watcher.log>"}
package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jjagpal/earl-scheib-watcher/internal/webhook"
)

// LogTailLines is how many trailing lines of ems_watcher.log to upload.
// ~500 lines ≈ a few hours of --scan runs at INFO level. Bump via remote-config
// later if we need more.
const LogTailLines = 500

// Poll fetches pending commands from the server. Returns nil map on 204 / error.
// Never blocks the scan — 5-second HTTP timeout, all errors logged at debug.
func Poll(ctx context.Context, webhookURL, secret string, logger *slog.Logger) map[string]any {
	url := strings.TrimRight(webhookURL, "/") + "/commands"
	sig := webhook.Sign(secret, []byte(""))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("X-EMS-Signature", sig)
	req.Header.Set("X-EMS-Source", "EarlScheibWatcher")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if logger != nil {
			logger.Debug("commands.Poll transport error", "err", err)
		}
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		if logger != nil {
			logger.Debug("commands.Poll unexpected status", "status", resp.StatusCode)
		}
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	var cmds map[string]any
	if err := json.Unmarshal(body, &cmds); err != nil {
		if logger != nil {
			logger.Debug("commands.Poll json parse failed", "err", err)
		}
		return nil
	}
	return cmds
}

// Handle executes any commands in the poll result.
// Returns count of commands handled (0 if none).
func Handle(ctx context.Context, cmds map[string]any, webhookURL, secret, dataDir, hostName string, logger *slog.Logger) int {
	if cmds == nil {
		return 0
	}
	handled := 0
	if v, ok := cmds["upload_log"].(bool); ok && v {
		if err := uploadLogTail(ctx, webhookURL, secret, dataDir, hostName, logger); err != nil {
			if logger != nil {
				logger.Warn("log tail upload failed", "err", err)
			}
		} else {
			handled++
			if logger != nil {
				logger.Info("log tail uploaded to server")
			}
		}
	}
	return handled
}

// uploadLogTail reads the last LogTailLines of ems_watcher.log and POSTs to /logs.
func uploadLogTail(ctx context.Context, webhookURL, secret, dataDir, hostName string, logger *slog.Logger) error {
	logPath := filepath.Join(dataDir, "ems_watcher.log")
	tail, err := readTail(logPath, LogTailLines)
	if err != nil {
		return fmt.Errorf("read tail: %w", err)
	}

	body, err := json.Marshal(map[string]string{
		"host": hostName,
		"log":  tail,
	})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	sig := webhook.Sign(secret, body)

	url := strings.TrimRight(webhookURL, "/") + "/logs"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-EMS-Signature", sig)
	req.Header.Set("X-EMS-Source", "EarlScheibWatcher")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("non-2xx %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// readTail returns the last n lines of a text file. Opens + reads whole file
// (we're talking about <10 MB of log; lumberjack rotates at 2 MB). For larger
// logs this would need a reverse-chunk read.
func readTail(path string, n int) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "(log file does not exist yet)", nil
		}
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) <= n {
		return string(data), nil
	}
	return strings.Join(lines[len(lines)-n:], "\n"), nil
}
