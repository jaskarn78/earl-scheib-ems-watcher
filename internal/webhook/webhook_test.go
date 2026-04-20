package webhook_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"io"
	"log/slog"

	"github.com/jjagpal/earl-scheib-watcher/internal/webhook"
)

// testLogger returns a discard logger for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --------------------------------------------------------------------------
// Sign tests
// --------------------------------------------------------------------------

func TestSignHMACParity(t *testing.T) {
	// Pinned hex values computed from Python:
	//   import hmac, hashlib
	//   hmac.new(b"test-secret-1234", body, hashlib.sha256).hexdigest()
	fixtures := []struct {
		name     string
		body     []byte
		expected string
	}{
		{
			name:     "empty body",
			body:     []byte{},
			expected: "7d5e48d090279ce242b5b05aaf181049eb2ff179addbdc46df55c05a81dab082",
		},
		{
			name:     "ASCII string",
			body:     []byte("TestSigningParity"),
			expected: "e187375b21749c469539f5196bc0dac9168f7486da30174facf29752b7a5bba6",
		},
		{
			name: "UTF-8 BMS XML with unicode (André)",
			// Canonical 283-byte UTF-8 BMS XML with André unicode character.
			// HMAC computed via Python:
			//   hmac.new(b"test-secret-1234", body, hashlib.sha256).hexdigest()
			// Body is exactly 283 bytes in UTF-8 (é in André is 2 bytes: c3 a9).
			body: []byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<VehicleDamageEstimateAddRq xmlns=\"http://www.cieca.com/BMS\">\n  <DocumentInfo><DocumentID>TEST-EMS-WATCHER-V1XXAB</DocumentID></DocumentInfo>\n  <Owner><GivenName>André</GivenName><Surname>Watcher</Surname></Owner>\n</VehicleDamageEstimateAddRq>"),
			expected: "6273efee2fd31ecdb515af117da606487502ee7fb9392dfbddfe1a91213d0182",
		},
	}

	for _, tt := range fixtures {
		t.Run(tt.name, func(t *testing.T) {
			got := webhook.Sign("test-secret-1234", tt.body)
			if got != tt.expected {
				t.Errorf("Sign() = %q\nwant    %q", got, tt.expected)
			}
		})
	}
}

func TestSignEmptySecret(t *testing.T) {
	got := webhook.Sign("", []byte("any body content"))
	if got != "" {
		t.Errorf("Sign(\"\", body) = %q, want empty string", got)
	}
}

// --------------------------------------------------------------------------
// Send tests
// --------------------------------------------------------------------------

// makeSendConfig returns a SendConfig pointing at url with a test secret.
func makeSendConfig(url string) webhook.SendConfig {
	return webhook.SendConfig{
		WebhookURL: url,
		SecretKey:  "test-secret-1234",
		Timeout:    5 * time.Second,
	}
}

func TestSend200(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Speed up backoff in tests
	orig := webhook.BackoffBase
	webhook.BackoffBase = 1 * time.Millisecond
	defer func() { webhook.BackoffBase = orig }()

	cfg := makeSendConfig(srv.URL)
	ok := webhook.Send(cfg, "test.xml", []byte("<xml/>"), testLogger())
	if !ok {
		t.Error("Send() returned false, want true for 200 response")
	}
	if n := atomic.LoadInt32(&callCount); n != 1 {
		t.Errorf("HTTP call count = %d, want 1", n)
	}
}

func TestSend503ThenSuccess(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&callCount, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable) // 503 first 2 times
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	orig := webhook.BackoffBase
	webhook.BackoffBase = 1 * time.Millisecond
	defer func() { webhook.BackoffBase = orig }()

	cfg := makeSendConfig(srv.URL)
	ok := webhook.Send(cfg, "test.xml", []byte("<xml/>"), testLogger())
	if !ok {
		t.Error("Send() returned false, want true after 503x2 then 200")
	}
	if n := atomic.LoadInt32(&callCount); n != 3 {
		t.Errorf("HTTP call count = %d, want 3", n)
	}
}

