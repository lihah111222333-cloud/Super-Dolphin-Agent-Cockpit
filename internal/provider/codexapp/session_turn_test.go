package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	codexprotocol "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/supportutil"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestStartTurnAppliesTurnToolScopeRuntimeConfig(t *testing.T) {
	serverURL := startCodexRPCServer(t, func(method string) json.RawMessage {
		if method == "turn/start" {
			return mustJSON(map[string]any{"turn": map[string]any{"id": "turn-1"}})
		}
		return mustJSON(map[string]any{"ok": true})
	})
	s, err := newSessionWithOptions(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, testApprovalManager(), nil, withSkillMetrics(testSkillMetrics(t)), withLogRuntime(testLoggerRuntime(t)))
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

func TestStartTurnRejectsUnknownInputBeforeRuntimeConfigMutation(t *testing.T) {
	turnStartCalls := 0
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		if msg.Method != "turn/start" {
			return mustJSON(map[string]any{"ok": true})
		}
		turnStartCalls++
		return mustJSON(map[string]any{"turn": map[string]any{"id": "turn-unexpected"}})
	})
	s, err := newSessionWithOptions(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, testApprovalManager(), nil, withSkillMetrics(testSkillMetrics(t)), withLogRuntime(testLoggerRuntime(t)))
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	s.setThreadID("provider-thread-1")
	s.setRuntimeConfig(map[string]any{
		"cwd":                          "/old",
		"additionalWorkingDirectories": []string{"/old-extra"},
	})

	_, err = s.StartTurn(context.Background(), dto.TurnRequest{
		CWD:                          "/new",
		AdditionalWorkingDirectories: []string{"/new-extra"},
		Inputs:                       []dto.InputItem{{Type: "mystery", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("StartTurn() error = nil, want unsupported input type")
	}
	if turnStartCalls != 0 {
		t.Fatalf("turn/start calls = %d, want no provider call after invalid input", turnStartCalls)
	}
	got := s.RuntimeConfigSnapshot()
	if got["cwd"] != "/old" {
		t.Fatalf("runtime cwd = %#v, want unchanged /old", got["cwd"])
	}
	if roots := providershared.ConfigStringSlice(got, "additionalWorkingDirectories"); len(roots) != 1 || roots[0] != "/old-extra" {
		t.Fatalf("runtime additionalWorkingDirectories = %#v, want unchanged [/old-extra]", got["additionalWorkingDirectories"])
	}
}

func TestStartTurnAdvertisesDynamicToolsFromTurnMCPManifest(t *testing.T) {
	turnParams := make(chan map[string]any, 1)
	serverURL := startTurnDynamicToolsRPCServer(t, turnParams)
	s, err := newSessionWithOptions(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, testApprovalManager(), nil, withSkillMetrics(testSkillMetrics(t)), withLogRuntime(testLoggerRuntime(t)))
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
			Description: "readonly sqlite query",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"}}}`),
		}}, nil
	}

	handle, err := s.StartTurn(context.Background(), dto.TurnRequest{
		ThreadID: "provider-thread-1",
		CWD:      workDir,
		Inputs:   []dto.InputItem{{Type: "text", Content: "select now()"}},
		MCP: dto.MCPManifest{Binaries: []dto.MCPBinary{{
			Name:    "sqlite",
			Command: []string{"npx", "-y", "@bytebase/dbhub@0.23.0", "--dsn=sqlite:///tmp/test.db"},
		}}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if handle.ProviderID() != "turn-sqlite" {
		t.Fatalf("ProviderID() = %q, want turn-sqlite", handle.ProviderID())
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
		return mustJSON(map[string]any{"turn": map[string]any{"id": "turn-sqlite"}})
	})
}

func assertTurnMCPToolScope(t *testing.T, got contract.CodexToolSurfaceScope, workDir string) {
	t.Helper()
	if got.AgentID != "agent-1" || got.ProviderThreadID != "provider-thread-1" || got.CWD != workDir {
		t.Fatalf("turn tool scope = %#v, want agent/provider thread/cwd", got)
	}
	if len(got.Manifest.Binaries) != 1 || got.Manifest.Binaries[0].Name != "sqlite" || got.Manifest.Binaries[0].Command[0] != "npx" {
		t.Fatalf("turn manifest binaries = %#v, want sqlite dbhub", got.Manifest.Binaries)
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

func TestStartTurnPreservesExplicitOverrideGPT5Model(t *testing.T) {
	modelListCalls := 0
	turnParams := make(chan map[string]any, 1)
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		switch msg.Method {
		case "model/list":
			modelListCalls++
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
	s, err := newSessionWithOptions(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, testApprovalManager(), nil, withSkillMetrics(testSkillMetrics(t)), withLogRuntime(testLoggerRuntime(t)))
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })

	handle, err := s.StartTurn(context.Background(), dto.TurnRequest{
		ThreadID: "provider-thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "1+1=几"}},
		Overrides: dto.TurnOverrides{
			Model: "gpt-5",
		},
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
	if modelListCalls != 0 {
		t.Fatalf("model/list calls = %d, want 0 for explicit turn override", modelListCalls)
	}
}

func TestStartTurnRuntimeGPTDefaultRequiresModelList(t *testing.T) {
	modelListCalls := 0
	turnStartCalls := 0
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		switch msg.Method {
		case "model/list":
			modelListCalls++
			return mustJSON(map[string]any{"models": []map[string]any{}})
		case "turn/start":
			turnStartCalls++
			return mustJSON(map[string]any{"turn": map[string]any{"id": "unexpected-turn"}})
		default:
			return mustJSON(map[string]any{"ok": true})
		}
	})
	s, err := newSessionWithOptions(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, testApprovalManager(), nil, withSkillMetrics(testSkillMetrics(t)), withLogRuntime(testLoggerRuntime(t)))
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	s.setThreadID("provider-thread-1")
	s.setRuntimeConfig(map[string]any{"model": " gpt-5.5 "})

	_, err = s.StartTurn(context.Background(), dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("StartTurn() error = nil, want required model resolution error")
	}
	if !errors.Is(err, supportutil.ErrModelResolutionRequired) {
		t.Fatalf("StartTurn() error = %v, want ErrModelResolutionRequired", err)
	}
	if !strings.Contains(err.Error(), "gpt-5.5") {
		t.Fatalf("StartTurn() error = %v, want requested runtime model in error", err)
	}
	if modelListCalls != 1 {
		t.Fatalf("model/list calls = %d, want 1", modelListCalls)
	}
	if turnStartCalls != 0 {
		t.Fatalf("turn/start calls = %d, want 0 after required runtime model/list empty", turnStartCalls)
	}
}

func TestTurnStartFailsWhenRequiredModelListEmpty(t *testing.T) {
	modelListCalls := 0
	turnStartCalls := 0
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		switch msg.Method {
		case "model/list":
			modelListCalls++
			return mustJSON(map[string]any{"models": []map[string]any{}})
		case "turn/start":
			turnStartCalls++
			return mustJSON(map[string]any{"turn": map[string]any{"id": "unexpected-turn"}})
		default:
			return mustJSON(map[string]any{"ok": true})
		}
	})
	s, err := newSessionWithOptions(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, testApprovalManager(), nil, withSkillMetrics(testSkillMetrics(t)), withLogRuntime(testLoggerRuntime(t)))
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	s.setThreadID("provider-thread-1")

	_, err = s.StartTurn(context.Background(), dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("StartTurn() error = nil, want required model resolution error")
	}
	if !errors.Is(err, supportutil.ErrModelResolutionRequired) {
		t.Fatalf("StartTurn() error = %v, want ErrModelResolutionRequired", err)
	}
	if modelListCalls != 1 {
		t.Fatalf("model/list calls = %d, want 1", modelListCalls)
	}
	if turnStartCalls != 0 {
		t.Fatalf("turn/start calls = %d, want 0 after required model/list empty", turnStartCalls)
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
	s, err := newSessionWithOptions(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, testApprovalManager(), nil, withSkillMetrics(testSkillMetrics(t)), withLogRuntime(testLoggerRuntime(t)))
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

func TestStartTurnSendsRuntimeSandboxPolicy(t *testing.T) {
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
	s, err := newSessionWithOptions(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, testApprovalManager(), nil, withSkillMetrics(testSkillMetrics(t)), withLogRuntime(testLoggerRuntime(t)))
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	s.setThreadID("provider-thread-1")
	s.setRuntimeConfig(map[string]any{
		"sandboxPolicy": map[string]any{
			"type":          "workspaceWrite",
			"writableRoots": []string{"/repo/app"},
			"networkAccess": false,
		},
	})

	_, err = s.StartTurn(context.Background(), dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	select {
	case params := <-turnParams:
		assertTurnStartSandboxPolicy(t, params)
	default:
		t.Fatal("turn/start params were not captured")
	}
}

func assertTurnStartSandboxPolicy(t *testing.T, params map[string]any) {
	t.Helper()
	policy, ok := params["sandboxPolicy"].(map[string]any)
	if !ok {
		t.Fatalf("turn/start sandboxPolicy = %#v, want object; params=%#v", params["sandboxPolicy"], params)
	}
	if policy["type"] != "workspaceWrite" {
		t.Fatalf("sandboxPolicy.type = %#v, want workspaceWrite; policy=%#v", policy["type"], policy)
	}
	roots, ok := policy["writableRoots"].([]any)
	if !ok || len(roots) != 1 || roots[0] != "/repo/app" {
		t.Fatalf("sandboxPolicy.writableRoots = %#v, want [/repo/app]", policy["writableRoots"])
	}
	if policy["networkAccess"] != false {
		t.Fatalf("sandboxPolicy.networkAccess = %#v, want false", policy["networkAccess"])
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
