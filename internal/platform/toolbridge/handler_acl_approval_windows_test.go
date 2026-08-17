//go:build windows

package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
)

type windowsACLApprovalRequesterStub struct {
	requests []contract.ApprovalRequest
	decision contract.ApprovalDecision
	err      error
}

func (s *windowsACLApprovalRequesterStub) RequestApproval(_ context.Context, req contract.ApprovalRequest) (contract.ApprovalDecision, error) {
	s.requests = append(s.requests, req)
	return s.decision, s.err
}

func TestParseWindowsACLAuthorizationStrict(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		raw      json.RawMessage
		wantCode uint32
		wantKind string
		wantOK   bool
	}{
		{name: "access denied", raw: windowsACLTestEnvelope(t, false, windowsACLAuthorizationRequiredCode, true, 5, "access_denied"), wantCode: 5, wantKind: "access_denied", wantOK: true},
		{name: "privilege not held", raw: windowsACLTestEnvelope(t, false, windowsACLAuthorizationRequiredCode, true, 1314, "privilege_not_held"), wantCode: 1314, wantKind: "privilege_not_held", wantOK: true},
		{name: "success true", raw: windowsACLTestEnvelope(t, true, windowsACLAuthorizationRequiredCode, true, 5, "access_denied")},
		{name: "wrong code", raw: windowsACLTestEnvelope(t, false, "permission_denied", true, 5, "access_denied")},
		{name: "authorization false", raw: windowsACLTestEnvelope(t, false, windowsACLAuthorizationRequiredCode, false, 5, "access_denied")},
		{name: "unknown Win32 code", raw: windowsACLTestEnvelope(t, false, windowsACLAuthorizationRequiredCode, true, 32, "access_denied")},
		{name: "kind mismatch", raw: windowsACLTestEnvelope(t, false, windowsACLAuthorizationRequiredCode, true, 5, "privilege_not_held")},
		{name: "malformed", raw: json.RawMessage(`{"success":false`)},
		{name: "text only", raw: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseWindowsACLAuthorization(tt.raw)
			if ok != tt.wantOK {
				t.Fatalf("parseWindowsACLAuthorization() ok = %v, want %v", ok, tt.wantOK)
			}
			if got.ErrorCode != tt.wantCode || got.PermissionKind != tt.wantKind {
				t.Fatalf("authorization = %+v, want code=%d kind=%q", got, tt.wantCode, tt.wantKind)
			}
		})
	}
}

func TestWindowsACLApprovalApprovedRetriesSamePeerOnce(t *testing.T) {
	const secretPath = `C:\Users\private\workspace\secret.go`
	approved := true
	requester := &windowsACLApprovalRequesterStub{decision: contract.ApprovalDecision{Approved: &approved}}
	callbackCalls := 0
	peer := &stubPeer{callbackFn: func(_ context.Context, method string, _ any, output any) error {
		if method != ProxyMethodToolsCall {
			t.Fatalf("method = %q, want %q", method, ProxyMethodToolsCall)
		}
		callbackCalls++
		response := output.(*peerToolCallResponse)
		if callbackCalls == 1 {
			*response = peerToolCallResponse{
				Content:           []peerToolCallContent{{Type: "text", Text: "permission failed at " + secretPath}},
				IsError:           true,
				StructuredContent: windowsACLTestEnvelope(t, false, windowsACLAuthorizationRequiredCode, true, 5, "access_denied"),
			}
			return nil
		}
		*response = peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: "retry succeeded"}}}
		return nil
	}}
	instance := &mcpcontrol.ToolInstance{ClientKind: mcpdto.ClientKindLSP, Peer: peer}
	h, _ := newHandlerForTest(instance)
	h.approvalRequester = requester
	arguments, err := json.Marshal(map[string]any{"action": "open_file", "file_path": secretPath})
	if err != nil {
		t.Fatal(err)
	}
	result, err := h.callPeerTool(context.Background(), instance, ToolCallRequest{
		Name:       "file",
		Arguments:  arguments,
		AgentID:    "agent-trusted",
		ThreadID:   "thread-trusted",
		TurnID:     "turn-trusted",
		CallID:     "call-trusted",
		ClientKind: mcpdto.ClientKindLSP,
	})
	if err != nil {
		t.Fatalf("callPeerTool() error = %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("callPeerTool() result = %+v, want retry success", result)
	}
	if callbackCalls != 2 || len(requester.requests) != 1 {
		t.Fatalf("peer calls / approval requests = %d / %d, want 2 / 1", callbackCalls, len(requester.requests))
	}
	request := requester.requests[0]
	if request.AgentID != "agent-trusted" || request.ThreadID != "thread-trusted" || request.TurnID != "turn-trusted" || request.CallID != "call-trusted" {
		t.Fatalf("approval identity = %+v, want trusted ToolCallRequest identity", request)
	}
	if request.Kind != windowsACLApprovalKind || request.SourceMethod != ProxyMethodToolsCall {
		t.Fatalf("approval kind/source = %q/%q", request.Kind, request.SourceMethod)
	}
	publicApproval, err := json.Marshal(struct {
		Reason  string         `json:"reason"`
		Payload map[string]any `json:"payload"`
	}{Reason: request.Reason, Payload: request.Payload})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicApproval), secretPath) || len(request.Payload) != 3 {
		t.Fatalf("approval payload leaked a path or changed schema: %s", publicApproval)
	}
}

