package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
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

type invalidThreadResultRecoveryRecorder struct {
	activeTurnRecoveryRecorder
	result json.RawMessage
}

func (r *invalidThreadResultRecoveryRecorder) handle(msg jsonRPCMessage) (json.RawMessage, bool) {
	if len(msg.ID) == 0 {
		return nil, false
	}
	switch msg.Method {
	case "initialize":
		r.incrementInitialize()
		return mustJSON(map[string]any{"ok": true}), true
	case "thread/resume":
		r.incrementThreadResume()
		return r.result, true
	case "turn/status":
		r.incrementTurnStatus()
		return mustJSON(map[string]any{"turn": map[string]any{"active": false}}), true
	case "turn/start":
		return r.nextTurnStartResponse(), true
	default:
		return mustJSON(map[string]any{"ok": true}), true
	}
}

func TestRecoveryInvalidThreadResultDoesNotUpdateOrReplay(t *testing.T) {
	tests := []struct {
		name   string
		result json.RawMessage
	}{
		{name: "invalid result JSON", result: json.RawMessage(`"invalid"`)},
		{name: "missing thread id", result: mustJSON(map[string]any{"thread": map[string]any{}})},
		{name: "empty thread id", result: mustJSON(map[string]any{"thread": map[string]any{"id": "  "}})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &invalidThreadResultRecoveryRecorder{result: tt.result}
			server := newCodexTestRPCServer(t, recorder.handle)
			defer server.Close()
			s := newRecoveryTestSession(t, server)
			defer closeCodexTestSession(t, s)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			startRecoveryTestTurn(t, ctx, s)
			prepareRecoveryReplayState(s)

			if err := s.attemptRecovery("invalid resume result"); err == nil {
				t.Fatal("attemptRecovery() error = nil, want invalid thread result error")
			}
			if got := s.ThreadID(); got != "thread-1" {
				t.Fatalf("ThreadID() = %q, want unchanged thread-1", got)
			}
			recorder.mu.Lock()
			turnStarts, turnStatusCalls := recorder.turnStarts, recorder.turnStatusCalls
			recorder.mu.Unlock()
			if turnStarts != 1 || turnStatusCalls != 0 {
				t.Fatalf("recovery replay calls = turn/start:%d turn/status:%d, want 1 initial start and no replay", turnStarts, turnStatusCalls)
			}
		})
	}
}

type completedTurnRecoveryRecorder struct {
	activeTurnRecoveryRecorder
}

// handle 模拟 provider 明确报告原 turn 已 completed；恢复流程必须阻断重放。
func (r *completedTurnRecoveryRecorder) handle(msg jsonRPCMessage) (json.RawMessage, bool) {
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
		return mustJSON(map[string]any{
			"turn": map[string]any{
				"id":     "turn-1",
				"active": false,
				"status": "completed",
			},
		}), true
	case "turn/start":
		return r.nextTurnStartResponse(), true
	default:
		return mustJSON(map[string]any{"ok": true}), true
	}
}

