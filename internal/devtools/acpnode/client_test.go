package acpnode

import (
	"bytes"
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

type fakeProcess struct {
	clientIn  *io.PipeWriter
	peerIn    *io.PipeReader
	clientOut *io.PipeReader
	peerOut   *io.PipeWriter
	stderr    io.ReadCloser

	waitRelease   chan struct{}
	waitErr       error
	waitCount     int
	interrupts    int
	kills         int
	order         []string
	mu            sync.Mutex
	releaseOnce   sync.Once
	closeOnce     sync.Once
	releaseOnKill bool
	releaseOnInt  bool
}

type fakeFactory struct{ process Process }

func (f fakeFactory) Start(context.Context, LaunchConfig) (Process, error) { return f.process, nil }

func newFakeProcess() *fakeProcess {
	peerIn, clientIn := io.Pipe()
	clientOut, peerOut := io.Pipe()
	return &fakeProcess{
		clientIn:      clientIn,
		peerIn:        peerIn,
		clientOut:     clientOut,
		peerOut:       peerOut,
		stderr:        io.NopCloser(strings.NewReader("")),
		waitRelease:   make(chan struct{}),
		releaseOnKill: true,
	}
}

type trackedStdin struct{ process *fakeProcess }

func (s trackedStdin) Write(data []byte) (int, error) { return s.process.clientIn.Write(data) }
func (s trackedStdin) Close() error {
	return s.process.closeInput()
}

func (p *fakeProcess) Stdin() io.WriteCloser { return trackedStdin{process: p} }
func (p *fakeProcess) Stdout() io.ReadCloser { return p.clientOut }
func (p *fakeProcess) Stderr() io.ReadCloser { return p.stderr }
func (p *fakeProcess) Wait() error {
	p.mu.Lock()
	p.waitCount++
	p.order = append(p.order, "wait")
	p.mu.Unlock()
	<-p.waitRelease
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}
func (p *fakeProcess) Interrupt() error {
	p.mu.Lock()
	p.interrupts++
	p.order = append(p.order, "interrupt")
	release := p.releaseOnInt
	p.mu.Unlock()
	if release {
		p.release()
	}
	return nil
}
func (p *fakeProcess) Kill() error {
	p.mu.Lock()
	p.kills++
	p.order = append(p.order, "kill")
	release := p.releaseOnKill
	p.mu.Unlock()
	if release {
		p.release()
	}
	return nil
}
func (p *fakeProcess) release() {
	p.releaseOnce.Do(func() {
		close(p.waitRelease)
		if err := p.peerOut.Close(); err != nil {
			p.mu.Lock()
			p.waitErr = err
			p.mu.Unlock()
		}
	})
}
func (p *fakeProcess) closeInput() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.order = append(p.order, "stdin-close")
		p.mu.Unlock()
	})
	return p.clientIn.Close()
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("injected write failure") }
func (failingWriter) Close() error              { return nil }

type failingStdinProcess struct{ *fakeProcess }

func (p failingStdinProcess) Stdin() io.WriteCloser { return failingWriter{} }

func testClient(t *testing.T, reverse ReverseRequestHandler) (*Client, *fakeProcess) {
	t.Helper()
	p := newFakeProcess()
	cfg := LaunchConfig{
		Enabled:         true,
		Executable:      "/bin/sh",
		CWD:             t.TempDir(),
		Env:             []string{"PATH=/usr/bin"},
		EnvAllowlist:    []string{"PATH"},
		StartupTimeout:  time.Second,
		RequestTimeout:  time.Second,
		ShutdownTimeout: 10 * time.Millisecond,
		MaxMessage:      DefaultMaxMessage,
		MaxStderr:       DefaultMaxStderr,
	}
	client, err := NewClient(cfg, fakeFactory{process: p}, reverse)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.release()
		if err := client.Close(); err != nil && !errors.Is(err, ErrShutdownTimeout) {
			t.Errorf("cleanup Close() error = %v", err)
		}
	})
	return client, p
}

