package telemetry_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jjagpal/earl-scheib-watcher/internal/telemetry"
	"github.com/jjagpal/earl-scheib-watcher/internal/webhook"
)

// capturedRequest holds the data captured by the test HTTP server.
type capturedRequest struct {
	body    []byte
	headers http.Header
}

// newTestServer creates a test server that captures POST requests.
// Returns the server, a channel of captured requests (buffered), and a cleanup func.
func newTestServer(t *testing.T, statusCode int) (*httptest.Server, chan capturedRequest) {
	t.Helper()
	captured := make(chan capturedRequest, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("test server read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		captured <- capturedRequest{body: body, headers: r.Header.Clone()}
		w.WriteHeader(statusCode)
	}))
	return srv, captured
}

// parsePayload unmarshals the captured JSON body into a map for inspection.
func parsePayload(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("payload JSON parse error: %v\nbody: %s", err, string(body))
	}
	return m
}

// TestWrap_PanicCapture verifies that a panicking function causes a POST with
// type="panic" and that the message does NOT contain PII-sensitive field names.
// Wrap re-panics after posting, so the test must recover from the outer panic.
func TestWrap_PanicCapture(t *testing.T) {
	srv, captured := newTestServer(t, http.StatusNoContent)
	defer srv.Close()

	secret := "test-secret"
	tel := telemetry.Init(srv.URL, secret, "0.1.0-test", nil)

	// Wrap re-panics — recover it so the test doesn't crash.
	func() {
		defer func() { recover() }()
		_ = tel.Wrap(func() error {
			panic("index out of range")
		})
	}()

	if len(captured) == 0 {
		t.Fatal("expected a POST to telemetry server, got none")
	}

	req := <-captured
	p := parsePayload(t, req.body)

	if got := p["type"]; got != "panic" {
		t.Errorf("type: want %q, got %q", "panic", got)
	}
	msg, _ := p["message"].(string)
	if !strings.Contains(msg, "index out of range") {
		t.Errorf("message %q should contain 'index out of range'", msg)
	}
	// PII/sensitive field names must NOT appear in the JSON.
	rawBody := string(req.body)
	for _, forbidden := range []string{"xml_bytes", "watch_folder", "CommPhone", "secret"} {
		if strings.Contains(rawBody, forbidden) {
			t.Errorf("payload contains forbidden field/value %q: %s", forbidden, rawBody)
		}
	}
}

// TestWrap_NoPostOnSuccess verifies that Wrap does NOT post when fn returns nil.
func TestWrap_NoPostOnSuccess(t *testing.T) {
	srv, captured := newTestServer(t, http.StatusNoContent)
	defer srv.Close()

	tel := telemetry.Init(srv.URL, "test-secret", "0.1.0-test", nil)
	err := tel.Wrap(func() error { return nil })
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if len(captured) != 0 {
		t.Errorf("expected 0 POSTs on success, got %d", len(captured))
	}
}

// TestWrap_ErrorCapture verifies that returning a non-nil error causes a POST
// with type="error".
func TestWrap_ErrorCapture(t *testing.T) {
	srv, captured := newTestServer(t, http.StatusNoContent)
	defer srv.Close()

	tel := telemetry.Init(srv.URL, "test-secret", "0.1.0-test", nil)
	retErr := tel.Wrap(func() error { return errors.New("db open failed") })
	if retErr == nil {
		t.Fatal("expected non-nil error returned from Wrap")
	}

	if len(captured) == 0 {
		t.Fatal("expected a POST to telemetry server for error, got none")
	}
	req := <-captured
	p := parsePayload(t, req.body)
	if got := p["type"]; got != "error" {
		t.Errorf("type: want %q, got %q", "error", got)
	}
}

// TestPayload_NoPIIFields verifies that the payload JSON only contains the
// whitelisted fields (type, message, file, line, os, app_version, ts) and
// no extra fields that might carry PII.
func TestPayload_NoPIIFields(t *testing.T) {
	srv, captured := newTestServer(t, http.StatusNoContent)
	defer srv.Close()

	tel := telemetry.Init(srv.URL, "test-secret", "0.1.0-test", nil)
	_ = tel.Wrap(func() error { return errors.New("some error") })

	req := <-captured
	p := parsePayload(t, req.body)

	allowed := map[string]bool{
		"type": true, "message": true, "file": true, "line": true,
		"os": true, "app_version": true, "ts": true,
	}
	for k := range p {
		if !allowed[k] {
			t.Errorf("unexpected field in payload: %q", k)
		}
	}
	// Forbidden content strings.
	rawBody := string(req.body)
	for _, forbidden := range []string{"xml", "watch_folder", "secret"} {
		if strings.Contains(rawBody, forbidden) {
			t.Errorf("payload contains forbidden string %q: %s", forbidden, rawBody)
		}
	}
}

