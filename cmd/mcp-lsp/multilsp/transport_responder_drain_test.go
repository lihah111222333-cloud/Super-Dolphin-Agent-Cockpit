package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

// newTestTransport builds an in-memory transport shell (no subprocess)
// suitable for unit testing spawnResponder / drainResponders semantics
// without spinning up a real language server binary. Fields touched by the
// responder (writeMu, pending, done) are populated so writeMessage
// can fail predictably via a nil stdin.
func newTestTransport() *transport {
	return &transport{
		pending: map[string]chan pendingResult{},
		done:    make(chan struct{}),
	}
}

// TestTransportSpawnResponderAfterCloseIsNoop asserts P22 P2 LSP-S3:
// once Close has flipped the closed flag, spawnResponder must not
// register a new goroutine — otherwise the drain would race against
// fresh work spawned behind its back.
func TestTransportSpawnResponderAfterCloseIsNoop(t *testing.T) {
	tr := newTestTransport()
	tr.closed.Store(true)

	// A nominal envelope is enough; spawnResponder should reject it
	// before any goroutine launches.
	tr.spawnResponder(protocol.Envelope{Method: "client/registerCapability", ID: json.RawMessage(`1`)})

	if err := tr.drainResponders(200 * time.Millisecond); err != nil {
		t.Fatalf("drainResponders() on empty group error = %v, want nil", err)
	}
}

// TestTransportDrainRespondersBoundedByTimeout asserts that a
// responder goroutine that refuses to finish does not pin Close
// forever: drainResponders must time out and surface an error while
// the caller proceeds with killProcess / waitForExit. This matches
// plan §492: Close must join/drain but stuck peers cannot block
// shutdown past the caller's stop budget.
func TestTransportDrainRespondersBoundedByTimeout(t *testing.T) {
	tr := newTestTransport()

	// Pretend a responder is still in-flight. The WaitGroup counter
	// sits at 1 until we explicitly Done() below, simulating the
	// stuck-responder scenario. Using Add directly (rather than
	// spawnResponder) lets us keep the test synchronous.
	tr.responderWG.Add(1)
	defer tr.responderWG.Done()

	start := time.Now()
	err := tr.drainResponders(50 * time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("drainResponders() with stuck responder = nil err, want timeout error")
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("drainResponders() returned too early: elapsed=%v", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("drainResponders() returned too late: elapsed=%v", elapsed)
	}
}

// TestTransportSpawnResponderRegistersWithWaitGroup asserts the
// core invariant of spawnResponder: the responder goroutine must be
// counted so drainResponders actually blocks on it. We simulate this
// by wiring a slow requestHandler that signals when invoked, then
// confirm drainResponders does not return before the handler
// completes.
func TestTransportSpawnResponderRegistersWithWaitGroup(t *testing.T) {
	handlerFired := make(chan struct{})
	tr := newTestTransport()
	tr.requestHandler = func(_ context.Context, method string, _ json.RawMessage) (any, error) {
		close(handlerFired)
		// Simulate a slow handler; keep the goroutine alive long
		// enough for the assertion below to catch the WaitGroup.
		time.Sleep(30 * time.Millisecond)
		return struct{}{}, nil
	}

	var drainStarted atomic.Bool
	done := make(chan error, 1)
	tr.spawnResponder(protocol.Envelope{Method: "client/registerCapability", ID: json.RawMessage(`1`)})
	<-handlerFired
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		drainStarted.Store(true)
		done <- tr.drainResponders(2 * time.Second)
	})

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("drainResponders() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("drainResponders() did not return within 2s; responder goroutine may not be tracked by the WaitGroup")
	}
	if !drainStarted.Load() {
		t.Fatalf("drainResponders goroutine did not run")
	}
}

func TestTransportReadFailureWaitsForProcessErrorOnEOF(t *testing.T) {
	tr := newTestTransport()
	want := errors.New("markdown server exited: missing vscode-uri")
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		time.Sleep(20 * time.Millisecond)
		tr.doneMu.Lock()
		tr.doneErr = want
		tr.doneMu.Unlock()
		close(tr.done)
	})

	got := tr.readFailure(io.EOF)
	if !errors.Is(got, want) {
		t.Fatalf("readFailure(io.EOF) = %v, want process error %v", got, want)
	}
}

type countingProcessTreeOwner struct {
	terminateCalls atomic.Int32
	releaseCalls   atomic.Int32
	prepareCalls   atomic.Int32
	rssCalls       atomic.Int32
	rss            uint64
	releaseErr     error
	prepareErr     error
}

