package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
)

func TestMCPOrchOrchestrationFacadeRequiresTools(t *testing.T) {
	facade := newMCPOrchOrchestrationFacade(newToolbridgeHandlerRef())
	if facade == nil {
		t.Fatal("newMCPOrchOrchestrationFacade(ref) returned nil")
	}

	ctx := context.Background()
	assertFacadeNotConfigured(t, "LaunchAgent", facade.LaunchAgent(ctx, thread.LaunchAgentRequest{AgentID: "agent-1"}), "toolbridge handler")
	assertFacadeNotConfigured(t, "StopAgent", facade.StopAgent(ctx, "agent-1"), "toolbridge handler")
	assertFacadeNotConfigured(t, "Recover", facade.Recover(ctx, "agent-1"), "toolbridge handler")

	if err := facade.BindSessionGeneration(ctx, "agent-1", 1); err != nil {
		t.Fatalf("BindSessionGeneration() error = %v, want nil (no-op)", err)
	}
}

func TestMCPOrchOrchestrationFacadeLaunchAgentAddsLifecycleWorkspaceMetadata(t *testing.T) {
	t.Parallel()

	caller := &recordingThreadOrchToolCaller{}
	facade := &mcpOrchOrchestrationFacade{tools: caller}

	err := facade.LaunchAgent(context.Background(), thread.LaunchAgentRequest{
		AgentID:     " agent-1 ",
		Name:        " Worker ",
		ParentID:    " parent-1 ",
		AgentType:   " assistant ",
		MemoryScope: " project ",
		Cwd:         " /repo/project ",
	})
	if err != nil {
		t.Fatalf("LaunchAgent() error = %v", err)
	}

	params := decodeThreadOrchToolParams(t, caller.params)
	if got := params[toolbridge.MetadataKeyCWD]; got != "/repo/project" {
		t.Fatalf("toolbridge %s = %#v, want /repo/project", toolbridge.MetadataKeyCWD, got)
	}
	if got := params[toolbridge.MetadataKeyAgentID]; got != "agent-1" {
		t.Fatalf("toolbridge %s = %#v, want agent-1", toolbridge.MetadataKeyAgentID, got)
	}
	roots, ok := params[toolbridge.MetadataKeyWorkspaceRoots].([]any)
	if !ok {
		t.Fatalf("toolbridge %s = %#v, want []any", toolbridge.MetadataKeyWorkspaceRoots, params[toolbridge.MetadataKeyWorkspaceRoots])
	}
	if len(roots) != 1 || roots[0] != "/repo/project" {
		t.Fatalf("toolbridge %s = %#v, want [/repo/project]", toolbridge.MetadataKeyWorkspaceRoots, roots)
	}
}

func assertFacadeNotConfigured(t *testing.T, op string, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s() error = nil, want not configured error", op)
	}
	if !strings.Contains(err.Error(), "not configured") || !strings.Contains(err.Error(), want) {
		t.Fatalf("%s() error = %q, want not configured failure containing %q", op, err.Error(), want)
	}
}

type recordingThreadOrchToolCaller struct {
	params json.RawMessage
}

func (c *recordingThreadOrchToolCaller) HandleToolCall(_ context.Context, msg contract.ToolCallRawMessage) (any, error) {
	c.params = append(c.params[:0], msg.Params...)
	return &toolbridge.ToolCallResult{Success: true}, nil
}

func decodeThreadOrchToolParams(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("decode tool params: %v", err)
	}
	return params
}
