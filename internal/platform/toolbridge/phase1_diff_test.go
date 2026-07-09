package toolbridge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
)

func TestShouldTrackDiff(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		arguments json.RawMessage
		want      bool
	}{
		{name: "patch_edit patch", toolName: "patch_edit", arguments: mustRawJSON(t, map[string]any{"file_path": "sample.go", "patch": "@@\n-old\n+new\n"}), want: true},
		{name: "edit no longer drives diff", toolName: "edit", arguments: mustRawJSON(t, map[string]any{"file_path": "sample.go", "patch": "@@\n-old\n+new\n"}), want: false},
		{name: "patch_edit without patch", toolName: "patch_edit", arguments: mustRawJSON(t, map[string]any{"file_path": "sample.go"}), want: false},
		{name: "patch_edit action without patch", toolName: "patch_edit", arguments: mustRawJSON(t, map[string]any{"action": "replace_range"}), want: false},
		{name: "other tool", toolName: "inspect", arguments: mustRawJSON(t, map[string]any{"file_path": "sample.go", "patch": "@@\n-old\n+new\n"}), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldTrackDiff(tt.toolName, tt.arguments); got != tt.want {
				t.Fatalf("shouldTrackDiff() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToolBridge_RouteToolCall_ForwardsCodexMetadata(t *testing.T) {
	args := mustRawJSON(t, map[string]any{"line": 7})
	peer := &mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
		if method != "tools/call" {
			t.Fatalf("Callback() method = %q, want tools/call", method)
		}
		payload, ok := params.(map[string]any)
		if !ok {
			t.Fatalf("Callback() params type = %T, want map[string]any", params)
		}
		if got := payload["_agentId"]; got != "agent-7" {
			t.Fatalf("Callback() _agentId = %v, want %q", got, "agent-7")
		}
		if got := payload["_threadId"]; got != "thread-7" {
			t.Fatalf("Callback() _threadId = %v, want %q", got, "thread-7")
		}
		if got := payload["_callId"]; got != "call-7" {
			t.Fatalf("Callback() _callId = %v, want %q", got, "call-7")
		}
		resp, ok := result.(*peerToolCallResponse)
		if !ok {
			t.Fatalf("Callback() result type = %T, want *peerToolCallResponse", result)
		}
		*resp = peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: "metadata ok"}}}
		return nil
	}}}
	h, _ := newHandlerForTest(peer)

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "inspect",
		Arguments: args,
		AgentID:   "agent-7",
		ThreadID:  "thread-7",
		CallID:    "call-7",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "metadata ok", true)
}

func TestToolBridgeSelectsPeerByScope(t *testing.T) {
	root := t.TempDir()
	args := scopedLSPArgs(t)
	h, registry := newHandlerForTest(wrongScopedLSPPeer(t), rightScopedLSPPeer(t, root))
	registry.scoped = true

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "file",
		Arguments: args,
		AgentID:   "agent-29",
		ThreadID:  "thread-29",
		CallID:    "call-29",
		CWD:       root,
	})
	if err != nil {
		t.Fatalf("HandleToolCall() error = %v", err)
	}
	result := requireToolCallResult(t, got)
	assertSingleTextItem(t, result, "scoped ok", true)
	assertTrustedScopedLookup(t, registry, root)
}

func scopedLSPArgs(t *testing.T) json.RawMessage {
	t.Helper()
	return mustRawJSON(t, map[string]any{
		"action":     "read_file",
		"file_path":  "go.mod",
		"agent_id":   "forged-agent",
		"thread_id":  "forged-thread",
		"cwd":        "/forged/root",
		"session_id": "forged-session",
	})
}

func wrongScopedLSPPeer(t *testing.T) *mcpcontrol.ToolInstance {
	t.Helper()
	return &mcpcontrol.ToolInstance{
		AgentID:    "agent-29",
		ThreadID:   "wrong-thread",
		ClientKind: "lsp",
		Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
			t.Fatal("wrong scoped peer was called")
			return nil
		}},
	}
}

func rightScopedLSPPeer(t *testing.T, root string) *mcpcontrol.ToolInstance {
	t.Helper()
	return &mcpcontrol.ToolInstance{
		AgentID:    "agent-29",
		ThreadID:   "thread-29",
		ClientKind: "lsp",
		Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
			assertScopedCallbackMethod(t, method)
			payload := requireCallbackPayload(t, params)
			assertScopedCallbackMetadata(t, payload, root)
			assertNoReservedTopLevelKeys(t, payload)
			assertForgedArgumentsPreserved(t, payload)
			resp := result.(*peerToolCallResponse)
			*resp = peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: "scoped ok"}}}
			return nil
		}},
	}
}

func assertScopedCallbackMethod(t *testing.T, method string) {
	t.Helper()
	if method != ProxyMethodToolsCall {
		t.Fatalf("Callback() method = %q, want %s", method, ProxyMethodToolsCall)
	}
}

