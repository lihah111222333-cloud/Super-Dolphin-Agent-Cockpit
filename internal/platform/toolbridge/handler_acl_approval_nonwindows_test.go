//go:build !windows

package toolbridge

import (
	"encoding/json"
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
