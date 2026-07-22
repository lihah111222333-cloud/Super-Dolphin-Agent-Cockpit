package observability

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestSafePreviewRedactsShortTextAndBoundsLongText(t *testing.T) {
	short := SafePreview("Authorization: Bearer abc.def.ghi\npath=/tmp/file", 128)
	if short.Preview == "" || short.Truncated {
		t.Fatalf("short preview = %+v, want visible untruncated preview", short)
	}
	if strings.Contains(short.Preview, "abc.def.ghi") || strings.Contains(short.Preview, "\n") {
		t.Fatalf("short preview leaked secret or newline: %+v", short)
	}

	longSecret := "token=sk-abcdefghijklmnopqrstuvwxyz1234567890 " + strings.Repeat("payload ", 200)
	long := SafePreview(longSecret, 64)
	if !long.Truncated || long.Bytes != int64(len(longSecret)) || long.SHA256 == "" {
		t.Fatalf("long preview = %+v, want truncated bytes/hash metadata", long)
	}
	if long.Preview != "" {
		t.Fatalf("long preview = %q, want no partial payload when truncated", long.Preview)
	}
	if strings.Contains(long.SHA256, "sk-") {
		t.Fatalf("long preview hash leaked secret: %+v", long)
	}
}

func TestSafeToolArgumentsPreviewRedactsQuotedAssignments(t *testing.T) {
	tests := map[string]struct {
		raw    string
		secret string
	}{
		"double_quoted":         {raw: `run password="double-quoted-value" keep=visible`, secret: "double-quoted-value"},
		"single_quoted_env":     {raw: `run TOKEN='single-quoted-value' keep=visible`, secret: "single-quoted-value"},
		"double_quoted_flag":    {raw: `run --password="flag-quoted-value" keep=visible`, secret: "flag-quoted-value"},
		"escaped_double_quotes": {raw: `run password=\"escaped-quoted-value\" keep=visible`, secret: "escaped-quoted-value"},
		"escaped_single_quotes": {raw: `run TOKEN=\'escaped-single-value\' keep=visible`, secret: "escaped-single-value"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			preview := SafeToolArgumentsPreviewString(test.raw)
			if strings.Contains(preview, test.secret) {
				t.Fatalf("SafeToolArgumentsPreviewString() = %q, must redact complete quoted value %q", preview, test.secret)
			}
			if !strings.Contains(preview, "[REDACTED]") || !strings.Contains(preview, "keep=visible") {
				t.Fatalf("SafeToolArgumentsPreviewString() = %q, want redaction marker and ordinary context", preview)
			}
			if len(preview) > argumentPreviewOutputLimit {
				t.Fatalf("SafeToolArgumentsPreviewString() length = %d, want <= %d", len(preview), argumentPreviewOutputLimit)
			}
		})
	}
}

func TestSafeToolArgumentsPreviewRedactsCamelCaseCredentialKeys(t *testing.T) {
	const (
		sshKeyValue      = "ssh-key-leak-f2ab2e"
		accessKeyValue   = "access-key-leak-63d19c"
		clientKeyValue   = "client-key-leak-c8e4b7"
		signingKeyValue  = "signing-key-leak-5a2d8e"
		ordinaryKey      = "ordinary-key-value-4e90d5"
		ordinaryKeyboard = "ordinary-keyboard-value-2b7fa1"
	)

	inputs := map[string]any{
		"map": map[string]any{
			"sshKey": sshKeyValue,
			"nested": map[string]any{
				"accessKey":  accessKeyValue,
				"clientKey":  clientKeyValue,
				"signingKey": signingKeyValue,
			},
			"key":      ordinaryKey,
			"keyboard": ordinaryKeyboard,
		},
		"raw_json": json.RawMessage(`{"sshKey":"ssh-key-leak-f2ab2e","nested":{"accessKey":"access-key-leak-63d19c","clientKey":"client-key-leak-c8e4b7","signingKey":"signing-key-leak-5a2d8e"},"key":"ordinary-key-value-4e90d5","keyboard":"ordinary-keyboard-value-2b7fa1"}`),
	}

	for name, raw := range inputs {
		t.Run(name, func(t *testing.T) {
			preview := SafeToolArgumentsPreview(raw)
			for _, sensitiveValue := range []string{sshKeyValue, accessKeyValue, clientKeyValue, signingKeyValue} {
				if strings.Contains(preview, sensitiveValue) {
					t.Fatalf("SafeToolArgumentsPreview() = %q, must not contain credential value %q", preview, sensitiveValue)
				}
			}
			for _, ordinaryValue := range []string{ordinaryKey, ordinaryKeyboard} {
				if !strings.Contains(preview, ordinaryValue) {
					t.Fatalf("SafeToolArgumentsPreview() = %q, want ordinary map value %q", preview, ordinaryValue)
				}
			}
			if strings.Count(preview, redacted) < 4 {
				t.Fatalf("SafeToolArgumentsPreview() = %q, want each camelCase credential value redacted", preview)
			}
		})
	}
}

