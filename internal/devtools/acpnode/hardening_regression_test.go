package acpnode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInboundRequestIDsDoNotEvictActiveOrOverwriteCancellation(t *testing.T) {
	client, _ := testClient(t, nil)
	if err := client.registerInboundID("active"); err != nil {
		t.Fatal(err)
	}
	for i := range MaxPending + 1 {
		if err := client.registerInboundID(fmt.Sprintf("completed-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := client.registerInboundID("active"); err == nil {
		t.Fatal("active inbound request ID was evicted and reused")
	}
	if err := client.registerInboundID("same"); err != nil {
		t.Fatal(err)
	}
	client.reverseSlots <- struct{}{}
	defer client.releaseReverse("same")
	_, cancel, ok := client.registerReverse("same")
	if !ok {
		t.Fatal("first reverse registration failed")
	}
	defer cancel()
	_, secondCancel, ok := client.registerReverse("same")
	if ok {
		secondCancel()
		t.Fatal("reverse cancellation entry was overwritten")
	}
	secondCancel()
}

func TestIDMatchesOfficialRequestIDUnionAndRejectsLossyNumbers(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "null", raw: "null", ok: true},
		{name: "minimum int64", raw: "-9223372036854775808", ok: true},
		{name: "maximum int64", raw: "9223372036854775807", ok: true},
		{name: "string", raw: `"request-id"`, ok: true},
		{name: "fraction", raw: "1.5"},
		{name: "overflow", raw: "9223372036854775808"},
		{name: "underflow", raw: "-9223372036854775809"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := decodeID(json.RawMessage(tt.raw))
			if (err == nil) != tt.ok {
				t.Fatalf("decodeID(%s) error = %v, want ok=%v", tt.raw, err, tt.ok)
			}
			if tt.ok && id.String() != tt.raw {
				t.Fatalf("round trip id = %q, want %q", id.String(), tt.raw)
			}
		})
	}
}

func TestSemanticIDKeyMatchesEscapesAndExactIntegerEncodings(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
		want  bool
	}{
		{name: "escaped strings", left: `"a"`, right: `"\u0061"`, want: true},
		{name: "decimal integer", left: `1`, right: `1.0`, want: true},
		{name: "exponent integer", left: `77`, right: `7.7e1`, want: true},
		{name: "negative zero", left: `0`, right: `-0.0`, want: true},
		{name: "different type", left: `1`, right: `"1"`, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			left, err := semanticIDKey(json.RawMessage(tt.left))
			if err != nil {
				t.Fatal(err)
			}
			right, err := semanticIDKey(json.RawMessage(tt.right))
			if err != nil {
				t.Fatal(err)
			}
			if (left == right) != tt.want {
				t.Fatalf("semantic keys = %q/%q, want match=%v", left, right, tt.want)
			}
		})
	}
}

func TestSemanticIDLookupMatchesEscapedResponse(t *testing.T) {
	client, process := testClient(t, nil)
	ctx := t.Context()
	done := make(chan error, 1)
	runTestAsync(t, func() {
		_, err := client.request(ctx, "semantic-id", map[string]any{})
		done <- err
	})
	request := nextPeerMessage(t, process)
	if string(request.ID) != "1" {
		t.Fatalf("request id = %s", request.ID)
	}
	sendPeerMessage(t, process, Message{JSONRPC: "2.0", ID: json.RawMessage(`1.0`), Result: json.RawMessage(`true`)})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("semantic response error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("semantic response did not resolve")
	}
}

func TestRedactorHandlesArraysAndStructArraysWithoutPlaintext(t *testing.T) {
	redactor, err := NewRedactor()
	if err != nil {
		t.Fatal(err)
	}
	type secret struct {
		Token string
	}
	value := [2]secret{{Token: "array-secret"}, {Token: "second-secret"}}
	encoded, err := json.Marshal(redactor.LogValue(value))
	if err != nil {
		t.Fatal(err)
	}
	if text := string(encoded); strings.Contains(text, "secret") {
		t.Fatalf("array redaction leaked plaintext: %s", text)
	}
}

