package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kelindar/event"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestSessionInterruptRequiresActiveTurnIDBeforeTransport(t *testing.T) {
	s := &session{}
	s.setThreadID("thread-1")

	err := s.Interrupt(context.Background(), dto.InterruptRequest{Source: "ui_stop"})
	if err == nil || !strings.Contains(err.Error(), "active turn id is required for interrupt") {
		t.Fatalf("Interrupt() error = %v, want active turn id required", err)
	}
}

func TestSessionInterruptSendsRequestTurnID(t *testing.T) {
	paramsCh := make(chan map[string]any, 1)
	s := newInterruptTestSession(t, paramsCh)
	s.mu.Lock()
	s.activeTurnID = "turn-req"
	s.mu.Unlock()

	err := s.Interrupt(context.Background(), dto.InterruptRequest{
		ThreadID: " thread-1 ",
		TurnID:   " turn-req ",
		Source:   "ui_stop",
	})
	if err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}

	params := receiveInterruptParams(t, paramsCh)
	if params["turnId"] != "turn-req" {
		t.Fatalf("interrupt turnId = %#v, want turn-req; params=%#v", params["turnId"], params)
	}
}

func TestSessionInterruptTargetChangedSkipsTransport(t *testing.T) {
	paramsCh := make(chan map[string]any, 1)
	s := newInterruptTestSession(t, paramsCh)
	s.mu.Lock()
	s.activeTurnID = "turn-2"
	s.mu.Unlock()

	err := s.Interrupt(context.Background(), dto.InterruptRequest{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Source:   "ui_stop",
	})
	if !errors.Is(err, contract.ErrInterruptTargetChanged) {
		t.Fatalf("Interrupt() error = %v, want interrupt target changed", err)
	}
	select {
	case params := <-paramsCh:
		t.Fatalf("target-changed interrupt reached transport with params %#v", params)
	default:
	}
}

func TestSessionInterruptFallsBackToActiveTurnID(t *testing.T) {
	paramsCh := make(chan map[string]any, 1)
	s := newInterruptTestSession(t, paramsCh)
	s.mu.Lock()
	s.activeTurnID = " active-turn "
	s.mu.Unlock()

	err := s.Interrupt(context.Background(), dto.InterruptRequest{
		ThreadID: "thread-1",
		Source:   "ui_stop",
	})
	if err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}

	params := receiveInterruptParams(t, paramsCh)
	if params["turnId"] != "active-turn" {
		t.Fatalf("interrupt fallback turnId = %#v, want active-turn; params=%#v", params["turnId"], params)
	}
}

func TestStartTurnConsumesEarlyTerminalBeforeResponse(t *testing.T) {
	fixture := newEarlyTerminalTurnFixture(t)
	handle := startEarlyTerminalTestTurn(t, fixture.session)
	assertEarlyTerminalProviderID(t, handle)
	assertEarlyTerminalHandle(t, handle)
	assertEarlyCanonicalTerminal(t, fixture.terminals)
	assertNoEarlyTerminalDuplicate(t, fixture.terminals)
	assertEarlyTerminalOwnerStateCleared(t, fixture.session)
}

type earlyTerminalTurnFixture struct {
	session   *session
	terminals <-chan turndto.TurnCompleted
}