func TestSafeToolArgumentsPreviewOversizedPrefixedQuotedAssignment(t *testing.T) {
	const sensitiveValue = "oversized-quoted-value-84d7c2"
	raw := `provider arguments: --password="` + sensitiveValue + `" keep=visible ` + strings.Repeat("x", 17*1024)

	preview := SafeToolArgumentsPreviewString(raw)
	if strings.Contains(preview, sensitiveValue) {
		t.Fatalf("SafeToolArgumentsPreviewString() = %q, must redact complete quoted value %q", preview, sensitiveValue)
	}
	if !strings.Contains(preview, "provider arguments:") || !strings.Contains(preview, "keep=visible") {
		t.Fatalf("SafeToolArgumentsPreviewString() = %q, want ordinary prefixed context", preview)
	}
	if !strings.Contains(preview, "[REDACTED]") || !strings.Contains(preview, "[truncated]") {
		t.Fatalf("SafeToolArgumentsPreviewString() = %q, want redaction and truncation markers", preview)
	}
	if len(preview) > argumentPreviewOutputLimit {
		t.Fatalf("SafeToolArgumentsPreviewString() length = %d, want <= %d", len(preview), argumentPreviewOutputLimit)
	}
	if argumentPreviewRawLimit != 16*1024 || argumentPreviewProbeLimit != 1024 || argumentPreviewOutputLimit != 512 {
		t.Fatalf("argument preview bounds drifted: raw=%d probe=%d output=%d", argumentPreviewRawLimit, argumentPreviewProbeLimit, argumentPreviewOutputLimit)
	}
}

func TestSafeToolArgumentsPreviewOversizedInvalidStructuredInputFailsClosed(t *testing.T) {
	const sensitiveValue = "invalid-value-5a82d1"
	raw := `{"credentials":"` + sensitiveValue + `","padding":"` + strings.Repeat("x", 17*1024)

	preview := SafeToolArgumentsPreviewString(raw)
	if strings.Contains(preview, sensitiveValue) {
		t.Fatalf("SafeToolArgumentsPreviewString() = %q, must not contain credentials value %q", preview, sensitiveValue)
	}
	if !strings.Contains(preview, "[REDACTED]") || !strings.Contains(preview, "[truncated]") {
		t.Fatalf("SafeToolArgumentsPreviewString() = %q, want fail-closed redaction and truncation markers", preview)
	}
}

func TestSafeToolArgumentsPreviewMultiMegabyteStructuredInputFailsClosed(t *testing.T) {
	const sensitiveValue = "multi-megabyte-value-91d2c4"
	payload := " \t\r\n" + `{"credentials":"` + sensitiveValue + `","padding":"` + strings.Repeat("x", 4*1024*1024) + `"}`
	want := redacted + argumentPreviewTruncated
	previews := map[string]string{
		"raw_message": SafeToolArgumentsPreview(json.RawMessage(payload)),
		"string":      SafeToolArgumentsPreviewString(payload),
	}
	for name, preview := range previews {
		if preview != want {
			t.Fatalf("%s preview = %q, want bounded fail-closed preview %q", name, preview, want)
		}
		if strings.Contains(preview, sensitiveValue) {
			t.Fatalf("%s preview = %q, must not contain credentials value %q", name, preview, sensitiveValue)
		}
	}
}

func TestSafeToolArgumentsPreviewOversizedStructuredPrefixSkipsInvalidUTF8(t *testing.T) {
	const sensitiveValue = "invalid-prefix-value-43a11f"
	payload := `{"credentials":"` + sensitiveValue + `","padding":"` + strings.Repeat("x", 17*1024) + `"}`
	raw := append([]byte{0xff, ' ', '\t'}, []byte(payload)...)

	preview := SafeToolArgumentsPreview(raw)
	want := redacted + argumentPreviewTruncated
	if preview != want {
		t.Fatalf("SafeToolArgumentsPreview() = %q, want UTF-8-safe fail-closed preview %q", preview, want)
	}
	if strings.Contains(preview, sensitiveValue) {
		t.Fatalf("SafeToolArgumentsPreview() = %q, must not contain credentials value %q", preview, sensitiveValue)
	}
}

