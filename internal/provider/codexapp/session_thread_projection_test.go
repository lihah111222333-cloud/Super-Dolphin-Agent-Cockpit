package codexapp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const (
	publicThreadProjectionID   = "agent_1786682942942387000"
	providerThreadProjectionID = "019ffe9a-5f56-7620-9a4b-01fe679abce1"
)

func TestStartTurnWireUsesProviderThreadID(t *testing.T) {
	s, paramsCh := newThreadProjectionTestSession(t, "turn/start")

	_, err := s.StartTurn(context.Background(), dto.TurnRequest{
		ThreadID: publicThreadProjectionID,
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
		Overrides: dto.TurnOverrides{
			Model: "gpt-5",
		},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	assertProviderThreadProjection(t, "turn/start", receiveThreadProjectionParams(t, paramsCh))
}

func TestSteerWireUsesProviderThreadID(t *testing.T) {
	s, paramsCh := newThreadProjectionTestSession(t, "turn/steer")

	err := s.Steer(context.Background(), dto.SteerRequest{
		ThreadID:       publicThreadProjectionID,
		ExpectedTurnID: "turn-1",
		Inputs:         []dto.InputItem{{Type: "text", Content: "continue"}},
	})
	if err != nil {
		t.Fatalf("Steer() error = %v", err)
	}
	assertProviderThreadProjection(t, "turn/steer", receiveThreadProjectionParams(t, paramsCh))
}

func TestInterruptWireUsesProviderThreadID(t *testing.T) {
	s, paramsCh := newThreadProjectionTestSession(t, "turn/interrupt")
	s.mu.Lock()
	s.activeTurnID = "turn-1"
	s.mu.Unlock()

	err := s.Interrupt(context.Background(), dto.InterruptRequest{
		ThreadID: publicThreadProjectionID,
		TurnID:   "turn-1",
		Source:   "ui_stop",
	})
	if err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	assertProviderThreadProjection(t, "turn/interrupt", receiveThreadProjectionParams(t, paramsCh))
}

func TestForceCompleteWireUsesProviderThreadID(t *testing.T) {
	s, paramsCh := newThreadProjectionTestSession(t, "turn/forceComplete")
	active := newTurnHandle("local-1", "turn-1")
	s.mu.Lock()
	s.turns["turn-1"] = active
	s.activeTurnID = "turn-1"
	s.mu.Unlock()

	err := s.ForceComplete(context.Background(), dto.ForceCompleteRequest{
		ThreadID:   publicThreadProjectionID,
		ProviderID: "turn-1",
	})
	if err != nil {
		t.Fatalf("ForceComplete() error = %v", err)
	}
	assertProviderThreadProjection(t, "turn/forceComplete", receiveThreadProjectionParams(t, paramsCh))
}

func TestLiveSessionOperationsRequireProviderThreadIDBeforeWire(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(*session) error
	}{
		{
			name: "start turn",
			invoke: func(s *session) error {
				_, err := s.StartTurn(context.Background(), dto.TurnRequest{
					ThreadID: publicThreadProjectionID,
					Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
					Overrides: dto.TurnOverrides{
						Model: "gpt-5",
					},
				})
				return err
			},
		},
		{
			name: "steer",
			invoke: func(s *session) error {
				return s.Steer(context.Background(), dto.SteerRequest{
					ThreadID:       publicThreadProjectionID,
					ExpectedTurnID: "turn-1",
					Inputs:         []dto.InputItem{{Type: "text", Content: "continue"}},
				})
			},
		},
		{
			name: "interrupt",
			invoke: func(s *session) error {
				s.mu.Lock()
				s.activeTurnID = "turn-1"
				s.mu.Unlock()
				return s.Interrupt(context.Background(), dto.InterruptRequest{
					ThreadID: publicThreadProjectionID,
					TurnID:   "turn-1",
					Source:   "ui_stop",
				})
			},
		},
		{
			name: "force complete",
			invoke: func(s *session) error {
				active := newTurnHandle("local-1", "turn-1")
				s.mu.Lock()
				s.turns["turn-1"] = active
				s.activeTurnID = "turn-1"
				s.mu.Unlock()
				return s.ForceComplete(context.Background(), dto.ForceCompleteRequest{
					ThreadID:   publicThreadProjectionID,
					ProviderID: "turn-1",
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wireCalls := make(chan string, 1)
			s := newUnboundThreadProjectionTestSession(t, wireCalls)
			err := tt.invoke(s)
			if err == nil || err.Error() != "codexapp: thread id is required" {
				t.Fatalf("operation error = %v, want codexapp: thread id is required", err)
			}
			select {
			case method := <-wireCalls:
				t.Fatalf("operation reached provider wire via %s without a bound provider thread", method)
			default:
			}
		})
	}
}

func TestForkThreadWirePreservesExplicitTargetOverSessionThread(t *testing.T) {
	s, paramsCh := newThreadProjectionTestSession(t, "thread/fork")

	result, err := s.ForkThread(context.Background(), dto.ForkRequest{ThreadID: publicThreadProjectionID})
	if err != nil {
		t.Fatalf("ForkThread() error = %v", err)
	}
	if result.NewThreadID != "forked-provider-thread" {
		t.Fatalf("ForkThread() NewThreadID = %q, want forked-provider-thread", result.NewThreadID)
	}
	params := receiveThreadProjectionParams(t, paramsCh)
	if got := params["threadId"]; got != publicThreadProjectionID {
		t.Fatalf("thread/fork threadId = %#v, want explicit target %q over session thread %q", got, publicThreadProjectionID, providerThreadProjectionID)
	}
}

func TestReadHistoryPreservesExplicitTargetOverSessionThread(t *testing.T) {
	codexHome := t.TempDir()
	writeThreadProjectionRollout(t, codexHome, publicThreadProjectionID, "requested history")
	writeThreadProjectionRollout(t, codexHome, providerThreadProjectionID, "session history")
	s := &session{history: &rolloutReader{skillMetrics: testSkillMetrics(t)}}
	s.setThreadID(providerThreadProjectionID)
	s.setRuntimeConfig(map[string]any{"codexHome": codexHome})

	messages, err := s.ReadHistory(context.Background(), publicThreadProjectionID, 10)
	if err != nil {
		t.Fatalf("ReadHistory() error = %v", err)
	}
	if len(messages) != 1 || messages[0].Content != "requested history" {
		t.Fatalf("ReadHistory() messages = %#v, want explicit target history over session history", messages)
	}
}

func newThreadProjectionTestSession(t *testing.T, captureMethod string) (*session, <-chan map[string]any) {
	t.Helper()
	paramsCh := make(chan map[string]any, 1)
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		if msg.Method == captureMethod {
			var params map[string]any
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				t.Fatalf("unmarshal %s params: %v", captureMethod, err)
			}
			paramsCh <- params
		}
		if msg.Method == "turn/start" {
			return mustJSON(map[string]any{"turn": map[string]any{"id": "turn-1"}})
		}
		if msg.Method == "thread/fork" {
			return mustJSON(map[string]any{"thread": map[string]any{"id": "forked-provider-thread"}})
		}
		return mustJSON(map[string]any{"ok": true})
	})
	s, err := newSessionWithOptions(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, testApprovalManager(), nil, withSkillMetrics(testSkillMetrics(t)), withLogRuntime(testLoggerRuntime(t)))
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	s.setThreadID(providerThreadProjectionID)
	return s, paramsCh
}

