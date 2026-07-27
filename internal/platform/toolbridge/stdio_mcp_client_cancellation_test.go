package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestStdioMCPClientRequestSkipsNotificationsUntilMatchingResponse(t *testing.T) {
	transport := newFakeStdioTransport(
		json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"p1"}}`),
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`),
	)
	client := newTestStdioMCPClient(t, transport)

	raw, err := client.request(context.Background(), "tools/list", map[string]any{})
	if err != nil {
		t.Fatalf("request() error = %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("request() result = %s, want {\"ok\":true}", raw)
	}
	if writes := transport.Writes(); len(writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(writes))
	}
}

func TestStdioMCPClientRequestCancellationKeepsTransportHealthy(t *testing.T) {
	transport := newFakeStdioTransport()
	client := newTestStdioMCPClient(t, transport)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	firstDone := make(chan stdioRequestOutcome, 1)
	wg.Go(func() {
		raw, err := client.request(ctx, "first", map[string]any{})
		firstDone <- stdioRequestOutcome{raw: raw, err: err}
	})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		wg.Wait()
	})

	firstWrites := waitForStdioWrites(t, transport, 1)
	firstID := stdioWriteID(t, firstWrites[0])
	cancel()
	first := waitForStdioOutcome(t, firstDone)
	if !errors.Is(first.err, context.Canceled) {
		t.Fatalf("first request error = %v, want context canceled", first.err)
	}

	cancelWrites := waitForStdioWrites(t, transport, 2)
	assertStdioCancellation(t, cancelWrites[1], firstID)
	if transport.Closed() {
		t.Fatal("request cancellation closed a healthy shared transport")
	}

	secondDone := make(chan stdioRequestOutcome, 1)
	wg.Go(func() {
		raw, err := client.request(context.Background(), "second", map[string]any{})
		secondDone <- stdioRequestOutcome{raw: raw, err: err}
	})
	secondWrites := waitForStdioWrites(t, transport, 3)
	secondID := stdioWriteID(t, secondWrites[2])
	transport.Enqueue(json.RawMessage(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"request":"second"}}`, secondID)))

	second := waitForStdioOutcome(t, secondDone)
	if second.err != nil {
		t.Fatalf("second request error = %v", second.err)
	}
	if string(second.raw) != `{"request":"second"}` {
		t.Fatalf("second request result = %s, want second response", second.raw)
	}
}

func TestStdioMCPClientConcurrentRequestsRouteOutOfOrderResponses(t *testing.T) {
	transport := newFakeStdioTransport()
	client := newTestStdioMCPClient(t, transport)
	var wg sync.WaitGroup
	firstDone := make(chan stdioRequestOutcome, 1)
	secondDone := make(chan stdioRequestOutcome, 1)
	wg.Go(func() {
		raw, err := client.request(context.Background(), "first", map[string]any{})
		firstDone <- stdioRequestOutcome{raw: raw, err: err}
	})
	wg.Go(func() {
		raw, err := client.request(context.Background(), "second", map[string]any{})
		secondDone <- stdioRequestOutcome{raw: raw, err: err}
	})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		wg.Wait()
	})

	writes := waitForStdioWrites(t, transport, 2)
	requestIDs := stdioRequestIDsByMethod(t, writes)
	transport.Enqueue(json.RawMessage(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":%d,"result":{"request":"second"}}`,
		requestIDs["second"],
	)))
	second := waitForStdioOutcome(t, secondDone)
	if second.err != nil || string(second.raw) != `{"request":"second"}` {
		t.Fatalf("second request outcome = (%s, %v), want second response", second.raw, second.err)
	}

	transport.Enqueue(json.RawMessage(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":%d,"result":{"request":"first"}}`,
		requestIDs["first"],
	)))
	first := waitForStdioOutcome(t, firstDone)
	if first.err != nil || string(first.raw) != `{"request":"first"}` {
		t.Fatalf("first request outcome = (%s, %v), want first response", first.raw, first.err)
	}
}

