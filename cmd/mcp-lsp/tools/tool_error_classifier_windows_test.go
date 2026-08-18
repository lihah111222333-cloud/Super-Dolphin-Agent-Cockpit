//go:build windows

package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

func TestToolErrorClassifierPromotesRawWindowsPathErrorToEnvelope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-state.db")
	raw := &os.PathError{Op: "open", Path: path, Err: syscall.Errno(5)}
	wrapped := securefs.WrapErrorForPath(raw, path)

	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(wrapped, &permissionErr) || permissionErr == nil {
		t.Fatalf("raw Windows ACL error was not promoted: %v", wrapped)
	}
	envelope := newToolErrorEnvelope("inspect", "go", wrapped)
	if envelope.Code != toolErrorCodeAuthorizationRequired {
		t.Fatalf("envelope code = %q, want %q", envelope.Code, toolErrorCodeAuthorizationRequired)
	}
	if envelope.Meta[toolErrorMetaAuthorizationRequired] != true {
		t.Fatalf("envelope authorization metadata = %#v, want true", envelope.Meta[toolErrorMetaAuthorizationRequired])
	}
	if envelope.Meta[toolErrorMetaWindowsErrorCode] != uint32(5) {
		t.Fatalf("envelope Windows error code = %#v, want 5", envelope.Meta[toolErrorMetaWindowsErrorCode])
	}
	if envelope.Meta[toolErrorMetaWindowsPermissionKind] != "access_denied" {
		t.Fatalf("envelope Windows permission kind = %#v, want access_denied", envelope.Meta[toolErrorMetaWindowsPermissionKind])
	}
	for _, privateToken := range []string{path, filepath.Dir(path)} {
		if strings.Contains(strings.ToLower(envelope.Error), strings.ToLower(privateToken)) {
			t.Fatalf("envelope leaked raw path %q: %q", privateToken, envelope.Error)
		}
	}
}

// TestToolErrorClassifierWindowsJobPolicyError 保证受限 Job 返回 typed failure，而不是 running_slow 或通用错误。
func TestToolErrorClassifierWindowsJobPolicyError(t *testing.T) {
	err := &hiddenexec.WindowsJobPolicyError{
		Operation:       "current Windows Job rejected language-server Job assignment",
		LimitFlags:      0x00002000,
		KillOnClose:     true,
		BreakawayOK:     false,
		SilentBreakaway: false,
		Cause:           &os.PathError{Op: "open", Path: `C:\Users\secret\state.db`, Err: syscall.Errno(5)},
	}
	envelope := newToolErrorEnvelope("file", "swift", err)
	if envelope.Code != "windows_job_restricted" {
		t.Fatalf("envelope code = %q, want windows_job_restricted", envelope.Code)
	}
	if envelope.Meta["windows_job_policy"] != true || envelope.Meta["windows_job_limit_flags"] != uint32(0x00002000) {
		t.Fatalf("envelope Job metadata = %#v, want typed policy facts", envelope.Meta)
	}
	if strings.Contains(envelope.Error, `C:\Users\`) {
		t.Fatalf("envelope leaked an absolute user path: %q", envelope.Error)
	}
}