func newUnboundThreadProjectionTestSession(t *testing.T, wireCalls chan<- string) *session {
	t.Helper()
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		switch msg.Method {
		case "turn/start", "turn/steer", "turn/interrupt", "turn/forceComplete":
			wireCalls <- msg.Method
		}
		if msg.Method == "turn/start" {
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
	return s
}

func writeThreadProjectionRollout(t *testing.T, codexHome, threadID, content string) {
	t.Helper()
	dir := filepath.Join(codexHome, "sessions", "2026", "08", "14")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir rollout fixture: %v", err)
	}
	raw, err := json.Marshal(map[string]any{
		"timestamp": "2026-08-14T00:00:00Z",
		"type":      "response_item",
		"payload": map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{{
				"type": "output_text",
				"text": content,
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal rollout fixture: %v", err)
	}
	path := filepath.Join(dir, "rollout-projection-"+threadID+".jsonl")
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("write rollout fixture: %v", err)
	}
}

func receiveThreadProjectionParams(t *testing.T, paramsCh <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case params := <-paramsCh:
		return params
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for projected thread params")
		return nil
	}
}

func assertProviderThreadProjection(t *testing.T, method string, params map[string]any) {
	t.Helper()
	if got := params["threadId"]; got != providerThreadProjectionID {
		t.Fatalf("%s threadId = %#v, want provider thread %q (public thread %q must stay local)", method, got, providerThreadProjectionID, publicThreadProjectionID)
	}
}
