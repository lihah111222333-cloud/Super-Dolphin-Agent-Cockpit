package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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

func TestBootstrapConfigChangedLogRedactsPayload(t *testing.T) {
	var buf bytes.Buffer
	pkglogger.InitModeWithLevel(pkglogger.Production, slog.LevelInfo)
	pkglogger.InitWithConsoleWriter(&buf)
	t.Cleanup(func() {
		pkglogger.InitWithConsoleWriter(os.Stderr)
	})

	cfg, err := buildBootstrapConfig(nil)
	if err != nil {
		t.Fatalf("buildBootstrapConfig() error = %v", err)
	}
	payload := json.RawMessage(`{"token":"super-secret-token","nested":{"password":"secret-password"}}`)
	cfg.OnConfigChanged(mcp.ConfigChangedNotify{
		Scope:         "agent",
		ConfigVersion: 42,
		Selector: mcp.Selector{
			Subscription: "config/agent",
			Scope: &mcp.SelectorScope{
				AgentID:    "agent-1",
				ClientKind: "ida",
			},
		},
		Payload: payload,
	})

	output := strings.TrimSpace(buf.String())
	if output == "" {
		t.Fatal("expected config changed log output")
	}
	assertLogOutputOmits(t, output, []string{"super-secret-token", "secret-password"})
	logPayload := mustDecodeLogPayload(t, output)
	assertLogPayloadMissingKeys(t, logPayload, output, []string{"payload", "binary_name", "instance_id"})
	if got := logPayload["scope"]; got != "agent" {
		t.Fatalf("scope = %#v, want agent", got)
	}
	if got := logPayload["config_version"]; got != float64(42) {
		t.Fatalf("config_version = %#v, want 42", got)
	}
	if got := logPayload["payload_size"]; got != float64(len(payload)) {
		t.Fatalf("payload_size = %#v, want %d", got, len(payload))
	}
	wantHash := sha256.Sum256(payload)
	if got := logPayload["payload_hash"]; got != fmt.Sprintf("%x", wantHash) {
		t.Fatalf("payload_hash = %#v, want %x", got, wantHash)
	}
	if _, ok := logPayload["selector"]; !ok {
		t.Fatalf("selector field missing: %s", output)
	}
}

func assertLogOutputOmits(t *testing.T, output string, forbidden []string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.Contains(output, value) {
			t.Fatalf("config changed log leaked payload content %q: %s", value, output)
		}
	}
}

func mustDecodeLogPayload(t *testing.T, output string) map[string]any {
	t.Helper()
	var logPayload map[string]any
	if err := json.Unmarshal([]byte(output), &logPayload); err != nil {
		t.Fatalf("unmarshal config changed log: %v", err)
	}
	return logPayload
}

func assertLogPayloadMissingKeys(t *testing.T, logPayload map[string]any, output string, keys []string) {
	t.Helper()
	for _, key := range keys {
		if _, ok := logPayload[key]; ok {
			t.Fatalf("config changed log contains extra field %q: %s", key, output)
		}
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
