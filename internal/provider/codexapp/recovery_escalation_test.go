package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kelindar/event"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// -----------------------------------------------------------------------------
// Regression: maxRecoveryAttempts must be 2 (not 3)
//
// History: was 3 prior to the fix. With 3 retries on a dead port, the agent
// wastes ~3 × 5s = 15s before escalating. At 2 the session-level recovery
// (CLI re-launch) triggers faster and the transport does not spin on a dead
// port.
// -----------------------------------------------------------------------------

func TestMaxRecoveryAttemptsIsTwo(t *testing.T) {
	t.Parallel()
	if maxRecoveryAttempts != 2 {
		t.Fatalf("maxRecoveryAttempts = %d, want 2; "+
			"a higher value delays session-level recovery (CLI re-launch) "+
			"while the transport hammers a dead port", maxRecoveryAttempts)
	}
}

func TestNewSessionTransportReconnectAttemptsIsTwo(t *testing.T) {
	t.Parallel()
	_, url := newRecoveryOrderServer(t)
	s := newTestRuntimeSession(t, url)
	if s.recovery == nil {
		t.Fatal("newSession() recovery manager is nil")
	}
	if got := s.recovery.maxRetry; got != maxRecoveryAttempts {
		t.Fatalf("recovery.maxRetry = %d, want %d", got, maxRecoveryAttempts)
	}
}

// TestAttemptRecoveryExhaustsAtTwo confirms the escalation boundary:
// after maxRecoveryAttempts (2), the (count+1)th call is immediately
// rejected with "max recovery attempts exceeded" and all pending turns
// are failed. This test is independent of any WS server.
func TestAttemptRecoveryExhaustsAtTwo(t *testing.T) {
	t.Parallel()
	handle := newTurnHandle("local-1", "provider-1")
	s := &session{
		turns:      map[string]*turnHandle{"provider-1": handle},
		suppressed: map[string]struct{}{},
	}

	for i := range maxRecoveryAttempts {
		err := s.attemptRecovery("test")
		if err == nil || !strings.Contains(err.Error(), "recovery unavailable") {
			t.Fatalf("attempt %d: err = %v, want 'recovery unavailable'", i+1, err)
		}
	}

	err := s.attemptRecovery("test")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("max recovery attempts (%d) exceeded", maxRecoveryAttempts)) {
		t.Fatalf("attempt %d: err = %v, want 'max recovery attempts exceeded'", maxRecoveryAttempts+1, err)
	}
	select {
	case <-handle.Done():
	default:
		t.Fatal("handle.Done() not closed after max recovery — turns not failed")
	}
}

// TestAttemptRecoverySerializesBeforeConsumingBudget 锁定并发断链的恢复预算边界。
// recoveryMu 被其它恢复持有时，等待者不得在锁外递增计数或提前 failTurns。
func TestAttemptRecoverySerializesBeforeConsumingBudget(t *testing.T) {
	t.Parallel()

	handle := newTurnHandle("local-1", "provider-1")
	s := &session{
		turns:      map[string]*turnHandle{"provider-1": handle},
		suppressed: map[string]struct{}{},
	}
	s.recoveryMu.Lock()

	const callers = maxRecoveryAttempts + 1
	ready := make(chan struct{}, callers)
	release := make(chan struct{})
	results := make(chan error, callers)
	startConcurrentRecoveryCalls(t, s, callers, ready, release, results)
	close(release)

	earlyResult := receiveEarlyRecoveryResult(results)
	countWhileLocked := s.recoveryCount.Load()
	turnFailedWhileLocked := turnHandleDone(handle)
	s.recoveryMu.Unlock()

	remaining := callers
	if earlyResult != nil {
		remaining--
	}
	waitForRecoveryResults(t, results, remaining)
	if earlyResult != nil {
		t.Fatalf("attemptRecovery() returned before serialization lock was released: %v", earlyResult)
	}
	if countWhileLocked != 0 {
		t.Fatalf("recoveryCount while recoveryMu held = %d, want 0", countWhileLocked)
	}
	if turnFailedWhileLocked {
		t.Fatal("pending turn failed before any serialized recovery attempt")
	}
}

func startConcurrentRecoveryCalls(
	t *testing.T,
	s *session,
	callers int,
	ready chan struct{},
	release <-chan struct{},
	results chan<- error,
) {
	t.Helper()
	for range callers {
		runtimesafe.SafeGo(t.Context(), nil, "codexapp.test.concurrent-recovery", func(context.Context) {
			ready <- struct{}{}
			<-release
			results <- s.attemptRecovery("concurrent disconnect")
		})
	}
	for range callers {
		<-ready
	}
}

