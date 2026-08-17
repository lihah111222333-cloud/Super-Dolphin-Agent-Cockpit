//go:build windows

package installer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

func TestWindowsTarXzSingleAssetDiagnostic(t *testing.T) {
	payloadPath := strings.TrimSpace(os.Getenv("MCP_LSP_WINDOWS_TAR_XZ_SINGLE_ASSET_PAYLOAD"))
	if payloadPath == "" {
		t.Skip("set MCP_LSP_WINDOWS_TAR_XZ_SINGLE_ASSET_PAYLOAD for the bounded locked-payload diagnostic")
	}
	if _, err := os.Stat(payloadPath); err != nil {
		t.Fatalf("locked diagnostic payload unavailable: %v", err)
	}
	outputRoot, err := os.MkdirTemp("", "sd-native-catalog-clangd-tar-diagnostic-windows-arm64-")
	if err != nil {
		t.Fatalf("create product-owned diagnostic stage: %v", err)
	}
	t.Cleanup(func() {
		if err := RemoveWindowsInstallerTreeChecked(filepath.Dir(outputRoot), outputRoot); err != nil {
			t.Errorf("remove product-owned diagnostic stage: %v", err)
		}
	})
	if err := securefs.RestrictPrivateOwnerOnly(outputRoot, 0o700); err != nil {
		t.Fatalf("restrict product-owned diagnostic stage: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	if err := extractTarXzAsset(ctx, payloadPath, outputRoot, "", 8<<30); err != nil {
		t.Fatalf("locked clangd tar.xz diagnostic failed: %v", err)
	}
	t.Logf("locked clangd tar.xz diagnostic passed; output_root_units=%d", len([]rune(outputRoot)))
}

func TestWindowsTarStderrSummaryRedactsPathsAndClassifiesFailure(t *testing.T) {
	for name, input := range map[string]string{
		"disk":  `tar: Cannot write C:\\Users\\private\\stage: No space left on device`,
		"acl":   `tar: C:\\private\\stage: Access is denied`,
		"open":  `tar: Cannot open C:\\private\\payload.tar.xz`,
		"other": `tar: archive checksum mismatch`,
	} {
		t.Run(name, func(t *testing.T) {
			got := windowsTarStderrSummary([]byte(input))
			if strings.Contains(got, `C:\\`) || strings.Contains(got, "private") || strings.Contains(got, "stage") {
				t.Fatalf("summary leaked path material: %q", got)
			}
			if !strings.Contains(got, "stderr_bytes=") || !strings.Contains(got, "stderr_sha256=") {
				t.Fatalf("summary missing stable stderr facts: %q", got)
			}
			wantClass := map[string]string{"disk": "disk_space_exhausted", "acl": "authorization_required", "open": "open_failed", "other": "tar_error"}[name]
			if !strings.Contains(got, "stderr_class="+wantClass) {
				t.Fatalf("summary=%q, want class %q", got, wantClass)
			}
		})
	}
}
