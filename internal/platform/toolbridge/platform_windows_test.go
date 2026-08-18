//go:build windows

package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	"golang.org/x/sys/windows"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/schema"
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

type windowsACLApprovalRequesterFunc func(context.Context, contract.ApprovalRequest) (contract.ApprovalDecision, error)

func (fn windowsACLApprovalRequesterFunc) RequestApproval(ctx context.Context, req contract.ApprovalRequest) (contract.ApprovalDecision, error) {
	return fn(ctx, req)
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

func TestWindowsACLAuthorizationFromTrustedMCPMeta(t *testing.T) {
	result := &ToolCallResult{
		ContentItems: []ToolCallContentItem{{
			Type: "text",
			Text: "ERROR code=authorization_required retryable=0",
			Meta: json.RawMessage("{\"authorization_required\":true,\"windows_error_code\":5,\"windows_permission_kind\":\"access_denied\"}"),
		}},
		Success: false,
	}
	got, ok := windowsACLAuthorizationFromResult(result)
	if !ok || got.ErrorCode != windowsACLAccessDeniedCode || got.PermissionKind != "access_denied" {
		t.Fatalf("windowsACLAuthorizationFromResult() = %+v, %v; want trusted _meta access_denied", got, ok)
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
				Content: []peerToolCallContent{{Type: "text", Text: "permission failed at " + secretPath, Meta: windowsACLTestMeta(t, true, 5, "access_denied")}},
				IsError: true,
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
					Content: []peerToolCallContent{{Type: "text", Text: "typed failure", Meta: windowsACLMetaFromEnvelope(t, tt.raw)}},
					IsError: true,
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

type codexSurfaceWindowsACLClient struct {
	responses []*ToolCallResult
	calls     int
}

func (c *codexSurfaceWindowsACLClient) ListTools(context.Context) ([]mcpdto.MCPTool, error) {
	return nil, nil
}

func (c *codexSurfaceWindowsACLClient) CallTool(context.Context, string, json.RawMessage, ToolCallRequest) (*ToolCallResult, error) {
	if c.calls >= len(c.responses) {
		return nil, errors.New("codex surface ACL test client exhausted")
	}
	result := c.responses[c.calls]
	c.calls++
	return result, nil
}

func (c *codexSurfaceWindowsACLClient) Close() error {
	return nil
}

func TestCodexSurfaceWindowsACLApprovalApprovedRetriesSameClientOnce(t *testing.T) {
	approved := true
	client := &codexSurfaceWindowsACLClient{responses: []*ToolCallResult{
		{Success: false, ContentItems: []ToolCallContentItem{{Type: "text", Meta: windowsACLTestMeta(t, true, 5, "access_denied")}}},
		{Success: true},
	}}
	requester := &windowsACLApprovalRequesterStub{decision: contract.ApprovalDecision{Approved: &approved}}
	h, entry := codexSurfaceWindowsACLTestHandler(t, client, requester)
	result, err := h.executeCodexSurfaceEntry(context.Background(), &codexToolSurface{}, entry, ToolCallRequest{
		Name: "file", Arguments: json.RawMessage(`{}`), AgentID: "agent-trusted", ThreadID: "thread-trusted", TurnID: "turn-trusted", CallID: "call-surface-acl",
	})
	if err != nil {
		t.Fatalf("executeCodexSurfaceEntry() error = %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("executeCodexSurfaceEntry() result = %+v, want retry success", result)
	}
	if client.calls != 2 || len(requester.requests) != 1 {
		t.Fatalf("surface client calls / approval requests = %d / %d, want 2 / 1", client.calls, len(requester.requests))
	}
	request := requester.requests[0]
	if request.AgentID != "agent-trusted" || request.ThreadID != "thread-trusted" || request.TurnID != "turn-trusted" || request.CallID != "call-surface-acl" {
		t.Fatalf("surface approval identity = %+v, want trusted ToolCallRequest identity", request)
	}
}

func TestCodexSurfaceWindowsACLApprovalFailsClosed(t *testing.T) {
	approved := true
	denied := false
	tests := []struct {
		name              string
		decision          contract.ApprovalDecision
		callID            string
		responses         []*ToolCallResult
		wantCalls         int
		wantApprovalCalls int
	}{
		{
			name:      "denied",
			decision:  contract.ApprovalDecision{Approved: &denied},
			callID:    "call-denied",
			responses: []*ToolCallResult{{Success: false, ContentItems: []ToolCallContentItem{{Type: "text", Meta: windowsACLTestMeta(t, true, 5, "access_denied")}}}},
			wantCalls: 1, wantApprovalCalls: 1,
		},
		{
			name:      "missing call ID",
			decision:  contract.ApprovalDecision{Approved: &approved},
			responses: []*ToolCallResult{{Success: false, ContentItems: []ToolCallContentItem{{Type: "text", Meta: windowsACLTestMeta(t, true, 5, "access_denied")}}}},
			wantCalls: 1, wantApprovalCalls: 0,
		},
		{
			name:     "second permission failure",
			decision: contract.ApprovalDecision{Approved: &approved},
			callID:   "call-repeat",
			responses: []*ToolCallResult{
				{Success: false, ContentItems: []ToolCallContentItem{{Type: "text", Meta: windowsACLTestMeta(t, true, 1314, "privilege_not_held")}}},
				{Success: false, ContentItems: []ToolCallContentItem{{Type: "text", Meta: windowsACLTestMeta(t, true, 5, "access_denied")}}},
			},
			wantCalls: 2, wantApprovalCalls: 1,
		},
		{
			name:      "windows job restricted",
			decision:  contract.ApprovalDecision{Approved: &approved},
			callID:    "call-job-policy",
			responses: []*ToolCallResult{{Success: false, ContentItems: []ToolCallContentItem{{Type: "text", Meta: json.RawMessage(`{"windows_job_policy":true}`)}}}},
			wantCalls: 1, wantApprovalCalls: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &codexSurfaceWindowsACLClient{responses: tt.responses}
			requester := &windowsACLApprovalRequesterStub{decision: tt.decision}
			h, entry := codexSurfaceWindowsACLTestHandler(t, client, requester)
			result, err := h.executeCodexSurfaceEntry(context.Background(), &codexToolSurface{}, entry, ToolCallRequest{
				Name: "file", Arguments: json.RawMessage(`{}`), CallID: tt.callID,
			})
			if err != nil {
				t.Fatalf("executeCodexSurfaceEntry() error = %v", err)
			}
			if result == nil || result.Success {
				t.Fatalf("executeCodexSurfaceEntry() result = %+v, want failure result", result)
			}
			if client.calls != tt.wantCalls || len(requester.requests) != tt.wantApprovalCalls {
				t.Fatalf("surface client calls / approval requests = %d / %d, want %d / %d", client.calls, len(requester.requests), tt.wantCalls, tt.wantApprovalCalls)
			}
		})
	}
}

func codexSurfaceWindowsACLTestHandler(t *testing.T, client mcpClient, requester contract.ApprovalRequester) (*Handler, codexToolEntry) {
	t.Helper()
	owner := newTask4BAuthorityOwner()
	token := contract.MCPToolAuthority{ServerID: mcpdto.ClientKindLSP, Generation: 1}
	owner.current[token.ServerID] = token
	return &Handler{authorityOwner: owner, approvalRequester: requester}, codexToolEntry{
		name: "file", realName: "file", executionKind: "stdio", family: mcpdto.ClientKindLSP,
		client: client, authority: &mcpSchemaAuthority{token: token},
	}
}

func TestRegisteredWindowsACLApprovalRetriesOriginallySelectedLSPPeerOnce(t *testing.T) {
	registry := mcpcontrol.NewRegistry()
	firstPeerCalls := 0
	firstLocal := newRegisteredWindowsACLPeer(t, registry, "lsp-acl-first", func() peerToolCallResponse {
		firstPeerCalls++
		if firstPeerCalls == 1 {
			return peerToolCallResponse{
				Content: []peerToolCallContent{{Type: "text", Text: "typed ACL failure", Meta: windowsACLTestMeta(t, true, 5, "access_denied")}},
				IsError: true,
			}
		}
		return peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: "original peer retry success"}}}
	})
	defer firstLocal.Close()
	instance := registry.FindActiveByKind(mcpdto.ClientKindLSP)[0]
	secondPeerCalls := 0
	var secondLocal jrpcserver.Local
	approved := true
	requester := windowsACLApprovalRequesterFunc(func(context.Context, contract.ApprovalRequest) (contract.ApprovalDecision, error) {
		secondLocal = newRegisteredWindowsACLPeer(t, registry, "lsp-acl-second", func() peerToolCallResponse {
			secondPeerCalls++
			return peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: "new peer must not receive retry"}}}
		})
		return contract.ApprovalDecision{Approved: &approved}, nil
	})
	h := &Handler{registry: registry, approvalRequester: requester, proxyAuthToken: newProxyAuthToken()}
	result, err := h.callPeerTool(context.Background(), instance, ToolCallRequest{
		Name:       "file",
		Arguments:  json.RawMessage(`{}`),
		AgentID:    "agent-trusted",
		ThreadID:   "thread-trusted",
		TurnID:     "turn-trusted",
		CallID:     "call-registered-acl",
		ClientKind: mcpdto.ClientKindLSP,
	})
	if secondLocal.Client != nil {
		defer secondLocal.Close()
	}
	if err != nil {
		t.Fatalf("callPeerTool() error = %v", err)
	}
	if result == nil || !result.Success || firstPeerCalls != 2 || secondPeerCalls != 0 {
		t.Fatalf("result/first/new calls = %+v/%d/%d, want success/2/0", result, firstPeerCalls, secondPeerCalls)
	}
}