func receiveEarlyRecoveryResult(results <-chan error) error {
	select {
	case err := <-results:
		return err
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

func turnHandleDone(handle *turnHandle) bool {
	select {
	case <-handle.Done():
		return true
	default:
		return false
	}
}

func waitForRecoveryResults(t *testing.T, results <-chan error, remaining int) {
	t.Helper()
	for range remaining {
		select {
		case <-results:
		case <-time.After(time.Second):
			t.Fatal("attemptRecovery() did not return after recoveryMu was released")
		}
	}
}

func TestConnectionDeadInvalidAPIKeyFailsWithoutRecovery(t *testing.T) {
	t.Parallel()

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()

	failedEvents := make(chan agentdto.AgentFailed, 1)
	cancelSub := event.Subscribe(bus, func(ev agentdto.AgentFailed) {
		failedEvents <- ev
	})
	defer cancelSub()

	handle := newTurnHandle("local-1", "provider-1")
	dispatcher := unified.NewEventDispatcher(bus, pkglogger.Get())
	RegisterTranslators(dispatcher)
	s := &session{
		agentID:    "agent-1",
		dispatcher: dispatcher,
		turns:      map[string]*turnHandle{"provider-1": handle},
		suppressed: map[string]struct{}{},
	}
	s.setThreadID("thread-1")
	s.activeTurnID = "provider-1"

	reason := "unexpected status 401 Unauthorized: Incorrect API key provided: sk-test, auth error code: invalid_api_key"
	payload := mustJSON(map[string]any{"error": reason})
	s.handleConnectionDead(payload)

	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("handle.Done() not closed after non-recoverable auth failure")
	}
	if err := handle.Err(); err == nil || !strings.Contains(err.Error(), "invalid_api_key") {
		t.Fatalf("turn error = %v, want invalid_api_key detail", err)
	}
	if got := s.recoveryCount.Load(); got != 0 {
		t.Fatalf("recoveryCount = %d, want 0 for non-recoverable auth failure", got)
	}
	select {
	case ev := <-failedEvents:
		if ev.Recoverable {
			t.Fatal("AgentFailed.Recoverable = true, want false")
		}
		if !strings.Contains(ev.Error, "invalid_api_key") {
			t.Fatalf("AgentFailed.Error = %q, want invalid_api_key detail", ev.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AgentFailed event")
	}
}

// TestConnectionDeadInvalidAPIKeyDoesNotLeakSecretToTurnOrAgentFailed 锁住 auth failure 的日志外边界。
// turn error 和 AgentFailed 只能暴露安全分类，不能把 provider 原始 API key reason 透传出去。
func TestConnectionDeadInvalidAPIKeyDoesNotLeakSecretToTurnOrAgentFailed(t *testing.T) {
	t.Parallel()

	reason := "unexpected status 401 Unauthorized: Incorrect API key provided: sk-l05-secret, auth error code: invalid_api_key"
	turnErr, failed := runConnectionDeadAuthFailure(t, reason)
	for _, got := range []struct {
		name string
		text string
	}{
		{name: "turn error", text: turnErr},
		{name: "AgentFailed.Error", text: failed.Error},
	} {
		if strings.Contains(got.text, "sk-l05-secret") || strings.Contains(got.text, "Incorrect API key provided") {
			t.Fatalf("%s leaked raw auth reason: %q", got.name, got.text)
		}
		if !strings.Contains(got.text, "invalid_api_key") {
			t.Fatalf("%s = %q, want safe invalid_api_key classification", got.name, got.text)
		}
	}
	if failed.Recoverable {
		t.Fatal("AgentFailed.Recoverable = true, want false for invalid API key")
	}
}

// TestConnectionDeadSafeErrorPreservesAuthClassification 覆盖 provider 未显式带 code 的 auth 文案。
// 即使 raw reason 只有 401/Incorrect API key，也必须被折叠为 invalid_api_key 分类。
func TestConnectionDeadSafeErrorPreservesAuthClassification(t *testing.T) {
	t.Parallel()

	reason := "401 Unauthorized: Incorrect API key provided: sk-l05-secret-without-code"
	turnErr, failed := runConnectionDeadAuthFailure(t, reason)
	for _, got := range []struct {
		name string
		text string
	}{
		{name: "turn error", text: turnErr},
		{name: "AgentFailed.Error", text: failed.Error},
	} {
		if strings.Contains(got.text, "sk-l05-secret-without-code") || strings.Contains(got.text, "Incorrect API key provided") {
			t.Fatalf("%s leaked raw auth reason: %q", got.name, got.text)
		}
		if !strings.Contains(got.text, "invalid_api_key") {
			t.Fatalf("%s = %q, want inferred invalid_api_key classification", got.name, got.text)
		}
	}
}

// runConnectionDeadAuthFailure 通过真实 connection.dead 分发链返回 turn error 和 AgentFailed。
func runConnectionDeadAuthFailure(t *testing.T, reason string) (string, agentdto.AgentFailed) {
	t.Helper()

	bus := event.NewDispatcher()
	t.Cleanup(func() { _ = bus.Close() })

	failedEvents := make(chan agentdto.AgentFailed, 1)
	cancelSub := event.Subscribe(bus, func(ev agentdto.AgentFailed) {
		failedEvents <- ev
	})
	t.Cleanup(cancelSub)

	handle := newTurnHandle("local-1", "provider-1")
	dispatcher := unified.NewEventDispatcher(bus, pkglogger.Get())
	RegisterTranslators(dispatcher)
	s := &session{
		agentID:    "agent-1",
		dispatcher: dispatcher,
		turns:      map[string]*turnHandle{"provider-1": handle},
		suppressed: map[string]struct{}{},
	}
	s.setThreadID("thread-1")
	s.activeTurnID = "provider-1"

	s.handleConnectionDead(mustJSON(map[string]any{"error": reason}))

	select {
	case <-handle.Done():
	case <-time.After(time.Second):
		t.Fatal("handle.Done() not closed after non-recoverable auth failure")
	}
	turnErr := ""
	if err := handle.Err(); err != nil {
		turnErr = err.Error()
	}
	select {
	case ev := <-failedEvents:
		return turnErr, ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for AgentFailed event")
		return "", agentdto.AgentFailed{}
	}
}

// -----------------------------------------------------------------------------
// Regression: completeRecoveryReplay failure MUST dispatch connection.dead
//
// History: completeRecoveryReplay used to `return err` directly, bypassing
// failRecovery. This silently broke the escalation chain because
// connection.dead was never dispatched and the thread layer never learned
// that the session needs a full CLI re-launch.
// -----------------------------------------------------------------------------

// replayFailServer accepts initialize (WS connect fine) but returns an
// RPC error for thread/resume, simulating a corrupt/dead session state.
type replayFailServer struct {
	mu          sync.Mutex
	resumeCalls int
}

func newReplayFailServer(t *testing.T) (*replayFailServer, string) {
	t.Helper()
	srv := &replayFailServer{}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, rawBytes, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg jsonRPCMessage
			if err := json.Unmarshal(rawBytes, &msg); err != nil {
				continue
			}
			if len(msg.ID) == 0 {
				continue
			}
			method := strings.TrimSpace(msg.Method)
			switch method {
			case "thread/resume":
				srv.mu.Lock()
				srv.resumeCalls++
				srv.mu.Unlock()
				resp := mustJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
					"error":   map[string]any{"code": -32000, "message": "session not found"},
				})
				_ = conn.WriteMessage(websocket.TextMessage, resp)
			default:
				resp := mustJSON(map[string]any{
					"jsonrpc": "2.0",
					"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
					"result":  map[string]any{"ok": true},
				})
				_ = conn.WriteMessage(websocket.TextMessage, resp)
			}
		}
	}))
	t.Cleanup(httpServer.Close)
	return srv, "ws" + strings.TrimPrefix(httpServer.URL, "http")
}