func TestIDBearingSpecialMethodsFailBeforeMutation(t *testing.T) {
	client, process := testClient(t, nil)
	markTestInitialized(client)
	client.mu.Lock()
	client.sessions["s1"] = &sessionState{id: "s1", generation: client.generation}
	client.mu.Unlock()
	sendPeerMessage(t, process, Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "session/update",
		Params:  rawJSON(t, map[string]any{"sessionId": "s1", "update": map[string]any{"secret": true}}),
	})
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("id-bearing session/update did not fail")
	}
	client.mu.Lock()
	queued := len(client.updates)
	client.mu.Unlock()
	if queued != 0 {
		t.Fatalf("id-bearing update mutated queue: %d", queued)
	}
}

func TestInboundHandlerNeverSeesScalarParams(t *testing.T) {
	called := make(chan struct{}, 1)
	client, process := testClient(t, func(context.Context, string, json.RawMessage) (any, error) {
		called <- struct{}{}
		return map[string]any{"unexpected": true}, nil
	})
	if _, err := process.peerOut.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"reverse\",\"params\":null}\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("scalar inbound params did not terminate the client")
	}
	select {
	case <-called:
		t.Fatal("handler received scalar params")
	default:
	}
}

func TestPeerCancelRequestCancelsInboundReverse(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	client, process := testClient(t, func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	})
	markTestInitialized(client)
	sendPeerMessage(t, process, Message{JSONRPC: "2.0", ID: json.RawMessage(`77`), Method: "reverse", Params: rawJSON(t, map[string]any{})})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("reverse handler did not start")
	}
	sendPeerMessage(t, process, Message{JSONRPC: "2.0", Method: "$/cancel_request", Params: rawJSON(t, map[string]any{"requestId": 77})})
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("peer cancel did not cancel reverse handler")
	}
}

func TestCancelRollsBackWhenNotificationCannotBeSent(t *testing.T) {
	client, _ := testClient(t, nil)
	markTestInitialized(client)
	turn := &turnState{}
	client.mu.Lock()
	client.sessions["s1"] = &sessionState{id: "s1", generation: client.generation, active: turn}
	client.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.Cancel(ctx, "s1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Cancel() error = %v", err)
	}
	turn.mu.Lock()
	cancelRequested := turn.cancelRequested
	turn.mu.Unlock()
	if cancelRequested {
		t.Fatal("cancelRequested mutated before notification succeeded")
	}
}

func TestCancelCommitsAfterSuccessfulSendEvenIfContextIsCanceled(t *testing.T) {
	baseProcess := newFakeProcess()
	delayed := &delayedTestStdin{WriteCloser: baseProcess.Stdin(), entered: make(chan struct{}), release: make(chan struct{})}
	process := &delayedTestProcess{Process: baseProcess, stdin: delayed}
	client, err := NewClient(testLaunchConfigForClient(t), fakeFactory{process: process}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		baseProcess.release()
		if closeErr := client.Close(); closeErr != nil && !errors.Is(closeErr, ErrShutdownTimeout) {
			t.Errorf("cleanup Close() error = %v", closeErr)
		}
	})
	markTestInitialized(client)
	turn := &turnState{}
	client.mu.Lock()
	client.sessions["s1"] = &sessionState{id: "s1", generation: client.generation, active: turn}
	client.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	runTestAsync(t, func() { done <- client.Cancel(ctx, "s1") })
	request := nextPeerMessage(t, baseProcess)
	if request.Method != "session/cancel" {
		t.Fatalf("cancel request = %+v", request)
	}
	cancel()
	delayed.releaseWrite()
	if err := <-done; err != nil {
		t.Fatalf("successful cancel write was rolled back: %v", err)
	}
	turn.mu.Lock()
	committed := turn.cancelRequested
	turn.mu.Unlock()
	if !committed {
		t.Fatal("successful cancel write did not commit cancelRequested")
	}
}

