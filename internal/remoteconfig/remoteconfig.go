// Package remoteconfig fetches server-driven config overrides and applies
// whitelisted keys to config.ini. Implements OPS-03, OPS-04, OPS-05.
//
// Fetch: HMAC-authenticated GET to {webhookURL}/remote-config
// Apply: writes ONLY AllowedKeys into config.ini via config.Merge (atomic rename)
//
// Both operations are best-effort — callers should log errors and continue with
// the local config if either fails.
package remoteconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jjagpal/earl-scheib-watcher/internal/config"
	"github.com/jjagpal/earl-scheib-watcher/internal/webhook"
)

// AllowedKeys is the whitelist of remote-config fields that may be written to
// config.ini. secret_key and watch_folder are intentionally absent (OPS-04).
// Never add secret_key or watch_folder here.
var AllowedKeys = []string{"webhook_url", "log_level"}

// Fetch performs a HMAC-signed GET to {webhookURL}/remote-config with a 5-second
// timeout. Returns the parsed JSON map on success, or an error if the request
// fails or returns non-2xx.
//
// HMAC signing of GET: sign the empty body ([]byte("")) — matches the server's
// validation of webhook.Sign(secret, []byte("")). Using the empty body for a GET
// request is the simplest approach and byte-identical to the Python reference
// hmac.new(secret.encode('utf-8'), b"", hashlib.sha256).hexdigest().
func Fetch(ctx context.Context, webhookURL, secret string, logger *slog.Logger) (map[string]string, error) {
	url := strings.TrimRight(webhookURL, "/") + "/remote-config"
	sig := webhook.Sign(secret, []byte(""))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("remoteconfig.Fetch build request: %w", err)
	}
	req.Header.Set("X-EMS-Signature", sig)
	req.Header.Set("X-EMS-Source", "EarlScheibWatcher")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remoteconfig.Fetch GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		// 204: server signals no changes.
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("remoteconfig.Fetch: server returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, fmt.Errorf("remoteconfig.Fetch read body: %w", err)
	}

	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("remoteconfig.Fetch parse JSON: %w", err)
	}
	return result, nil
}

// Apply merges remote into config.ini at cfgPath, accepting ONLY AllowedKeys.
// Keys not in AllowedKeys are silently discarded (OPS-04).
// Returns changed=true if at least one key was written.
func Apply(cfgPath string, remote map[string]string, logger *slog.Logger) (changed bool, err error) {
	changed, err = config.Merge(cfgPath, remote, AllowedKeys)
	if err != nil {
		if logger != nil {
			logger.Error("remoteconfig.Apply failed", "err", err)
		}
		return false, err
	}
	if changed && logger != nil {
		logger.Info("remote config applied", "keys", allowedKeysPresent(remote))
	}
	return changed, nil
}

// allowedKeysPresent returns the subset of remote keys that are in AllowedKeys.
func allowedKeysPresent(remote map[string]string) []string {
	allow := make(map[string]bool, len(AllowedKeys))
	for _, k := range AllowedKeys {
		allow[k] = true
	}
	var present []string
	for k := range remote {
		if allow[k] {
			present = append(present, k)
		}
	}
	return present
}
