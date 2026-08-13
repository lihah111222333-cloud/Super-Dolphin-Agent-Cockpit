package turn

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

func TestInterruptTurnReturnsEnvelope(t *testing.T) {
	t.Parallel()

	handle := newStubTurnHandle("local-1", "provider-1")
	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error {
			time.AfterFunc(20*time.Millisecond, func() {
				handle.complete(errors.New("turn aborted"))
			})
			return nil
		},
	}

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime())
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-1",
		ThreadID: "thread-1",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	status, err := svc.InterruptTurn(context.Background(), session, "user")
	if err != nil {
		t.Fatalf("InterruptTurn() error = %v", err)
	}
	envelope := status.interruptEnvelope()
	if !envelope.confirmed || envelope.mode != "interrupt_confirmed" {
		t.Fatalf("interrupt envelope = %#v, want confirmed interrupt", envelope)
	}
	if !envelope.interruptSent || envelope.stateBefore != "running" || envelope.stateAfter != "idle" {
		t.Fatalf("interrupt envelope = %#v, want sent/running->idle", envelope)
	}
	if envelope.waitedMS <= 0 || !envelope.activeObserved {
		t.Fatalf("interrupt envelope = %#v, want waitedMS>0 and activeObserved", envelope)
	}
}

func TestInterruptTerminalBeforeProviderACKPersistsIdempotentDelivery(t *testing.T) {
	handle := newStubTurnHandle("local-terminal-before-ack", "provider-terminal-before-ack")
	var providerCalls atomic.Int32
	var svc *service
	session := &stubSession{
		threadID:  "thread-terminal-before-ack",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) { return handle, nil },
		interrupt: func(context.Context, dto.InterruptRequest) error {
			providerCalls.Add(1)
			svc.tracker.Complete(handle.LocalID(), true, "")
			handle.complete(nil)
			return nil
		},
	}
	svc = NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime()).(*service)
	if _, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID: handle.LocalID(), ThreadID: session.threadID, Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
	}); err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	first, accepted, err := svc.InterruptTurnForTarget(context.Background(), session, "user", handle.LocalID(), "stop-terminal-before-ack")
	if err != nil || !accepted {
		t.Fatalf("first InterruptTurnForTarget() accepted=%v err=%v", accepted, err)
	}
	assertTerminalFirstACKEnvelope(t, first, "stop-terminal-before-ack")
	replay, accepted, err := svc.InterruptTurnForTarget(context.Background(), session, "user", handle.LocalID(), "stop-terminal-before-ack")
	if err != nil || !accepted {
		t.Fatalf("same request replay accepted=%v err=%v", accepted, err)
	}
	assertTerminalFirstACKEnvelope(t, replay, "stop-terminal-before-ack")
	if calls := providerCalls.Load(); calls != 1 {
		t.Fatalf("provider interrupt calls = %d, want 1", calls)
	}
	_, accepted, err = svc.InterruptTurnForTarget(context.Background(), session, "user", handle.LocalID(), "stop-terminal-before-ack-conflict")
	if err != nil || accepted {
		t.Fatalf("different request accepted=%v err=%v, want conflict", accepted, err)
	}
}

func assertTerminalFirstACKEnvelope(t *testing.T, status TurnStatus, requestID string) {
	t.Helper()
	envelope := status.interruptEnvelope()
	if requestID == "" || envelope.mode != "interrupt_terminal_completed" || envelope.confirmed || !envelope.interruptSent || !envelope.activeObserved {
		t.Fatalf("terminal-first ACK status=%+v envelope=%+v", status, envelope)
	}
}

func TestInterruptTurnNoActiveReturnsEnvelope(t *testing.T) {
	t.Parallel()

	interruptCalls := 0
	session := &stubSession{
		threadID: "thread-2",
		interrupt: func(context.Context, dto.InterruptRequest) error {
			interruptCalls++
			return nil
		},
	}
	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime())

	status, err := svc.InterruptTurn(context.Background(), session, "user")
	if err != nil {
		t.Fatalf("InterruptTurn() error = %v", err)
	}
	if interruptCalls != 0 {
		t.Fatalf("Interrupt() calls = %d, want 0 for no active turn", interruptCalls)
	}
	envelope := status.interruptEnvelope()
	if envelope.confirmed || envelope.mode != "no_active_turn" || envelope.interruptSent {
		t.Fatalf("interrupt envelope = %#v, want no_active_turn without send", envelope)
	}
	if envelope.stateBefore != "idle" || envelope.stateAfter != "idle" {
		t.Fatalf("interrupt envelope = %#v, want idle->idle", envelope)
	}
}