func TestNewSessionCommitFailureReleasesExactlyOnceAndCompensates(t *testing.T) {
	client, process := testClient(t, nil)
	initializeTestClient(t, client, process, map[string]any{})
	client.mu.Lock()
	client.sessions["existing"] = &sessionState{id: "existing", generation: client.generation}
	client.mu.Unlock()

	firstDone := make(chan error, 1)
	runTestAsync(t, func() {
		_, err := client.NewSession(context.Background(), map[string]any{"cwd": "/tmp", "mcpServers": []any{}})
		firstDone <- err
	})
	newRequest := nextPeerMessage(t, process)
	sendPeerMessage(t, process, Message{JSONRPC: "2.0", ID: newRequest.ID, Result: rawJSON(t, map[string]any{"sessionId": "existing"})})
	closeRequest := nextPeerMessage(t, process)
	if closeRequest.Method != "session/close" {
		t.Fatalf("compensating request = %+v", closeRequest)
	}
	sendPeerMessage(t, process, Message{JSONRPC: "2.0", ID: closeRequest.ID, Result: rawJSON(t, map[string]any{})})
	if err := <-firstDone; err == nil || !strings.Contains(err.Error(), "duplicate session") {
		t.Fatalf("commit failure = %v", err)
	}

	client.mu.Lock()
	reservations, sessionCount := client.sessionReservations, len(client.sessions)
	client.mu.Unlock()
	if reservations != 0 || sessionCount != 1 {
		t.Fatalf("local reservation/session state = %d/%d", reservations, sessionCount)
	}

	secondDone := make(chan error, 1)
	runTestAsync(t, func() {
		_, err := client.NewSession(context.Background(), map[string]any{"cwd": "/tmp", "mcpServers": []any{}})
		secondDone <- err
	})
	secondRequest := nextPeerMessage(t, process)
	sendPeerMessage(t, process, Message{JSONRPC: "2.0", ID: secondRequest.ID, Result: rawJSON(t, map[string]any{"sessionId": "fresh"})})
	if err := <-secondDone; err != nil {
		t.Fatalf("reservation was not released exactly once: %v", err)
	}
}

func TestResumeRequiresStrictSessionCapabilityBeforeReservation(t *testing.T) {
	tests := map[string]map[string]any{
		"missing":   {},
		"false":     {"sessionCapabilities": map[string]any{"resume": false}},
		"malformed": {"sessionCapabilities": "not-an-object"},
	}
	for name, caps := range tests {
		t.Run(name, func(t *testing.T) {
			client, process := testClient(t, nil)
			initializeTestClient(t, client, process, caps)
			_, err := client.ResumeSession(context.Background(), map[string]any{"sessionId": "resume-me"})
			if err == nil || !strings.Contains(err.Error(), "resume") {
				t.Fatalf("ResumeSession() error = %v", err)
			}
			client.mu.Lock()
			pending, sessions := len(client.pending), len(client.sessions)
			client.mu.Unlock()
			if pending != 0 || sessions != 0 {
				t.Fatalf("resume mutated local state before capability failure: pending=%d sessions=%d", pending, sessions)
			}
		})
	}
}

func TestSessionUpdateUnknownAndStaleStatesAreObservable(t *testing.T) {
	client, _ := testClient(t, nil)
	markTestInitialized(client)
	raw := rawJSON(t, map[string]any{"sessionId": "unknown", "update": map[string]any{"ok": true}})
	if err := client.publishSessionUpdate("unknown", raw, nil); err == nil || !strings.Contains(err.Error(), "unknown session update") {
		t.Fatalf("unknown session update error = %v", err)
	}
	client.mu.Lock()
	client.sessions["stale"] = &sessionState{id: "stale", generation: client.generation - 1}
	client.mu.Unlock()
	if _, err := client.activeTurn("unknown"); err == nil || !strings.Contains(err.Error(), "unknown session") {
		t.Fatalf("unknown active session error = %v", err)
	}
	if _, err := client.activeTurn("stale"); err == nil || !strings.Contains(err.Error(), "stale session") {
		t.Fatalf("stale active session error = %v", err)
	}
	if err := client.publishSessionUpdate("stale", raw, nil); err == nil || !strings.Contains(err.Error(), "stale session update") {
		t.Fatalf("stale session update error = %v", err)
	}
	client.terminate(nil)
	if err := client.publishSessionUpdate("late", raw, nil); err != nil {
		t.Fatalf("teardown-late update was not ignored: %v", err)
	}
}