func (o *countingProcessTreeOwner) Terminate() error {
	o.terminateCalls.Add(1)
	return nil
}

func (o *countingProcessTreeOwner) Release() error {
	call := o.releaseCalls.Add(1)
	if call == 1 && o.releaseErr != nil {
		return o.releaseErr
	}
	return nil
}

func (o *countingProcessTreeOwner) RSSBytes() (uint64, error) {
	o.rssCalls.Add(1)
	return o.rss, nil
}

// PrepareShutdown 记录协议 shutdown/exit 前的 owner 入册调用，并可注入准备失败。
func (o *countingProcessTreeOwner) PrepareShutdown() error {
	o.prepareCalls.Add(1)
	return o.prepareErr
}

func TestTransportConcurrentCloseTerminatesExplicitOwnerOnce(t *testing.T) {
	owner := &countingProcessTreeOwner{}
	done := make(chan struct{})
	close(done)
	tr := &transport{
		processTree: owner,
		pending:     map[string]chan pendingResult{},
		done:        done,
	}
	const closeCount = 16
	errs := make(chan error, closeCount)
	var closers sync.WaitGroup
	for range closeCount {
		closers.Go(func() {
			errs <- tr.Close()
		})
	}
	closers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Close() error = %v", err)
		}
	}
	if got := owner.terminateCalls.Load(); got != 1 {
		t.Fatalf("Terminate() calls = %d, want 1", got)
	}
	if got := owner.releaseCalls.Load(); got != 1 {
		t.Fatalf("Release() calls = %d, want 1", got)
	}
}

func TestTransportCloseRetriesProcessTreeReleaseAfterFailure(t *testing.T) {
	releaseErr := errors.New("injected process-tree release failure")
	owner := &countingProcessTreeOwner{releaseErr: releaseErr}
	tr := newTestTransportWithExitedProcess()
	tr.processTree = owner

	if err := tr.Close(); !errors.Is(err, releaseErr) {
		t.Fatalf("first Close() error = %v, want %v", err, releaseErr)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("retry Close() error = %v, want nil", err)
	}
	if got := owner.terminateCalls.Load(); got != 1 {
		t.Fatalf("Terminate() calls = %d, want 1", got)
	}
	if got := owner.releaseCalls.Load(); got != 2 {
		t.Fatalf("Release() calls = %d, want 2", got)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("completed Close() error = %v, want nil", err)
	}
	if got := owner.releaseCalls.Load(); got != 2 {
		t.Fatalf("Release() calls after completed Close = %d, want 2", got)
	}
}

func TestTransportProcessTreeRSSUsesExplicitOwner(t *testing.T) {
	owner := &countingProcessTreeOwner{rss: 4096}
	tr := &transport{processTree: owner}
	got, err := tr.processTreeRSSBytes()
	if err != nil {
		t.Fatalf("processTreeRSSBytes() error = %v", err)
	}
	if got != owner.rss {
		t.Fatalf("processTreeRSSBytes() = %d, want %d", got, owner.rss)
	}
	if calls := owner.rssCalls.Load(); calls != 1 {
		t.Fatalf("RSSBytes() calls = %d, want 1", calls)
	}
}

func TestTransportResponderHandlerUsesActorContext(t *testing.T) {
	type actorContextKey struct{}
	actorCtx, cancelActors := context.WithCancel(context.WithValue(context.Background(), actorContextKey{}, "actor"))
	releaseHandler := make(chan struct{})
	defer close(releaseHandler)
	handlerEntered := make(chan struct{})
	handlerExited := make(chan struct{})
	var actorContextMatched atomic.Bool

	tr := newTestTransport()
	tr.actorCtx = actorCtx
	tr.cancelActors = cancelActors
	tr.requestHandler = func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		actorContextMatched.Store(ctx.Value(actorContextKey{}) == "actor")
		close(handlerEntered)
		defer close(handlerExited)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releaseHandler:
			return struct{}{}, nil
		}
	}

	tr.spawnResponder(protocol.Envelope{Method: "client/registerCapability", ID: json.RawMessage(`1`)})
	<-handlerEntered
	tr.cancelActorContext()
	if err := tr.drainResponders(250 * time.Millisecond); err != nil {
		t.Fatalf("drainResponders() after actor cancellation error = %v", err)
	}
	if !actorContextMatched.Load() {
		t.Fatal("request handler did not receive transport actor context")
	}
	select {
	case <-handlerExited:
	default:
		t.Fatal("request handler did not exit after actor context cancellation")
	}
}

