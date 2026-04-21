package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jjagpal/earl-scheib-watcher/internal/config"
	_ "modernc.org/sqlite"
)

// diagnosticResponse is the JSON shape returned by GET /api/diagnostic.
// Consumed by internal/admin/ui/main.js (see fetchDiagnostic poll loop).
// All fields are always populated — on errors the string form carries
// context rather than the handler returning non-200.
type diagnosticResponse struct {
	WatchFolder       string `json:"watch_folder"`
	FolderExists      bool   `json:"folder_exists"`
	FileCount         int    `json:"file_count"`
	FolderError       string `json:"folder_error"`
	LastScanAt        string `json:"last_scan_at"`
	LastScanProcessed int    `json:"last_scan_processed"`
	LastScanErrors    int    `json:"last_scan_errors"`
	LastScanNote      string `json:"last_scan_note"`
	LastHeartbeatAt   string `json:"last_heartbeat_at"`
	HMACSecretPresent bool   `json:"hmac_secret_present"`
	AppVersion        string `json:"app_version"`
}

// devDefaultSecret is the in-source dev fallback from cmd/earlscheib/main.go.
// A build without GSD_HMAC_SECRET keeps this value → every HMAC request to the
// live server returns 401. The Diagnostic panel must surface this distinctly.
const devDefaultSecret = "dev-test-secret-do-not-use-in-production"

// handleDiagnostic serves the admin Diagnostic panel data.
//
// Reads cfg.WatchFolder via config.LoadConfig (no file writes), probes the
// folder with os.Stat + os.ReadDir (counting .xml/.ems case-insensitive),
// queries the most recent row from runs in ems_watcher.db (read-only), fetches
// live heartbeat state from {webhook}/status (no HMAC needed — same endpoint
// public browser probes use), and reports whether the binary has a non-dev
// HMAC secret baked in.
//
// The secret itself is NEVER returned — only a boolean presence flag.
func (s *server) handleDiagnostic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	dataDir := config.DataDir()
	cfg, _ := config.LoadConfig(filepath.Join(dataDir, "config.ini"))

	resp := diagnosticResponse{
		WatchFolder:       cfg.WatchFolder,
		HMACSecretPresent: s.cfg.Secret != "" && s.cfg.Secret != devDefaultSecret,
		AppVersion:        s.cfg.AppVersion,
	}

	// Folder probe — stat first to distinguish "not found" from "other OS error",
	// then count .xml/.ems (case-insensitive, matches scanner.Candidates).
	if _, statErr := os.Stat(cfg.WatchFolder); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			resp.FolderExists = false
			resp.FolderError = "folder does not exist"
		} else {
			resp.FolderExists = false
			resp.FolderError = statErr.Error()
		}
	} else {
		resp.FolderExists = true
		entries, readErr := os.ReadDir(cfg.WatchFolder)
		if readErr != nil {
			resp.FolderError = readErr.Error()
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
			resp.FileCount = count
		}
	}

	// Last-scan row from ems_watcher.db — read-only, close immediately.
	// Matches status.Print SQL exactly to stay in lockstep with the CLI.
	dbPath := filepath.Join(dataDir, "ems_watcher.db")
	if _, statErr := os.Stat(dbPath); statErr == nil {
		// sql.Open with the modernc driver is cheap; no busy timeout tuning
		// needed for a single read with LIMIT 1.
		sqlDB, openErr := sql.Open("sqlite", dbPath+"?mode=ro")
		if openErr == nil {
			var runAt, note string
			var processed, errs int
			rowErr := sqlDB.QueryRow(
				"SELECT run_at, processed, errors, note FROM runs ORDER BY rowid DESC LIMIT 1",
			).Scan(&runAt, &processed, &errs, &note)
			if rowErr == nil {
				resp.LastScanAt = runAt
				resp.LastScanProcessed = processed
				resp.LastScanErrors = errs
				resp.LastScanNote = note
			} else if errors.Is(rowErr, sql.ErrNoRows) {
				resp.LastScanNote = "no scans yet"
			} else {
				resp.LastScanNote = "db query error: " + rowErr.Error()
			}
			_ = sqlDB.Close()
		} else {
			resp.LastScanNote = "db open error: " + openErr.Error()
		}
	} else {
		resp.LastScanNote = "no scans yet"
	}

	// Last heartbeat — fetch live from the server's public /status endpoint
	// (no HMAC required; same endpoint browser probes use). A 3s timeout keeps
	// the panel responsive when the webhook is unreachable.
	resp.LastHeartbeatAt = fetchLastHeartbeat(s.cfg.WebhookURL, s.client)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// fetchLastHeartbeat GETs {webhookURL}/status and extracts the "last_seen"
// human-readable field (e.g. "2m ago"). On any error it returns a
// diagnostic-friendly string — never propagates the error to the caller
// (the panel would rather show "(server unreachable)" than 502).
func fetchLastHeartbeat(webhookURL string, client *http.Client) string {
	if webhookURL == "" {
		return "(no webhook configured)"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	statusURL := strings.TrimRight(webhookURL, "/") + "/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		return "(bad webhook url)"
	}
	resp, err := client.Do(req)
	if err != nil {
		return "(server unreachable)"
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "(status " + http.StatusText(resp.StatusCode) + ")"
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "(read error)"
	}
	var parsed struct {
		Status   string `json:"status"`
		LastSeen string `json:"last_seen"`
		Host     string `json:"host"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "(malformed status)"
	}
	if parsed.LastSeen == "" || parsed.LastSeen == "never" {
		return "never"
	}
	if parsed.Host != "" {
		return parsed.LastSeen + " (" + parsed.Host + ")"
	}
	return parsed.LastSeen
}