func testClientWithMaxMessage(t *testing.T, max int) (*Client, *fakeProcess) {
	t.Helper()
	p := newFakeProcess()
	cfg := testLaunchConfigForClient(t)
	cfg.MaxMessage = max
	client, err := NewClient(cfg, fakeFactory{process: p}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.release()
		if err := client.Close(); err != nil && !errors.Is(err, ErrShutdownTimeout) {
			t.Errorf("cleanup Close() error = %v", err)
		}
	})
	return client, p
}

func rawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := mustJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func nextPeerMessage(t *testing.T, p *fakeProcess) Message {
	t.Helper()
	m, err := readMessage(p.peerIn, DefaultMaxMessage)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func sendPeerMessage(t *testing.T, p *fakeProcess, m Message) {
	t.Helper()
	if err := writeMessage(p.peerOut, m); err != nil {
		t.Fatal(err)
	}
}

func initializeTestClient(t *testing.T, c *Client, p *fakeProcess, caps map[string]any) {
	t.Helper()
	done := make(chan error, 1)
	runTestAsync(t, func() { done <- c.Initialize(context.Background(), map[string]any{"name": "test", "version": "1"}) })
	req := nextPeerMessage(t, p)
	if req.Method != "initialize" || len(req.ID) == 0 {
		t.Fatalf("initialize request = %+v", req)
	}
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: req.ID, Result: rawJSON(t, map[string]any{
		"protocolVersion":   ProtocolVersion,
		"agentCapabilities": caps,
	})})
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("initialize did not complete")
	}
}

func TestClientReverseRequestCanNestOutgoingRequest(t *testing.T) {
	var client *Client
	client, p := testClient(t, func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method != "reverse" {
			return nil, fmt.Errorf("method = %s", method)
		}
		raw, err := client.request(ctx, "nested", map[string]any{"from": "reverse"})
		if err != nil {
			return nil, err
		}
		return map[string]any{"nested": json.RawMessage(raw)}, nil
	})
	initializeTestClient(t, client, p, map[string]any{})
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: json.RawMessage(`99`), Method: "reverse", Params: rawJSON(t, map[string]any{})})
	nested := nextPeerMessage(t, p)
	if nested.Method != "nested" {
		t.Fatalf("nested request = %+v", nested)
	}
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: nested.ID, Result: rawJSON(t, map[string]any{"ok": true})})
	response := nextPeerMessage(t, p)
	if response.Error != nil || response.Result == nil || string(response.ID) != "99" {
		t.Fatalf("reverse response = %+v", response)
	}
}

func TestClientNilAndFailingReverseHandlersReturnValidErrorOnlyResponses(t *testing.T) {
	client, p := testClient(t, nil)
	initializeTestClient(t, client, p, map[string]any{})
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "unknown", Params: rawJSON(t, map[string]any{})})
	response := nextPeerMessage(t, p)
	if response.Error == nil || response.Result != nil {
		t.Fatalf("nil handler response = %+v", response)
	}
	if response.Error.Code != -32601 {
		t.Fatalf("error code = %d", response.Error.Code)
	}
	var failing *Client
	failing, p2 := testClient(t, func(context.Context, string, json.RawMessage) (any, error) {
		return nil, errors.New("handler failed")
	})
	initializeTestClient(t, failing, p2, map[string]any{})
	sendPeerMessage(t, p2, Message{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "failing", Params: rawJSON(t, map[string]any{})})
	response = nextPeerMessage(t, p2)
	if response.Error == nil || response.Result != nil || response.Error.Code != -32601 {
		t.Fatalf("failing handler response = %+v", response)
	}
}

func TestClientPendingEntriesAreRemovedOnCancelTimeoutAndSendFailure(t *testing.T) {
	client, p := testClient(t, nil)
	initializeTestClient(t, client, p, map[string]any{})
	testPendingCancellation(t, client, p)
	testPendingTimeout(t, client, p)
	testPendingSendFailure(t)
}