func TestSend503AllFail(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusServiceUnavailable) // always 503
	}))
	defer srv.Close()

	orig := webhook.BackoffBase
	webhook.BackoffBase = 1 * time.Millisecond
	defer func() { webhook.BackoffBase = orig }()

	cfg := makeSendConfig(srv.URL)
	ok := webhook.Send(cfg, "test.xml", []byte("<xml/>"), testLogger())
	if ok {
		t.Error("Send() returned true, want false when all 3 attempts return 503")
	}
	if n := atomic.LoadInt32(&callCount); n != 3 {
		t.Errorf("HTTP call count = %d, want 3", n)
	}
}

func TestSend400NoRetry(t *testing.T) {
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		w.WriteHeader(http.StatusBadRequest) // 400 — non-retryable
	}))
	defer srv.Close()

	orig := webhook.BackoffBase
	webhook.BackoffBase = 1 * time.Millisecond
	defer func() { webhook.BackoffBase = orig }()

	cfg := makeSendConfig(srv.URL)
	ok := webhook.Send(cfg, "test.xml", []byte("<xml/>"), testLogger())
	if ok {
		t.Error("Send() returned true, want false for 400 response")
	}
	if n := atomic.LoadInt32(&callCount); n != 1 {
		t.Errorf("HTTP call count = %d, want 1 (no retry on 400)", n)
	}
}

func TestSendNetworkErrorRetries(t *testing.T) {
	// Point at a port nothing is listening on — guaranteed connection refused
	orig := webhook.BackoffBase
	webhook.BackoffBase = 1 * time.Millisecond
	defer func() { webhook.BackoffBase = orig }()

	cfg := webhook.SendConfig{
		WebhookURL: "http://127.0.0.1:1", // nothing listens here
		SecretKey:  "test-secret",
		Timeout:    100 * time.Millisecond,
	}
	ok := webhook.Send(cfg, "test.xml", []byte("<xml/>"), testLogger())
	if ok {
		t.Error("Send() returned true, want false for network error")
	}
}

func TestSendHeaders(t *testing.T) {
	var capturedReq *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	orig := webhook.BackoffBase
	webhook.BackoffBase = 1 * time.Millisecond
	defer func() { webhook.BackoffBase = orig }()

	body := []byte("<xml>test</xml>")
	cfg := makeSendConfig(srv.URL)
	webhook.Send(cfg, "invoice.xml", body, testLogger())

	if capturedReq == nil {
		t.Fatal("server received no request")
	}

	tests := []struct{ header, want string }{
		{"Content-Type", "application/xml; charset=utf-8"},
		{"X-Ems-Filename", "invoice.xml"},
		{"X-Ems-Source", "EarlScheibWatcher"},
	}
	for _, tt := range tests {
		got := capturedReq.Header.Get(tt.header)
		if got != tt.want {
			t.Errorf("Header %q = %q, want %q", tt.header, got, tt.want)
		}
	}

	// X-EMS-Signature should be present and be valid HMAC
	sig := capturedReq.Header.Get("X-Ems-Signature")
	if sig == "" {
		t.Error("X-EMS-Signature header is absent, want HMAC hex")
	}
	expectedSig := webhook.Sign("test-secret-1234", body)
	if sig != expectedSig {
		t.Errorf("X-EMS-Signature = %q, want %q", sig, expectedSig)
	}

	// X-EMS-Filename should use basename only
	filename := capturedReq.Header.Get("X-Ems-Filename")
	if filepath.Base(filename) != "invoice.xml" {
		t.Errorf("X-EMS-Filename basename = %q, want invoice.xml", filename)
	}
}

func TestSendNoSignatureOnEmptySecret(t *testing.T) {
	var capturedReq *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	orig := webhook.BackoffBase
	webhook.BackoffBase = 1 * time.Millisecond
	defer func() { webhook.BackoffBase = orig }()

	cfg := webhook.SendConfig{
		WebhookURL: srv.URL,
		SecretKey:  "", // empty secret
		Timeout:    5 * time.Second,
	}
	webhook.Send(cfg, "test.xml", []byte("<xml/>"), testLogger())

	if capturedReq == nil {
		t.Fatal("server received no request")
	}

	// X-EMS-Signature header must be ABSENT (not set to empty string)
	_, present := capturedReq.Header["X-Ems-Signature"]
	if present {
		t.Error("X-EMS-Signature header is present with empty secret, want absent")
	}
}
