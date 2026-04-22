// Package update implements the client-side half of the self-update mechanism.
//
// Flow (per scan):
//  1. Cooldown/fail-limit gate (read <dataDir>/update_last_check.json)
//  2. HMAC-signed GET {webhook}/version
//  3. If server-version == SHA256(os.Executable())[:16] → no-op (reset fail_count)
//  4. Else download installer, verify its SHA256[:16] matches server version
//  5. Launch installer via `/VERYSILENT /NORESTART /SUPPRESSMSGBOXES /SP-` and os.Exit(0)
//
// The caller (runScan) treats any returned error as non-fatal: self-update
// must never block the scan cycle.
//
// Design notes:
//   - Pure stdlib — no new go.mod deps.
//   - launcher is injected so tests never exec a real installer.
//   - exitFn + sleepFn are unexported package-level vars (test-speed pattern
//     already established in webhook.BackoffBase / db.RetryBaseDelay).
//   - Non-windows GOOS is a no-op unless UPDATE_TEST_FORCE=1 (for linux test runs).
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jjagpal/earl-scheib-watcher/internal/webhook"
)

const (
	cooldownSeconds = 3600
	maxFailCount    = 3
	pollTimeout     = 5 * time.Second
	downloadTimeout = 60 * time.Second
	exitSleep       = 500 * time.Millisecond
)

// exitFn and sleepFn are package-level so tests can stub them without
// changing the public Check signature. Production: os.Exit + time.Sleep.
var (
	exitFn  = os.Exit
	sleepFn = time.Sleep
)

// cooldownState is persisted at <dataDir>/update_last_check.json between scans.
// Ts is Unix seconds of the last poll (successful or otherwise).
// FailCount increments on every failure (HTTP error, hash mismatch, download
// error). Resets to 0 on any successful poll, whether or not an update was
// applied.
type cooldownState struct {
	Ts        int64 `json:"ts"`
	FailCount int   `json:"fail_count"`
}

// versionResponse mirrors the JSON shape returned by /earlscheibconcord/version.
type versionResponse struct {
	Version     string `json:"version"`
	DownloadURL string `json:"download_url"`
	Paused      bool   `json:"paused"`
}

// DefaultLauncher spawns the installer in silent mode and returns immediately.
// Intended for production; tests pass a stub.
func DefaultLauncher(installerPath string) error {
	cmd := exec.Command(installerPath,
		"/VERYSILENT",
		"/NORESTART",
		"/SUPPRESSMSGBOXES",
		"/SP-",
	)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start installer: %w", err)
	}
	// Release the child so our exit doesn't kill it.
	return cmd.Process.Release()
}

// Check performs one self-update poll cycle. Idempotent, best-effort.
// Caller should log any returned error and continue the scan.
//
// If the update is applied successfully, Check calls exitFn(0) after launcher
// returns + a short sleep so the spawned installer can overwrite the running
// exe. In production that is os.Exit; the Scheduled Task will re-launch with
// the new binary on the next cadence.
func Check(
	ctx context.Context,
	webhookURL, secret, dataDir, appVersion string,
	logger *slog.Logger,
	launcher func(installerPath string) error,
) error {
	// Platform gate: windows only, unless tests force it.
	if runtime.GOOS != "windows" && os.Getenv("UPDATE_TEST_FORCE") != "1" {
		return nil
	}

	statePath := filepath.Join(dataDir, "update_last_check.json")
	state, _ := readCooldown(statePath)
	now := time.Now().Unix()

	// fail_count gate BEFORE cooldown gate: if we've hit the limit, skip
	// entirely until someone resets the state file.
	if state.FailCount >= maxFailCount {
		if logger != nil {
			logger.Debug("update: fail_count at limit, skipping",
				"fail_count", state.FailCount)
		}
		return nil
	}
	if now-state.Ts < cooldownSeconds {
		if logger != nil {
			logger.Debug("update: cooldown active, skipping",
				"age_seconds", now-state.Ts)
		}
		return nil
	}

	// Poll /version.
	remote, err := pollVersion(ctx, webhookURL, secret)
	if err != nil {
		bumpFail(statePath, state, now, logger, "poll version")
		return err
	}

	if remote.Paused {
		if logger != nil {
			logger.Info("update: server reports paused=true, skipping")
		}
		_ = writeCooldown(statePath, cooldownState{Ts: now, FailCount: 0})
		return nil
	}

	ownHash, err := currentExeHash16()
	if err != nil {
		bumpFail(statePath, state, now, logger, "hash own exe")
		return err
	}

	if ownHash == remote.Version {
		if logger != nil {
			logger.Debug("update: no update (hash match)",
				"own_hash", ownHash,
				"remote_version", remote.Version,
				"app_version", appVersion)
		}
		_ = writeCooldown(statePath, cooldownState{Ts: now, FailCount: 0})
		return nil
	}

	if logger != nil {
		logger.Info("update: new version detected",
			"own_hash", ownHash,
			"remote_version", remote.Version)
	}

	installerPath := filepath.Join(os.TempDir(),
		"EarlScheibWatcher-Update-"+remote.Version+".exe")

	downloadedHash, err := downloadInstaller(ctx, webhookURL, remote.DownloadURL, secret, installerPath)
	if err != nil {
		bumpFail(statePath, state, now, logger, "download installer")
		return err
	}

	if downloadedHash != remote.Version {
		if logger != nil {
			logger.Error("update: downloaded installer hash mismatch",
				"expected", remote.Version,
				"got", downloadedHash)
		}
		_ = os.Remove(installerPath)
		bumpFail(statePath, state, now, logger, "hash mismatch")
		return fmt.Errorf("update: downloaded installer sha256[:16]=%s does not match server version=%s",
			downloadedHash, remote.Version)
	}

	if logger != nil {
		logger.Info("update: launching installer",
			"path", installerPath,
			"version", remote.Version)
	}

	if err := launcher(installerPath); err != nil {
		bumpFail(statePath, state, now, logger, "launcher")
		return err
	}

	_ = writeCooldown(statePath, cooldownState{Ts: now, FailCount: 0})
	// Sleep so the spawned child detaches, then exit so the installer can
	// overwrite this binary. In tests exitFn and sleepFn are stubs.
	sleepFn(exitSleep)
	exitFn(0)
	return nil
}