func TestRequestRawOversizePreflightPreservesRequestState(t *testing.T) {
	client, _ := testClientWithMaxMessage(t, 128)
	params := json.RawMessage(`{"payload":"` + strings.Repeat("x", 128) + `"}`)

	_, err := client.requestRaw(context.Background(), "preflight", params, time.Second)
	if !errors.Is(err, ErrOutboundMessageTooLarge) {
		t.Fatalf("oversized requestRaw error = %v", err)
	}
	client.mu.Lock()
	next, pending := client.next, len(client.pending)
	client.mu.Unlock()
	if next != 0 || pending != 0 {
		t.Fatalf("oversized requestRaw mutated request state: next=%d pending=%d", next, pending)
	}
	client.writeOwnersMu.Lock()
	writeOwners := len(client.writeOwners)
	client.writeOwnersMu.Unlock()
	if writeOwners != 0 {
		t.Fatalf("oversized requestRaw started %d write owners", writeOwners)
	}
}

// testPendingCancellation 验证取消中的请求会从活动表移入有界墓碑表。
func testPendingCancellation(t *testing.T, client *Client, p *fakeProcess) {
	ctx, cancel := context.WithCancel(context.Background())
	requestDone := make(chan error, 1)
	runTestAsync(t, func() {
		_, err := client.request(ctx, "cancel-me", map[string]any{})
		requestDone <- err
	})
	req := nextPeerMessage(t, p)
	cancel()
	select {
	case err := <-requestDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled request did not return")
	}
	client.mu.Lock()
	if len(client.pending) != 0 || len(client.tombstones) == 0 {
		t.Fatalf("pending/tombstones after cancel = %d/%d", len(client.pending), len(client.tombstones))
	}
	client.mu.Unlock()
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: req.ID, Result: rawJSON(t, true)})
}

// testPendingTimeout 验证超时请求会释放 pending 槽位。
func testPendingTimeout(t *testing.T, client *Client, p *fakeProcess) {
	client.cfg.RequestTimeout = time.Millisecond
	timeoutDone := make(chan error, 1)
	runTestAsync(t, func() {
		_, requestErr := client.request(context.Background(), "timeout-me", map[string]any{})
		timeoutDone <- requestErr
	})
	_ = nextPeerMessage(t, p)
	err := <-timeoutDone
	if !errors.Is(err, ErrRequestTimeout) {
		t.Fatalf("timeout error = %v", err)
	}
	client.mu.Lock()
	pendingAfterTimeout := len(client.pending)
	client.mu.Unlock()
	if pendingAfterTimeout != 0 {
		t.Fatalf("pending after timeout = %d", pendingAfterTimeout)
	}
}

// testPendingSendFailure 验证写入失败会清理请求并暴露底层错误。
func testPendingSendFailure(t *testing.T) {
	fake := newFakeProcess()
	failingClient, err := NewClient(testLaunchConfigForClient(t), fakeFactory{process: &failingStdinProcess{fake}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		fake.release()
		if closeErr := failingClient.Close(); closeErr != nil {
			t.Errorf("cleanup Close() error = %v", closeErr)
		}
	}()
	_, err = failingClient.request(context.Background(), "send-fails", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "write failure") {
		t.Fatalf("send failure = %v", err)
	}
	failingClient.mu.Lock()
	pendingAfterSend := len(failingClient.pending)
	failingClient.mu.Unlock()
	if pendingAfterSend != 0 {
		t.Fatalf("pending after send failure = %d", pendingAfterSend)
	}
}

func testLaunchConfigForClient(t *testing.T) LaunchConfig {
	t.Helper()
	cfg := testLaunchConfig(t)
	cfg.ShutdownTimeout = 10 * time.Millisecond
	return cfg
}

func TestClientRejectsUnmatchedAndDuplicateInboundIDs(t *testing.T) {
	client, p := testClient(t, func(context.Context, string, json.RawMessage) (any, error) {
		return true, nil
	})
	initializeTestClient(t, client, p, map[string]any{})
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: json.RawMessage(`404`), Result: rawJSON(t, true)})
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("unmatched response did not contaminate generation")
	}
	if client.Generation() <= 1 || client.Err() == nil {
		t.Fatalf("generation/error = %d/%v", client.Generation(), client.Err())
	}

	client2, p2 := testClient(t, func(context.Context, string, json.RawMessage) (any, error) {
		return true, nil
	})
	initializeTestClient(t, client2, p2, map[string]any{})
	sendPeerMessage(t, p2, Message{JSONRPC: "2.0", ID: json.RawMessage(`7`), Method: "reverse", Params: rawJSON(t, map[string]any{})})
	first := nextPeerMessage(t, p2)
	if first.Method != "" {
		t.Fatalf("unexpected first peer output = %+v", first)
	}
	sendPeerMessage(t, p2, Message{JSONRPC: "2.0", ID: json.RawMessage(`7`), Method: "reverse", Params: rawJSON(t, map[string]any{})})
	select {
	case <-client2.Done():
	case <-time.After(time.Second):
		t.Fatal("duplicate inbound id did not contaminate generation")
	}
}