func TestInterruptTurnSettleTimeoutReturnsEnvelope(t *testing.T) {
	t.Parallel()

	handle := newStubTurnHandle("local-timeout", "provider-timeout")
	session := &stubSession{
		threadID: "thread-timeout",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error { return nil },
	}

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime()).(*service)
	svc.interruptSettleTimeout = 25 * time.Millisecond
	_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
		LocalID:  "local-timeout",
		ThreadID: "thread-timeout",
		Inputs:   []dto.InputItem{{Type: "text", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}

	status, err := svc.InterruptTurn(context.Background(), session, "user")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("InterruptTurn() error = %v, want deadline exceeded with timeout envelope", err)
	}
	envelope := status.interruptEnvelope()
	if !envelope.confirmed || envelope.mode != "interrupt_timeout" || !envelope.interruptSent {
		t.Fatalf("interrupt envelope = %#v, want confirmed timeout envelope", envelope)
	}
	if envelope.stateBefore != "running" || envelope.stateAfter != "running" {
		t.Fatalf("interrupt envelope = %#v, want running->running timeout state", envelope)
	}
	if envelope.waitedMS < 20 || !envelope.activeObserved {
		t.Fatalf("interrupt envelope = %#v, want waitedMS>=20 and activeObserved", envelope)
	}
}

func TestInterruptTurnRegistersPreparingCancellationUntilProviderBinding(t *testing.T) {
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	providerInterrupts := make(chan dto.InterruptRequest, 2)
	handle := newStubTurnHandle("local-preparing", "provider-preparing")
	session := &stubSession{
		threadID: "thread-preparing",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			close(startEntered)
			<-releaseStart
			return handle, nil
		},
		interrupt: func(_ context.Context, request dto.InterruptRequest) error {
			providerInterrupts <- request
			handle.complete(context.Canceled)
			return nil
		},
	}
	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime()).(*service)
	startDone := startPreparingTurn(svc, session)
	<-startEntered

	interruptDone := startInterruptAttemptWithRequestID(svc, session, "turn.test.preparing-cancel", "local-preparing", "stop-preparing")
	assertPreparingCancellationRegistered(t, providerInterrupts, interruptDone)

	close(releaseStart)
	assertProviderInterruptAfterBinding(t, providerInterrupts)
	if err := <-startDone; err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
}

func startPreparingTurn(svc *service, session contract.Session) <-chan error {
	done := make(chan error, 1)
	runtimesafe.SafeGo(context.Background(), silentLogger(), "turn.test.preparing-start", func(context.Context) {
		_, err := svc.StartTurn(context.Background(), session, dto.TurnRequest{
			LocalID: "local-preparing", ThreadID: "thread-preparing", Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
		})
		done <- err
	})
	return done
}

func TestRegisteredCancellationBindDoesNotDuplicateDirectProviderInterrupt(t *testing.T) {
	bindEntered := make(chan struct{})
	releaseBind := make(chan struct{})
	handle := newBindWindowHandle("local-bind-window", "provider-bind-window", bindEntered, releaseBind)
	var providerCalls atomic.Int32
	session := &stubSession{
		threadID: "thread-bind-window",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error {
			providerCalls.Add(1)
			time.AfterFunc(20*time.Millisecond, func() { handle.complete(context.Canceled) })
			return nil
		},
	}
	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime()).(*service)
	startDone := startPreparingTurnWithRequest(svc, session, "local-bind-window", "thread-bind-window")
	<-bindEntered
	result := <-startInterruptAttemptWithRequestID(svc, session, "turn.test.bind-window", "local-bind-window", "stop-bind-window")
	if result.err != nil || !result.accepted {
		t.Fatalf("InterruptTurnForTarget() result = %+v, want accepted direct provider interrupt", result)
	}
	close(releaseBind)
	if err := <-startDone; err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if calls := providerCalls.Load(); calls != 1 {
		t.Fatalf("provider interrupt calls = %d, want exactly one", calls)
	}
}

func startPreparingTurnWithRequest(svc *service, session contract.Session, localID, threadID string) <-chan error {
	return startTurnRequest(svc, session, dto.TurnRequest{
		LocalID: localID, ThreadID: threadID, Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
	})
}

func startTurnRequest(svc *service, session contract.Session, request dto.TurnRequest) <-chan error {
	done := make(chan error, 1)
	runtimesafe.SafeGo(context.Background(), silentLogger(), "turn.test.preparing-start", func(context.Context) {
		_, err := svc.StartTurn(context.Background(), session, request)
		done <- err
	})
	return done
}

