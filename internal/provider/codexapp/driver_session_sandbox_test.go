package codexapp

import (
	"context"
	"encoding/json"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestDriverStartSessionCanonicalizesRuntimeCodexHome(t *testing.T) {
	home := t.TempDir()
	wantHome := mustCanonicalCodexHome(t, home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config := map[string]any{
		"codexHome":          "~/.codex",
		"codexInstanceKey":   "default",
		"codexModelProvider": "openai",
	}
	serverURL := startCodexRPCServer(t, func(method string) json.RawMessage {
		return canonicalCodexHomeResult(method, wantHome)
	})
	d := &driver{logRuntime: testLoggerRuntime(t),
		approvals:    testApprovalManager(),
		skillMetrics: testSkillMetrics(t),
		pool:         newSingleURLPoolForTest(t, serverURL),
		mirror:       &recordingSkillMirrorReconciler{},
		listTools:    noopCodexToolLister,
	}
	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider:      "codex",
		AgentID:       "agent-canonical",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
		Config:        config,
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s := mustCodexSession(t, got, "StartSession")
	defer closeCodexTestSession(t, s)
	assertRuntimeConfigValue(t, s, "codexHome", wantHome)
	assertCodexHomeConfigUnchanged(t, config)
}

// TestDriverStartSessionSendsRestrictedSandboxPolicyOnWire 确认受限沙箱策略会进入 thread/start wire payload。
func TestDriverStartSessionSendsRestrictedSandboxPolicyOnWire(t *testing.T) {
	t.Parallel()

	startParams := make(chan map[string]any, 1)
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		if msg.Method == "thread/start" {
			var params map[string]any
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				t.Fatalf("decode thread/start params: %v", err)
			}
			startParams <- params
			return mustJSON(map[string]any{
				"thread": map[string]any{"id": "provider-thread-sandbox", "cwd": "/repo"},
				"model":  "gpt-5.5",
			})
		}
		return mustJSON(map[string]any{"ok": true})
	})
	d := &driver{logRuntime: testLoggerRuntime(t),
		approvals:    testApprovalManager(),
		skillMetrics: testSkillMetrics(t),
		pool:         newSingleURLPoolForTest(t, serverURL),
		mirror:       &recordingSkillMirrorReconciler{},
		listTools:    noopCodexToolLister,
	}

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider:      "codex",
		AgentID:       "agent-sandbox",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
		Config: map[string]any{
			"sandbox": map[string]any{
				"type": "readOnly",
				"access": map[string]any{
					"type":                    "restricted",
					"readableRoots":           []string{"/repo/app", "/repo/docs"},
					"includePlatformDefaults": true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s := mustCodexSession(t, got, "StartSession")
	defer closeCodexTestSession(t, s)

	var params map[string]any
	select {
	case params = <-startParams:
	default:
		t.Fatal("thread/start params were not captured")
	}
	rawPolicy, err := json.Marshal(params["sandboxPolicy"])
	if err != nil {
		t.Fatalf("marshal wire sandboxPolicy: %v", err)
	}
	assertJSONEqual(t, rawPolicy, `{
		"type":"readOnly",
		"access":{
			"type":"restricted",
			"readableRoots":["/repo/app","/repo/docs"],
			"includePlatformDefaults":true
		}
	}`)
}