func TestClientReverseLimitIsBounded(t *testing.T) {
	entered := make(chan struct{}, MaxReverse)
	release := make(chan struct{})
	client, p := testClient(t, func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		entered <- struct{}{}
		<-release
		return method, nil
	})
	initializeTestClient(t, client, p, map[string]any{})
	for i := range MaxReverse + 1 {
		sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprintf("%d", i+1)), Method: "reverse", Params: rawJSON(t, map[string]any{})})
	}
	m := nextPeerMessage(t, p)
	if m.Error == nil || m.Error.Code != -32000 {
		t.Fatal("reverse limit response not observed")
	}
	close(release)
}

func TestClientCapabilitiesAreImmutableAndSessionsRequireInitialization(t *testing.T) {
	client, p := testClient(t, nil)
	if _, err := client.NewSession(context.Background(), map[string]any{}); err == nil {
		t.Fatal("session succeeded before initialize")
	}
	initializeTestClient(t, client, p, map[string]any{
		"loadSession":         true,
		"sessionCapabilities": map[string]any{"resume": true},
	})
	snapshot := client.Capabilities()
	snapshot.Capabilities["loadSession"][0] = 'f'
	if !client.capabilityEnabled("loadSession") {
		t.Fatal("capability snapshot was mutable")
	}
}

func TestClientNewSessionAndSingleActiveTurn(t *testing.T) {
	client, p := testClient(t, nil)
	initializeTestClient(t, client, p, map[string]any{})
	newDone := make(chan struct {
		id  string
		err error
	}, 1)
	runTestAsync(t, func() {
		id, err := client.NewSession(context.Background(), map[string]any{"cwd": "/tmp", "mcpServers": []any{}})
		newDone <- struct {
			id  string
			err error
		}{id: id, err: err}
	})
	newReq := nextPeerMessage(t, p)
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: newReq.ID, Result: rawJSON(t, map[string]any{"sessionId": "s1"})})
	created := <-newDone
	if created.err != nil || created.id != "s1" {
		t.Fatalf("new session = %#v", created)
	}
	promptDone := make(chan error, 1)
	runTestAsync(t, func() {
		_, err := client.Prompt(context.Background(), "s1", []any{map[string]any{"type": "text"}})
		promptDone <- err
	})
	promptReq := nextPeerMessage(t, p)
	if promptReq.Method != "session/prompt" {
		t.Fatalf("prompt request = %+v", promptReq)
	}
	if _, err := client.Prompt(context.Background(), "s1", []any{}); err == nil {
		t.Fatal("concurrent prompt accepted")
	}
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: promptReq.ID, Result: rawJSON(t, map[string]any{"stopReason": "end_turn"})})
	if err := <-promptDone; err != nil {
		t.Fatal(err)
	}
	snapshot, ok := client.Session("s1")
	if !ok || snapshot.ActiveTurn || snapshot.LastTerminal != "end_turn" {
		t.Fatalf("session snapshot = %+v, ok=%v", snapshot, ok)
	}
}

