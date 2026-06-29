package observability

import (
	"errors"
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