func TestRegisteredCancellationDeliveryFailureConvergesTrackerDedupeAndWatcher(t *testing.T) {
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	watchObserved := make(chan struct{})
	deliveryErr := errors.New("registered interrupt delivery failed")
	providerAttempts := 0
	handle := &watchObservedHandle{stubTurnHandle: newStubTurnHandle("local-delivery-failure", "provider-delivery-failure"), observed: watchObserved}
	store := newFakeDedupeStore()
	session := &stubSession{
		threadID: "thread-delivery-failure",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			close(startEntered)
			<-releaseStart
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error {
			providerAttempts++
			return deliveryErr
		},
	}
	svc := serviceWithStore(store)
	startDone := startTurnRequest(svc, session, dto.TurnRequest{
		LocalID: "local-delivery-failure", ThreadID: "thread-delivery-failure", DedupeKey: "dk-delivery-failure",
	})
	<-startEntered
	assertPreparingCancellationRegistered(t, make(chan dto.InterruptRequest), startInterruptAttemptWithRequestID(svc, session, "turn.test.delivery-failure", "local-delivery-failure", "stop-delivery-failure"))
	close(releaseStart)
	if err := <-startDone; err != nil {
		t.Fatalf("StartTurn() error = %v, want successful provider start", err)
	}
	assertRegisteredCancellationDeliveryFailurePreservesLiveTurn(t, svc, store, watchObserved, "local-delivery-failure")
	if _, _, err := svc.InterruptTurnForTarget(context.Background(), session, "user", "local-delivery-failure", "stop-delivery-failure"); !errors.Is(err, deliveryErr) || providerAttempts != 2 {
		t.Fatalf("retry after delivery failure error=%v providerAttempts=%d, want second provider delivery attempt", err, providerAttempts)
	}
	handle.complete(nil)
	assertRegisteredCancellationDeliveryFailureSettlesAfterProviderTerminal(t, svc, store, "local-delivery-failure")
}

func TestRegisteredCancellationDeliveryAndDedupeProviderIDFailuresRemainVisible(t *testing.T) {
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	watchObserved := make(chan struct{})
	deliveryErr := errors.New("registered interrupt delivery failed")
	handle := &watchObservedHandle{stubTurnHandle: newStubTurnHandle("local-combined-failure", "provider-combined-failure"), observed: watchObserved}
	store := newFakeDedupeStore()
	store.bindFn = func(context.Context, DedupeBindProviderTurnIDParams) error {
		return errors.New("dedupe provider id bind failed")
	}
	session := &stubSession{
		threadID: "thread-combined-failure",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			close(startEntered)
			<-releaseStart
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error {
			return deliveryErr
		},
	}
	svc := serviceWithStore(store)
	startDone := startTurnRequest(svc, session, dto.TurnRequest{
		LocalID: "local-combined-failure", ThreadID: "thread-combined-failure", DedupeKey: "dk-combined-failure",
	})
	<-startEntered
	assertPreparingCancellationRegistered(t, make(chan dto.InterruptRequest), startInterruptAttemptWithRequestID(svc, session, "turn.test.combined-failure", "local-combined-failure", "stop-combined-failure"))
	close(releaseStart)
	if err := <-startDone; err != nil {
		t.Fatalf("StartTurn() error = %v, want partial-start success", err)
	}
	assertCombinedFailurePreservesLiveTurn(t, svc, store, watchObserved)
	handle.complete(nil)
	assertRegisteredCancellationDeliveryFailureSettlesAfterProviderTerminal(t, svc, store, "local-combined-failure")
}

func assertCombinedFailurePreservesLiveTurn(t *testing.T, svc *service, store *fakeDedupeStore, watchObserved <-chan struct{}) {
	t.Helper()
	status := requireTurnStatus(t, svc.tracker, "local-combined-failure")
	if status.State != string(StateRunning) || status.InterruptRetryable || status.InterruptRetryableCode != "" || status.StartDiagnosticCode != "TURN_DEDUPE_PROVIDER_ID_BIND_FAILED" {
		t.Fatalf("tracker status = %+v, want live running turn with durable bind diagnostic only", status)
	}
	store.mu.Lock()
	terminalCalls := store.terminalCalls
	store.mu.Unlock()
	if terminalCalls != 0 {
		t.Fatalf("dedupe terminal calls = %d, want no fabricated terminal", terminalCalls)
	}
	awaitCombinedFailureWatcher(t, watchObserved)
}