func newEarlyTerminalTurnFixture(t *testing.T) earlyTerminalTurnFixture {
	t.Helper()
	bus := event.NewDispatcher()
	t.Cleanup(func() { _ = bus.Close() })
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	terminals := make(chan turndto.TurnCompleted, 2)
	cancelTerminal := event.Subscribe(bus, func(ev turndto.TurnCompleted) { terminals <- ev })
	t.Cleanup(cancelTerminal)
	s, err := newSession(context.Background(), pkglogger.Get(), startEarlyTerminalTurnStartServer(t), "agent-1", dispatcher, testApprovalManager(), nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	s.setThreadID("thread-1")
	return earlyTerminalTurnFixture{session: s, terminals: terminals}
}

func startEarlyTerminalTestTurn(t *testing.T, s *session) contract.TurnHandle {
	t.Helper()

	handle, err := s.StartTurn(context.Background(), dto.TurnRequest{
		ThreadID: "thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "finish before response"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	return handle
}

func assertEarlyTerminalProviderID(t *testing.T, handle contract.TurnHandle) {
	t.Helper()
	if got := handle.ProviderID(); got != "turn-early" {
		t.Fatalf("ProviderID() = %q, want turn-early", got)
	}
}

func assertEarlyTerminalHandle(t *testing.T, handle contract.TurnHandle) {
	t.Helper()
	select {
	case <-handle.Done():
		if err := handle.Err(); err != nil {
			t.Fatalf("early terminal handle error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("early terminal left returned turn handle active")
	}
}

func assertEarlyCanonicalTerminal(t *testing.T, terminals <-chan turndto.TurnCompleted) {
	t.Helper()
	select {
	case terminal := <-terminals:
		if terminal.ThreadID != "agent-1" || terminal.TurnID != "turn-early" || !terminal.Success || terminal.Status != "completed" {
			t.Fatalf("early terminal = %#v, want canonical completed terminal", terminal)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for early canonical terminal")
	}
}

func assertNoEarlyTerminalDuplicate(t *testing.T, terminals <-chan turndto.TurnCompleted) {
	t.Helper()
	select {
	case duplicate := <-terminals:
		t.Fatalf("early terminal dispatched more than once: %#v", duplicate)
	case <-time.After(50 * time.Millisecond):
	}
}

func assertEarlyTerminalOwnerStateCleared(t *testing.T, s *session) {
	t.Helper()
	s.mu.Lock()
	tracked := s.turns["turn-early"]
	active := s.activeTurnID
	pending := s.pendingTurn
	s.mu.Unlock()
	if tracked != nil || active != "" || pending != nil {
		t.Fatalf("early terminal retained owner state: tracked=%#v active=%q pending=%#v", tracked, active, pending)
	}
}

func TestSessionInterruptTargetChangesBeforeWireWrite(t *testing.T) {
	paramsCh := make(chan map[string]any, 1)
	s := newInterruptTestSession(t, paramsCh)
	s.recovery = nil
	s.mu.Lock()
	s.activeTurnID = "turn-1"
	s.mu.Unlock()

	s.transport.writeMu.Lock()
	writeLocked := true
	t.Cleanup(func() {
		if writeLocked {
			s.transport.writeMu.Unlock()
		}
	})
	baseline := s.transport.nextID.Load()
	errCh := make(chan error, 1)
	runtimesafe.SafeGo(context.Background(), pkglogger.Get(), "codexapp.test.interrupt-target-change", func(context.Context) {
		errCh <- s.Interrupt(context.Background(), dto.InterruptRequest{ThreadID: "thread-1", TurnID: "turn-1", Source: "ui_stop"})
	})
	waitForInterruptCallRegistration(t, s.transport, baseline)

	s.mu.Lock()
	s.activeTurnID = "turn-2"
	s.mu.Unlock()
	s.transport.writeMu.Unlock()
	writeLocked = false

	if err := <-errCh; !errors.Is(err, contract.ErrInterruptTargetChanged) {
		t.Fatalf("Interrupt() error = %v, want interrupt target changed", err)
	}
	select {
	case params := <-paramsCh:
		t.Fatalf("target-changed interrupt reached transport with params %#v", params)
	default:
	}
	if got := s.recoveryCount.Load(); got != 0 {
		t.Fatalf("target-changed interrupt recoveryCount = %d, want 0", got)
	}
}

func TestSessionInterruptPendingClaimRejectsSecondRequestWithoutWire(t *testing.T) {
	serverURL, firstSeen, releaseFirst, calls := startBlockedInterruptResponseServer(t)
	s := newActiveInterruptTestSession(t, serverURL, nil)
	firstErr := make(chan error, 1)
	runtimesafe.SafeGo(context.Background(), pkglogger.Get(), "codexapp.test.first-pending-interrupt", func(context.Context) {
		firstErr <- s.Interrupt(context.Background(), interruptTestRequest("request-A"))
	})
	waitForInterruptSignal(t, firstSeen, "first interrupt wire request")
	baseline := s.transport.nextID.Load()

	err := s.Interrupt(context.Background(), interruptTestRequest("request-B"))
	if !errors.Is(err, errInterruptRequestAlreadyClaimed) {
		close(releaseFirst)
		t.Fatalf("second Interrupt() error = %v, want already claimed", err)
	}
	if got := s.transport.nextID.Load(); got != baseline {
		close(releaseFirst)
		t.Fatalf("second Interrupt() advanced wire request ID from %d to %d", baseline, got)
	}
	if got := calls.Load(); got != 1 {
		close(releaseFirst)
		t.Fatalf("interrupt wire calls = %d, want 1", got)
	}
	s.mu.Lock()
	retained := s.interruptRequests["turn-1"]
	s.mu.Unlock()
	if retained == nil || retained.requestID != "request-A" || retained.state != interruptRequestPending {
		close(releaseFirst)
		t.Fatalf("retained claim = %#v, want pending request-A", retained)
	}

	close(releaseFirst)
	if err := waitForInterruptError(t, firstErr, "first interrupt response"); err != nil {
		t.Fatalf("first Interrupt() error = %v", err)
	}
}

func TestSessionInterruptEmptyRequestIDClaimsStopOwnership(t *testing.T) {
	serverURL, firstSeen, releaseFirst, calls := startBlockedInterruptResponseServer(t)
	defer closeInterruptSignal(releaseFirst)
	s := newActiveInterruptTestSession(t, serverURL, nil)
	firstErr := make(chan error, 1)
	runtimesafe.SafeGo(context.Background(), pkglogger.Get(), "codexapp.test.first-empty-id-interrupt", func(context.Context) {
		firstErr <- s.Interrupt(context.Background(), interruptTestRequest(""))
	})
	waitForInterruptSignal(t, firstSeen, "first interrupt wire request")
	baseline := s.transport.nextID.Load()

	secondErr := make(chan error, 1)
	runtimesafe.SafeGo(context.Background(), pkglogger.Get(), "codexapp.test.second-empty-id-interrupt", func(context.Context) {
		secondErr <- s.Interrupt(context.Background(), interruptTestRequest(""))
	})
	var err error
	select {
	case err = <-secondErr:
	case <-time.After(time.Second):
		t.Fatal("second Interrupt() did not reject while first response remained blocked")
	}
	if !errors.Is(err, errInterruptRequestAlreadyClaimed) {
		t.Fatalf("second Interrupt() error = %v, want already claimed", err)
	}
	if got := s.transport.nextID.Load(); got != baseline {
		t.Fatalf("second Interrupt() advanced wire request ID from %d to %d", baseline, got)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("interrupt wire calls = %d, want 1", got)
	}
	s.mu.Lock()
	retained := s.interruptRequests["turn-1"]
	s.mu.Unlock()
	if retained == nil || retained.requestID != "" || retained.state != interruptRequestPending {
		t.Fatalf("retained claim = %#v, want pending empty request ID", retained)
	}

	closeInterruptSignal(releaseFirst)
	if err := waitForInterruptError(t, firstErr, "first interrupt response"); err != nil {
		t.Fatalf("first Interrupt() error = %v", err)
	}
}

func TestSessionInterruptEmptyRequestIDClaimDoesNotAttributeTerminal(t *testing.T) {
	s := &session{activeTurnID: "turn-1", activeTurnGeneration: 7}
	claim, err := s.reserveInterruptRequest(interruptTargetClaim{turnID: "turn-1", generation: 7}, "")
	if err != nil {
		t.Fatalf("reserveInterruptRequest() error = %v", err)
	}
	if claim == nil || claim.requestID != "" {
		t.Fatalf("reserveInterruptRequest() claim = %#v, want internal empty-ID claim", claim)
	}
	if err := s.markInterruptRequestPending(interruptTargetClaim{turnID: "turn-1", generation: 7}, claim); err != nil {
		t.Fatalf("markInterruptRequestPending() error = %v", err)
	}
	payload := map[string]any{"turnId": "turn-1"}
	outcome := canonicalTurnTerminalOutcome("turn/aborted", payload)
	if s.applyAcceptedInterruptRequest(payload, &outcome) {
		t.Fatalf("empty request ID claimed terminal attribution: %#v", payload)
	}
	if outcome.Cause == "user_request" || outcome.RequestID != "" {
		t.Fatalf("terminal outcome attribution = (%q, %q), want no user request", outcome.Cause, outcome.RequestID)
	}
	if _, exists := payload["termination_request_id"]; exists {
		t.Fatalf("payload wrote empty termination_request_id: %#v", payload)
	}
}

func TestSessionInterruptNotificationBeforeResponseKeepsAcceptedRequestID(t *testing.T) {
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	terminals := make(chan turndto.TurnCompleted, 2)
	cancelTerminal := event.Subscribe(bus, func(ev turndto.TurnCompleted) { terminals <- ev })
	defer cancelTerminal()

	serverURL, notificationSent, releaseResponse, calls := startInterruptNotificationBeforeResponseServer(t)
	s := newActiveInterruptTestSession(t, serverURL, dispatcher)
	firstErr := make(chan error, 1)
	runtimesafe.SafeGo(context.Background(), pkglogger.Get(), "codexapp.test.notification-before-response", func(context.Context) {
		firstErr <- s.Interrupt(context.Background(), interruptTestRequest("request-A"))
	})
	waitForInterruptSignal(t, notificationSent, "interrupt terminal notification")
	select {
	case terminal := <-terminals:
		assertAcceptedInterruptTerminal(t, terminal)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for notification-before-response terminal")
	}
	baseline := s.transport.nextID.Load()
	secondErr := s.Interrupt(context.Background(), interruptTestRequest("request-B"))
	if !errors.Is(secondErr, errInterruptRequestAlreadyClaimed) && !errors.Is(secondErr, contract.ErrInterruptTargetChanged) {
		close(releaseResponse)
		t.Fatalf("second concurrent Interrupt() error = %v, want claimed or target changed", secondErr)
	}
	if got := s.transport.nextID.Load(); got != baseline {
		close(releaseResponse)
		t.Fatalf("second concurrent Interrupt() advanced wire request ID from %d to %d", baseline, got)
	}
	if got := calls.Load(); got != 1 {
		close(releaseResponse)
		t.Fatalf("interrupt wire calls = %d, want 1", got)
	}

	close(releaseResponse)
	if err := waitForInterruptError(t, firstErr, "late interrupt response"); err != nil {
		t.Fatalf("first Interrupt() error = %v", err)
	}
	select {
	case duplicate := <-terminals:
		t.Fatalf("late interrupt response published duplicate terminal: %#v", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSessionInterruptConsumedClaimBlocksReplacementUntilTurnCleanup(t *testing.T) {
	s := &session{
		activeTurnID:         "turn-1",
		activeTurnGeneration: 7,
		interruptRequests: map[string]*interruptRequestClaim{"turn-1": {
			turnID: "turn-1", requestID: "request-A", generation: 7, state: interruptRequestPending,
		}},
	}
	payload := map[string]any{"turnId": "turn-1"}
	outcome := canonicalTurnTerminalOutcome("turn/aborted", payload)
	if !s.applyAcceptedInterruptRequest(payload, &outcome) {
		t.Fatal("pending interrupt terminal was not attributed")
	}
	claim, err := s.claimInterruptTarget("turn-1")
	if err != nil {
		t.Fatalf("claimInterruptTarget() error = %v", err)
	}
	if _, err = s.reserveInterruptRequest(claim, "request-B"); !errors.Is(err, errInterruptRequestAlreadyClaimed) {
		t.Fatalf("reserveInterruptRequest() error = %v, want already claimed", err)
	}
	s.mu.Lock()
	retained := s.interruptRequests["turn-1"]
	s.mu.Unlock()
	if retained == nil || retained.requestID != "request-A" || retained.state != interruptRequestConsumed {
		t.Fatalf("retained consumed claim = %#v, want request-A", retained)
	}
}

func assertAcceptedInterruptTerminal(t *testing.T, terminal turndto.TurnCompleted) {
	t.Helper()
	if terminal.TurnID != "turn-1" || terminal.Success || terminal.Status != "cancelled" ||
		terminal.Reason != "user_request" || terminal.TerminationRequestID != "request-A" {
		t.Fatalf("terminal = %#v, want unique accepted user_request/request-A", terminal)
	}
}

func TestSessionInterruptResponseFailureRollsBackPendingClaim(t *testing.T) {
	var calls atomic.Int32
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		if strings.TrimSpace(msg.Method) == "turn/interrupt" {
			if calls.Add(1) == 1 {
				return mustJSON(map[string]any{"$rpcError": "interrupt rejected"})
			}
		}
		return mustJSON(map[string]any{"ok": true})
	})
	s := newActiveInterruptTestSession(t, serverURL, nil)

	err := s.Interrupt(context.Background(), interruptTestRequest("request-A"))
	if err == nil {
		t.Fatal("Interrupt() error = nil, want provider rejection")
	}
	s.mu.Lock()
	_, retained := s.interruptRequests["turn-1"]
	s.mu.Unlock()
	if retained {
		t.Fatal("failed interrupt retained pending request claim")
	}
	if err := s.Interrupt(context.Background(), interruptTestRequest("request-B")); err != nil {
		t.Fatalf("Interrupt() after rollback error = %v", err)
	}
	s.mu.Lock()
	replacement := s.interruptRequests["turn-1"]
	s.mu.Unlock()
	if calls.Load() != 2 || replacement == nil || replacement.requestID != "request-B" || replacement.state != interruptRequestAccepted {
		t.Fatalf("replacement claim = %#v, calls=%d, want accepted request-B after two wires", replacement, calls.Load())
	}
}

func startInterruptNotificationBeforeResponseServer(t *testing.T) (string, <-chan struct{}, chan struct{}, *atomic.Int32) {
	t.Helper()
	notificationSent := make(chan struct{}, 1)
	releaseResponse := make(chan struct{})
	calls := &atomic.Int32{}
	t.Cleanup(func() { closeInterruptSignal(releaseResponse) })
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serveInterruptNotificationBeforeResponse(t, conn, notificationSent, releaseResponse, calls)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), notificationSent, releaseResponse, calls
}

func serveInterruptNotificationBeforeResponse(
	t *testing.T,
	conn *websocket.Conn,
	notificationSent chan<- struct{},
	releaseResponse <-chan struct{},
	calls *atomic.Int32,
) {
	t.Helper()
	defer conn.Close()
	for handleInterruptServerMessage(t, conn, notificationSent, releaseResponse, calls) {
	}
}

func handleInterruptServerMessage(
	t *testing.T,
	conn *websocket.Conn,
	notificationSent chan<- struct{},
	releaseResponse <-chan struct{},
	calls *atomic.Int32,
) bool {
	t.Helper()
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return false
	}
	msg, ok := decodeCodexTestRPCMessage(raw)
	if !ok {
		return true
	}
	if strings.TrimSpace(msg.Method) == "turn/interrupt" {
		calls.Add(1)
		if !writeInterruptTerminalNotification(conn) {
			return false
		}
		notificationSent <- struct{}{}
		<-releaseResponse
	}
	result := codexTestRPCResultOrDefault(msg.Method, mustJSON(map[string]any{"ok": true}))
	return writeCodexTestRPCResponse(t, conn, msg.ID, result)
}

func writeInterruptTerminalNotification(conn *websocket.Conn) bool {
	notification := map[string]any{
		"jsonrpc": "2.0", "method": "turn/aborted",
		"params": map[string]any{"threadId": "thread-1", "turnId": "turn-1", "timestamp": "2026-07-16T10:11:12.123Z"},
	}
	return conn.WriteJSON(notification) == nil
}

func startEarlyTerminalTurnStartServer(t *testing.T) string {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serveEarlyTerminalTurnStart(t, conn)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func serveEarlyTerminalTurnStart(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	defer conn.Close()
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		msg, ok := decodeCodexTestRPCMessage(raw)
		if !ok {
			continue
		}
		result := mustJSON(map[string]any{"ok": true})
		if msg.Method == "turn/start" {
			if err := conn.WriteJSON(map[string]any{
				"jsonrpc": "2.0",
				"method":  "turn/completed",
				"params": map[string]any{
					"threadId": "thread-1", "turnId": "turn-early", "timestamp": "2026-07-16T10:11:12.123Z",
					"success": true, "status": "completed",
				},
			}); err != nil {
				return
			}
			result = mustJSON(map[string]any{"turn": map[string]any{"id": "turn-early"}})
		}
		if !writeCodexTestRPCResponse(t, conn, msg.ID, codexTestRPCResultOrDefault(msg.Method, result)) {
			return
		}
	}
}

func startBlockedInterruptResponseServer(t *testing.T) (string, <-chan struct{}, chan struct{}, *atomic.Int32) {
	t.Helper()
	firstSeen := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	calls := &atomic.Int32{}
	t.Cleanup(func() { closeInterruptSignal(releaseFirst) })
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		if strings.TrimSpace(msg.Method) == "turn/interrupt" {
			calls.Add(1)
			firstSeen <- struct{}{}
			<-releaseFirst
		}
		return mustJSON(map[string]any{"ok": true})
	})
	return serverURL, firstSeen, releaseFirst, calls
}

func newActiveInterruptTestSession(t *testing.T, serverURL string, dispatcher *unified.EventDispatcher) *session {
	t.Helper()
	s, err := newSession(context.Background(), pkglogger.Get(), serverURL, "agent-1", dispatcher, testApprovalManager(), nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.setThreadID("thread-1")
	s.mu.Lock()
	s.turns["turn-1"] = newTurnHandle("local-1", "turn-1")
	s.setActiveTurnLocked("turn-1")
	s.mu.Unlock()
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	return s
}

func interruptTestRequest(requestID string) dto.InterruptRequest {
	return dto.InterruptRequest{
		ThreadID: "thread-1", TurnID: "turn-1", Source: "ui_stop", RequestID: requestID,
	}
}

func waitForInterruptSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitForInterruptError(t *testing.T, errCh <-chan error, name string) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
		return nil
	}
}

func closeInterruptSignal(signal chan struct{}) {
	select {
	case <-signal:
	default:
		close(signal)
	}
}

func waitForInterruptCallRegistration(t *testing.T, tr *transport, baseline int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for tr.nextID.Load() == baseline {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for interrupt call registration")
		}
		time.Sleep(time.Millisecond)
	}
}

func newInterruptTestSession(t *testing.T, paramsCh chan<- map[string]any) *session {
	t.Helper()
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		if strings.TrimSpace(msg.Method) == "turn/interrupt" {
			var params map[string]any
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				t.Fatalf("unmarshal interrupt params: %v", err)
			}
			paramsCh <- params
		}
		return mustJSON(map[string]any{"ok": true})
	})
	s, err := newSession(context.Background(), pkglogger.Get(), serverURL, "agent-1", nil, testApprovalManager(), nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	s.runtime.Start()
	t.Cleanup(func() { closeCodexTestSession(t, s) })
	return s
}

func receiveInterruptParams(t *testing.T, paramsCh <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case params := <-paramsCh:
		return params
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for turn/interrupt params")
		return nil
	}
}
