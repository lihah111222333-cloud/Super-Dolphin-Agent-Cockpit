package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/supportutil"
)

func TestDriverStartSessionSelectsDefaultModelFromModelList(t *testing.T) {
	setDefaultCodexHomeEnvForTest(t)
	modelListCalled := make(chan struct{}, 1)
	startParams := make(chan map[string]any, 1)
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		switch msg.Method {
		case "model/list":
			select {
			case modelListCalled <- struct{}{}:
			default:
			}
			return mustJSON(map[string]any{
				"models": []map[string]any{
					{"id": "gpt-5"},
					{"id": "gpt-5-codex"},
				},
			})
		case "thread/start":
			var params map[string]any
			_ = json.Unmarshal(msg.Params, &params)
			startParams <- params
			return mustJSON(map[string]any{
				"thread": map[string]any{"id": "provider-thread-model-list", "cwd": "/repo"},
				"model":  "gpt-5",
			})
		case "initialize":
			return mustJSON(map[string]any{"ok": true})
		default:
			return mustJSON(map[string]any{"ok": true})
		}
	})
	d := &driver{
		approvals:    testApprovalManager(),
		skillMetrics: testSkillMetrics(t),
		pool:         newSingleURLPoolForTest(t, serverURL),
		mirror:       &recordingSkillMirrorReconciler{},
		listTools:    noopCodexToolLister,
	}

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider:      "codex",
		AgentID:       "agent-model-list",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s := mustCodexSession(t, got, "StartSession")
	defer closeCodexTestSession(t, s)

	select {
	case <-modelListCalled:
	default:
		t.Fatal("model/list was not called for blank start model")
	}
	select {
	case params := <-startParams:
		if params["model"] != "gpt-5" {
			t.Fatalf("thread/start model = %#v, want gpt-5; params=%#v", params["model"], params)
		}
	default:
		t.Fatal("thread/start params were not captured")
	}
	assertRuntimeConfigValue(t, s, "model", "gpt-5")
}

func TestThreadStartFailsWhenRequiredModelListFails(t *testing.T) {
	setDefaultCodexHomeEnvForTest(t)
	threadStartCalls := 0
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		switch msg.Method {
		case "model/list":
			return mustJSON(map[string]any{"models": []map[string]any{}})
		case "thread/start":
			threadStartCalls++
			return mustJSON(map[string]any{"thread": map[string]any{"id": "unexpected-thread"}})
		case "initialize":
			return mustJSON(map[string]any{"ok": true})
		default:
			return mustJSON(map[string]any{"ok": true})
		}
	})
	d := &driver{
		approvals:    testApprovalManager(),
		skillMetrics: testSkillMetrics(t),
		pool:         newSingleURLPoolForTest(t, serverURL),
		mirror:       &recordingSkillMirrorReconciler{},
		listTools:    noopCodexToolLister,
	}

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider:      "codex",
		AgentID:       "agent-model-list-fail",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
	})
	if err == nil {
		if got != nil {
			closeCodexTestSession(t, mustCodexSession(t, got, "StartSession"))
		}
		t.Fatal("StartSession() error = nil, want required model resolution error")
	}
	if !errors.Is(err, supportutil.ErrModelResolutionRequired) {
		t.Fatalf("StartSession() error = %v, want ErrModelResolutionRequired", err)
	}
	if threadStartCalls != 0 {
		t.Fatalf("thread/start calls = %d, want 0 after required model/list failure", threadStartCalls)
	}
}

func TestThreadStartConfigGPTDefaultRequiresModelList(t *testing.T) {
	setDefaultCodexHomeEnvForTest(t)
	modelListCalls := 0
	threadStartCalls := 0
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		switch msg.Method {
		case "model/list":
			modelListCalls++
			return mustJSON(map[string]any{"models": []map[string]any{}})
		case "thread/start":
			threadStartCalls++
			return mustJSON(map[string]any{"thread": map[string]any{"id": "unexpected-thread"}})
		case "initialize":
			return mustJSON(map[string]any{"ok": true})
		default:
			return mustJSON(map[string]any{"ok": true})
		}
	})
	d := &driver{
		approvals:    testApprovalManager(),
		skillMetrics: testSkillMetrics(t),
		pool:         newSingleURLPoolForTest(t, serverURL),
		mirror:       &recordingSkillMirrorReconciler{},
		listTools:    noopCodexToolLister,
	}

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider:      "codex",
		AgentID:       "agent-config-model-list-fail",
		CWD:           t.TempDir(),
		Config:        map[string]any{"model": " gpt-5.5 "},
		StartAssembly: validStartAssemblyForTest(),
	})
	if err == nil {
		if got != nil {
			closeCodexTestSession(t, mustCodexSession(t, got, "StartSession"))
		}
		t.Fatal("StartSession() error = nil, want required model resolution error")
	}
	if !errors.Is(err, supportutil.ErrModelResolutionRequired) {
		t.Fatalf("StartSession() error = %v, want ErrModelResolutionRequired", err)
	}
	if !strings.Contains(err.Error(), "gpt-5.5") {
		t.Fatalf("StartSession() error = %v, want requested config model in error", err)
	}
	if modelListCalls != 1 {
		t.Fatalf("model/list calls = %d, want 1", modelListCalls)
	}
	if threadStartCalls != 0 {
		t.Fatalf("thread/start calls = %d, want 0 after required config model/list empty", threadStartCalls)
	}
}

