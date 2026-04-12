package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
		{name: "rename", toolName: "lsp_edit", arguments: mustRawJSON(t, map[string]any{"action": "rename"}), want: true},
		{name: "replace_range", toolName: "lsp_edit", arguments: mustRawJSON(t, map[string]any{"action": "replace_range"}), want: true},
		{name: "format", toolName: "lsp_edit", arguments: mustRawJSON(t, map[string]any{"action": "format"}), want: false},
		{name: "other tool", toolName: "lsp_hover", arguments: mustRawJSON(t, map[string]any{"action": "rename"}), want: false},
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
		Name:      "lsp_hover",
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

func TestToolBridge_RouteToolCall_EmitsDiffForTrackedLspEdit(t *testing.T) {
	repo := initGitRepo(t, map[string]string{"tracked.txt": "before\n"})
	args := mustRawJSON(t, map[string]any{"action": "rename"})
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
	var emitted []difftracker.DiffResult
	h.resolver = resolverFunc(func(context.Context, string) (string, error) {
		return repo, nil
	})
	h.emitter = func(_ context.Context, diff difftracker.DiffResult) error {
		emitted = append(emitted, diff)
		return nil
	}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "lsp_edit",
		Arguments: args,
		AgentID:   "agent-9",
		ThreadID:  "thread-9",
		CallID:    "call-9",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "diff ok", true)
	if len(emitted) != 1 {
		t.Fatalf("emitted diff count = %d, want 1", len(emitted))
	}
	if emitted[0].AgentID != "agent-9" || emitted[0].ThreadID != "thread-9" || emitted[0].CallID != "call-9" {
		t.Fatalf("emitted metadata = %+v, want agent/thread/call ids", emitted[0])
	}
	if emitted[0].ToolName != "lsp_edit" {
		t.Fatalf("emitted ToolName = %q, want %q", emitted[0].ToolName, "lsp_edit")
	}
	if len(emitted[0].Files) != 1 || emitted[0].Files[0] != "tracked.txt" {
		t.Fatalf("emitted Files = %#v, want [tracked.txt]", emitted[0].Files)
	}
	if !strings.Contains(emitted[0].DiffText, "tracked.txt") {
		t.Fatalf("emitted DiffText missing file path: %q", emitted[0].DiffText)
	}
	if !strings.Contains(emitted[0].DiffText, "-before") || !strings.Contains(emitted[0].DiffText, "+after") {
		t.Fatalf("emitted DiffText = %q, want before/after lines", emitted[0].DiffText)
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
		t.Fatalf("git %s error = %v\n%s", strings.Join(args, " "), err, fmt.Sprintf("%s", output))
	}
}
