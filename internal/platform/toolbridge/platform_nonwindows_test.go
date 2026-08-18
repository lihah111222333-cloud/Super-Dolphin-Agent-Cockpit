//go:build !windows

package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"

	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
)

func TestWindowsACLApprovalIsCompileTimeDisabledOutsideWindows(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	request, ok := h.windowsACLApprovalRequest(
		&mcpcontrol.ToolInstance{ClientKind: mcpdto.ClientKindLSP},
		ToolCallRequest{Name: "file", CallID: "call-nonwindows"},
		&ToolCallResult{
			Success:           false,
			StructuredContent: json.RawMessage(`{"success":false,"code":"authorization_required","meta":{"authorization_required":true,"windows_error_code":5,"windows_permission_kind":"access_denied"}}`),
		},
	)
	if ok || request.CallID != "" {
		t.Fatalf("non-Windows ACL approval = %+v, %v; want disabled", request, ok)
	}
}

func TestSchemaValidationAuthorizationIsNonWindowsNoOp(t *testing.T) {
	decision := (&Handler{}).handleSchemaValidationAuthorization(
		context.Background(), codexToolEntry{}, ToolCallRequest{CallID: "trusted"}, errors.New("permission"),
	)
	if decision.handled || decision.result != nil || decision.err != nil || decision.validationDone {
		t.Fatalf("non-Windows schema authorization decision = %#v, want strict no-op", decision)
	}
}

func stdioTestProcessAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

func TestStdioStartGuardedProcessNonWindowsStartsProcessGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30")
	guard, err := stdioStartGuardedProcess(cmd, false)
	if err != nil {
		t.Fatalf("stdioStartGuardedProcess() error = %v", err)
	}
	if guard == nil {
		t.Fatal("stdioStartGuardedProcess() guard = nil, want non-nil")
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("SysProcAttr = %+v, want Setpgid", cmd.SysProcAttr)
	}
	t.Cleanup(func() {
		if err := stdioTerminateProcessTree(cmd, guard); err != nil {
			t.Errorf("stdioTerminateProcessTree() error = %v", err)
		}
		if err := cmd.Wait(); err != nil {
			if !errors.Is(err, os.ErrProcessDone) {
				t.Logf("cmd.Wait() after forced cleanup = %v", err)
			}
		}
		if err := stdioCleanupProcessTree(cmd, guard); err != nil {
			t.Errorf("stdioCleanupProcessTree() error = %v", err)
		}
	})
}