func TestNonCooperativeLifecycleActionRetainsOwner(t *testing.T) {
	release := make(chan struct{})
	err := boundedLifecycleAction(func() error {
		<-release
		return nil
	}, time.Millisecond)
	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("bounded lifecycle error = %v", err)
	}
	var pending *lifecycleActionPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("lifecycle owner missing from error: %v", err)
	}
	close(release)
	select {
	case <-pending.Done():
	case <-time.After(time.Second):
		t.Fatal("lifecycle owner did not finish after release")
	}
}

func TestClientCloseRetainsNonCooperativeWaitOwner(t *testing.T) {
	process := &nonCooperativeWaitProcess{release: make(chan struct{})}
	client, err := NewClient(testLaunchConfigForClient(t), fakeFactory{process: process}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = client.Close()
	var owner ownerLifecycle
	if !errors.As(err, &owner) {
		t.Fatalf("Close() omitted the non-cooperative Wait owner: %v", err)
	}
	close(process.release)
	if joinErr := owner.Join(); joinErr != nil {
		t.Fatalf("joined Wait owner error = %v", joinErr)
	}
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("client did not become done after releasing Wait owner")
	}
}

type nonCooperativeReadCloser struct {
	release chan struct{}
	done    chan struct{}
	once    sync.Once
}

func newNonCooperativeReadCloser() *nonCooperativeReadCloser {
	return &nonCooperativeReadCloser{release: make(chan struct{}), done: make(chan struct{})}
}

func (r *nonCooperativeReadCloser) Read([]byte) (int, error) {
	<-r.release
	r.once.Do(func() { close(r.done) })
	return 0, io.EOF
}

func (r *nonCooperativeReadCloser) Close() error { return nil }

func (r *nonCooperativeReadCloser) releaseRead() {
	select {
	case <-r.release:
	default:
		close(r.release)
	}
}

type nonCooperativeStreamProcess struct {
	*fakeProcess
	stdout *nonCooperativeReadCloser
	stderr *nonCooperativeReadCloser
}

func (p *nonCooperativeStreamProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *nonCooperativeStreamProcess) Stderr() io.ReadCloser { return p.stderr }

func TestClientCloseRetainsNonCooperativeStreamOwnersUntilRelease(t *testing.T) {
	base := newFakeProcess()
	process := &nonCooperativeStreamProcess{
		fakeProcess: base,
		stdout:      newNonCooperativeReadCloser(),
		stderr:      newNonCooperativeReadCloser(),
	}
	client, err := NewClient(testLaunchConfigForClient(t), fakeFactory{process: process}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = client.Close()
	var owner ownerLifecycle
	if !errors.As(err, &owner) {
		t.Fatalf("Close() omitted non-cooperative stdout/stderr owners: %v", err)
	}
	process.stdout.releaseRead()
	process.stderr.releaseRead()
	if joinErr := owner.Join(); joinErr != nil && !errors.Is(joinErr, io.EOF) {
		t.Fatalf("joined stream owner error = %v", joinErr)
	}
	for name, done := range map[string]<-chan struct{}{
		"stdout": process.stdout.done,
		"stderr": process.stderr.done,
	} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s owner did not finish after stream release", name)
		}
	}
	base.release()
}

type nonCooperativeWriteCloser struct {
	release     chan struct{}
	started     chan struct{}
	releaseOnce sync.Once
	startOnce   sync.Once
}

func newNonCooperativeWriteCloser() *nonCooperativeWriteCloser {
	return &nonCooperativeWriteCloser{release: make(chan struct{}), started: make(chan struct{})}
}

func (w *nonCooperativeWriteCloser) Write([]byte) (int, error) {
	w.startOnce.Do(func() { close(w.started) })
	<-w.release
	return 0, io.ErrClosedPipe
}