func TestClientLoadReplayArrivesBeforeResponseAndResumeDoesNotSynthesizeReplay(t *testing.T) {
	client, p := testClient(t, nil)
	initializeTestClient(t, client, p, map[string]any{
		"loadSession":         true,
		"sessionCapabilities": map[string]any{"resume": true},
	})
	loadDone := make(chan error, 1)
	runTestAsync(t, func() {
		_, err := client.LoadSession(context.Background(), map[string]any{"sessionId": "loaded", "cwd": "/tmp", "mcpServers": []any{}})
		loadDone <- err
	})
	loadReq := nextPeerMessage(t, p)
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", Method: "session/update", Params: rawJSON(t, map[string]any{"sessionId": "loaded", "update": map[string]any{"order": 1}})})
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: loadReq.ID, Result: rawJSON(t, map[string]any{})})
	if err := <-loadDone; err != nil {
		t.Fatal(err)
	}
	update := <-client.Updates()
	if update.SessionID != "loaded" || !bytes.Contains(update.Params, []byte(`"order":1`)) {
		t.Fatalf("load replay update = %+v", update)
	}
	resumeDone := make(chan error, 1)
	runTestAsync(t, func() {
		_, err := client.ResumeSession(context.Background(), map[string]any{"sessionId": "resumed", "cwd": "/tmp", "mcpServers": []any{}})
		resumeDone <- err
	})
	resumeReq := nextPeerMessage(t, p)
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: resumeReq.ID, Result: rawJSON(t, map[string]any{})})
	if err := <-resumeDone; err != nil {
		t.Fatal(err)
	}
	select {
	case update := <-client.Updates():
		t.Fatalf("resume synthesized update = %+v", update)
	case <-time.After(5 * time.Millisecond):
	}
}

func TestClientCancelUsesSingleCancelledTerminal(t *testing.T) {
	client, p := testClient(t, nil)
	initializeTestClient(t, client, p, map[string]any{})
	newDone := make(chan string, 1)
	runTestAsync(t, func() {
		id, err := client.NewSession(context.Background(), map[string]any{"cwd": "/tmp", "mcpServers": []any{}})
		if err != nil {
			newDone <- "error"
			return
		}
		newDone <- id
	})
	newReq := nextPeerMessage(t, p)
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: newReq.ID, Result: rawJSON(t, map[string]any{"sessionId": "cancel"})})
	if <-newDone != "cancel" {
		t.Fatal("session setup failed")
	}
	promptDone := make(chan error, 1)
	runTestAsync(t, func() {
		_, err := client.Prompt(context.Background(), "cancel", []any{})
		promptDone <- err
	})
	promptReq := nextPeerMessage(t, p)
	cancelDone := make(chan error, 1)
	runTestAsync(t, func() { cancelDone <- client.Cancel(context.Background(), "cancel") })
	cancelReq := nextPeerMessage(t, p)
	if cancelReq.Method != "session/cancel" {
		t.Fatalf("cancel request = %+v", cancelReq)
	}
	if err := <-cancelDone; err != nil {
		t.Fatal(err)
	}
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", Method: "session/update", Params: rawJSON(t, map[string]any{"sessionId": "cancel", "update": map[string]any{"tail": true}})})
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: promptReq.ID, Result: rawJSON(t, map[string]any{"stopReason": "end_turn"})})
	if err := <-promptDone; err != nil {
		t.Fatal(err)
	}
	snapshot, ok := client.Session("cancel")
	if !ok || snapshot.ActiveTurn || snapshot.LastTerminal != "cancelled" {
		t.Fatalf("cancel terminal = %+v, ok=%v", snapshot, ok)
	}
}

func TestClientUpdatesCloseAndOverflowFailClosed(t *testing.T) {
	client, p := testClient(t, nil)
	initializeTestClient(t, client, p, map[string]any{})
	testOverflowSession(t, client, p)
	sendOverflowUpdates(t, p)
	assertUpdateOverflowClosed(t, client)
}

// testOverflowSession 创建用于边界验证的会话。
func testOverflowSession(t *testing.T, client *Client, p *fakeProcess) {
	newDone := make(chan string, 1)
	runTestAsync(t, func() {
		id, err := client.NewSession(context.Background(), map[string]any{"cwd": "/tmp", "mcpServers": []any{}})
		if err != nil {
			newDone <- ""
			return
		}
		newDone <- id
	})
	newReq := nextPeerMessage(t, p)
	sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: newReq.ID, Result: rawJSON(t, map[string]any{"sessionId": "overflow"})})
	if <-newDone != "overflow" {
		t.Fatal("session setup failed")
	}
}