func newRegisteredWindowsACLPeer(
	t *testing.T,
	registry *mcpcontrol.ToolRegistry,
	instanceID string,
	response func() peerToolCallResponse,
) jrpcserver.Local {
	t.Helper()
	callback := platformrpc.StrictHandler(func(_ context.Context, _ map[string]any) (peerToolCallResponse, error) {
		return response(), nil
	})
	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodRegister: platformrpc.StrictHandler(func(ctx context.Context, request mcpdto.RegisterRequest) (mcpdto.RegisterResponse, error) {
			return registry.Register(ctx, request)
		}),
	}, &jrpcserver.LocalOptions{
		Client: &jrpc2.ClientOptions{OnCallback: callback},
		Server: &jrpc2.ServerOptions{AllowPush: true},
	})
	var registered mcpdto.RegisterResponse
	err := local.Client.CallResult(context.Background(), mcpdto.MethodRegister, mcpdto.RegisterRequest{
		InstanceID: instanceID,
		BinaryName: "mcp-lsp",
		ClientKind: mcpdto.ClientKindLSP,
		PeerKind:   mcpdto.PeerKindTool,
		PID:        200,
	}, &registered)
	if err != nil {
		_ = local.Close()
		t.Fatalf("register mcp-lsp peer %q: %v", instanceID, err)
	}
	return local
}

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
	if authorization, ok := windowsACLAuthorizationFromResult(result); !ok || authorization.ErrorCode != 1314 {
		t.Fatalf("denied metadata authorization = %+v/%t, want privilege_not_held", authorization, ok)
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

func TestStdioConfigureCommandHidesWindowsConsole(t *testing.T) {
	cmd := exec.Command("mcp-lsp.exe")

	stdioConfigureCommand(cmd)

	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow = false, want true")
	}
	if cmd.SysProcAttr.CreationFlags&stdioCreateNoWindow == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}
	if cmd.SysProcAttr.CreationFlags&stdioCreateNewProcessGroup == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NEW_PROCESS_GROUP", cmd.SysProcAttr.CreationFlags)
	}
	if cmd.SysProcAttr.CreationFlags&stdioCreateSuspended == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_SUSPENDED", cmd.SysProcAttr.CreationFlags)
	}
}

func stdioTestProcessAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	const stillActive = 259
	return code == stillActive
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

func windowsACLTestMeta(t *testing.T, authorizationRequired bool, windowsCode uint32, kind string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"authorization_required":  authorizationRequired,
		"windows_error_code":      windowsCode,
		"windows_permission_kind": kind,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func windowsACLMetaFromEnvelope(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var envelope struct {
		Code string          `json:"code"`
		Meta json.RawMessage `json:"meta"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != windowsACLAuthorizationRequiredCode {
		return nil
	}
	return envelope.Meta
}

func TestStdioStartGuardedProcessWindowsFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		injectFail func(*stdioWindowsOps, error)
		wantClosed int
	}{
		{
			name: "create job",
			injectFail: func(ops *stdioWindowsOps, wantErr error) {
				ops.createJobObject = func() (windows.Handle, error) {
					return 0, wantErr
				}
			},
		},
		{
			name: "open process",
			injectFail: func(ops *stdioWindowsOps, wantErr error) {
				ops.openProcess = func(uint32, bool, uint32) (windows.Handle, error) {
					return 0, wantErr
				}
			},
			wantClosed: 1,
		},
		{
			name: "assign process",
			injectFail: func(ops *stdioWindowsOps, wantErr error) {
				ops.assignProcessToJobObject = func(windows.Handle, windows.Handle) error {
					return wantErr
				}
			},
			wantClosed: 2,
		},
		{
			name: "resume process",
			injectFail: func(ops *stdioWindowsOps, wantErr error) {
				ops.resumeProcess = func(windows.Handle) error {
					return wantErr
				}
			},
			wantClosed: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantErr := errors.New("injected " + tt.name + " failure")
			closed := make([]windows.Handle, 0, tt.wantClosed)
			ops := stdioWindowsOps{
				createJobObject: func() (windows.Handle, error) {
					return windows.Handle(101), nil
				},
				setInformationJobObject: func(windows.Handle, *windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION) error {
					return nil
				},
				openProcess: func(uint32, bool, uint32) (windows.Handle, error) {
					return windows.Handle(202), nil
				},
				assignProcessToJobObject: func(windows.Handle, windows.Handle) error {
					return nil
				},
				resumeProcess: func(windows.Handle) error {
					return nil
				},
				terminateJobObject: func(windows.Handle, uint32) error {
					return nil
				},
				closeHandle: func(handle windows.Handle) error {
					closed = append(closed, handle)
					return nil
				},
			}
			tt.injectFail(&ops, wantErr)

			cmd := exec.Command("cmd.exe", "/c", "exit", "0")
			guard, err := stdioStartGuardedProcessWithOps(cmd, false, ops)
			if err == nil {
				t.Fatal("stdioStartGuardedProcessWithOps() error = nil, want injected failure")
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("stdioStartGuardedProcessWithOps() error = %v, want injected error %v", err, wantErr)
			}
			if guard != nil {
				t.Fatalf("stdioStartGuardedProcessWithOps() guard = %+v, want nil on failure", guard)
			}
			if cmd.Process != nil && stdioTestProcessAlive(cmd.Process.Pid) {
				t.Fatalf("started process %d is still alive after guarded-start failure", cmd.Process.Pid)
			}
			if len(closed) != tt.wantClosed {
				t.Fatalf("closed handles = %v, want %d", closed, tt.wantClosed)
			}
		})
	}
}