func (w *nonCooperativeWriteCloser) Close() error { return nil }

func (w *nonCooperativeWriteCloser) releaseWrite() {
	w.releaseOnce.Do(func() { close(w.release) })
}

type nonCooperativeWriteProcess struct {
	*fakeProcess
	stdin *nonCooperativeWriteCloser
}

func (p *nonCooperativeWriteProcess) Stdin() io.WriteCloser { return p.stdin }

func TestCloseWaitsForGatedWriteOwnerAdmission(t *testing.T) {
	process := &nonCooperativeWriteProcess{
		fakeProcess: newFakeProcess(),
		stdin:       newNonCooperativeWriteCloser(),
	}
	client, err := NewClient(testLaunchConfigForClient(t), fakeFactory{process: process}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		process.stdin.releaseWrite()
		process.release()
		_ = client.Close()
	})
	client.writeAdmissionMu.Lock()
	requestDone := make(chan error, 1)
	runTestAsync(t, func() {
		_, requestErr := client.request(context.Background(), "gated", map[string]any{})
		requestDone <- requestErr
	})
	waitForPendingReservation(t, client)
	closeDone := make(chan error, 1)
	runTestAsync(t, func() { closeDone <- client.Close() })
	assertCloseWaitsForAdmission(t, client, closeDone)
	client.writeAdmissionMu.Unlock()
	waitForTestSignal(t, process.stdin.started, "gated writer did not start after admission release")
	owner := waitForWriteOwner(t, client)
	closeErr := <-closeDone
	var pending *writeOwnerPendingError
	if !errors.As(closeErr, &pending) {
		t.Fatalf("Close omitted admitted pending write owner: %v", closeErr)
	}
	process.stdin.releaseWrite()
	if joinErr := owner.Join(); !errors.Is(joinErr, io.ErrClosedPipe) {
		t.Fatalf("joined write owner error = %v", joinErr)
	}
	waitForTestSignal(t, client.Done(), "client Done did not close after gated write owner joined")
	waitForTestSignal(t, requestDone, "gated request did not return")
}

func waitForPendingReservation(t *testing.T, client *Client) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		client.mu.Lock()
		pending := len(client.pending)
		client.mu.Unlock()
		if pending == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	client.writeAdmissionMu.Unlock()
	t.Fatal("gated request did not reserve pending state")
}

func assertCloseWaitsForAdmission(t *testing.T, client *Client, closeDone <-chan error) {
	t.Helper()
	select {
	case err := <-closeDone:
		client.writeAdmissionMu.Unlock()
		t.Fatalf("Close completed before gated write admission: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
}

func waitForWriteOwner(t *testing.T, client *Client) *writeOwner {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		client.writeOwnersMu.Lock()
		for candidate := range client.writeOwners {
			client.writeOwnersMu.Unlock()
			return candidate
		}
		client.writeOwnersMu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("gated write owner was not admitted")
	return nil
}

func waitForTestSignal[T any](t *testing.T, signal <-chan T, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(failure)
	}
}

type delayedErrorReadCloser struct {
	release     chan struct{}
	started     chan struct{}
	releaseOnce sync.Once
	startOnce   sync.Once
	err         error
}

func newDelayedErrorReadCloser(err error) *delayedErrorReadCloser {
	return &delayedErrorReadCloser{
		release: make(chan struct{}),
		started: make(chan struct{}),
		err:     err,
	}
}

func (r *delayedErrorReadCloser) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.release
	return 0, r.err
}

func (r *delayedErrorReadCloser) Close() error { return nil }

func (r *delayedErrorReadCloser) releaseRead() {
	r.releaseOnce.Do(func() { close(r.release) })
}

type delayedStreamErrorProcess struct {
	*fakeProcess
	stdout *delayedErrorReadCloser
	stderr *delayedErrorReadCloser
}

func (p *delayedStreamErrorProcess) Stdout() io.ReadCloser { return p.stdout }
func (p *delayedStreamErrorProcess) Stderr() io.ReadCloser { return p.stderr }
func (p *delayedStreamErrorProcess) Wait() error           { return nil }