func TestTransportConcurrentCloseSharesResponderDrain(t *testing.T) {
	handlerEntered := make(chan struct{})
	releaseHandler := make(chan struct{})
	tr := newTestTransportWithExitedProcess()
	tr.requestHandler = func(context.Context, string, json.RawMessage) (any, error) {
		close(handlerEntered)
		<-releaseHandler
		return struct{}{}, nil
	}
	tr.spawnResponder(protocol.Envelope{Method: "client/registerCapability", ID: json.RawMessage(`1`)})
	<-handlerEntered

	const closeCount = 8
	results := make(chan error, closeCount)
	runtimesafe.SafeGo(t.Context(), nil, "multilsp.transport.concurrent-close.owner.test", func(context.Context) {
		results <- tr.Close()
	})
	waitForTransportClosed(t, tr)
	for range closeCount - 1 {
		runtimesafe.SafeGo(t.Context(), nil, "multilsp.transport.concurrent-close.peer.test", func(context.Context) {
			results <- tr.Close()
		})
	}
	select {
	case err := <-results:
		close(releaseHandler)
		t.Fatalf("concurrent Close returned before shared responder drain completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseHandler)
	for range closeCount {
		if err := <-results; err != nil {
			t.Fatalf("concurrent Close() error = %v", err)
		}
	}
}

func TestTransportCloseRetriesResponderDrainAfterTimeout(t *testing.T) {
	handlerEntered := make(chan struct{})
	releaseHandler := make(chan struct{})
	tr := newTestTransportWithExitedProcess()
	tr.requestHandler = func(context.Context, string, json.RawMessage) (any, error) {
		close(handlerEntered)
		<-releaseHandler
		return struct{}{}, nil
	}
	tr.spawnResponder(protocol.Envelope{Method: "client/registerCapability", ID: json.RawMessage(`1`)})
	<-handlerEntered

	if err := tr.Close(); err == nil {
		close(releaseHandler)
		t.Fatal("first Close() error = nil, want responder drain timeout")
	}
	retryResult := make(chan error, 1)
	runtimesafe.SafeGo(t.Context(), nil, "multilsp.transport.retry-close.test", func(context.Context) {
		retryResult <- tr.Close()
	})
	select {
	case err := <-retryResult:
		close(releaseHandler)
		t.Fatalf("retry Close returned before responder cleanup completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseHandler)
	if err := <-retryResult; err != nil {
		t.Fatalf("retry Close() error = %v, want successful retained cleanup", err)
	}
}

func newTestTransportWithExitedProcess() *transport {
	tr := newTestTransport()
	close(tr.done)
	return tr
}

func waitForTransportClosed(t *testing.T, tr *transport) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !tr.closed.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !tr.closed.Load() {
		t.Fatal("timed out waiting for transport Close to seal admission")
	}
}

func TestTransportResponderAdmissionAndCloseUseSameBarrier(t *testing.T) {
	tr := newTestTransportWithExitedProcess()
	tr.responderMu.Lock()
	barrierLocked := true
	defer func() {
		if barrierLocked {
			tr.responderMu.Unlock()
		}
	}()

	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	spawnReturned := make(chan struct{})
	closeReturned := make(chan error, 1)
	runtimesafe.SafeGo(t.Context(), nil, "multilsp.transport.admission-spawn.test", func(context.Context) {
		ready <- struct{}{}
		<-start
		tr.spawnResponder(protocol.Envelope{Method: "client/registerCapability", ID: json.RawMessage(`1`)})
		close(spawnReturned)
	})
	runtimesafe.SafeGo(t.Context(), nil, "multilsp.transport.admission-close.test", func(context.Context) {
		ready <- struct{}{}
		<-start
		closeReturned <- tr.Close()
	})
	<-ready
	<-ready
	close(start)

	select {
	case <-spawnReturned:
		t.Fatal("spawnResponder bypassed the responder admission barrier")
	case err := <-closeReturned:
		t.Fatalf("Close bypassed the responder admission barrier: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	tr.responderMu.Unlock()
	barrierLocked = false

	select {
	case <-spawnReturned:
	case <-time.After(time.Second):
		t.Fatal("spawnResponder remained blocked after admission barrier release")
	}
	select {
	case err := <-closeReturned:
		if err != nil {
			t.Fatalf("Close() after admission barrier release error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close remained blocked after admission barrier release")
	}
}