func requireCallbackPayload(t *testing.T, params any) map[string]any {
	t.Helper()
	payload, ok := params.(map[string]any)
	if !ok {
		t.Fatalf("Callback() params type = %T, want map[string]any", params)
	}
	return payload
}

func assertScopedCallbackMetadata(t *testing.T, payload map[string]any, root string) {
	t.Helper()
	if got := payload[MetadataKeyAgentID]; got != "agent-29" {
		t.Fatalf("Callback() _agentId = %#v, want agent-29", got)
	}
	if got := payload[MetadataKeyThreadID]; got != "thread-29" {
		t.Fatalf("Callback() _threadId = %#v, want thread-29", got)
	}
	if got := payload[MetadataKeyCallID]; got != "call-29" {
		t.Fatalf("Callback() _callId = %#v, want call-29", got)
	}
	if got := payload[MetadataKeyCWD]; got != root {
		t.Fatalf("Callback() _cwd = %#v, want %s", got, root)
	}
}

func assertNoReservedTopLevelKeys(t *testing.T, payload map[string]any) {
	t.Helper()
	for _, key := range []string{"agent_id", "thread_id", "cwd", "sessionId", "session_id", "_sessionId"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("Callback() leaked untrusted/reserved top-level key %q in payload: %#v", key, payload)
		}
	}
}

func assertForgedArgumentsPreserved(t *testing.T, payload map[string]any) {
	t.Helper()
	gotArgs, ok := payload["arguments"].(json.RawMessage)
	if !ok {
		t.Fatalf("Callback() arguments type = %T, want json.RawMessage", payload["arguments"])
	}
	for _, token := range []string{`"agent_id":"forged-agent"`, `"cwd":"/forged/root"`, `"session_id":"forged-session"`} {
		if !strings.Contains(string(gotArgs), token) {
			t.Fatalf("Callback() arguments = %s, want to preserve forged arg token %s", gotArgs, token)
		}
	}
}

func requireToolCallResult(t *testing.T, got any) *ToolCallResult {
	t.Helper()
	result, ok := got.(*ToolCallResult)
	if !ok {
		t.Fatalf("HandleToolCall() result type = %T, want *ToolCallResult", got)
	}
	return result
}

func assertTrustedScopedLookup(t *testing.T, registry *stubRegistry, root string) {
	t.Helper()
	if len(registry.gotScopes) != 1 {
		t.Fatalf("FindActiveForScope() calls = %d, want 1", len(registry.gotScopes))
	}
	scope := registry.gotScopes[0]
	if scope.AgentID != "agent-29" || scope.ThreadID != "thread-29" || scope.CallID != "call-29" || scope.CWD != root || scope.Family != "lsp" {
		t.Fatalf("FindActiveForScope() scope = %#v, want trusted agent/thread/call/cwd lsp", scope)
	}
}

func TestToolBridgeAmbiguousWithoutScope(t *testing.T) {
	h, registry := newHandlerForTest(
		&mcpcontrol.ToolInstance{AgentID: "agent-a", ThreadID: "thread-a", ClientKind: "lsp", Peer: &stubPeer{}},
		&mcpcontrol.ToolInstance{AgentID: "agent-b", ThreadID: "thread-b", ClientKind: "lsp", Peer: &stubPeer{}},
	)
	registry.scoped = true

	got, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{
		ID:     json.RawMessage(`30`),
		Method: "item/tool/call",
		Params: mustRawJSON(t, map[string]any{
			"name": "file",
			"arguments": map[string]any{
				"action":    "read_file",
				"file_path": "go.mod",
				"agent_id":  "agent-b",
				"thread_id": "thread-b",
				"cwd":       "/forged/root",
				"sessionId": "forged-session",
			},
		}),
	})
	if err != ErrAmbiguousPeer {
		t.Fatalf("HandleToolCall() error = %v, want %v", err, ErrAmbiguousPeer)
	}
	assertNilToolCallResult(t, got)
	assertFamilyOnlyScopedLookup(t, registry)
}

func assertNilToolCallResult(t *testing.T, got any) {
	t.Helper()
	if result, ok := got.(*ToolCallResult); ok {
		if result != nil {
			t.Fatalf("HandleToolCall() result = %#v, want nil", result)
		}
		return
	}
	if got != nil {
		t.Fatalf("HandleToolCall() result type = %T, want nil *ToolCallResult", got)
	}
}

func assertFamilyOnlyScopedLookup(t *testing.T, registry *stubRegistry) {
	t.Helper()
	if len(registry.gotScopes) != 1 {
		t.Fatalf("FindActiveForScope() calls = %d, want 1", len(registry.gotScopes))
	}
	scope := registry.gotScopes[0]
	if scope.AgentID != "" || scope.ThreadID != "" || scope.CallID != "" || scope.CWD != "" || scope.Family != "lsp" {
		t.Fatalf("FindActiveForScope() scope = %#v, want only lsp family without trusted identity/cwd", scope)
	}
}

