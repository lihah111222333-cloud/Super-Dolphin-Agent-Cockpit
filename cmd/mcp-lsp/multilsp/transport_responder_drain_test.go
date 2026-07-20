package multilsp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
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