func TestSafeToolArgumentsPreviewOversizedJSONLikePrefixesFailClosed(t *testing.T) {
	const sensitiveValue = "oversized-secret-7d3e91"
	padding := strings.Repeat("x", 17*1024)
	structured := `{"password":"` + sensitiveValue + `","padding":"` + padding + `"}`
	tripleEncoded := structured
	for range 3 {
		tripleEncoded = strconv.Quote(tripleEncoded)
	}
	maxNested := structured
	maxNestedLayers := 0
	for {
		candidate := strconv.Quote(maxNested)
		secretStart := strings.Index(candidate, sensitiveValue)
		if secretStart < 0 {
			t.Fatal("nested fixture lost sensitive value")
		}
		secretEnd := secretStart + len(sensitiveValue)
		if secretEnd > argumentPreviewProbeLimit {
			break
		}
		maxNested = candidate
		maxNestedLayers++
	}
	if maxNestedLayers <= 3 {
		t.Fatalf("max nested layers = %d, want a convergence case deeper than triple encoding", maxNestedLayers)
	}
	tests := map[string]string{
		"bom":             "\uFEFF \t" + `{"password":"` + sensitiveValue + `","padding":"` + padding,
		"text_prefix":     "provider arguments: " + `{"password":"` + sensitiveValue + `","padding":"` + padding,
		"json_string":     `"{\"password\":\"` + sensitiveValue + `\",\"padding\":\"` + padding,
		"double_encoded":  `\\"password\\":\\"` + sensitiveValue + `\\",\\"padding\\":\\"` + padding,
		"triple_encoded":  tripleEncoded,
		"max_nested":      maxNested,
		"unicode_encoded": `\u007b\u0022password\u0022:\u0022` + sensitiveValue + `\u0022,\u0022padding\u0022:\u0022` + padding,
	}
	want := redacted + argumentPreviewTruncated
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			preview := SafeToolArgumentsPreviewString(raw)
			if preview != want {
				t.Fatalf("SafeToolArgumentsPreviewString() = %q, want bounded fail-closed preview %q", preview, want)
			}
			if strings.Contains(preview, sensitiveValue) {
				t.Fatalf("SafeToolArgumentsPreviewString() = %q, must not contain password value %q", preview, sensitiveValue)
			}
		})
	}
}

func TestSafeToolArgumentsPreviewOversizedOrdinaryTextRemainsVisible(t *testing.T) {
	raw := "ordinary release note with [brackets] and {braces} but no JSON fields " + strings.Repeat("x", 17*1024)

	preview := SafeToolArgumentsPreviewString(raw)
	if !strings.HasPrefix(preview, "ordinary release note") || !strings.Contains(preview, "[truncated]") {
		t.Fatalf("SafeToolArgumentsPreviewString() = %q, want visible bounded ordinary text", preview)
	}
	if strings.Contains(preview, "[REDACTED]") {
		t.Fatalf("SafeToolArgumentsPreviewString() = %q, ordinary text must not be classified as structured", preview)
	}
}

func TestSafePreviewRedactsJSONSecretKeys(t *testing.T) {
	preview := SafePreview(map[string]any{
		"password": "hunter2",
		"nested": map[string]any{
			"token": "ghp_secret_token",
		},
		"message": "plain diagnostic",
	}, 512)
	if preview.Truncated || preview.Preview == "" {
		t.Fatalf("SafePreview() = %+v, want visible sanitized JSON preview", preview)
	}
	if strings.Contains(preview.Preview, "hunter2") || strings.Contains(preview.Preview, "ghp_secret_token") {
		t.Fatalf("SafePreview() leaked JSON secret values: %q", preview.Preview)
	}
	if !strings.Contains(preview.Preview, "[REDACTED]") || !strings.Contains(preview.Preview, "plain diagnostic") {
		t.Fatalf("SafePreview() = %q, want redacted secrets and non-secret context", preview.Preview)
	}
}

func TestSafeErrorPreviewUsesStandardFieldAndRedacts(t *testing.T) {
	preview := SafeErrorPreview(errors.New("provider failed: password=hunter2\nexit status 42"))
	if preview == "" {
		t.Fatal("SafeErrorPreview() = empty, want diagnostic preview")
	}
	if strings.Contains(preview, "hunter2") || strings.Contains(preview, "\n") {
		t.Fatalf("SafeErrorPreview() leaked secret or newline: %q", preview)
	}

	if ErrorPreviewField != "error_preview" || ErrorCodeField != "error_code" || ProviderExitCodeField != "provider_exit_code" || PeerIDField != "peer_id" {
		t.Fatalf("standard trace fields drifted: %q %q %q %q", ErrorPreviewField, ErrorCodeField, ProviderExitCodeField, PeerIDField)
	}
}
