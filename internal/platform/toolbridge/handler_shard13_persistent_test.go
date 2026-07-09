package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

const persistentSubagentDefaultBlockText = "当前会话启用了 persistent_subagent_default：禁止使用 `spawn_agent` 创建临时子 agent。请改用 `launch_agent` 创建持续化 UI 子 agent；等待单个子 agent 用 `get_agent_report(wait=true)`，等待多个子 agent 用 `get_agent_reports(wait=true)`。"

func TestToolBridge_RejectsSpawnAgentWhenPersistentSubagentDefaultEnabled(t *testing.T) {
	args := mustRawJSON(t, map[string]any{"message": "create child agent"})
	h, registry := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
		t.Fatal("Callback() should not be invoked when spawn_agent is blocked")
		return nil
	}}})
	raw := mustRawJSON(t, map[string]any{
		"runtime": map[string]any{
			"enabledTools": []string{"spawn_agent", "launch_agent"},
			"sessionFlags": map[string]any{"persistent_subagent_default": true},
		},
	})
	h.threadStore = &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-1", ConfigOverride: raw}}
	h.cfg = &platformconfig.Config{Agent: platformconfig.AgentConfig{PersistentSubagentDefault: true}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: args,
		ThreadID:  "thread-1",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, persistentSubagentDefaultBlockText, false)
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestToolBridge_RejectsSpawnAgentWhenPersistentSubagentDefaultEnabledWithShortLaunchTool(t *testing.T) {
	args := mustRawJSON(t, map[string]any{"message": "create child agent"})
	h, registry := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
		t.Fatal("Callback() should not be invoked when spawn_agent is blocked")
		return nil
	}}})
	raw := mustRawJSON(t, map[string]any{
		"runtime": map[string]any{
			"enabledTools": []string{"spawn_agent", "launch_agent"},
			"sessionFlags": map[string]any{"persistent_subagent_default": true},
		},
	})
	h.threadStore = &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-short-launch", ConfigOverride: raw}}
	h.cfg = &platformconfig.Config{Agent: platformconfig.AgentConfig{PersistentSubagentDefault: true}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: args,
		ThreadID:  "thread-short-launch",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, persistentSubagentDefaultBlockText, false)
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestToolBridge_RejectsSpawnAgentFromChildAgent(t *testing.T) {
	args := mustRawJSON(t, map[string]any{"message": "create child agent"})
	h, registry := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
		t.Fatal("Callback() should not be invoked when child agent spawn_agent is blocked")
		return nil
	}}})
	h.bindingStore = &toolCallBindingStoreStub{bindingsByAgent: map[string]toolCallBinding{
		"agent-child": {
			AgentID:       "agent-child",
			ParentAgentID: "agent-root",
		},
	}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: args,
		AgentID:   "agent-child",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "Sub-agents are not allowed to spawn further agents (delegation depth limit).", false)
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestPersistentSubagentAllowsExplicitRuntimeFlagFalse(t *testing.T) {
	args := mustRawJSON(t, map[string]any{"message": "create child agent"})
	h, registry := newHandlerForTest(newToolCallPeer(t, "spawn_agent", args, "spawned", nil))
	raw := mustRawJSON(t, map[string]any{
		"runtime": map[string]any{
			"enabledTools": []string{"spawn_agent", "launch_agent"},
			"sessionFlags": map[string]any{"persistent_subagent_default": false},
		},
	})
	h.threadStore = &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-flag-false", ConfigOverride: raw}}
	h.cfg = &platformconfig.Config{Agent: platformconfig.AgentConfig{PersistentSubagentDefault: true}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: args,
		ThreadID:  "thread-flag-false",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, "spawned", true)
	if len(registry.gotKinds) != 1 || registry.gotKinds[0] != dto.ClientKindOrch {
		t.Fatalf("FindActiveByKind() kinds = %#v, want [%q]", registry.gotKinds, dto.ClientKindOrch)
	}
}