func TestDriverStartSessionPreservesExplicitOverrideGPT5Model(t *testing.T) {
	setDefaultCodexHomeEnvForTest(t)
	modelListCalls := 0
	startParams := make(chan map[string]any, 1)
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
		case "thread/start":
			var params map[string]any
			_ = json.Unmarshal(msg.Params, &params)
			startParams <- params
			return mustJSON(map[string]any{
				"thread": map[string]any{"id": "provider-thread-model-replaced", "cwd": "/repo"},
				"model":  "gpt-5",
			})
		case "initialize":
			return mustJSON(map[string]any{"ok": true})
		default:
			return mustJSON(map[string]any{"ok": true})
		}
	})
	d := &driver{
		approvals:    testApprovalManager(),
		skillMetrics: testSkillMetrics(t),
		pool:         newSingleURLPoolForTest(t, serverURL),
		mirror:       &recordingSkillMirrorReconciler{},
		listTools:    noopCodexToolLister,
	}

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider:      "codex",
		AgentID:       "agent-model-replaced",
		CWD:           t.TempDir(),
		Model:         "gpt-5",
		StartAssembly: validStartAssemblyForTest(),
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s := mustCodexSession(t, got, "StartSession")
	defer closeCodexTestSession(t, s)

	select {
	case params := <-startParams:
		if params["model"] != "gpt-5" {
			t.Fatalf("thread/start model = %#v, want gpt-5; params=%#v", params["model"], params)
		}
	default:
		t.Fatal("thread/start params were not captured")
	}
	if modelListCalls != 0 {
		t.Fatalf("model/list calls = %d, want 0 for explicit req.Model", modelListCalls)
	}
	assertRuntimeConfigValue(t, s, "model", "gpt-5")
}

func TestDriverStartSessionPreservesExplicitOverrideGPT55Model(t *testing.T) {
	setDefaultCodexHomeEnvForTest(t)
	modelListCalls := 0
	startParams := make(chan map[string]any, 1)
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		switch msg.Method {
		case "model/list":
			modelListCalls++
			return mustJSON(map[string]any{
				"models": []map[string]any{
					{"id": "gpt-5.3-codex"},
				},
			})
		case "thread/start":
			var params map[string]any
			_ = json.Unmarshal(msg.Params, &params)
			startParams <- params
			return mustJSON(map[string]any{
				"thread": map[string]any{"id": "provider-thread-gpt55", "cwd": "/repo"},
				"model":  "gpt-5.5",
			})
		case "initialize":
			return mustJSON(map[string]any{"ok": true})
		default:
			return mustJSON(map[string]any{"ok": true})
		}
	})
	d := &driver{
		approvals:    testApprovalManager(),
		skillMetrics: testSkillMetrics(t),
		pool:         newSingleURLPoolForTest(t, serverURL),
		mirror:       &recordingSkillMirrorReconciler{},
		listTools:    noopCodexToolLister,
	}

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider:      "codex",
		AgentID:       "agent-gpt55-preserved",
		CWD:           t.TempDir(),
		Model:         "gpt-5.5",
		StartAssembly: validStartAssemblyForTest(),
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s := mustCodexSession(t, got, "StartSession")
	defer closeCodexTestSession(t, s)

	select {
	case params := <-startParams:
		if params["model"] != "gpt-5.5" {
			t.Fatalf("thread/start model = %#v, want gpt-5.5; params=%#v", params["model"], params)
		}
	default:
		t.Fatal("thread/start params were not captured")
	}
	if modelListCalls != 0 {
		t.Fatalf("model/list calls = %d, want 0 for explicit req.Model", modelListCalls)
	}
	assertRuntimeConfigValue(t, s, "model", "gpt-5.5")
}
