// Package heartbeat sends a lightweight POST to the webhook /heartbeat endpoint.
// Port of send_heartbeat() from ems_watcher.py (lines 330-349).
// Any HTTP error is non-fatal — logged at Debug level only.
package heartbeat

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jjagpal/earl-scheib-watcher/internal/webhook"
)

// heartbeatTimeout matches Python's timeout=10 in send_heartbeat.
const heartbeatTimeout = 10 * time.Second

// Send POSTs a heartbeat to webhookURL+"/heartbeat".
//
// Port of Python send_heartbeat:
//   - Body: <Heartbeat><Host>{hostname}</Host></Heartbeat>
//   - Headers: Content-Type: application/xml, X-EMS-Signature: <hex>, X-EMS-Heartbeat: 1
//   - With empty secretKey, X-EMS-Signature is set to empty string (matches Python behaviour)
//   - Any error is logged at Debug only (function always returns — non-fatal)
//   - If webhookURL is empty, returns immediately without any network call
func Send(webhookURL string, secretKey string, logger *slog.Logger) {
	if webhookURL == "" {
		return
	}

	hostname, _ := os.Hostname()
	body := []byte(fmt.Sprintf("<Heartbeat><Host>%s</Host></Heartbeat>", hostname))

	// Python: sig = hmac.new(secret_key.encode(), body, "sha256").hexdigest() if secret_key else ""
	// webhook.Sign returns "" when secretKey is ""; heartbeat always sends the header even if empty.
	sig := webhook.Sign(secretKey, body)

	req, err := http.NewRequest(http.MethodPost, webhookURL+"/heartbeat", bytes.NewReader(body))
	if err != nil {
		logger.Debug("heartbeat: request build failed", "err", err)
		return
	}

	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("X-EMS-Signature", sig) // empty string when no secret (matches Python)
	req.Header.Set("X-EMS-Heartbeat", "1")

	client := &http.Client{Timeout: heartbeatTimeout}
	resp, err := client.Do(req)
	if err != nil {
		logger.Debug("heartbeat: failed (non-fatal)", "err", err)
		return
	}
	defer resp.Body.Close()
	logger.Debug("heartbeat: sent", "status", resp.StatusCode)
}
