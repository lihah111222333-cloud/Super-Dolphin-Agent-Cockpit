package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/creachadair/jrpc2/handler"
)

func TestEmptyToolProviderListToolsReturnsEmptyArray(t *testing.T) {
	tools, err := emptyToolProvider{}.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if tools == nil {
		t.Fatalf("ListTools() returned nil; want empty non-nil slice")
	}
	if len(tools) != 0 {
		t.Fatalf("ListTools() len = %d, want 0", len(tools))
	}
}

func TestEmptyToolProviderCallToolFailsClosed(t *testing.T) {
	_, err := emptyToolProvider{}.CallTool(context.Background(), "ida_ping", json.RawMessage(`{}`))
	if err == nil {
		t.Fatalf("CallTool() error = nil, want unknown tool error")
	}
}

func TestBootstrapRunnerRequiresRPCAddr(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	runner := bootstrapRunner{
		cfg:        bootstrap.Config{BinaryName: "mcp-ida"},
		client:     bootstrap.New(bootstrap.Config{BinaryName: "mcp-ida"}),
		stdioReady: ready,
	}
	err := runner.Run(ctx)
	if err == nil {
		t.Fatalf("Run() error = nil, want missing RPC address error")
	}
}

func TestRunFailsWhenRPCAddrMissing(t *testing.T) {
	originalStdin := os.Stdin
	t.Cleanup(func() {
		os.Stdin = originalStdin
	})
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", "")
	t.Setenv("RPC_ADDR", "")
	t.Setenv("GO_AGENT_CTL_BOOTSTRAP_JSON", "")
	t.Setenv("GO_AGENT_MCP_BOOT_CONTEXT", "")

	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	defer stdinRead.Close()
	defer stdinWrite.Close()
	os.Stdin = stdinRead

	err = run()
	if err == nil {
		t.Fatalf("run() error = nil, want missing RPC address error")
	}
	if !strings.Contains(err.Error(), "GO_AGENT_CTL_RPC_ADDR is required") {
		t.Fatalf("run() error = %v, want missing RPC address error", err)
	}
}

func TestBootstrapConfigDoesNotOfferBootSnapshotIDACapability(t *testing.T) {
	addr, captured := startRegisterCaptureServer(t)
	t.Setenv("GO_AGENT_CTL_RPC_ADDR", addr)
	t.Setenv("GO_AGENT_CTL_BOOTSTRAP_JSON", `{"instance_id":"snap-1","boot_id":"boot-1","binary_name":"mcp-ida","client_kind":"ida","capabilities":["tools/ida"]}`)

	cfg, err := buildBootstrapConfig(nil)
	if err != nil {
		t.Fatalf("buildBootstrapConfig() error = %v", err)
	}
	client := bootstrap.New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer client.Close()

	select {
	case req := <-captured:
		if len(req.CapabilitiesOffered) != 0 {
			t.Fatalf("CapabilitiesOffered = %#v, want empty", req.CapabilitiesOffered)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for register request")
	}
}

func startRegisterCaptureServer(t *testing.T) (string, <-chan mcp.RegisterRequest) {
	t.Helper()
	captured := make(chan mcp.RegisterRequest, 1)
	methods := handler.Map{
		mcp.MethodRegister: platformrpc.StrictHandler(func(_ context.Context, req mcp.RegisterRequest) (mcp.RegisterResponse, error) {
			captured <- req
			return mcp.RegisterResponse{
				InstanceID:            req.InstanceID,
				Generation:            1,
				CapabilitiesRejected:  []string{},
				HeartbeatIntervalMs:   1000,
				HeartbeatTimeoutMs:    500,
				ServerProtocolVersion: mcp.ProtocolVersion,
			}, nil
		}),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = jrpc2.NewServer(methods, nil).Start(channel.Line(conn, conn)).WaitStatus()
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), captured
}
