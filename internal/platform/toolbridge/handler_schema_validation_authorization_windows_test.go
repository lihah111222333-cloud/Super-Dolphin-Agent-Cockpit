//go:build windows

package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"syscall"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/schema"
)

func TestSchemaValidationWindowsACLApprovalRetriesOnce(t *testing.T) {
	approved := true
	requester := &windowsACLApprovalRequesterStub{decision: contract.ApprovalDecision{Approved: &approved}}
	calls := 0
	h, entry := schemaValidationWindowsACLTestHandler(t, requester, func() error {
		calls++
		if calls == 1 {
			return schema.TransientInitializationError(securefs.WrapErrorForPath(
				syscall.Errno(5), `C:\private\schema-helper.exe`,
			))
		}
		return nil
	})
	result, err, done := h.validateCodexSurfaceEntryResult(context.Background(), entry, ToolCallRequest{
		Name: "file", Arguments: json.RawMessage(`{"file_path":"main.go"}`),
		AgentID: "agent", ThreadID: "thread", TurnID: "turn", CallID: "call-schema-acl",
	})
	if err != nil || result != nil || done {
		t.Fatalf("approved schema validation = result=%#v err=%v done=%t, want continue to tool", result, err, done)
	}
	if calls != 2 || len(requester.requests) != 1 {
		t.Fatalf("schema validation calls/approvals = %d/%d, want 2/1", calls, len(requester.requests))
	}
}

func TestSchemaValidationWindowsACLDeniedDoesNotExecuteOrRetry(t *testing.T) {
	denied := false
	requester := &windowsACLApprovalRequesterStub{decision: contract.ApprovalDecision{Approved: &denied}}
	calls := 0
	h, entry := schemaValidationWindowsACLTestHandler(t, requester, func() error {
		calls++
		return securefs.WrapErrorForPath(syscall.Errno(1314), `C:\private\schema-helper.exe`)
	})
	result, err, done := h.validateCodexSurfaceEntryResult(context.Background(), entry, ToolCallRequest{
		Name: "file", Arguments: json.RawMessage(`{}`), CallID: "call-denied",
	})
	if err != nil || result == nil || result.Success || !done {
		t.Fatalf("denied schema validation = result=%#v err=%v done=%t, want structured denial", result, err, done)
	}
	if calls != 1 || len(requester.requests) != 1 {
		t.Fatalf("denied schema validation calls/approvals = %d/%d, want 1/1", calls, len(requester.requests))
	}
	if authorization, ok := parseWindowsACLAuthorization(result.StructuredContent); !ok || authorization.ErrorCode != 1314 {
		t.Fatalf("denied structured authorization = %+v/%t, want privilege_not_held", authorization, ok)
	}
}

func TestSchemaValidationWindowsACLSecondFailureDoesNotLoop(t *testing.T) {
	approved := true
	requester := &windowsACLApprovalRequesterStub{decision: contract.ApprovalDecision{Approved: &approved}}
	calls := 0
	h, entry := schemaValidationWindowsACLTestHandler(t, requester, func() error {
		calls++
		return securefs.WrapErrorForPath(syscall.Errno(5), `C:\private\schema-helper.exe`)
	})
	result, err, done := h.validateCodexSurfaceEntryResult(context.Background(), entry, ToolCallRequest{
		Name: "file", Arguments: json.RawMessage(`{}`), CallID: "call-repeat",
	})
	if err != nil || result == nil || result.Success || !done {
		t.Fatalf("second schema failure = result=%#v err=%v done=%t, want terminal denial", result, err, done)
	}
	if calls != 2 || len(requester.requests) != 1 {
		t.Fatalf("second schema failure calls/approvals = %d/%d, want 2/1", calls, len(requester.requests))
	}
}

func TestSchemaValidationWindowsACLApprovalErrorDoesNotExecute(t *testing.T) {
	requester := &windowsACLApprovalRequesterStub{err: errors.New("no UI peer")}
	calls := 0
	h, entry := schemaValidationWindowsACLTestHandler(t, requester, func() error {
		calls++
		return securefs.WrapErrorForPath(syscall.Errno(5), `C:\private\schema-helper.exe`)
	})
	result, err, done := h.validateCodexSurfaceEntryResult(context.Background(), entry, ToolCallRequest{
		Name: "file", Arguments: json.RawMessage(`{}`), CallID: "call-no-ui",
	})
	if err != nil || result == nil || result.Success || !done {
		t.Fatalf("approval error schema validation = result=%#v err=%v done=%t, want structured denial", result, err, done)
	}
	if calls != 1 || len(requester.requests) != 1 {
		t.Fatalf("approval error schema validation calls/approvals = %d/%d, want 1/1", calls, len(requester.requests))
	}
}

func TestSchemaValidationWindowsACLRequiresTrustedCallID(t *testing.T) {
	requester := &windowsACLApprovalRequesterStub{decision: contract.ApprovalDecision{}}
	h, entry := schemaValidationWindowsACLTestHandler(t, requester, func() error {
		return securefs.WrapErrorForPath(syscall.Errno(5), `C:\private\schema-helper.exe`)
	})
	_, err, done := h.validateCodexSurfaceEntryResult(context.Background(), entry, ToolCallRequest{Name: "file", Arguments: json.RawMessage(`{}`)})
	if err == nil || !done || len(requester.requests) != 0 {
		t.Fatalf("missing CallID schema validation = err=%v done=%t approvals=%d, want typed fail-fast", err, done, len(requester.requests))
	}
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(err, &permissionErr) || permissionErr == nil {
		t.Fatalf("missing CallID error = %v, want typed WindowsPermissionError", err)
	}
}

func TestSchemaValidationWindowsOrdinaryProcessFailureDoesNotRequestApproval(t *testing.T) {
	requester := &windowsACLApprovalRequesterStub{decision: contract.ApprovalDecision{}}
	h, entry := schemaValidationWindowsACLTestHandler(t, requester, func() error {
		return schema.TransientInitializationError(errors.New("ordinary process start failed"))
	})
	_, err, done := h.validateCodexSurfaceEntryResult(context.Background(), entry, ToolCallRequest{
		Name: "file", Arguments: json.RawMessage(`{}`), CallID: "call-ordinary-failure",
	})
	if err == nil || !done || len(requester.requests) != 0 {
		t.Fatalf("ordinary process failure = err=%v done=%t approvals=%d, want fail-fast without approval", err, done, len(requester.requests))
	}
}

func schemaValidationWindowsACLTestHandler(
	t *testing.T,
	requester contract.ApprovalRequester,
	next func() error,
) (*Handler, codexToolEntry) {
	t.Helper()
	owner := newTask4BAuthorityOwner()
	token := contract.MCPToolAuthority{ServerID: mcpdto.ClientKindLSP, Generation: 1}
	owner.current[token.ServerID] = token
	authority := &mcpSchemaAuthority{token: token}
	h := &Handler{
		authorityOwner:    owner,
		approvalRequester: requester,
		schemaExecutor: mcpSchemaExecutorFunc(func(context.Context, schema.Invocation, schema.FenceHook) (schema.Result, error) {
			if err := next(); err != nil {
				return schema.Result{}, err
			}
			return schema.Result{ArgumentsValid: true}, nil
		}),
	}
	return h, codexToolEntry{
		name: "file", realName: "file", executionKind: "stdio", family: mcpdto.ClientKindLSP,
		authority: authority,
	}
}
