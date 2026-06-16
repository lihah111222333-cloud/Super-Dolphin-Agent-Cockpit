package codexapp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestStartTurnAppliesTurnToolScopeRuntimeConfig(t *testing.T) {
	serverURL := startCodexRPCServer(t, func(method string) json.RawMessage {
		if method == "turn/start" {
			return mustJSON(map[string]any{"turn": map[string]any{"id": "turn-1"}})
		}
		return mustJSON(map[string]any{"ok": true})
	})
	s, err := newSession(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	s.setRuntimeConfig(map[string]any{
		"cwd":                          "/old",
		"additionalWorkingDirectories": []string{"/old-extra"},
	})

	handle, err := s.StartTurn(context.Background(), dto.TurnRequest{
		ThreadID:                     "provider-thread-1",
		CWD:                          " /new ",
		AdditionalWorkingDirectories: []string{" /new-extra "},
		Inputs:                       []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if handle.ProviderID() != "turn-1" {
		t.Fatalf("ProviderID() = %q, want turn-1", handle.ProviderID())
	}

	got := s.RuntimeConfigSnapshot()
	if got["cwd"] != "/new" {
		t.Fatalf("runtime cwd = %#v, want /new", got["cwd"])
	}
	if roots := providershared.ConfigStringSlice(got, "additionalWorkingDirectories"); len(roots) != 1 || roots[0] != "/new-extra" {
		t.Fatalf("runtime additionalWorkingDirectories = %#v, want [/new-extra]", got["additionalWorkingDirectories"])
	}
}

func TestStartTurnAdvertisesDynamicToolsFromTurnMCPManifest(t *testing.T) {
	turnParams := make(chan map[string]any, 1)
	serverURL := startTurnDynamicToolsRPCServer(t, turnParams)
	s, err := newSession(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	workDir := t.TempDir()
	s.setThreadID("provider-thread-1")
	s.setRuntimeConfig(map[string]any{"cwd": workDir})
	s.dynamicToolsEnabled = true
	prepareCalls := 0
	var gotScope contract.CodexToolSurfaceScope
	s.prepareTools = func(_ context.Context, scope contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error) {
		prepareCalls++
		gotScope = scope
		return []codexprotocol.DynamicToolSchema{{
			Name:        "query",
			Description: "readonly postgres query",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
		}}, nil
	}

	handle, err := s.StartTurn(context.Background(), dto.TurnRequest{
		ThreadID: "provider-thread-1",
		CWD:      workDir,
		Inputs:   []dto.InputItem{{Type: "text", Content: "select now()"}},
		MCP: dto.MCPManifest{Binaries: []dto.MCPBinary{{
			Name:    "postgres",
			Command: []string{"mcp-server-postgres", "postgres://readonly@example.test/app"},
		}}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if handle.ProviderID() != "turn-pg" {
		t.Fatalf("ProviderID() = %q, want turn-pg", handle.ProviderID())
	}
	if prepareCalls != 1 {
		t.Fatalf("prepareTools calls = %d, want 1", prepareCalls)
	}
	assertTurnMCPToolScope(t, gotScope, workDir)
	assertTurnStartQueryTool(t, <-turnParams)
}

func startTurnDynamicToolsRPCServer(t *testing.T, turnParams chan<- map[string]any) string {
	t.Helper()
	return startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		if msg.Method != "turn/start" {
			return mustJSON(map[string]any{"ok": true})
		}
		var params map[string]any
		_ = json.Unmarshal(msg.Params, &params)
		turnParams <- params
		return mustJSON(map[string]any{"turn": map[string]any{"id": "turn-pg"}})
	})
}

func assertTurnMCPToolScope(t *testing.T, got contract.CodexToolSurfaceScope, workDir string) {
	t.Helper()
	if got.AgentID != "agent-1" || got.ProviderThreadID != "provider-thread-1" || got.CWD != workDir {
		t.Fatalf("turn tool scope = %#v, want agent/provider thread/cwd", got)
	}
	if len(got.Manifest.Binaries) != 1 || got.Manifest.Binaries[0].Name != "postgres" || got.Manifest.Binaries[0].Command[0] != "mcp-server-postgres" {
		t.Fatalf("turn manifest binaries = %#v, want postgres mcp-server-postgres", got.Manifest.Binaries)
	}
}

func assertTurnStartQueryTool(t *testing.T, params map[string]any) {
	t.Helper()
	tools, ok := params["dynamicTools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("turn/start dynamicTools = %#v, want one query tool; params=%#v", params["dynamicTools"], params)
	}
	tool, ok := tools[0].(map[string]any)
	if !ok || tool["name"] != "query" {
		t.Fatalf("turn/start dynamicTools[0] = %#v, want query tool", tools[0])
	}
}

func TestStartTurnPreservesExplicitGPT5Model(t *testing.T) {
	turnParams := make(chan map[string]any, 1)
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		switch msg.Method {
		case "model/list":
			return mustJSON(map[string]any{
				"models": []map[string]any{
					{"id": "gpt-5"},
					{"id": "gpt-5-codex"},
				},
			})
		case "turn/start":
			var params map[string]any
			_ = json.Unmarshal(msg.Params, &params)
			turnParams <- params
			return mustJSON(map[string]any{"turn": map[string]any{"id": "turn-model-replaced"}})
		default:
			return mustJSON(map[string]any{"ok": true})
		}
	})
	s, err := newSession(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	s.setRuntimeConfig(map[string]any{"model": "gpt-5"})

	handle, err := s.StartTurn(context.Background(), dto.TurnRequest{
		ThreadID: "provider-thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "1+1=几"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if handle.ProviderID() != "turn-model-replaced" {
		t.Fatalf("ProviderID() = %q, want turn-model-replaced", handle.ProviderID())
	}
	select {
	case params := <-turnParams:
		if params["model"] != "gpt-5" {
			t.Fatalf("turn/start model = %#v, want gpt-5; params=%#v", params["model"], params)
		}
	default:
		t.Fatal("turn/start params were not captured")
	}
	if got := s.RuntimeConfigSnapshot()["model"]; got != "gpt-5" {
		t.Fatalf("runtime model = %#v, want gpt-5", got)
	}
}

func TestStartTurnNormalizesRuntimeMinimalEffortToLow(t *testing.T) {
	turnParams := make(chan map[string]any, 1)
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		switch msg.Method {
		case "turn/start":
			var params map[string]any
			_ = json.Unmarshal(msg.Params, &params)
			turnParams <- params
			return mustJSON(map[string]any{"turn": map[string]any{"id": "turn-1"}})
		default:
			return mustJSON(map[string]any{"ok": true})
		}
	})
	s, err := newSession(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	s.setThreadID("provider-thread-1")
	s.setRuntimeConfig(map[string]any{"effort": "minimal"})

	_, err = s.StartTurn(context.Background(), dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	select {
	case params := <-turnParams:
		if params["effort"] != "low" {
			t.Fatalf("turn/start effort = %#v, want low; params=%#v", params["effort"], params)
		}
	default:
		t.Fatal("turn/start params were not captured")
	}
}

func TestApplyTurnToolScopeRuntimeConfigUpdatesCWDAndRoots(t *testing.T) {
	s := &session{runtimeConfig: map[string]any{
		"cwd":                          "/old",
		"additionalWorkingDirectories": []string{"/old-extra"},
	}}

	err := s.applyTurnToolScopeRuntimeConfig(dto.TurnRequest{
		CWD:                          " /new ",
		AdditionalWorkingDirectories: []string{"/new-extra"},
	})
	if err != nil {
		t.Fatalf("applyTurnToolScopeRuntimeConfig() error = %v", err)
	}

	got := s.RuntimeConfigSnapshot()
	if got["cwd"] != "/new" {
		t.Fatalf("runtime cwd = %#v, want /new", got["cwd"])
	}
	if roots := providershared.ConfigStringSlice(got, "additionalWorkingDirectories"); len(roots) != 1 || roots[0] != "/new-extra" {
		t.Fatalf("runtime additionalWorkingDirectories = %#v, want [/new-extra]", got["additionalWorkingDirectories"])
	}
}

func TestApplyTurnToolScopeRuntimeConfigClearsStaleAdditionalRoots(t *testing.T) {
	s := &session{runtimeConfig: map[string]any{
		"cwd":                          "/old",
		"additionalWorkingDirectories": []string{"/old-extra"},
	}}

	err := s.applyTurnToolScopeRuntimeConfig(dto.TurnRequest{CWD: "/new"})
	if err != nil {
		t.Fatalf("applyTurnToolScopeRuntimeConfig() error = %v", err)
	}

	got := s.RuntimeConfigSnapshot()
	if got["cwd"] != "/new" {
		t.Fatalf("runtime cwd = %#v, want /new", got["cwd"])
	}
	if roots := got["additionalWorkingDirectories"]; roots != nil {
		t.Fatalf("runtime additionalWorkingDirectories = %#v, want cleared", roots)
	}
}

func TestApplyTurnToolScopeRuntimeConfigRejectsAdditionalRootsWithoutCWD(t *testing.T) {
	s := &session{runtimeConfig: map[string]any{"cwd": "/old"}}

	err := s.applyTurnToolScopeRuntimeConfig(dto.TurnRequest{
		AdditionalWorkingDirectories: []string{"/new-extra"},
	})
	if err == nil {
		t.Fatal("applyTurnToolScopeRuntimeConfig() error = nil, want error")
	}
	if got := s.RuntimeConfigSnapshot()["cwd"]; got != "/old" {
		t.Fatalf("runtime cwd = %#v, want old cwd preserved", got)
	}
}
