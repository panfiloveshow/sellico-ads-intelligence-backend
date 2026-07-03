package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateExtensionNetworkCaptureURL_AllowsMatchingWBEndpoint(t *testing.T) {
	payload := json.RawMessage(`{
		"url": "https://cmp.wildberries.ru/api/v1/advert/preset-bids?nm_id=439046552"
	}`)

	if err := validateExtensionNetworkCaptureURL("wb.bid.recommendations", payload); err != nil {
		t.Fatalf("expected matching WB endpoint to pass, got %v", err)
	}
}

func TestValidateExtensionNetworkCaptureURL_RejectsMismatchedEndpointKey(t *testing.T) {
	payload := json.RawMessage(`{
		"url": "https://cmp.wildberries.ru/api/v1/advert/preset-bids?nm_id=439046552"
	}`)

	if err := validateExtensionNetworkCaptureURL("wb.query.stats", payload); err == nil {
		t.Fatalf("expected mismatched endpoint key to be rejected")
	}
}

func TestValidateExtensionNetworkCaptureURL_RejectsUnexpectedHost(t *testing.T) {
	payload := json.RawMessage(`{
		"url": "https://example.com/api/v1/advert/preset-bids?nm_id=439046552"
	}`)

	if err := validateExtensionNetworkCaptureURL("wb.bid.recommendations", payload); err == nil {
		t.Fatalf("expected unexpected host to be rejected")
	}
}

func TestValidateExtensionNetworkCaptureURL_RejectsSensitiveQueryParams(t *testing.T) {
	payload := json.RawMessage(`{
		"url": "https://cmp.wildberries.ru/api/v1/advert/preset-bids?access_token=secret-token-value&nm_id=439046552"
	}`)

	if err := validateExtensionNetworkCaptureURL("wb.bid.recommendations", payload); err == nil {
		t.Fatalf("expected sensitive URL query param to be rejected")
	}
}

func TestValidateExtensionNetworkCaptureURL_RejectsSensitiveRelativeQueryParams(t *testing.T) {
	payload := json.RawMessage(`{
		"url": "/api/v1/advert/preset-bids?session_id=private-session&nm_id=439046552"
	}`)

	if err := validateExtensionNetworkCaptureURL("wb.bid.recommendations", payload); err == nil {
		t.Fatalf("expected sensitive relative URL query param to be rejected")
	}
}

func TestSanitizeExtensionPayload_RedactsSensitiveKeysAndStrings(t *testing.T) {
	payload := json.RawMessage(`{
		"url": "https://cmp.wildberries.ru/api/v1/advert/preset-bids?access_token=secret-token-value&nm_id=439046552",
		"headers": {
			"Authorization": "Bearer abcdefghijklmnopqrstuvwxyz",
			"x-debug": "jwt=aaaaabbbbbccccc.dddddeeeeefffff.ggggghhhhhiiiii"
		},
		"items": [
			"sessionid=private-cookie-value"
		]
	}`)

	sanitized := string(sanitizeExtensionPayload(payload))
	for _, secret := range []string{
		"secret-token-value",
		"abcdefghijklmnopqrstuvwxyz",
		"aaaaabbbbbccccc",
		"private-cookie-value",
	} {
		if strings.Contains(sanitized, secret) {
			t.Fatalf("expected secret %q to be redacted from %s", secret, sanitized)
		}
	}
}

func TestDOMRowHelpers_RedactAndClampVisibleText(t *testing.T) {
	row := "Кампания 123 Bearer abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz расход 450"
	redacted := truncateExtensionText(redactSensitiveString(row), 48)

	if strings.Contains(redacted, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("expected bearer-like value to be redacted from %q", redacted)
	}
	if len([]rune(redacted)) > 48 {
		t.Fatalf("expected text to be clamped, got len=%d text=%q", len([]rune(redacted)), redacted)
	}
}
