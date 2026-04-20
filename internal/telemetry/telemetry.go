// Package telemetry captures unhandled panics and non-nil errors from command
// entry points, serialises a minimal structured record (NO BMS XML, NO variable
// values, NO customer PII — see OPS-01/OPS-02), and POSTs it HMAC-signed to
// {webhookURL}/telemetry with header X-EMS-Telemetry: 1.
//
// Failures to post are silently dropped — a broken telemetry endpoint MUST NOT
// break the scan.
//
// Payload shape (exactly these fields, nothing else):
//
//	{
//	  "type":        "panic" | "error",
//	  "message":     "<error string, max 200 chars>",
//	  "file":        "internal/scanner/scan.go",
//	  "line":        123,
//	  "os":          "windows",
//	  "app_version": "0.1.0",
//	  "ts":          "2026-04-20T12:34:56Z"
//	}
package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/jjagpal/earl-scheib-watcher/internal/webhook"
)

// Telemetry holds the configuration needed to send crash reports.
// All fields are immutable after Init — safe for concurrent use.
type Telemetry struct {
	webhookURL string
	secret     string
	appVersion string
	logger     *slog.Logger
}

// Init creates a Telemetry reporter.
//
// webhookURL is the base webhook URL (e.g. "https://support.jjagpal.me/earlscheibconcord");
// /telemetry is appended automatically.
//
// secret is the HMAC-SHA256 key. Empty string disables signing.
//
// appVersion is injected from main.appVersion ldflags var.
//
// logger may be nil — debug logging is skipped when nil.
func Init(webhookURL, secret, appVersion string, logger *slog.Logger) *Telemetry {
	return &Telemetry{
		webhookURL: webhookURL,
		secret:     secret,
		appVersion: appVersion,
		logger:     logger,
	}
}

// payload is the JSON structure sent on crash. Contains ONLY safe fields.
//
// WARNING: do not add variable contents here — PII risk (OPS-01).
// The message field is capped at 200 characters to limit accidental exposure.
type payload struct {
	Type       string `json:"type"`        // "panic" or "error"
	Message    string `json:"message"`     // error string only, no variable values
	File       string `json:"file"`        // source file of crash site
	Line       int    `json:"line"`        // line number of crash site
	OS         string `json:"os"`          // e.g. "windows"
	AppVersion string `json:"app_version"` // from ldflags
	TS         string `json:"ts"`          // RFC3339 UTC
}

// Wrap executes fn inside a deferred recover so any panic is captured and
// POSTed before the process exits. Non-nil error returns are also reported.
//
// A telemetry POST failure is silent — it must never block or crash the caller.
//
// After capturing a panic, Wrap re-panics so the process exits with non-zero
// status. The caller must NOT swallow the panic.
func (t *Telemetry) Wrap(fn func() error) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			// Capture stack here, in the defer, before any further unwinding.
			pcs := make([]uintptr, 32)
			n := runtime.Callers(2, pcs)
			t.Capture(r, pcs[:n])
			// Re-panic so the process exits non-zero.
			// This ensures the OS-level exit code is non-zero, signalling failure.
			panic(r)
		}
	}()
	if err := fn(); err != nil {
		pcs := make([]uintptr, 32)
		n := runtime.Callers(2, pcs)
		t.captureError(err, pcs[:n])
		retErr = err
	}
	return retErr
}

// Capture serialises and POSTs a crash record. Called from a deferred recover.
//
// r is the value returned by recover(). pcs is the captured call stack from
// runtime.Callers — must be captured in the defer before any stack unwinding.
//
// WARNING: do not log r's full string representation without truncation —
// may contain variable values (PII risk, OPS-01).
func (t *Telemetry) Capture(r any, pcs []uintptr) {
	file, line := crashLocation(pcs)

	// Truncate message to 200 chars max to prevent accidental PII inclusion.
	// fmt.Sprintf produces the panic value string — safe for runtime errors
	// (e.g. "runtime error: index out of range") but may leak for user-triggered
	// panics. The 200-char cap limits exposure.
	msg := fmt.Sprintf("%v", r)
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}

	p := payload{
		Type:       "panic",
		Message:    msg,
		File:       file,
		Line:       line,
		OS:         os.Getenv("GOOS"), // set at test time if needed; falls back below
		AppVersion: t.appVersion,
		TS:         time.Now().UTC().Format(time.RFC3339),
	}
	// os.Getenv("GOOS") is empty at runtime — use runtime.GOOS instead.
	p.OS = runtime.GOOS
	t.post(p)
}

// captureError serialises and POSTs an error record.
//
// WARNING: err.Error() may contain variable values depending on how the error
// was constructed. The 200-char cap limits accidental PII exposure (OPS-01).
func (t *Telemetry) captureError(err error, pcs []uintptr) {
	file, line := crashLocation(pcs)
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	p := payload{
		Type:       "error",
		Message:    msg,
		File:       file,
		Line:       line,
		OS:         runtime.GOOS,
		AppVersion: t.appVersion,
		TS:         time.Now().UTC().Format(time.RFC3339),
	}
	t.post(p)
}

// post marshals p to JSON and sends it to {webhookURL}/telemetry.
//
// All failures are logged at debug level (if logger is set) and silently dropped.
// This method MUST NOT return an error or panic under any circumstances.
func (t *Telemetry) post(p payload) {
	body, err := json.Marshal(p)
	if err != nil {
		if t.logger != nil {
			t.logger.Debug("telemetry marshal failed", "err", err)
		}
		return
	}

	url := strings.TrimRight(t.webhookURL, "/") + "/telemetry"
	sig := webhook.Sign(t.secret, body)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		if t.logger != nil {
			t.logger.Debug("telemetry request build failed", "err", err)
		}
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-EMS-Telemetry", "1")
	if sig != "" {
		req.Header.Set("X-EMS-Signature", sig)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if t.logger != nil {
			t.logger.Debug("telemetry POST failed", "err", err)
		}
		return
	}
	defer resp.Body.Close()
	if t.logger != nil {
		t.logger.Debug("telemetry POST", "status", resp.StatusCode)
	}
}

// crashLocation extracts the first non-runtime frame's file and line number
// from the provided program counter slice (from runtime.Callers).
//
// Returns ("unknown", 0) if no suitable frame is found.
func crashLocation(pcs []uintptr) (file string, line int) {
	if len(pcs) == 0 {
		return "unknown", 0
	}
	frames := runtime.CallersFrames(pcs)
	goroot := runtime.GOROOT()
	for {
		f, more := frames.Next()
		if f.File == "" {
			if !more {
				break
			}
			continue
		}
		// Skip Go runtime internals.
		if strings.HasPrefix(f.File, goroot) {
			if !more {
				break
			}
			continue
		}
		// Strip absolute path prefix down to the module-relative portion
		// so file names in telemetry are not machine-specific paths.
		result := f.File
		if idx := strings.Index(f.File, "/internal/"); idx >= 0 {
			result = f.File[idx+1:]
		} else if idx := strings.Index(f.File, "/cmd/"); idx >= 0 {
			result = f.File[idx+1:]
		}
		return result, f.Line
	}
	return "unknown", 0
}