func TestWindowsACLApprovalFailsClosedAndNeverLoops(t *testing.T) {
	approved := true
	denied := false
	tests := []struct {
		name              string
		decision          contract.ApprovalDecision
		approvalErr       error
		callID            string
		raw               json.RawMessage
		wantPeerCalls     int
		wantApprovalCalls int
	}{
		{name: "denied", decision: contract.ApprovalDecision{Approved: &denied}, callID: "call-denied", raw: windowsACLTestEnvelope(t, false, windowsACLAuthorizationRequiredCode, true, 5, "access_denied"), wantPeerCalls: 1, wantApprovalCalls: 1},
		{name: "no decision", decision: contract.ApprovalDecision{}, callID: "call-nil", raw: windowsACLTestEnvelope(t, false, windowsACLAuthorizationRequiredCode, true, 5, "access_denied"), wantPeerCalls: 1, wantApprovalCalls: 1},
		{name: "no UI", approvalErr: errors.New("no UI peer"), callID: "call-no-ui", raw: windowsACLTestEnvelope(t, false, windowsACLAuthorizationRequiredCode, true, 1314, "privilege_not_held"), wantPeerCalls: 1, wantApprovalCalls: 1},
		{name: "approved but still denied", decision: contract.ApprovalDecision{Approved: &approved}, callID: "call-repeat", raw: windowsACLTestEnvelope(t, false, windowsACLAuthorizationRequiredCode, true, 5, "access_denied"), wantPeerCalls: 2, wantApprovalCalls: 1},
		{name: "non permission", decision: contract.ApprovalDecision{Approved: &approved}, callID: "call-other", raw: windowsACLTestEnvelope(t, false, "tool_error", true, 5, "access_denied"), wantPeerCalls: 1},
		{name: "missing call ID", decision: contract.ApprovalDecision{Approved: &approved}, raw: windowsACLTestEnvelope(t, false, windowsACLAuthorizationRequiredCode, true, 5, "access_denied"), wantPeerCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requester := &windowsACLApprovalRequesterStub{decision: tt.decision, err: tt.approvalErr}
			peerCalls := 0
			peer := &stubPeer{callbackFn: func(_ context.Context, _ string, _ any, output any) error {
				peerCalls++
				*output.(*peerToolCallResponse) = peerToolCallResponse{
					Content:           []peerToolCallContent{{Type: "text", Text: "typed failure"}},
					IsError:           true,
					StructuredContent: tt.raw,
				}
				return nil
			}}
			instance := &mcpcontrol.ToolInstance{ClientKind: mcpdto.ClientKindLSP, Peer: peer}
			h, _ := newHandlerForTest(instance)
			h.approvalRequester = requester
			result, err := h.callPeerTool(context.Background(), instance, ToolCallRequest{
				Name: "file", Arguments: json.RawMessage(`{}`), CallID: tt.callID, ClientKind: mcpdto.ClientKindLSP,
			})
			if err != nil {
				t.Fatalf("callPeerTool() error = %v", err)
			}
			if result == nil || result.Success {
				t.Fatalf("callPeerTool() result = %+v, want original/second typed failure", result)
			}
			if peerCalls != tt.wantPeerCalls || len(requester.requests) != tt.wantApprovalCalls {
				t.Fatalf("peer calls / approval requests = %d / %d, want %d / %d", peerCalls, len(requester.requests), tt.wantPeerCalls, tt.wantApprovalCalls)
			}
		})
	}
}

func TestNewHandlerWiresWindowsACLApprovalRequester(t *testing.T) {
	requester := &windowsACLApprovalRequesterStub{}
	in := toolbridgeDependencyFixture{profile: contract.DependencyProfileProduction}.handlerIn()
	in.Config.ProjectRoot = t.TempDir()
	in.ApprovalRequester = requester
	h, err := NewHandler(in)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	if h.approvalRequester != requester {
		t.Fatal("NewHandler() did not wire ApprovalRequester into Handler")
	}
}

func windowsACLTestEnvelope(t *testing.T, success bool, code string, authorizationRequired bool, windowsCode uint32, kind string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"success": success,
		"error":   "redacted Windows permission error",
		"code":    code,
		"meta": map[string]any{
			"authorization_required":  authorizationRequired,
			"windows_error_code":      windowsCode,
			"windows_permission_kind": kind,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