func awaitCombinedFailureWatcher(t *testing.T, watchObserved <-chan struct{}) {
	t.Helper()
	select {
	case <-watchObserved:
	case <-time.After(time.Second):
		t.Fatal("watcher did not retain handle after durable bind failure")
	}
}

func TestCompletedProviderBeforeStartResponseClearsRetryableDiagnostic(t *testing.T) {
	startEntered := make(chan struct{})
	releaseStart := make(chan struct{})
	deliveryErr := errors.New("registered interrupt delivery failed")
	handle := newStubTurnHandle("local-terminal-before-response", "provider-terminal-before-response")
	handle.complete(nil)
	session := &stubSession{
		threadID: "thread-terminal-before-response",
		startTurn: func(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
			close(startEntered)
			<-releaseStart
			return handle, nil
		},
		interrupt: func(context.Context, dto.InterruptRequest) error {
			return deliveryErr
		},
	}
	svc := serviceWithStore(newFakeDedupeStore())
	startDone := startTurnRequest(svc, session, dto.TurnRequest{
		LocalID: "local-terminal-before-response", ThreadID: "thread-terminal-before-response",
	})
	<-startEntered
	assertPreparingCancellationRegistered(t, make(chan dto.InterruptRequest), startInterruptAttemptWithRequestID(svc, session, "turn.test.terminal-before-response", "local-terminal-before-response", "stop-terminal-before-response"))
	close(releaseStart)
	if err := <-startDone; err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	status := requireTurnStatus(t, svc.tracker, "local-terminal-before-response")
	if !isTerminalTurnState(status.State) || status.InterruptRetryable || status.InterruptRetryableCode != "" {
		t.Fatalf("tracker status = %+v, want terminal state without retryable diagnostic", status)
	}
	result := turnStartResult{TurnID: handle.LocalID()}
	if err := attachTurnStartInterruptRetryable(svc, handle.LocalID(), &result); err != nil {
		t.Fatalf("attachTurnStartInterruptRetryable() error = %v", err)
	}
	if result.InterruptRetryable || result.InterruptRetryableCode != "" {
		t.Fatalf("turn/start result = %+v, want terminal response without retryable diagnostic", result)
	}
}

func TestTerminalSameRequestReplayUsesTerminalEnvelope(t *testing.T) {
	t.Parallel()

	tests := []terminalReplayCase{
		{name: "completed", success: true, wantState: StateCompleted, wantMode: "interrupt_terminal_completed"},
		{name: "failed", errMsg: "provider failed", wantState: StateFailed, wantMode: "interrupt_terminal_failed"},
		{name: "interrupted", errMsg: "canceled", interrupt: true, wantState: StateInterrupted, wantMode: "interrupt_confirmed", confirmed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runTerminalReplayCase(t, tt)
		})
	}
}

type terminalReplayCase struct {
	name      string
	success   bool
	errMsg    string
	interrupt bool
	wantState TurnState
	wantMode  string
	confirmed bool
}

func runTerminalReplayCase(t *testing.T, tt terminalReplayCase) {
	t.Helper()
	localID := "local-terminal-replay-" + tt.name
	tracker := newTurnTracker()
	tracker.Start(localID, "provider-"+tt.name, "thread-terminal-replay")
	markTerminalReplayInterrupt(t, tracker, localID, tt.interrupt)
	tracker.store.Mutate(localID, func(turn *trackedTurn) {
		turn.interruptAcceptedRequestID = "stop-terminal-replay"
		turn.interruptDeliverySent = true
	})
	tracker.Complete(localID, tt.success, tt.errMsg)
	svc := &service{tracker: tracker, logger: silentLogger()}
	status, accepted, err := svc.InterruptTurnForTarget(context.Background(), &stubSession{threadID: "thread-terminal-replay"}, "user", localID, "stop-terminal-replay")
	assertTerminalReplayStatus(t, status, accepted, err, tt.wantState)
	assertTerminalReplayEnvelope(t, status.interruptEnvelope(), tt)
}

func markTerminalReplayInterrupt(t *testing.T, tracker *turnTracker, localID string, interrupt bool) {
	t.Helper()
	if interrupt && !tracker.MarkInterruptRequested(localID) {
		t.Fatal("MarkInterruptRequested() = false")
	}
}

func assertTerminalReplayStatus(t *testing.T, status TurnStatus, accepted bool, err error, wantState TurnState) {
	t.Helper()
	if err != nil || !accepted || status.State != string(wantState) {
		t.Fatalf("InterruptTurnForTarget() status=%+v accepted=%v err=%v", status, accepted, err)
	}
}