// pollVersion performs the HMAC-signed GET /version request.
func pollVersion(ctx context.Context, webhookURL, secret string) (versionResponse, error) {
	var out versionResponse
	url := strings.TrimRight(webhookURL, "/") + "/version"
	sig := webhook.Sign(secret, []byte(""))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return out, fmt.Errorf("build version request: %w", err)
	}
	req.Header.Set("X-EMS-Signature", sig)
	req.Header.Set("X-EMS-Source", "EarlScheibWatcher")

	client := &http.Client{Timeout: pollTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return out, fmt.Errorf("version poll transport: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("version poll non-200: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return out, fmt.Errorf("version poll read: %w", err)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("version poll json: %w", err)
	}
	if out.Version == "" || out.DownloadURL == "" {
		return out, errors.New("version poll: missing version or download_url")
	}
	return out, nil
}

// downloadInstaller fetches the installer over HMAC-signed HTTP, streams it to
// installerPath, and returns the first-16 hex of its SHA256.
func downloadInstaller(
	ctx context.Context,
	webhookURL, downloadPath, secret, installerPath string,
) (string, error) {
	url := strings.TrimRight(webhookURL, "/") + downloadPath
	sig := webhook.Sign(secret, []byte(""))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build download request: %w", err)
	}
	req.Header.Set("X-EMS-Signature", sig)
	req.Header.Set("X-EMS-Source", "EarlScheibWatcher")

	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download transport: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download non-200: %d", resp.StatusCode)
	}

	f, err := os.Create(installerPath)
	if err != nil {
		return "", fmt.Errorf("create installer file: %w", err)
	}

	h := sha256.New()
	tee := io.TeeReader(resp.Body, h)
	if _, err := io.Copy(f, tee); err != nil {
		_ = f.Close()
		_ = os.Remove(installerPath)
		return "", fmt.Errorf("stream installer: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(installerPath)
		return "", fmt.Errorf("close installer: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// currentExeHash16 returns the first-16 hex SHA256 of the running executable.
func currentExeHash16() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("os.Executable: %w", err)
	}
	f, err := os.Open(exe)
	if err != nil {
		return "", fmt.Errorf("open exe: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash exe: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// ---- cooldown file helpers ----

func readCooldown(path string) (cooldownState, error) {
	var s cooldownState
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return cooldownState{}, err
	}
	return s, nil
}

// writeCooldown atomically persists state via temp-file + rename.
func writeCooldown(path string, s cooldownState) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir datadir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "update_last_check.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	b, err := json.Marshal(s)
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("marshal: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// bumpFail increments FailCount, stamps Ts=now, and persists. Logs at Debug on
// error (persist failure is never fatal).
func bumpFail(statePath string, prev cooldownState, now int64, logger *slog.Logger, reason string) {
	next := cooldownState{Ts: now, FailCount: prev.FailCount + 1}
	if err := writeCooldown(statePath, next); err != nil && logger != nil {
		logger.Debug("update: cooldown write failed",
			"reason", reason,
			"err", err)
	}
}
