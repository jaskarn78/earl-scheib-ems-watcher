package webhook

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"
)

// BackoffBase is the initial sleep between retry attempts.
// Exported so tests can set it to 1ms to avoid real sleeps.
// Matches Python's HTTP_BACKOFF_BASE = 1.0 (seconds).
var BackoffBase = 1 * time.Second

// httpAttempts is the total number of HTTP attempts before giving up.
// Matches Python's HTTP_ATTEMPTS = 3.
const httpAttempts = 3

// httpTimeout is the per-request timeout.
// Matches Python's HTTP_TIMEOUT = 30 (seconds).
const httpTimeout = 30 * time.Second

// SendConfig holds the configuration needed for a webhook POST.
type SendConfig struct {
	WebhookURL string
	SecretKey  string
	// Timeout overrides the per-request HTTP timeout. Zero means httpTimeout (30s).
	Timeout time.Duration
}

// isRetryableStatus returns true for HTTP status codes that warrant a retry.
// Matches Python's _is_retryable_status: 408, 425, 429, or 500-599.
func isRetryableStatus(code int) bool {
	return code == 408 || code == 425 || code == 429 || (code >= 500 && code < 600)
}

// Send POSTs body to cfg.WebhookURL with retry/backoff, returning true on success.
//
// Port of Python send_to_webhook:
//   - 3 total attempts
//   - Initial backoff 1s, doubles on each retry
//   - Retries on network errors and HTTP 408/425/429/5xx
//   - Returns false immediately on non-retryable HTTP status (e.g. 400)
//
// No BMS XML content is logged (PII protection — logs filename and size only).
func Send(cfg SendConfig, path string, body []byte, logger *slog.Logger) bool {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = httpTimeout
	}
	client := &http.Client{Timeout: timeout}
	return SendWithClient(cfg, path, body, logger, client)
}

// SendWithClient is the testable variant of Send. Tests inject an *http.Client
// backed by httptest.Server; production code uses a default client.
func SendWithClient(cfg SendConfig, path string, body []byte, logger *slog.Logger, client *http.Client) bool {
	filename := filepath.Base(path)
	sig := Sign(cfg.SecretKey, body)

	delay := BackoffBase
	for attempt := 1; attempt <= httpAttempts; attempt++ {
		req, err := http.NewRequest(http.MethodPost, cfg.WebhookURL, bytes.NewReader(body))
		if err != nil {
			logger.Error("webhook: build request failed", "err", err, "filename", filename)
			return false
		}

		req.Header.Set("Content-Type", "application/xml; charset=utf-8")
		req.Header.Set("X-EMS-Filename", filename)
		req.Header.Set("X-EMS-Source", "EarlScheibWatcher")
		if sig != "" {
			req.Header.Set("X-EMS-Signature", sig)
		}

		resp, err := client.Do(req)
		if err != nil {
			// Network error — treat identically to 5xx (retryable).
			logger.Warn("webhook: POST failed",
				"filename", filename,
				"attempt", fmt.Sprintf("%d/%d", attempt, httpAttempts),
				"err", err,
			)
			if attempt == httpAttempts {
				logger.Error("webhook: giving up after network errors",
					"filename", filename,
					"attempts", attempt,
				)
				return false
			}
			time.Sleep(delay)
			delay *= 2
			continue
		}

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 210))
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			logger.Info("webhook: sent",
				"filename", filename,
				"url", cfg.WebhookURL,
				"status", resp.StatusCode,
				"bytes", len(body),
			)
			return true
		}

		bodyPreview := string(respBody)
		if len(bodyPreview) > 200 {
			bodyPreview = bodyPreview[:200]
		}

		if isRetryableStatus(resp.StatusCode) && attempt < httpAttempts {
			logger.Warn("webhook: retryable HTTP error",
				"status", resp.StatusCode,
				"filename", filename,
				"attempt", fmt.Sprintf("%d/%d", attempt, httpAttempts),
				"retry_in", delay,
			)
			time.Sleep(delay)
			delay *= 2
			continue
		}

		// Non-retryable status or exhausted attempts.
		logger.Error("webhook: HTTP error",
			"status", resp.StatusCode,
			"filename", filename,
		)
		return false
	}

	return false
}