func TestDelayedStreamErrorsAfterImmediateNilWaitRemainDurable(t *testing.T) {
	stdoutErr := errors.New("delayed stdout owner error")
	stderrErr := errors.New("delayed stderr owner error")
	process := &delayedStreamErrorProcess{
		fakeProcess: newFakeProcess(),
		stdout:      newDelayedErrorReadCloser(stdoutErr),
		stderr:      newDelayedErrorReadCloser(stderrErr),
	}
	client, err := NewClient(testLaunchConfigForClient(t), fakeFactory{process: process}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		process.stdout.releaseRead()
		process.stderr.releaseRead()
		process.fakeProcess.release()
		_ = client.Close()
	})
	for name, started := range map[string]<-chan struct{}{
		"stdout": process.stdout.started,
		"stderr": process.stderr.started,
	} {
		waitForTestSignal(t, started, name+" delayed reader did not start")
	}
	if err := client.WaitErr(); err != nil {
		t.Fatalf("immediate nil process WaitErr = %v", err)
	}
	process.stdout.releaseRead()
	process.stderr.releaseRead()
	if err := client.stdoutOwner.Join(); !errors.Is(err, stdoutErr) {
		t.Fatalf("stdout owner Join() = %v", err)
	}
	if err := client.stderrOwner.Join(); !errors.Is(err, stderrErr) {
		t.Fatalf("stderr owner Join() = %v", err)
	}
	assertJoinedErrors(t, "WaitErr", client.WaitErr(), stdoutErr, stderrErr)
	assertJoinedErrors(t, "Err", client.Err(), stdoutErr, stderrErr)
	assertJoinedErrors(t, "Close", client.Close(), stdoutErr, stderrErr)
}

func TestCloseSessionRejectsSetupPendingAndDeletedSessionsCannotLoad(t *testing.T) {
	client, _ := testClient(t, nil)
	markTestInitialized(client)
	client.mu.Lock()
	client.sessions["setup"] = &sessionState{id: "setup", generation: client.generation, setupPending: true}
	client.sessions["deleted"] = &sessionState{id: "deleted", generation: client.generation}
	client.mu.Unlock()
	if err := client.validateClosableSession("setup"); err == nil {
		t.Fatal("CloseSession accepted setup-pending session")
	}
	client.finishSessionClose("deleted")
	if err := client.reserveExistingSession("deleted", true); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("LoadSession after deletion error = %v", err)
	}
}

func TestInitializeRequiresCanonicalAgentCapabilities(t *testing.T) {
	client, process := testClient(t, nil)
	done := make(chan error, 1)
	runTestAsync(t, func() { done <- client.Initialize(context.Background(), map[string]any{"name": "test"}) })
	request := nextPeerMessage(t, process)
	sendPeerMessage(t, process, Message{JSONRPC: "2.0", ID: request.ID, Result: rawJSON(t, map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
	})})
	if err := <-done; err == nil {
		t.Fatal("initialize accepted noncanonical capabilities field")
	}
}

