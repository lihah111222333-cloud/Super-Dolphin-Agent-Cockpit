package bootstrap

import (
	"context"
	"strings"
	"testing"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
)

// TestPendingHooks_BootAgentIDDoesNotFallback 验证 PendingHooks 只信任 cfg.AgentID。
// cfg.AgentID 为空时必须在本地 fail-closed，不能回退 boot.AgentID 后向 peer 发起 RPC。
func TestPendingHooks_BootAgentIDDoesNotFallback(t *testing.T) {
	t.Parallel()

	// 即使连接可用，空 cfg.AgentID 也必须在发出 RPC 前失败，避免 peer 看到 boot.AgentID。
	var calls int
	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodHookPending: platformrpc.StrictHandler(func(context.Context, mcpdto.HookPendingRequest) (mcpdto.HookPendingResponse, error) {
			calls++
			return mcpdto.HookPendingResponse{}, nil
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	client := &Client{
		conn: local.Client,
		cfg:  Config{AgentID: ""}, // authoritative: empty
		boot: bootSnapshot{AgentID: "boot-ghost-agent"},
	}
	_, err := client.PendingHooks(context.Background())
	if err == nil {
		t.Fatalf("PendingHooks() returned nil err, want errHookPendingAgentIDRequired()")
	}
	if !strings.Contains(err.Error(), "agent_id") {
		t.Fatalf("PendingHooks() error = %v, want message naming agent_id", err)
	}
	if calls != 0 {
		t.Fatalf("PendingHooks() dispatched to peer %d times; expected zero before failing closed", calls)
	}
}

// TestHandleCallback_UnknownMethodFailsClosed 验证未知 callback method 不会被静默 ACK。
// 用内存 client 直接调用 handleCallback，确保未登记的控制面方法返回 MethodNotFound。
func TestHandleCallback_UnknownMethodFailsClosed(t *testing.T) {
	t.Parallel()

	// 本地 peer 先登记未知方法，避免 transport 层提前拒绝；真正断言发生在 handleCallback。
	var handlerResult any
	var handlerErr error
	local := jrpcserver.NewLocal(handler.Map{
		"never.opted.in": platformrpc.StrictHandler(func(ctx context.Context, _ struct{}) (any, error) {
			return nil, nil
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	client := &Client{
		conn: local.Client,
	}

	// jrpc2.Request 不能直接构造，fakeJRPCRequest 通过 loopback 捕获真实请求对象。
	req := fakeJRPCRequest(t, "never.opted.in")
	handlerResult, handlerErr = client.handleCallback(context.Background(), req)
	if handlerErr == nil {
		t.Fatalf("handleCallback(unknown method) err = nil, want MethodNotFound")
	}
	if !strings.Contains(handlerErr.Error(), "unknown callback method") {
		t.Fatalf("handleCallback(unknown method) err = %q, want message to mention 'unknown callback method'", handlerErr.Error())
	}
	if handlerResult != nil {
		t.Fatalf("handleCallback(unknown method) result = %v, want nil", handlerResult)
	}
}

// fakeJRPCRequest 通过 NewLocal loopback 生成最小 jrpc2.Request。
// passthrough handler 捕获服务端收到的请求对象，供私有 callback 入口复用。
func fakeJRPCRequest(t *testing.T, method string) *jrpc2.Request {
	t.Helper()

	captured := make(chan *jrpc2.Request, 1)
	local := jrpcserver.NewLocal(handler.Map{
		method: handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
			captured <- req
			return nil, nil
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	t.Cleanup(func() { _ = local.Close() })

	// Notify 不需要响应体，但仍会让 handler 捕获到真实 Request。
	if err := local.Client.Notify(context.Background(), method, nil); err != nil {
		t.Fatalf("Notify(%q) error = %v", method, err)
	}
	req := <-captured
	return req
}