func TestStdioMCPClientDropsLateResponseAfterCancellation(t *testing.T) {
	transport := newFakeStdioTransport()
	client := newTestStdioMCPClient(t, transport)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	canceledDone := make(chan stdioRequestOutcome, 1)
	wg.Go(func() {
		raw, err := client.request(ctx, "canceled", map[string]any{})
		canceledDone <- stdioRequestOutcome{raw: raw, err: err}
	})
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		wg.Wait()
	})

	firstWrites := waitForStdioWrites(t, transport, 1)
	canceledID := stdioWriteID(t, firstWrites[0])
	cancel()
	canceled := waitForStdioOutcome(t, canceledDone)
	if !errors.Is(canceled.err, context.Canceled) {
		t.Fatalf("canceled request error = %v, want context canceled", canceled.err)
	}
	waitForStdioWrites(t, transport, 2)
	transport.Enqueue(json.RawMessage(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":%d,"result":{"request":"late"}}`,
		canceledID,
	)))

	nextDone := make(chan stdioRequestOutcome, 1)
	wg.Go(func() {
		raw, err := client.request(context.Background(), "next", map[string]any{})
		nextDone <- stdioRequestOutcome{raw: raw, err: err}
	})
	nextWrites := waitForStdioWrites(t, transport, 3)
	nextID := stdioWriteID(t, nextWrites[2])
	transport.Enqueue(json.RawMessage(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":%d,"result":{"request":"next"}}`,
		nextID,
	)))

	next := waitForStdioOutcome(t, nextDone)
	if next.err != nil || string(next.raw) != `{"request":"next"}` {
		t.Fatalf("next request outcome = (%s, %v), want next response", next.raw, next.err)
	}
}

func TestStdioMCPClientTerminalReadErrorFailsAllPending(t *testing.T) {
	transport := newFakeStdioTransport()
	client := newTestStdioMCPClient(t, transport)
	var wg sync.WaitGroup
	results := make(chan stdioRequestOutcome, 2)
	for _, method := range []string{"first", "second"} {
		wg.Go(func() {
			raw, err := client.request(context.Background(), method, map[string]any{})
			results <- stdioRequestOutcome{raw: raw, err: err}
		})
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		wg.Wait()
	})

	waitForStdioWrites(t, transport, 2)
	terminalErr := errors.New("terminal stdio read failure")
	transport.EnqueueError(terminalErr)
	for range 2 {
		outcome := waitForStdioOutcome(t, results)
		if !errors.Is(outcome.err, terminalErr) {
			t.Fatalf("pending request error = %v, want terminal read failure", outcome.err)
		}
	}
	waitForStdioTransportClosed(t, transport)

	_, err := client.request(context.Background(), "after-terminal", map[string]any{})
	if !errors.Is(err, terminalErr) {
		t.Fatalf("request after terminal error = %v, want terminal read failure", err)
	}
}

func TestStdioMCPClientEnforcesPendingRequestLimit(t *testing.T) {
	const pendingLimit = maxStdioPendingRequests

	transport := newFakeStdioTransport()
	client := newTestStdioMCPClient(t, transport)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	results := make(chan stdioRequestOutcome, pendingLimit+1)
	for index := range pendingLimit + 1 {
		method := fmt.Sprintf("request-%d", index)
		wg.Go(func() {
			raw, err := client.request(ctx, method, map[string]any{})
			results <- stdioRequestOutcome{raw: raw, err: err}
		})
	}
	defer func() {
		cancel()
		wg.Wait()
	}()

	limitOutcome := waitForStdioOutcome(t, results)
	if limitOutcome.err == nil || !strings.Contains(limitOutcome.err.Error(), "pending request limit") {
		t.Fatalf("limit request error = %v, want pending request limit", limitOutcome.err)
	}
	if writes := waitForStdioWrites(t, transport, pendingLimit); len(writes) != pendingLimit {
		t.Fatalf("request writes = %d, want %d", len(writes), pendingLimit)
	}
}
