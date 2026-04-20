// Package webhook provides HMAC-SHA256 signing and HTTP POST with retry/backoff.
// Byte-identical to the Python reference ems_watcher.py send_to_webhook + hmac signing.
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Sign returns the hex HMAC-SHA256 of body using secret as the raw UTF-8 key.
//
// Byte-identical to Python:
//
//	hmac.new(secret.encode('utf-8'), body, hashlib.sha256).hexdigest()
//
// Returns empty string when secret is empty, matching Python's `if secret_key:` guard.
func Sign(secret string, body []byte) string {
	if secret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
