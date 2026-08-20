package heterocitransport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// TestNewRejectsMissingRequester 验证构造器不为缺失依赖提供默认实现。
func TestNewRejectsMissingRequester(t *testing.T) {
	if _, err := New(nil); !errors.Is(err, ErrInvalidDependencies) {
		t.Fatalf("New(nil) error = %v, want ErrInvalidDependencies", err)
	}
	var requester *testRequester
	if _, err := New(requester); !errors.Is(err, ErrInvalidDependencies) {
		t.Fatalf("New(typed nil) error = %v, want ErrInvalidDependencies", err)
	}
}

// TestProbePreservesOpaqueRequestContextAndBodyClose 验证请求、上下文和响应体边界。
func TestProbePreservesOpaqueRequestContextAndBodyClose(t *testing.T) {
	ctx := context.WithValue(context.Background(), testContextKey("probe"), "value")
	body := &testBody{payload: []byte("response")}
	requester, transport := newOpaqueTransport(t, body)
	original := []byte{0, 1, 2, 0xff}
	want := append([]byte(nil), original...)
	response, err := transport.Probe(ctx, "ssh://node.invalid/probe", original)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	original[0] = 99
	assertOpaqueRequesterCall(t, requester, ctx, "ssh://node.invalid/probe", want)
	assertOpaqueResponseBody(t, response, body)
}

// newOpaqueTransport 构造带可观察 requester 的不透明字节测试传输。
func newOpaqueTransport(t *testing.T, body *testBody) (*testRequester, *Transport) {
	t.Helper()
	requester := &testRequester{body: body}
	transport, err := New(requester)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return requester, transport
}

// assertOpaqueRequesterCall 验证 endpoint、context 和 request 字节均未被改写。
func assertOpaqueRequesterCall(t *testing.T, requester *testRequester, ctx context.Context, endpoint string, want []byte) {
	t.Helper()
	if requester.ctx != ctx || requester.endpoint != endpoint || !bytes.Equal(requester.request, want) {
		t.Fatalf("requester call = ctx=%p endpoint=%q request=%v", requester.ctx, requester.endpoint, requester.request)
	}
}

// assertOpaqueResponseBody 验证读取传递和底层 body 的恰好一次关闭。
func assertOpaqueResponseBody(t *testing.T, response heteroci.ProbeResponseBody, body *testBody) {
	t.Helper()
	readContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, err := io.ReadAll(&contextReader{ctx: readContext, body: response})
	if err != nil || string(got) != "response" {
		t.Fatalf("response read = %q, %v", got, err)
	}
	if err := response.Close(); err != nil {
		t.Fatalf("response Close() error = %v", err)
	}
	if err := response.Close(); err != nil {
		t.Fatalf("response repeated Close() error = %v", err)
	}
	if body.closeCount() != 1 {
		t.Fatalf("underlying Close calls = %d, want 1", body.closeCount())
	}
}

// TestProbeRejectsInvalidInput 验证上下文、端点和请求字节缺失时不调用 requester。
func TestProbeRejectsInvalidInput(t *testing.T) {
	requester := &testRequester{body: &testBody{payload: []byte("ok")}}
	transport, err := New(requester)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name     string
		ctx      context.Context
		endpoint string
		request  []byte
	}{
		{name: "nil context", endpoint: "node", request: []byte("x")},
		{name: "canceled context", ctx: canceled, endpoint: "node", request: []byte("x")},
		{name: "empty endpoint", ctx: context.Background(), request: []byte("x")},
		{name: "trimmed endpoint", ctx: context.Background(), endpoint: " node", request: []byte("x")},
		{name: "control endpoint", ctx: context.Background(), endpoint: "node\nprobe", request: []byte("x")},
		{name: "empty request", ctx: context.Background(), endpoint: "node"},
		{name: "oversized request", ctx: context.Background(), endpoint: "node", request: bytes.Repeat([]byte{'x'}, maxProbeRequestBytes+1)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			before := requester.calls
			_, err := transport.Probe(testCase.ctx, testCase.endpoint, testCase.request)
			if err == nil {
				t.Fatal("Probe() returned nil error")
			}
			if requester.calls != before {
				t.Fatalf("requester calls = %d, want %d", requester.calls, before)
			}
		})
	}
}