func TestWaitErrPreservesLaterWireContamination(t *testing.T) {
	client, process := testClient(t, nil)
	if _, err := process.peerOut.Write([]byte("not-json\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("wire contamination did not terminate client")
	}
	if err := client.WaitErr(); err == nil {
		t.Fatal("WaitErr masked wire contamination after nil process wait")
	}
}

func TestCancelledPromptResponseIsBounded(t *testing.T) {
	raw := json.RawMessage(`{"stopReason":"refusal","payload":"` + strings.Repeat("x", 64) + `"}`)
	canonical, err := cancelledPromptResponse(raw, len(raw))
	if !errors.Is(err, ErrOutboundMessageTooLarge) {
		t.Fatalf("near-limit cancelled prompt response error = %v", err)
	}
	if canonical != nil {
		t.Fatalf("fail-closed cancelled prompt response returned %s", canonical)
	}
}

func TestNewClientCleansIncompleteProcessAndSurfacesCleanupErrors(t *testing.T) {
	cleanup := &partialProcess{
		waitErr:     errors.New("wait cleanup failed"),
		killErr:     errors.New("kill cleanup failed"),
		waitRelease: make(chan struct{}),
	}
	factory := processFactoryFunc(func(context.Context, LaunchConfig) (Process, error) { return cleanup, nil })
	_, err := NewClient(testLaunchConfig(t), factory, nil)
	if err == nil || !strings.Contains(err.Error(), "wait cleanup failed") || !strings.Contains(err.Error(), "kill cleanup failed") {
		t.Fatalf("incomplete process cleanup error = %v", err)
	}
}

func TestNewClientStartupCleanupErrorsAreObservable(t *testing.T) {
	cleanup := &partialProcess{
		waitErr:     errors.New("startup wait cleanup failed"),
		killErr:     errors.New("startup kill cleanup failed"),
		waitRelease: make(chan struct{}),
	}
	factory := &delayedFactory{release: make(chan struct{}), returned: make(chan struct{}), process: cleanup}
	cfg := testLaunchConfig(t)
	cfg.StartupTimeout = time.Millisecond
	done := make(chan error, 1)
	runTestAsync(t, func() {
		_, startErr := NewClient(cfg, factory, nil)
		done <- startErr
	})
	select {
	case <-factory.returned:
		t.Fatal("startup factory returned before timeout")
	case <-time.After(50 * time.Millisecond):
	}
	close(factory.release)
	var err error
	select {
	case err = <-done:
	case <-time.After(time.Second):
		t.Fatal("NewClient did not return after startup cleanup")
	}
	if !errors.Is(err, ErrStartupTimeout) {
		t.Fatalf("startup error = %v", err)
	}
	if !strings.Contains(err.Error(), "startup wait cleanup failed") || !strings.Contains(err.Error(), "startup kill cleanup failed") {
		t.Fatalf("startup cleanup errors not joined: %v", err)
	}
}

func TestLateStartOwnerTracksNonCooperativeFactoryAndSurfacesCleanup(t *testing.T) {
	cleanup := &partialProcess{
		waitErr:     errors.New("late wait cleanup failed"),
		killErr:     errors.New("late kill cleanup failed"),
		waitRelease: make(chan struct{}),
	}
	factory := &delayedFactory{release: make(chan struct{}), returned: make(chan struct{}), process: cleanup}
	cfg := testLaunchConfig(t)
	cfg.StartupTimeout = time.Millisecond
	cfg.ShutdownTimeout = time.Millisecond
	_, err := NewClient(cfg, factory, nil)
	if err == nil || !errors.Is(err, ErrStartupTimeout) {
		t.Fatalf("late startup error = %v", err)
	}
	var late *lateStartError
	if !errors.As(err, &late) {
		t.Fatalf("late startup owner missing from error: %v", err)
	}
	select {
	case <-late.Done():
		t.Fatal("non-cooperative factory was not retained by late owner")
	case <-time.After(10 * time.Millisecond):
	}
	close(factory.release)
	waitForTestSignal(t, factory.returned, "late factory did not return")
	waitForTestSignal(t, late.Done(), "late cleanup owner did not finish")
	assertErrorContains(t, "late cleanup errors", late.Err(), "late wait cleanup failed", "late kill cleanup failed")
}

func TestBlockedProtocolWriterIsInterruptedByRequestContext(t *testing.T) {
	process := newBlockingWriterProcess()
	factory := processFactoryFunc(func(context.Context, LaunchConfig) (Process, error) { return process, nil })
	cfg := testLaunchConfig(t)
	cfg.ShutdownTimeout = 10 * time.Millisecond
	client, err := NewClient(cfg, factory, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	runTestAsync(t, func() {
		_, requestErr := client.request(ctx, "blocked", map[string]any{})
		done <- requestErr
	})
	select {
	case <-process.stdinStarted():
	case <-time.After(time.Second):
		t.Fatal("blocking writer did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked protocol writer was not interrupted")
	}
	if closeErr := client.Close(); closeErr != nil && !errors.Is(closeErr, ErrClientClosed) {
		t.Fatalf("Close() error = %v", closeErr)
	}
}