func TestRecoveryDoesNotReplayCompletedTurn(t *testing.T) {
	recorder := &completedTurnRecoveryRecorder{}
	server := newCodexTestRPCServer(t, recorder.handle)
	defer server.Close()

	s := newRecoveryTestSession(t, server)
	defer closeCodexTestSession(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	handle := startRecoveryTestTurn(t, ctx, s)
	prepareRecoveryReplayState(s)
	if err := s.attemptRecovery("test recovery"); err != nil {
		t.Fatalf("attemptRecovery() error = %v", err)
	}

	if got := handle.ProviderID(); got != "turn-1" {
		t.Fatalf("ProviderID() after completed confirmation = %q, want original turn-1", got)
	}
	if got := s.activeTurnID; got != "turn-1" {
		t.Fatalf("activeTurnID = %q, want original turn-1", got)
	}
	recorder.assertNoReplayWhileActive(t)
}

func TestTurnStartNotReplayedAfterTransportWrite(t *testing.T) {
	recorder, err := runTurnStartWithDroppedResponse(t)
	if err == nil {
		t.Fatal("StartTurn() error = nil, want uncertain write outcome error")
	}
	if got := recorder.count("turn/start"); got != 1 {
		t.Fatalf("turn/start count = %d, want 1 after write outcome is unknown", got)
	}
}

func TestTurnStartReturnsRecoverableWhenWriteOutcomeUnknown(t *testing.T) {
	_, err := runTurnStartWithDroppedResponse(t)
	if err == nil {
		t.Fatal("StartTurn() error = nil, want uncertain write outcome error")
	}
	var recoverable interface{ Recoverable() bool }
	if !errors.As(err, &recoverable) || !recoverable.Recoverable() {
		t.Fatalf("StartTurn() error = %T %[1]v, want recoverable error", err)
	}
	if !strings.Contains(err.Error(), "turn/start write outcome unknown") {
		t.Fatalf("StartTurn() error = %v, want turn/start write outcome unknown", err)
	}
}

func TestTurnStartWriteTimeoutTriggersRecoveryWithoutReplay(t *testing.T) {
	recorder := &rpcMethodRecorder{}
	releaseTurnStart := make(chan struct{})
	server := newTurnStartHangServer(t, recorder, releaseTurnStart)
	t.Cleanup(func() {
		close(releaseTurnStart)
		server.Close()
	})
	s := newRecoveryTestSession(t, server)
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	s.setThreadID("thread-1")
	s.setRuntimeConfigValue("model", "gpt-5")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := s.StartTurn(ctx, dto.TurnRequest{
		ThreadID: "thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	cancel()
	if err == nil {
		t.Fatal("StartTurn() error = nil, want uncertain write outcome error")
	}
	var recoverable interface{ Recoverable() bool }
	if !errors.As(err, &recoverable) || !recoverable.Recoverable() {
		t.Fatalf("StartTurn() error = %T %[1]v, want recoverable error", err)
	}

	waitForRecordedMethod(t, recorder, "thread/resume")
	if got := recorder.count("turn/start"); got != 1 {
		t.Fatalf("turn/start count = %d, want 1; recovery must not replay uncertain user input", got)
	}
}

func runTurnStartWithDroppedResponse(t *testing.T) (*rpcMethodRecorder, error) {
	t.Helper()
	recorder := &rpcMethodRecorder{}
	server := newTurnStartDisconnectServer(t, recorder)
	t.Cleanup(server.Close)
	s := newRecoveryTestSession(t, server)
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	s.setThreadID("thread-1")
	s.setRuntimeConfigValue("model", "gpt-5")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	_, err := s.StartTurn(ctx, dto.TurnRequest{
		ThreadID: "thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	return recorder, err
}

func newTurnStartHangServer(t *testing.T, recorder *rpcMethodRecorder, releaseTurnStart <-chan struct{}) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serveTurnStartHangConn(t, conn, recorder, releaseTurnStart)
	}))
}

func serveTurnStartHangConn(t *testing.T, conn *websocket.Conn, recorder *rpcMethodRecorder, releaseTurnStart <-chan struct{}) {
	t.Helper()
	defer conn.Close()
	for {
		_, rawBytes, err := conn.ReadMessage()
		if err != nil {
			return
		}
		msg, ok := decodeRecoveryTransportRPCMessage(rawBytes)
		if !ok {
			continue
		}
		recorder.record(msg.Method)
		if msg.Method == "turn/start" {
			<-releaseTurnStart
			return
		}
		if len(msg.ID) != 0 {
			writeRecoveryTransportRPCResponse(t, conn, msg, turnStartDisconnectResult(msg.Method))
		}
	}
}

func waitForRecordedMethod(t *testing.T, recorder *rpcMethodRecorder, method string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if recorder.count(method) > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %s; methods=%v", method, recorder.snapshot())
		case <-ticker.C:
		}
	}
}

func newTurnStartDisconnectServer(t *testing.T, recorder *rpcMethodRecorder) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serveTurnStartDisconnectConn(t, conn, recorder)
	}))
}

func serveTurnStartDisconnectConn(t *testing.T, conn *websocket.Conn, recorder *rpcMethodRecorder) {
	t.Helper()
	defer conn.Close()
	for {
		_, rawBytes, err := conn.ReadMessage()
		if err != nil {
			return
		}
		msg, ok := decodeRecoveryTransportRPCMessage(rawBytes)
		if !ok {
			continue
		}
		recorder.record(msg.Method)
		if msg.Method == "turn/start" {
			return
		}
		if len(msg.ID) != 0 {
			writeRecoveryTransportRPCResponse(t, conn, msg, turnStartDisconnectResult(msg.Method))
		}
	}
}

func turnStartDisconnectResult(method string) json.RawMessage {
	switch method {
	case "model/list":
		return validCodexModelListResult()
	case "thread/resume":
		return mustJSON(map[string]any{"thread": map[string]any{"id": "thread-1"}})
	default:
		return mustJSON(map[string]any{"ok": true})
	}
}
