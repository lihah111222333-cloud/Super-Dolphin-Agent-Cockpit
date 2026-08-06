package common

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const expectedMaxStdioInFlightToolCalls = 64

type cancellationTestProvider struct {
	started  chan string
	canceled chan string

	mu    sync.Mutex
	calls map[string]int
}

type delayedSuccessProvider struct{}

func (delayedSuccessProvider) ListTools(context.Context) ([]MCPTool, error) {
	return nil, nil
}

func (delayedSuccessProvider) CallTool(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return map[string]any{"ok": true}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func newCancellationTestProvider() *cancellationTestProvider {
	return &cancellationTestProvider{
		started:  make(chan string, expectedMaxStdioInFlightToolCalls+8),
		canceled: make(chan string, expectedMaxStdioInFlightToolCalls+8),
		calls:    make(map[string]int),
	}
}

func (p *cancellationTestProvider) ListTools(context.Context) ([]MCPTool, error) {
	return nil, nil
}

func (p *cancellationTestProvider) CallTool(ctx context.Context, name string, _ json.RawMessage) (any, error) {
	p.mu.Lock()
	p.calls[name]++
	p.mu.Unlock()
	if name == "fast" {
		return map[string]any{"ok": true}, nil
	}
	p.started <- name
	<-ctx.Done()
	p.canceled <- name
	return nil, ctx.Err()
}

func (p *cancellationTestProvider) callCount(name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[name]
}

type cancellationResponseWriter struct {
	responses chan jsonRPCResponse
}

func newCancellationResponseWriter() *cancellationResponseWriter {
	return &cancellationResponseWriter{
		responses: make(chan jsonRPCResponse, expectedMaxStdioInFlightToolCalls+16),
	}
}

func (w *cancellationResponseWriter) Write(p []byte) (int, error) {
	var resp jsonRPCResponse
	if err := json.Unmarshal(bytes.TrimSpace(p), &resp); err != nil {
		return 0, err
	}
	w.responses <- resp
	return len(p), nil
}

type cancellationServerHarness struct {
	input   *io.PipeWriter
	output  *cancellationResponseWriter
	cancel  context.CancelFunc
	done    chan struct{}
	runWG   sync.WaitGroup
	runErr  error
	runErrM sync.Mutex
}

func newCancellationServerHarness(
	t *testing.T,
	input io.Reader,
	inputWriter *io.PipeWriter,
	provider ToolProvider,
) *cancellationServerHarness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	harness := &cancellationServerHarness{
		input:  inputWriter,
		output: newCancellationResponseWriter(),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	server := NewServer("test", "dev", NewStdioTransport(input, harness.output), provider)
	harness.runWG.Go(func() {
		err := server.Run(ctx)
		harness.runErrM.Lock()
		harness.runErr = err
		harness.runErrM.Unlock()
		close(harness.done)
	})
	t.Cleanup(func() {
		harness.cancel()
		if harness.input != nil {
			_ = harness.input.Close()
		}
		select {
		case <-harness.done:
			harness.runWG.Wait()
			harness.runErrM.Lock()
			err := harness.runErr
			harness.runErrM.Unlock()
			if err != nil {
				t.Errorf("Run() error = %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Errorf("Run() did not stop after cancellation")
		}
	})
	return harness
}

func newPipedCancellationServerHarness(t *testing.T, provider ToolProvider) *cancellationServerHarness {
	t.Helper()
	inputReader, inputWriter := io.Pipe()
	return newCancellationServerHarness(t, inputReader, inputWriter, provider)
}

func (h *cancellationServerHarness) send(t *testing.T, payload string) {
	t.Helper()
	if h.input == nil {
		t.Fatal("send called without a pipe input")
	}
	if _, err := io.WriteString(h.input, payload+"\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}
}

func (h *cancellationServerHarness) response(t *testing.T) jsonRPCResponse {
	t.Helper()
	select {
	case resp := <-h.output.responses:
		return resp
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for JSON-RPC response")
		return jsonRPCResponse{}
	}
}

func (h *cancellationServerHarness) expectNoResponse(t *testing.T) {
	t.Helper()
	select {
	case resp := <-h.output.responses:
		t.Fatalf("unexpected JSON-RPC response: %#v", resp)
	case <-time.After(100 * time.Millisecond):
	}
}

func waitProviderEvent(t *testing.T, events <-chan string, want string) {
	t.Helper()
	select {
	case got := <-events:
		if got != want {
			t.Fatalf("provider event = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for provider event %q", want)
	}
}

func TestServerCancellationCancelsActiveToolCallWithoutBlockingNextRequest(t *testing.T) {
	provider := newCancellationTestProvider()
	harness := newPipedCancellationServerHarness(t, provider)

	harness.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"block","arguments":{}}}`)
	waitProviderEvent(t, provider.started, "block")
	harness.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`)
	waitProviderEvent(t, provider.canceled, "block")
	harness.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fast","arguments":{}}}`)

	responseIDs := map[string]bool{}
	for range 2 {
		responseIDs[string(harness.response(t).ID)] = true
	}
	if !responseIDs["1"] || !responseIDs["2"] {
		t.Fatalf("response IDs = %#v, want tool responses for 1 and 2", responseIDs)
	}
	harness.expectNoResponse(t)
}

func TestServerCancellationAcceptsOptionalReasonWithoutReturningIt(t *testing.T) {
	const reason = "caller canceled: token=secret"
	provider := newCancellationTestProvider()
	harness := newPipedCancellationServerHarness(t, provider)

	harness.send(t, `{"jsonrpc":"2.0","id":51,"method":"tools/call","params":{"name":"reason","arguments":{}}}`)
	waitProviderEvent(t, provider.started, "reason")
	harness.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":51,"reason":"`+reason+`"}}`)
	waitProviderEvent(t, provider.canceled, "reason")
	response, err := json.Marshal(harness.response(t))
	if err != nil {
		t.Fatalf("marshal tools/call response: %v", err)
	}
	if strings.Contains(string(response), reason) {
		t.Fatalf("tools/call response exposed cancellation reason: %s", response)
	}
	harness.expectNoResponse(t)
}

func TestServerCancellationKeepsNumericAndStringIDsDistinct(t *testing.T) {
	provider := newCancellationTestProvider()
	harness := newPipedCancellationServerHarness(t, provider)

	harness.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"number","arguments":{}}}`)
	waitProviderEvent(t, provider.started, "number")
	harness.send(t, `{"jsonrpc":"2.0","id":"1","method":"tools/call","params":{"name":"string","arguments":{}}}`)
	waitProviderEvent(t, provider.started, "string")

	harness.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`)
	waitProviderEvent(t, provider.canceled, "number")
	select {
	case got := <-provider.canceled:
		t.Fatalf("numeric cancellation also canceled %q", got)
	case <-time.After(100 * time.Millisecond):
	}

	harness.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"1"}}`)
	waitProviderEvent(t, provider.canceled, "string")
	for range 2 {
		_ = harness.response(t)
	}
	harness.expectNoResponse(t)
}

func TestServerCancellationIgnoresUnknownIDAndKeepsServing(t *testing.T) {
	provider := newCancellationTestProvider()
	harness := newPipedCancellationServerHarness(t, provider)

	harness.send(t, `{"jsonrpc":"2.0","id":"known","method":"tools/call","params":{"name":"known","arguments":{}}}`)
	waitProviderEvent(t, provider.started, "known")
	harness.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"missing"}}`)
	select {
	case got := <-provider.canceled:
		t.Fatalf("unknown cancellation canceled %q", got)
	case <-time.After(100 * time.Millisecond):
	}
	harness.expectNoResponse(t)

	harness.send(t, `{"jsonrpc":"2.0","id":"ping","method":"ping"}`)
	if got := string(harness.response(t).ID); got != `"ping"` {
		t.Fatalf("ping response ID = %s, want %q", got, "ping")
	}
	harness.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"known"}}`)
	waitProviderEvent(t, provider.canceled, "known")
	if got := string(harness.response(t).ID); got != `"known"` {
		t.Fatalf("tool response ID = %s, want %q", got, "known")
	}
}

func TestServerRejectsDuplicateActiveToolCallID(t *testing.T) {
	provider := newCancellationTestProvider()
	harness := newPipedCancellationServerHarness(t, provider)

	harness.send(t, `{"jsonrpc":"2.0","id":"dup","method":"tools/call","params":{"name":"block","arguments":{}}}`)
	waitProviderEvent(t, provider.started, "block")
	harness.send(t, `{"jsonrpc":"2.0","id":"dup","method":"tools/call","params":{"name":"second","arguments":{}}}`)

	resp := harness.response(t)
	if resp.Error == nil || resp.Error.Code != codeInvalidReq ||
		!strings.Contains(resp.Error.Message, "duplicate active request id") {
		t.Fatalf("duplicate response = %#v, want invalid request duplicate rejection", resp)
	}
	if got := provider.callCount("second"); got != 0 {
		t.Fatalf("duplicate provider calls = %d, want 0", got)
	}

	harness.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"dup"}}`)
	waitProviderEvent(t, provider.canceled, "block")
	_ = harness.response(t)
	harness.expectNoResponse(t)
}