func assertTerminalReplayEnvelope(t *testing.T, envelope turnInterruptEnvelope, tt terminalReplayCase) {
	t.Helper()
	if envelope.mode != tt.wantMode || envelope.confirmed != tt.confirmed || !envelope.interruptSent || !envelope.activeObserved {
		t.Fatalf("terminal replay envelope=%+v, want mode=%s confirmed=%v", envelope, tt.wantMode, tt.confirmed)
	}
}

type bindWindowHandle struct {
	*stubTurnHandle
	providerCalls atomic.Int32
	entered       chan struct{}
	release       chan struct{}
	once          sync.Once
}

func newBindWindowHandle(localID, providerID string, entered, release chan struct{}) *bindWindowHandle {
	return &bindWindowHandle{stubTurnHandle: newStubTurnHandle(localID, providerID), entered: entered, release: release}
}

func (h *bindWindowHandle) ProviderID() string {
	if h.providerCalls.Add(1) == 2 {
		h.once.Do(func() { close(h.entered) })
		<-h.release
	}
	return h.providerID
}

func assertPreparingCancellationRegistered(t *testing.T, providerInterrupts <-chan dto.InterruptRequest, interruptDone <-chan interruptAttemptResult) {
	t.Helper()
	select {
	case request := <-providerInterrupts:
		t.Fatalf("provider interrupt before provider binding = %#v, want registration only", request)
	case result := <-interruptDone:
		assertRegisteredPreparingCancellation(t, result)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for preparing cancellation registration")
	}
}

func assertRegisteredPreparingCancellation(t *testing.T, result interruptAttemptResult) {
	t.Helper()
	envelope := result.status.interruptEnvelope()
	if result.err != nil || !result.accepted {
		t.Fatalf("InterruptTurnForTarget() accepted=%v error=%v, want registered cancellation", result.accepted, result.err)
	}
	if result.status.State != string(StateInterrupting) || envelope.mode != "interrupt_registered" || envelope.confirmed || envelope.interruptSent {
		t.Fatalf("registered cancellation status=%#v envelope=%#v, want non-terminal registered interrupt", result.status, envelope)
	}
}

func assertProviderInterruptAfterBinding(t *testing.T, providerInterrupts <-chan dto.InterruptRequest) {
	t.Helper()
	select {
	case request := <-providerInterrupts:
		if request.TurnID != "provider-preparing" || request.RequestID != "stop-preparing" {
			t.Fatalf("provider interrupt after binding = %#v, want provider turn identity and registered request id", request)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for registered provider interrupt")
	}
}

func assertRegisteredCancellationDeliveryFailurePreservesLiveTurn(t *testing.T, svc *service, store *fakeDedupeStore, watchObserved <-chan struct{}, localID string) {
	t.Helper()
	status, ok := svc.tracker.Get(localID)
	if !ok || status.State != string(StateRunning) {
		t.Fatalf("tracker status = %+v found=%v, want running non-terminal state", status, ok)
	}
	if !status.InterruptRetryable || status.InterruptRetryableCode != "REGISTERED_INTERRUPT_DELIVERY_RETRYABLE" {
		t.Fatalf("tracker status = %+v, want retryable registered cancellation delivery failure", status)
	}
	store.mu.Lock()
	terminalCalls := store.terminalCalls
	store.mu.Unlock()
	if terminalCalls != 0 {
		t.Fatalf("dedupe terminal calls = %d, want no fabricated terminal before provider stops", terminalCalls)
	}
	select {
	case <-watchObserved:
	case <-time.After(time.Second):
		t.Fatal("watcher did not retain provider handle after cancellation delivery failure")
	}
}

func assertRegisteredCancellationDeliveryFailureSettlesAfterProviderTerminal(t *testing.T, svc *service, store *fakeDedupeStore, localID string) {
	t.Helper()
	if _, err := svc.waitForTrackedTerminal(context.Background(), localID, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("waitForTrackedTerminal() error = %v", err)
	}
	store.mu.Lock()
	terminalCalls := store.terminalCalls
	store.mu.Unlock()
	if terminalCalls == 0 {
		t.Fatal("dedupe terminal record was not written after provider terminal")
	}
}

type watchObservedHandle struct {
	*stubTurnHandle
	observed chan struct{}
	once     sync.Once
}

func (h *watchObservedHandle) Done() <-chan struct{} {
	h.once.Do(func() { close(h.observed) })
	return h.stubTurnHandle.Done()
}