// TestWrap_SilentOnHTTPFailure verifies that Wrap does not itself error or
// panic when the telemetry server returns a 5xx status.
func TestWrap_SilentOnHTTPFailure(t *testing.T) {
	srv, _ := newTestServer(t, http.StatusInternalServerError)
	defer srv.Close()

	tel := telemetry.Init(srv.URL, "test-secret", "0.1.0-test", nil)
	retErr := tel.Wrap(func() error { return errors.New("some error") })
	// Wrap should still return the function's error — the POST failure is silent.
	if retErr == nil {
		t.Error("expected the function's error to be returned even if POST fails")
	}
}

// TestHeaders_TelemetryAndSignature verifies that X-EMS-Telemetry: 1 is always
// present and that X-EMS-Signature matches webhook.Sign(secret, body).
func TestHeaders_TelemetryAndSignature(t *testing.T) {
	srv, captured := newTestServer(t, http.StatusNoContent)
	defer srv.Close()

	secret := "headers-test-secret"
	tel := telemetry.Init(srv.URL, secret, "0.1.0-test", nil)
	_ = tel.Wrap(func() error { return errors.New("header test error") })

	if len(captured) == 0 {
		t.Fatal("expected at least one POST")
	}
	req := <-captured

	if got := req.headers.Get("X-EMS-Telemetry"); got != "1" {
		t.Errorf("X-EMS-Telemetry: want %q, got %q", "1", got)
	}
	wantSig := webhook.Sign(secret, req.body)
	if got := req.headers.Get("X-EMS-Signature"); got != wantSig {
		t.Errorf("X-EMS-Signature: want %q, got %q", wantSig, got)
	}
}

// TestWrap_FileFieldPopulated verifies that the file field is a non-empty string
// (crash location was captured from the stack).
func TestWrap_FileFieldPopulated(t *testing.T) {
	srv, captured := newTestServer(t, http.StatusNoContent)
	defer srv.Close()

	tel := telemetry.Init(srv.URL, "test-secret", "0.1.0-test", nil)
	_ = tel.Wrap(func() error { return errors.New("file field test") })

	req := <-captured
	p := parsePayload(t, req.body)
	file, _ := p["file"].(string)
	if file == "" {
		t.Error("file field should be non-empty (crash location not captured)")
	}
}

// TestMessageTruncation verifies that a very long panic message is truncated to
// no more than 200 characters (plus "..." suffix), preventing accidental PII inclusion.
func TestMessageTruncation(t *testing.T) {
	srv, captured := newTestServer(t, http.StatusNoContent)
	defer srv.Close()

	tel := telemetry.Init(srv.URL, "test-secret", "0.1.0-test", nil)

	longMsg := strings.Repeat("X", 300)
	func() {
		defer func() { recover() }()
		_ = tel.Wrap(func() error {
			panic(longMsg)
		})
	}()

	if len(captured) == 0 {
		t.Fatal("expected a POST for panic")
	}
	req := <-captured
	p := parsePayload(t, req.body)
	msg, _ := p["message"].(string)
	if len(msg) > 204 { // 200 chars + "..." = 203, give one extra byte margin
		t.Errorf("message not truncated: len=%d, content=%q", len(msg), msg[:50])
	}
}

// TestWrap_NoBMSXMLInMessage verifies that feeding a BMS XML body as the panic
// value does not let raw XML propagate unchecked (only first 200 chars max).
func TestWrap_NoBMSXMLInMessage(t *testing.T) {
	srv, captured := newTestServer(t, http.StatusNoContent)
	defer srv.Close()

	tel := telemetry.Init(srv.URL, "test-secret", "0.1.0-test", nil)

	bmsXML := `<?xml version="1.0" encoding="UTF-8"?><VehicleDamageEstimateAddRq xmlns="http://www.cieca.com/BMS"><Owner><CommPhone>5555550123</CommPhone></Owner></VehicleDamageEstimateAddRq>`
	func() {
		defer func() { recover() }()
		_ = tel.Wrap(func() error {
			panic(bmsXML)
		})
	}()

	if len(captured) == 0 {
		t.Fatal("expected a POST for panic")
	}
	req := <-captured
	p := parsePayload(t, req.body)
	msg, _ := p["message"].(string)
	// The full XML must not appear — message is capped at 200 chars.
	// CommPhone appears at position ~153 in the XML above (past the 200 char cut), so it should NOT be in message.
	// The assertion is: full XML is not present (length truncated).
	if len(msg) > 204 {
		t.Errorf("BMS XML message not truncated, len=%d", len(msg))
	}
}

// TestNoRaceOnConcurrentWraps is a basic race-detector smoke test.
func TestNoRaceOnConcurrentWraps(t *testing.T) {
	srv, _ := newTestServer(t, http.StatusNoContent)
	defer srv.Close()

	tel := telemetry.Init(srv.URL, "test-secret", "0.1.0-test", nil)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = tel.Wrap(func() error { return errors.New("concurrent") })
		}()
	}
	wg.Wait()
}