func TestServerBoundsActiveToolCallsAndCancelsThemOnExit(t *testing.T) {
	provider := newCancellationTestProvider()
	input := boundedToolCallInput(expectedMaxStdioInFlightToolCalls + 1)
	harness := newCancellationServerHarness(t, strings.NewReader(input), nil, provider)

	waitForProviderEventCount(
		t,
		provider.started,
		expectedMaxStdioInFlightToolCalls,
		"bounded provider calls",
		time.Second,
	)
	assertInFlightOverflowResponse(t, harness.response(t))
	waitForProviderEventCount(
		t,
		provider.canceled,
		expectedMaxStdioInFlightToolCalls,
		"Run exit cancellation",
		stdioToolCallDrainTimeout+time.Second,
	)
	waitForCancellationServerExit(t, harness.done)
}

func TestServerDrainsAcceptedToolCallBeforeEOFExit(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delayed","arguments":{}}}` + "\n"
	harness := newCancellationServerHarness(t, strings.NewReader(input), nil, delayedSuccessProvider{})

	resp := harness.response(t)
	if resp.Error != nil {
		t.Fatalf("EOF-drained response error = %#v, want success", resp.Error)
	}
	waitForCancellationServerExit(t, harness.done)
}

func boundedToolCallInput(count int) string {
	var input strings.Builder
	for id := range count {
		rawID := strconv.Itoa(id)
		input.WriteString(`{"jsonrpc":"2.0","id":`)
		input.WriteString(rawID)
		input.WriteString(`,"method":"tools/call","params":{"name":"block-`)
		input.WriteString(rawID)
		input.WriteString(`","arguments":{}}}`)
		input.WriteByte('\n')
	}
	return input.String()
}

func waitForProviderEventCount(t *testing.T, events <-chan string, count int, description string, timeout time.Duration) {
	t.Helper()
	for range count {
		select {
		case <-events:
		case <-time.After(timeout):
			t.Fatalf("timed out waiting for %d %s", count, description)
		}
	}
}

func assertInFlightOverflowResponse(t *testing.T, resp jsonRPCResponse) {
	t.Helper()
	if resp.Error == nil || resp.Error.Code != codeInternal ||
		!strings.Contains(resp.Error.Message, "too many active tools/call requests") {
		t.Fatalf("overflow response = %#v, want bounded in-flight rejection", resp)
	}
}

func waitForCancellationServerExit(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run exit did not wait for canceled provider calls")
	}
}