// TestReplayFailureDispatchesConnectionDead verifies the critical
// regression: when reconnect succeeds (WS connects) but
// completeRecoveryReplay fails (thread/resume returns error),
// failRecovery MUST be called so that connection.dead is dispatched.
//
// We subscribe to AgentFailed (the typed event translated from
// connection.dead) on the bus, which is exactly what the thread module's
// onAgentFailed handler subscribes to in production.
func TestReplayFailureDispatchesConnectionDead(t *testing.T) {
	t.Parallel()
	_, url := newReplayFailServer(t)

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()

	var agentFailedCount atomic.Int32
	var lastRecoverable atomic.Bool
	cancelSub := event.Subscribe(bus, func(ev agentdto.AgentFailed) {
		agentFailedCount.Add(1)
		lastRecoverable.Store(ev.Recoverable)
	})
	defer cancelSub()

	dispatcher := unified.NewEventDispatcher(bus, pkglogger.Get())
	// Register the codexapp event translator so connection.dead → AgentFailed.
	RegisterTranslators(dispatcher)

	s, err := newSession(context.Background(), pkglogger.Get(), url, "agent-test", dispatcher, testApprovalManager(), nil)
	if err != nil {
		t.Fatalf("newSession(): %v", err)
	}
	// Do NOT start the runtime — we call attemptRecovery directly to avoid
	// racing with the runtime's own recovery worker / read loop.
	defer func() { _ = s.ForceStop() }()
	s.setThreadID("thread-1")

	// attemptRecovery → reconnect (OK) → completeRecoveryReplay (FAIL)
	// → failRecovery → dispatch connection.dead
	err = s.attemptRecovery("replay-test")
	if err == nil {
		t.Fatal("attemptRecovery() should fail because thread/resume returns error")
	}

	// Give the bus a moment to deliver the event.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if agentFailedCount.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := agentFailedCount.Load(); got == 0 {
		t.Fatal("AgentFailed event was NOT published after replay failure; " +
			"failRecovery is being bypassed — thread layer cannot escalate to CLI re-launch")
	}
	if !lastRecoverable.Load() {
		t.Fatal("AgentFailed published but Recoverable=false; " +
			"thread.onAgentFailed will ignore it")
	}
}

