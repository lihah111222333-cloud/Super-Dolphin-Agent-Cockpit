package codexapp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type activeTurnRecoveryRecorder struct {
	mu              sync.Mutex
	turnStarts      int
	initializeCalls int
	threadResumes   int
	turnStatusCalls int
}

// handle 模拟 provider 在恢复后确认原 turn 仍处于 active 状态。
func (r *activeTurnRecoveryRecorder) handle(msg jsonRPCMessage) (json.RawMessage, bool) {
	if len(msg.ID) == 0 {
		return nil, false
	}
	switch msg.Method {
	case "initialize":
		r.incrementInitialize()
		return mustJSON(map[string]any{"ok": true}), true
	case "thread/resume":
		r.incrementThreadResume()
		return mustJSON(map[string]any{"thread": map[string]any{"id": "thread-1"}}), true
	case "turn/status":
		r.incrementTurnStatus()
		return mustJSON(map[string]any{"turn": map[string]any{"id": "turn-1", "active": true}}), true
	case "turn/start":
		return r.nextTurnStartResponse(), true
	default:
		return mustJSON(map[string]any{"ok": true}), true
	}
}

// incrementInitialize 记录 initialize 次数，恢复前后各一次。
func (r *activeTurnRecoveryRecorder) incrementInitialize() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.initializeCalls++
}

// incrementThreadResume 记录恢复流程是否重新绑定 provider thread。
func (r *activeTurnRecoveryRecorder) incrementThreadResume() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.threadResumes++
}

// incrementTurnStatus 记录恢复流程是否查询 provider 侧 turn 状态。
func (r *activeTurnRecoveryRecorder) incrementTurnStatus() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turnStatusCalls++
}

// nextTurnStartResponse 返回初始 turn-1；若发生不安全重放，会暴露为 turn-2。
func (r *activeTurnRecoveryRecorder) nextTurnStartResponse() json.RawMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turnStarts++
	if r.turnStarts == 1 {
		return mustJSON(map[string]any{"turn": map[string]any{"id": "turn-1"}})
	}
	return mustJSON(map[string]any{"turn": map[string]any{"id": "turn-2"}})
}

// assertNoReplayWhileActive 验证 provider 确认 active 后没有再次发送 turn/start。
func (r *activeTurnRecoveryRecorder) assertNoReplayWhileActive(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.turnStarts != 1 {
		t.Fatalf("turn/start count = %d, want 1 when provider turn is still active", r.turnStarts)
	}
	if r.turnStatusCalls != 1 {
		t.Fatalf("turn/status count = %d, want 1 provider state confirmation", r.turnStatusCalls)
	}
	if r.initializeCalls != 2 {
		t.Fatalf("initialize count = %d, want 2", r.initializeCalls)
	}
	if r.threadResumes != 1 {
		t.Fatalf("thread/resume count = %d, want 1", r.threadResumes)
	}
}

func TestRecoveryDoesNotReplayWhenProviderTurnStillActive(t *testing.T) {
	recorder := &activeTurnRecoveryRecorder{}
	server := newCodexTestRPCServer(t, recorder.handle)
	defer server.Close()

	s := newRecoveryTestSession(t, server)
	defer closeCodexTestSession(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	handle := startRecoveryTestTurn(t, ctx, s)
	beforeReadAt := prepareRecoveryReplayState(s)
	if err := s.attemptRecovery("test recovery"); err != nil {
		t.Fatalf("attemptRecovery() error = %v", err)
	}

	if got := handle.ProviderID(); got != "turn-1" {
		t.Fatalf("ProviderID() after active confirmation = %q, want original turn-1", got)
	}
	if got := s.activeTurnID; got != "turn-1" {
		t.Fatalf("activeTurnID = %q, want original turn-1", got)
	}
	if got := s.lastReadAt.Load(); got <= beforeReadAt {
		t.Fatalf("lastReadAt = %d, want value newer than %d after recovery", got, beforeReadAt)
	}
	recorder.assertNoReplayWhileActive(t)
}
