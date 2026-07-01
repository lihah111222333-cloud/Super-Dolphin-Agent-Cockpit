package codexapp

import (
	"context"
	"encoding/json"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
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
		pool:      newSingleURLPoolForTest(t, serverURL),
		mirror:    &recordingSkillMirrorReconciler{},
		listTools: noopCodexToolLister,
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

func TestDriverStartSessionPreservesExplicitGPT5Model(t *testing.T) {
	setDefaultCodexHomeEnvForTest(t)
	startParams := make(chan map[string]any, 1)
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		switch msg.Method {
		case "model/list":
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
		pool:      newSingleURLPoolForTest(t, serverURL),
		mirror:    &recordingSkillMirrorReconciler{},
		listTools: noopCodexToolLister,
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
	assertRuntimeConfigValue(t, s, "model", "gpt-5")
}

func TestDriverStartSessionPreservesExplicitGPT55Model(t *testing.T) {
	setDefaultCodexHomeEnvForTest(t)
	startParams := make(chan map[string]any, 1)
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		switch msg.Method {
		case "model/list":
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
		pool:      newSingleURLPoolForTest(t, serverURL),
		mirror:    &recordingSkillMirrorReconciler{},
		listTools: noopCodexToolLister,
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
	assertRuntimeConfigValue(t, s, "model", "gpt-5.5")
}