// -----------------------------------------------------------------------------
// Regression: successful recovery resets the counter (no permanent lockout)
// -----------------------------------------------------------------------------

func TestRecoveryCounterResetsOnSuccess(t *testing.T) {
	t.Parallel()
	_, url := newRecoveryOrderServer(t)
	s := newTestRuntimeSession(t, url)
	s.runtime.Start()
	s.setThreadID("thread-1")

	if err := s.attemptRecovery("transient-1"); err != nil {
		t.Fatalf("first attemptRecovery(): %v", err)
	}
	if got := s.recoveryCount.Load(); got != 0 {
		t.Fatalf("recoveryCount after success = %d, want 0", got)
	}

	if err := s.attemptRecovery("transient-2"); err != nil {
		t.Fatalf("second attemptRecovery(): %v", err)
	}
	if got := s.recoveryCount.Load(); got != 0 {
		t.Fatalf("recoveryCount after second success = %d, want 0", got)
	}
}

// -----------------------------------------------------------------------------
// Regression: replay failure increments count and does NOT loop forever
// -----------------------------------------------------------------------------

// TestReplayFailureIncrementsCountTowardsMax verifies that each replay
// failure increments the counter (through failRecovery, not resetting it).
// After maxRecoveryAttempts replay failures, further attempts are rejected
// and the session dispatches connection.dead on each failure so the thread
// layer can escalate.
func TestReplayFailureIncrementsCountTowardsMax(t *testing.T) {
	t.Parallel()
	_, url := newReplayFailServer(t)

	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	var connectionDeadCount atomic.Int32
	cancelSub := event.Subscribe(bus, func(ev agentdto.AgentFailed) {
		connectionDeadCount.Add(1)
	})
	defer cancelSub()

	dispatcher := unified.NewEventDispatcher(bus, pkglogger.Get())
	RegisterTranslators(dispatcher)

	s, err := newSession(context.Background(), pkglogger.Get(), url, "agent-test", dispatcher, testApprovalManager(), nil)
	if err != nil {
		t.Fatalf("newSession(): %v", err)
	}
	defer func() { _ = s.ForceStop() }()
	s.setThreadID("thread-1")

	// Each attempt: reconnect succeeds, replay fails → failRecovery.
	for i := range maxRecoveryAttempts {
		err := s.attemptRecovery(fmt.Sprintf("replay-fail-%d", i+1))
		if err == nil {
			t.Fatalf("attempt %d: expected error from replay failure", i+1)
		}
	}

	// Counter must NOT have been reset — it should be at maxRecoveryAttempts.
	if got := s.recoveryCount.Load(); got < int32(maxRecoveryAttempts) {
		t.Fatalf("recoveryCount = %d after %d failures, want >= %d",
			got, maxRecoveryAttempts, maxRecoveryAttempts)
	}

	// Next attempt must be immediately rejected (max exceeded).
	err = s.attemptRecovery("one-too-many")
	if err == nil || !strings.Contains(err.Error(), "max recovery attempts") {
		t.Fatalf("post-max attempt err = %v, want 'max recovery attempts exceeded'", err)
	}

	// Wait for events to be delivered.
	time.Sleep(100 * time.Millisecond)

	// Each replay failure must have dispatched AgentFailed (= connection.dead).
	if got := connectionDeadCount.Load(); got < int32(maxRecoveryAttempts) {
		t.Fatalf("AgentFailed dispatched %d times, want >= %d; "+
			"replay failures are not escalating to thread layer",
			got, maxRecoveryAttempts)
	}
}

// Ensure dto import is used (keeps it pinned for future assertions).
var _ = dto.RawProviderEvent{}