func TestToolBridge_RouteToolCall_EmitsDiffForTrackedPatchEdit(t *testing.T) {
	repo := initGitRepo(t, map[string]string{"tracked.txt": "before\n"})
	args := mustRawJSON(t, map[string]any{"file_path": "tracked.txt", "patch": "@@\n-before\n+after\n"})
	peer := trackedDiffPeer(t, repo)
	h, _ := newHandlerForTest(peer)
	var emitted []difftracker.DiffResult
	h.resolver = resolverFunc(func(context.Context, string) (string, error) {
		return repo, nil
	})
	h.emitter = func(_ context.Context, diff difftracker.DiffResult) error {
		emitted = append(emitted, diff)
		return nil
	}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "patch_edit",
		Arguments: args,
		AgentID:   "agent-9",
		ThreadID:  "thread-9",
		CallID:    "call-9",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "diff ok", true)
	assertTrackedDiffEmission(t, emitted)
}

func trackedDiffPeer(t *testing.T, repo string) *mcpcontrol.ToolInstance {
	t.Helper()
	return &mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
		if method != "tools/call" {
			t.Fatalf("Callback() method = %q, want tools/call", method)
		}
		if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("after\n"), 0o644); err != nil {
			return err
		}
		resp, ok := result.(*peerToolCallResponse)
		if !ok {
			t.Fatalf("Callback() result type = %T, want *peerToolCallResponse", result)
		}
		*resp = peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: "diff ok"}}}
		return nil
	}}}
}

func assertTrackedDiffEmission(t *testing.T, emitted []difftracker.DiffResult) {
	t.Helper()
	if len(emitted) != 1 {
		t.Fatalf("emitted diff count = %d, want 1", len(emitted))
	}
	diff := emitted[0]
	assertTrackedDiffMetadata(t, diff)
	assertTrackedDiffText(t, diff)
}

func assertTrackedDiffMetadata(t *testing.T, diff difftracker.DiffResult) {
	t.Helper()
	if diff.AgentID != "agent-9" || diff.ThreadID != "thread-9" || diff.CallID != "call-9" {
		t.Fatalf("emitted metadata = %+v, want agent/thread/call ids", diff)
	}
	if diff.ToolName != "patch_edit" {
		t.Fatalf("emitted ToolName = %q, want %q", diff.ToolName, "patch_edit")
	}
	if len(diff.Files) != 1 || diff.Files[0] != "tracked.txt" {
		t.Fatalf("emitted Files = %#v, want [tracked.txt]", diff.Files)
	}
}

func assertTrackedDiffText(t *testing.T, diff difftracker.DiffResult) {
	t.Helper()
	if !strings.Contains(diff.DiffText, "tracked.txt") {
		t.Fatalf("emitted DiffText missing file path: %q", diff.DiffText)
	}
	if !strings.Contains(diff.DiffText, "-before") || !strings.Contains(diff.DiffText, "+after") {
		t.Fatalf("emitted DiffText = %q, want before/after lines", diff.DiffText)
	}
}

func TestToolBridge_RouteToolCall_EmitsDiffWithInjectedCWD(t *testing.T) {
	repo := initGitRepo(t, map[string]string{"tracked.txt": "before\n"})
	args := mustRawJSON(t, map[string]any{"file_path": "tracked.txt", "patch": "@@\n-before\n+after\n"})
	peer := &mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(_ context.Context, method string, params any, result any) error {
		if method != "tools/call" {
			t.Fatalf("Callback() method = %q, want tools/call", method)
		}
		if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("after\n"), 0o644); err != nil {
			return err
		}
		resp, ok := result.(*peerToolCallResponse)
		if !ok {
			t.Fatalf("Callback() result type = %T, want *peerToolCallResponse", result)
		}
		*resp = peerToolCallResponse{Content: []peerToolCallContent{{Type: "text", Text: "diff ok"}}}
		return nil
	}}}
	h, _ := newHandlerForTest(peer)
	resolver := &stubCWDResolver{cwd: initGitRepo(t, map[string]string{"tracked.txt": "stale\n"})}
	h.resolver = resolver
	var emitted []difftracker.DiffResult
	h.emitter = func(_ context.Context, diff difftracker.DiffResult) error {
		emitted = append(emitted, diff)
		return nil
	}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "patch_edit",
		Arguments: args,
		AgentID:   "agent-9",
		ThreadID:  "thread-9",
		CallID:    "call-9",
		CWD:       repo,
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "diff ok", true)
	if len(emitted) != 1 {
		t.Fatalf("emitted diff count = %d, want 1", len(emitted))
	}
	if resolver.callSeen {
		t.Fatalf("resolver should not be called when request carries trusted cwd")
	}
	if len(emitted[0].Files) != 1 || emitted[0].Files[0] != "tracked.txt" {
		t.Fatalf("emitted Files = %#v, want [tracked.txt]", emitted[0].Files)
	}
}

func initGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "toolbridge@test")
	runGit(t, repo, "config", "user.name", "Toolbridge Test")
	for name, content := range files {
		path := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "init")
	return repo
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s error = %v\n%s", strings.Join(args, " "), err, string(output))
	}
}
