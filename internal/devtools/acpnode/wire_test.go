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

func TestWireRejectsIllegalMessages(t *testing.T) {
	for _, input := range []string{"[{}]\n", "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{},\"error\":{}}\n", "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}} trailing\n", "\ufeff{\"jsonrpc\":\"2.0\",\"id\":1}\n"} {
		if _, err := readMessage(strings.NewReader(input), 4096); err == nil {
			t.Fatalf("input accepted: %q", input)
		}
	}
}

func TestWireRoundTrip(t *testing.T) {
	var out bytes.Buffer
	m := Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize"}
	if err := writeMessage(&out, m); err != nil {
		t.Fatal(err)
	}
	got, err := readMessage(&out, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != m.Method || string(got.ID) != "1" {
		t.Fatalf("got %+v", got)
	}
	if _, err := readMessage(strings.NewReader(""), 4096); err != io.EOF {
		t.Fatalf("empty error = %v", err)
	}
}

func TestWireResponsePreservesOriginalIDBytes(t *testing.T) {
	var out bytes.Buffer
	if err := writeMessage(&out, Message{JSONRPC: "2.0", ID: json.RawMessage(`"\u0061"`), Result: json.RawMessage(`true`)}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"\u0061"`)) {
		t.Fatalf("response rewrote original id bytes: %s", out.String())
	}
}

func TestRedactorRecursesAndDoesNotExposePlaintext(t *testing.T) {
	r, err := NewRedactor()
	if err != nil {
		t.Fatal(err)
	}
	value := r.LogValue(map[string]any{"secret": []any{"token", map[string]any{"nested": "password"}}})
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, plain := range []string{"secret", "token", "nested", "password"} {
		if strings.Contains(text, plain) {
			t.Fatalf("plaintext leaked: %s", text)
		}
	}
}

func TestWireRejectsStrictContaminationFixtures(t *testing.T) {
	invalidUTF8 := string([]byte{'{', '"', 'j', 's', 'o', 'n', 'r', 'p', 'c', '"', ':', '"', '2', '.', '0', '"', ',', '"', 'm', 'e', 't', 'h', 'o', 'd', '"', ':', '"', 0xff, '"', '}', '\n'})
	depth := `{"jsonrpc":"2.0","method":"x","params":` + strings.Repeat(`[`, MaxJSONDepth+1) + `null` + strings.Repeat(`]`, MaxJSONDepth+1) + "}\n"
	members := `{"jsonrpc":"2.0","method":"x","params":{"items":[` + strings.TrimSuffix(strings.Repeat("0,", MaxMembers), ",") + "]}}\n"
	fixtures := []string{
		"[{}]\n",
		"\ufeff{\"jsonrpc\":\"2.0\",\"method\":\"x\"}\n",
		"{\"jsonrpc\":\"2.0\",\"method\":\"x\",\"id\":1,\"id\":2}\n",
		"{\"jsonrpc\":\"2.0\",\"id\":1}\n",
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{},\"error\":{\"code\":-1,\"message\":\"x\"}}\n",
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"x\",\"result\":{}}\n",
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{},\"params\":{}}\n",
		"{\"jsonrpc\":\"2.0\",\"id\":1,\"error\":{\"code\":-1}}\n",
		"{\"jsonrpc\":\"2.0\",\"method\":\"x\"} trailing\n",
		"{\"jsonrpc\":\"2.0\",\"method\":\"x\",\"params\":{\"nul\":\u0000}}\n",
		invalidUTF8,
		depth,
		members,
	}
	for _, fixture := range fixtures {
		if _, err := readMessage(strings.NewReader(fixture), 1<<20); err == nil {
			t.Errorf("fixture accepted: %q", fixture)
		}
	}
}

func TestWireRejectsOversizedAndPreservesReaderBoundary(t *testing.T) {
	if _, err := readMessage(strings.NewReader("{}\n"), 1); err == nil {
		t.Fatal("oversized message accepted")
	}
	var stream bytes.Buffer
	stream.WriteString("{\"jsonrpc\":\"2.0\",\"method\":\"one\"}\n")
	stream.WriteString("{\"jsonrpc\":\"2.0\",\"method\":\"two\"}\n")
	first, err := readMessage(&stream, 4096)
	if err != nil || first.Method != "one" {
		t.Fatalf("first message = %+v, err = %v", first, err)
	}
	second, err := readMessage(&stream, 4096)
	if err != nil || second.Method != "two" {
		t.Fatalf("second message = %+v, err = %v", second, err)
	}
}

func TestWireResponseMarshalFailureIsErrorOnly(t *testing.T) {
	m := response(json.RawMessage(`1`), func() {}, nil)
	if m.Result != nil || m.Error == nil {
		t.Fatalf("marshal failure response = %+v", m)
	}
	var out bytes.Buffer
	if err := writeMessage(&out, m); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"error"`) || strings.Contains(out.String(), `"result"`) {
		t.Fatalf("not error-only: %s", out.String())
	}
	if _, err := mustJSON(func() {}); err == nil {
		t.Fatal("mustJSON swallowed marshal failure")
	}
	if _, err := methodParamsObject(json.RawMessage(`null`), "x"); err == nil {
		t.Fatal("null params accepted")
	}
}

func TestWireBoundsHaveDeterministicErrors(t *testing.T) {
	input := fmt.Sprintf("{\"jsonrpc\":\"2.0\",\"method\":\"%s\"}\n", strings.Repeat("x", 100))
	if _, err := readMessage(strings.NewReader(input), 16); err == nil {
		t.Fatal("bounded reader accepted oversized line")
	}
}

func TestBoundedOutboundPreflightRejectsBeforeWrite(t *testing.T) {
	var out bytes.Buffer
	err := writeMessageBounded(&out, Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "oversized",
		Params:  json.RawMessage(`{"payload":"` + strings.Repeat("x", 512) + `"}`),
	}, 64)
	if !errors.Is(err, ErrOutboundMessageTooLarge) {
		t.Fatalf("bounded write error = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("bounded preflight wrote %d bytes", out.Len())
	}
}

type countingJSONMarshaler struct {
	called *bool
}

func (m countingJSONMarshaler) MarshalJSON() ([]byte, error) {
	*m.called = true
	return []byte(`"` + strings.Repeat("x", 1<<20) + `"`), nil
}

func TestBoundedJSONRejectsCustomMarshalerBeforeMarshal(t *testing.T) {
	called := false
	_, err := mustJSONBounded(map[string]any{
		"payload": countingJSONMarshaler{called: &called},
	}, 64)
	if !errors.Is(err, ErrOutboundMessageTooLarge) {
		t.Fatalf("custom marshaler error = %v", err)
	}
	if called {
		t.Fatal("custom marshaler was called before bounded preflight")
	}
}

func TestBoundedJSONRejectsOversizedRawMessageBeforeEncoder(t *testing.T) {
	_, err := mustJSONBounded(json.RawMessage(`"`+strings.Repeat("x", 1<<20)+`"`), 64)
	if !errors.Is(err, ErrOutboundMessageTooLarge) {
		t.Fatalf("raw message error = %v", err)
	}
}

func TestParamsMustBeObjectOrArrayOnInboundAndOutbound(t *testing.T) {
	for _, raw := range []string{"null", "true", "1", `"scalar"`} {
		input := fmt.Sprintf(`{"jsonrpc":"2.0","method":"request","params":%s}`+"\n", raw)
		if _, err := readMessage(strings.NewReader(input), 4096); err == nil {
			t.Errorf("inbound scalar params accepted: %s", raw)
		}

		var out bytes.Buffer
		err := writeMessageBounded(&out, Message{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`1`),
			Method:  "request",
			Params:  json.RawMessage(raw),
		}, 4096)
		if err == nil {
			t.Errorf("outbound scalar params accepted: %s", raw)
		}
		if out.Len() != 0 {
			t.Errorf("outbound scalar params wrote %d bytes for %s", out.Len(), raw)
		}
	}
	for _, raw := range []string{`{}`, `[]`, `[1, {"ok":true}]`} {
		input := fmt.Sprintf(`{"jsonrpc":"2.0","method":"request","params":%s}`+"\n", raw)
		if _, err := readMessage(strings.NewReader(input), 4096); err != nil {
			t.Errorf("valid params rejected: %s: %v", raw, err)
		}
	}
}

type ownerLifecycle interface {
	error
	Done() <-chan struct{}
	Err() error
	Join() error
}

func TestBlockedWriteOwnerExposesTypedCompletionErrorAndJoin(t *testing.T) {
	w := &nonCooperativeWire{writeRelease: make(chan struct{})}
	closeRelease := make(chan struct{})
	err := writeBytesBoundedContext(context.Background(), w, []byte("payload"), time.Millisecond, func() error {
		<-closeRelease
		return nil
	})
	var owner ownerLifecycle
	if !errors.As(err, &owner) {
		t.Fatalf("blocked write owner is not typed/joinable: %v", err)
	}
	var pending *writeOwnerPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("write owner pending error missing: %v", err)
	}
	close(closeRelease)
	close(w.writeRelease)
	if joinErr := pending.Join(); !errors.Is(joinErr, io.ErrClosedPipe) {
		t.Fatalf("joined write error = %v", joinErr)
	}
	if pending.Err() == nil {
		t.Fatal("completed write owner did not retain its error")
	}
}

type recordingWriteCloser struct {
	mu     sync.Mutex
	data   bytes.Buffer
	closed bool
}

func (w *recordingWriteCloser) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	return w.data.Write(data)
}

func (w *recordingWriteCloser) Close() error {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	return nil
}

func (w *recordingWriteCloser) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.Len()
}

type recordingStdinProcess struct {
	*fakeProcess
	stdin *recordingWriteCloser
}

func (p *recordingStdinProcess) Stdin() io.WriteCloser { return p.stdin }

func TestRequestRejectsScalarParamsBeforePendingReservationAndWrite(t *testing.T) {
	base := newFakeProcess()
	stdin := &recordingWriteCloser{}
	process := &recordingStdinProcess{fakeProcess: base, stdin: stdin}
	client, err := NewClient(testLaunchConfigForClient(t), fakeFactory{process: process}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		base.release()
		if closeErr := client.Close(); closeErr != nil && !errors.Is(closeErr, ErrShutdownTimeout) {
			t.Errorf("cleanup Close() error = %v", closeErr)
		}
	})
	for _, raw := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`1`), json.RawMessage(`"scalar"`)} {
		if _, err := client.requestRaw(context.Background(), "invalid-params", raw, time.Millisecond); err == nil {
			t.Fatalf("request accepted scalar params %s", raw)
		}
		client.mu.Lock()
		pending := len(client.pending)
		client.mu.Unlock()
		if pending != 0 {
			t.Fatalf("pending reservation survived scalar params %s: %d", raw, pending)
		}
		if got := stdin.Len(); got != 0 {
			t.Fatalf("scalar params wrote %d bytes", got)
		}
	}
}

type nonCooperativeWire struct {
	writeRelease chan struct{}
}

func (w *nonCooperativeWire) Write([]byte) (int, error) {
	<-w.writeRelease
	return 0, io.ErrClosedPipe
}

func (w *nonCooperativeWire) Close() error { return nil }

func TestBlockedWriteRetainsOwnerUntilNonCooperativeWriteReturns(t *testing.T) {
	w := &nonCooperativeWire{writeRelease: make(chan struct{})}
	closeRelease := make(chan struct{})
	err := writeBytesBoundedContext(context.Background(), w, []byte("payload"), time.Millisecond, func() error {
		<-closeRelease
		return nil
	})
	if !errors.Is(err, ErrWriteTimeout) {
		t.Fatalf("blocked write error = %v", err)
	}
	var pending *writeOwnerPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("blocked write owner missing: %v", err)
	}
	close(closeRelease)
	close(w.writeRelease)
	select {
	case <-pending.Done():
	case <-time.After(time.Second):
		t.Fatal("write owner did not finish after release")
	}
}