// sendOverflowUpdates 发送超过上限的通知序列。
func sendOverflowUpdates(t *testing.T, p *fakeProcess) {
	t.Helper()
	for i := 0; i <= MaxUpdates; i++ {
		sendPeerMessage(t, p, Message{JSONRPC: "2.0", Method: "session/update", Params: rawJSON(t, map[string]any{"sessionId": "overflow", "update": map[string]any{"i": i}})})
	}
}

// assertUpdateOverflowClosed 验证更新溢出会关闭客户端和通知通道。
func assertUpdateOverflowClosed(t *testing.T, client *Client) {
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("update overflow did not terminate client")
	}
	if !errors.Is(client.Err(), ErrUpdateOverflow) {
		t.Fatalf("overflow error = %v", client.Err())
	}
	select {
	case _, ok := <-client.Updates():
		if ok {
			for range client.Updates() {
			}
		}
	case <-time.After(time.Second):
		t.Fatal("Updates channel did not close")
	}
}

func TestClientCloseOwnsWaitAndSignalsInOrder(t *testing.T) {
	client, p := testClient(t, nil)
	initializeTestClient(t, client, p, map[string]any{})
	startPendingClose(t, client, p)
	assertShutdownSignals(t, p)
}

// startPendingClose 验证关闭会先解析 pending 请求再返回。
func startPendingClose(t *testing.T, client *Client, p *fakeProcess) {
	requestDone := make(chan error, 1)
	runTestAsync(t, func() {
		_, err := client.request(context.Background(), "pending", map[string]any{})
		requestDone <- err
	})
	_ = nextPeerMessage(t, p)
	closeDone := make(chan error, 1)
	runTestAsync(t, func() { closeDone <- client.Close() })
	select {
	case err := <-requestDone:
		if !errors.Is(err, ErrClientClosed) {
			t.Fatalf("pending close error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pending request did not resolve on close")
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// assertShutdownSignals 验证关闭只等待一次并按固定顺序发出信号。
func assertShutdownSignals(t *testing.T, p *fakeProcess) {
	p.mu.Lock()
	waitCount, interrupts, kills, order := p.waitCount, p.interrupts, p.kills, append([]string(nil), p.order...)
	p.mu.Unlock()
	if waitCount != 1 || interrupts != 1 || kills != 1 {
		t.Fatalf("wait/signals = %d/%d/%d", waitCount, interrupts, kills)
	}
	stdinIndex, interruptIndex, killIndex := indexOf(order, "stdin-close"), indexOf(order, "interrupt"), indexOf(order, "kill")
	if stdinIndex < 0 || interruptIndex < 0 || killIndex < 0 || !(stdinIndex < interruptIndex && interruptIndex < killIndex) {
		t.Fatalf("shutdown order = %v", order)
	}
}

func TestClientCloseReportsShutdownTimeoutUntilWaitConfirms(t *testing.T) {
	client, p := testClient(t, nil)
	p.releaseOnKill = false
	client.cfg.ShutdownTimeout = time.Millisecond
	err := client.Close()
	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("Close() error = %v", err)
	}
	p.release()
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("wait result did not eventually close Done")
	}
	p.mu.Lock()
	waitCount := p.waitCount
	p.mu.Unlock()
	if waitCount != 1 {
		t.Fatalf("wait count = %d", waitCount)
	}
}

func TestClientJoinTrackedOwnersRequiresClock(t *testing.T) {
	client, _ := testClient(t, nil)
	client.now = nil
	defer func() {
		recovered := recover()
		client.now = time.Now
		if recovered == nil {
			t.Fatal("joinTrackedOwners() did not reject a nil clock")
		}
	}()
	_ = client.joinTrackedOwners(time.Second)
}

func TestClientStartupTimeoutBoundsInitializeHandshake(t *testing.T) {
	client, p := testClient(t, nil)
	client.cfg.StartupTimeout = time.Millisecond
	done := make(chan error, 1)
	runTestAsync(t, func() { done <- client.Initialize(context.Background(), map[string]any{}) })
	_ = nextPeerMessage(t, p)
	select {
	case err := <-done:
		if !errors.Is(err, ErrRequestTimeout) {
			t.Fatalf("startup timeout error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("startup timeout did not fire")
	}
	client.mu.Lock()
	pending := len(client.pending)
	client.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending after startup timeout = %d", pending)
	}
}

func TestClientDistinguishesNormalExitFromWireContamination(t *testing.T) {
	client, p := testClient(t, nil)
	p.release()
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("normal exit did not close Done")
	}
	if client.Err() != nil || client.WaitErr() != nil {
		t.Fatalf("normal exit errors = terminal:%v wait:%v", client.Err(), client.WaitErr())
	}
	if client.Generation() != 2 {
		t.Fatalf("normal exit generation = %d", client.Generation())
	}
}

func TestClientEnforcesPendingSessionAndStderrBounds(t *testing.T) {
	client, p := testClient(t, nil)
	initializeTestClient(t, client, p, map[string]any{})
	testPendingBound(t, client, p)
	testSessionBound(t, client, p)
	testStderrBound(t)
}

// testPendingBound 验证 pending 请求达到上限后拒绝新增请求。
func testPendingBound(t *testing.T, client *Client, p *fakeProcess) {
	type pendingEntry struct {
		cancel context.CancelFunc
		done   chan error
	}
	entries := make([]pendingEntry, 0, MaxPending)
	for i := range MaxPending {
		index := i
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		runTestAsync(t, func() {
			_, err := client.request(ctx, "bounded", map[string]any{"i": index})
			done <- err
		})
		_ = nextPeerMessage(t, p)
		entries = append(entries, pendingEntry{cancel: cancel, done: done})
	}
	if _, err := client.request(context.Background(), "overflow", map[string]any{}); !errors.Is(err, ErrPendingLimit) {
		t.Fatalf("pending overflow error = %v", err)
	}
	for _, entry := range entries {
		entry.cancel()
		if err := <-entry.done; !errors.Is(err, context.Canceled) {
			t.Fatalf("pending cancellation error = %v", err)
		}
	}
}

// testSessionBound 验证会话数量达到上限后拒绝新增会话。
func testSessionBound(t *testing.T, client *Client, p *fakeProcess) {
	for i := range MaxSessions {
		result := make(chan error, 1)
		index := i
		runTestAsync(t, func() {
			_, err := client.NewSession(context.Background(), map[string]any{"cwd": "/tmp", "mcpServers": []any{}, "index": index})
			result <- err
		})
		request := nextPeerMessage(t, p)
		sendPeerMessage(t, p, Message{JSONRPC: "2.0", ID: request.ID, Result: rawJSON(t, map[string]any{"sessionId": fmt.Sprintf("s-%d", i)})})
		if err := <-result; err != nil {
			t.Fatalf("session %d error = %v", i, err)
		}
	}
	if _, err := client.NewSession(context.Background(), map[string]any{"cwd": "/tmp", "mcpServers": []any{}}); !errors.Is(err, ErrSessionLimit) {
		t.Fatalf("session overflow error = %v", err)
	}
}

// testStderrBound 验证 stderr 超限会终止客户端并保留错误。
func testStderrBound(t *testing.T) {
	stderrProcess := newFakeProcess()
	stderrProcess.stderr = io.NopCloser(strings.NewReader(strings.Repeat("x", DefaultMaxStderr+1)))
	stderrClient, err := NewClient(testLaunchConfigForClient(t), fakeFactory{process: stderrProcess}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stderrProcess.release()
		if closeErr := stderrClient.Close(); closeErr != nil && !errors.Is(closeErr, ErrShutdownTimeout) {
			t.Errorf("stderr cleanup Close() error = %v", closeErr)
		}
	})
	select {
	case <-stderrClient.Done():
	case <-time.After(time.Second):
		t.Fatal("stderr overflow did not terminate client")
	}
	if !strings.Contains(stderrClient.Err().Error(), "stderr exceeds") {
		t.Fatalf("stderr overflow error = %v", stderrClient.Err())
	}
}

func indexOf(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}