// TestProbeMapsOnlyExplicitUnavailable 验证只有单一 unavailable 错误才被映射。
func TestProbeMapsOnlyExplicitUnavailable(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantMapped bool
	}{
		{name: "direct", err: ErrTransportUnavailable, wantMapped: true},
		{name: "wrapped", err: wrappedUnavailableError{cause: ErrTransportUnavailable}, wantMapped: true},
		{name: "joined fatal", err: errors.Join(ErrTransportUnavailable, errors.New("fatal")), wantMapped: false},
		{name: "ordinary", err: errors.New("ordinary"), wantMapped: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			transport, err := New(&testRequester{err: testCase.err})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			_, err = transport.Probe(context.Background(), "node", []byte("request"))
			if err == nil {
				t.Fatal("Probe() returned nil error")
			}
			mapped := err == heteroci.ErrProbeTransportUnavailable
			if mapped != testCase.wantMapped {
				t.Fatalf("mapped = %v, error = %v, want %v", mapped, err, testCase.wantMapped)
			}
		})
	}
}

// TestProbeClosesDiscardedBodies 验证 requester 返回错误或取消时 body 不泄漏。
func TestProbeClosesDiscardedBodies(t *testing.T) {
	body := &testBody{payload: []byte("discarded")}
	transport, err := New(&testRequester{body: body, err: errors.New("request failed")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = transport.Probe(context.Background(), "node", []byte("request"))
	if err == nil || body.closeCount() != 1 {
		t.Fatalf("Probe() error=%v close_calls=%d, want error and one close", err, body.closeCount())
	}

	body = &testBody{payload: []byte("canceled")}
	requester := &testRequester{body: body}
	transport, err = New(requester)
	if err != nil {
		t.Fatalf("New() canceled error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = transport.Probe(ctx, "node", []byte("request"))
	if !errors.Is(err, context.Canceled) || body.closeCount() != 0 || requester.calls != 0 {
		t.Fatalf("canceled Probe() error=%v close_calls=%d calls=%d, want preflight cancellation", err, body.closeCount(), requester.calls)
	}
}

// TestResponseBodyReadChecksContext 验证响应体在读取前观察取消状态。
func TestResponseBodyReadChecksContext(t *testing.T) {
	body := &testBody{payload: []byte("response")}
	transport, err := New(&testRequester{body: body})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response, err := transport.Probe(context.Background(), "node", []byte("request"))
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	buffer := make([]byte, 1)
	_, err = response.Read(ctx, buffer)
	if !errors.Is(err, context.Canceled) || body.readCount() != 0 {
		t.Fatalf("Read() error=%v reads=%d, want canceled before delegate", err, body.readCount())
	}
	_ = response.Close()
}

type testContextKey string

type testRequester struct {
	ctx      context.Context
	endpoint string
	request  []byte
	body     ResponseBody
	err      error
	calls    int
}

// Do 记录注入调用并返回预设响应。
func (requester *testRequester) Do(ctx context.Context, endpoint string, request []byte) (ResponseBody, error) {
	requester.calls++
	requester.ctx = ctx
	requester.endpoint = endpoint
	requester.request = append([]byte(nil), request...)
	return requester.body, requester.err
}

type testBody struct {
	mu      sync.Mutex
	payload []byte
	reads   int
	closes  int
}

// Read 返回测试响应字节并记录读取次数。
func (body *testBody) Read(ctx context.Context, destination []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	body.mu.Lock()
	defer body.mu.Unlock()
	body.reads++
	if len(body.payload) == 0 {
		return 0, io.EOF
	}
	count := copy(destination, body.payload)
	body.payload = body.payload[count:]
	return count, nil
}

// Close 记录测试响应关闭次数。
func (body *testBody) Close() error {
	body.mu.Lock()
	defer body.mu.Unlock()
	body.closes++
	return nil
}

// readCount 返回测试响应读取次数。
func (body *testBody) readCount() int {
	body.mu.Lock()
	defer body.mu.Unlock()
	return body.reads
}

// closeCount 返回测试响应关闭次数。
func (body *testBody) closeCount() int {
	body.mu.Lock()
	defer body.mu.Unlock()
	return body.closes
}

type contextReader struct {
	ctx  context.Context
	body heteroci.ProbeResponseBody
}

// Read 将 io.Reader 读取转发到带 context 的探针 body。
func (reader *contextReader) Read(destination []byte) (int, error) {
	return reader.body.Read(reader.ctx, destination)
}

type wrappedUnavailableError struct {
	cause error
}

// Error 返回测试包装错误描述。
func (err wrappedUnavailableError) Error() string {
	return "wrapped unavailable"
}

// Unwrap 返回测试包装错误根因。
func (err wrappedUnavailableError) Unwrap() error {
	return err.cause
}

var _ Requester = (*testRequester)(nil)
var _ ResponseBody = (*testBody)(nil)