func TestPersistentSubagentRequiresExplicitRuntimeFlag(t *testing.T) {
	h, registry := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
		t.Fatal("Callback() should not be invoked when persistent-subagent flag is absent")
		return nil
	}}})
	raw := mustRawJSON(t, map[string]any{
		"runtime": map[string]any{
			"enabledTools": []string{"spawn_agent", "launch_agent"},
		},
	})
	h.threadStore = &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-missing-flag", ConfigOverride: raw}}
	h.cfg = &platformconfig.Config{Agent: platformconfig.AgentConfig{PersistentSubagentDefault: true}}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: mustRawJSON(t, map[string]any{"message": "create child agent"}),
		ThreadID:  "thread-missing-flag",
	})
	if !errors.Is(err, contract.ErrPersistentSubagentFlagRequired) {
		t.Fatalf("routeToolCall() error = %v, want %v", err, contract.ErrPersistentSubagentFlagRequired)
	}
	if got != nil {
		t.Fatalf("routeToolCall() result = %#v, want nil", got)
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestPersistentSubagentAllowsLegacyOptInFallback(t *testing.T) {
	t.Setenv(allowDefaultPersistentSubagentEnv, "1")
	var logs bytes.Buffer
	h, registry := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
		t.Fatal("Callback() should not be invoked when legacy fallback blocks spawn_agent")
		return nil
	}}})
	h.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	raw := mustRawJSON(t, map[string]any{
		"runtime": map[string]any{
			"enabledTools": []string{"spawn_agent", "launch_agent"},
		},
	})
	h.threadStore = &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-legacy-fallback", ConfigOverride: raw}}
	h.cfg = &platformconfig.Config{Agent: platformconfig.AgentConfig{PersistentSubagentDefault: true}}
	before := persistentSubagentDefaultFallbackCount()

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: mustRawJSON(t, map[string]any{"message": "create child agent"}),
		ThreadID:  "thread-legacy-fallback",
	})
	if err != nil {
		t.Fatalf("routeToolCall() error = %v", err)
	}
	assertSingleTextItem(t, got, persistentSubagentDefaultBlockText, false)
	if after := persistentSubagentDefaultFallbackCount(); after != before+1 {
		t.Fatalf("persistentSubagentDefaultFallbackCount() = %d, want %d", after, before+1)
	}
	if !strings.Contains(logs.String(), "compatibility-only: persistent subagent default fallback") {
		t.Fatalf("logs = %q, want compatibility warning", logs.String())
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestProxyMapsPersistentSubagentFlagRequired(t *testing.T) {
	h, registry := newHandlerForTest(&mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
		t.Fatal("Callback() should not be invoked when persistent-subagent flag is absent")
		return nil
	}}})
	raw := mustRawJSON(t, map[string]any{
		"runtime": map[string]any{
			"enabledTools": []string{"spawn_agent", "launch_agent"},
		},
	})
	h.bindingStore = &toolCallBindingStoreStub{threadID: "thread-proxy-missing-flag"}
	h.threadStore = &stubThreadStore{thread: &threadstore.Thread{ThreadID: "thread-proxy-missing-flag", ConfigOverride: raw}}
	body := string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-flag",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "spawn_agent",
			"arguments": map[string]any{},
		},
	}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-flag", body)
	if got.Error == nil {
		t.Fatal("proxy response error = nil, want invalid params")
	}
	if got.Error.Code != jsonRPCCodeInvalidParam {
		t.Fatalf("proxy error code = %d, want %d", got.Error.Code, jsonRPCCodeInvalidParam)
	}
	if !strings.Contains(got.Error.Message, contract.ErrPersistentSubagentFlagRequired.Error()) {
		t.Fatalf("proxy error message = %q, want substring %q", got.Error.Message, contract.ErrPersistentSubagentFlagRequired.Error())
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}

func TestToolBridge_RejectsSpawnAgentWithoutThreadRuntime(t *testing.T) {
	h, _ := newHandlerForTest()

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: mustRawJSON(t, map[string]any{"message": "create child agent"}),
	})
	if !errors.Is(err, contract.ErrThreadRuntimeRequired) {
		t.Fatalf("routeToolCall() error = %v, want %v", err, contract.ErrThreadRuntimeRequired)
	}
	if got != nil {
		t.Fatalf("routeToolCall() result = %#v, want nil", got)
	}
}

func TestToolBridge_RejectsSpawnAgentWithoutStoredRuntime(t *testing.T) {
	h, _ := newHandlerForTest()
	h.threadStore = &stubThreadStore{}

	got, err := h.routeToolCall(context.Background(), ToolCallRequest{
		Name:      "spawn_agent",
		Arguments: mustRawJSON(t, map[string]any{"message": "create child agent"}),
		ThreadID:  "thread-missing-runtime",
	})
	if !errors.Is(err, contract.ErrPersistentSubagentRuntimeRequired) {
		t.Fatalf("routeToolCall() error = %v, want %v", err, contract.ErrPersistentSubagentRuntimeRequired)
	}
	if got != nil {
		t.Fatalf("routeToolCall() result = %#v, want nil", got)
	}
}

func TestProxyToolCall_RejectsMissingRuntimeAsInvalidParams(t *testing.T) {
	h, registry := newHandlerForTest(newToolCallPeer(t, "spawn_agent", json.RawMessage(`{}`), "ignored", nil))
	body := string(mustRawJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      "req-1",
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "spawn_agent",
			"arguments": map[string]any{},
		},
	}))

	got := callProxyRequest(t, h, "/mcp/orch/agent-1", body)
	if got.Error == nil {
		t.Fatal("proxy response error = nil, want invalid params")
	}
	if got.Error.Code != jsonRPCCodeInvalidParam {
		t.Fatalf("proxy error code = %d, want %d", got.Error.Code, jsonRPCCodeInvalidParam)
	}
	if !strings.Contains(got.Error.Message, contract.ErrThreadRuntimeRequired.Error()) {
		t.Fatalf("proxy error message = %q, want substring %q", got.Error.Message, contract.ErrThreadRuntimeRequired.Error())
	}
	if len(registry.gotKinds) != 0 {
		t.Fatalf("FindActiveByKind() kinds = %#v, want none", registry.gotKinds)
	}
}
