package bootstrap_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/creachadair/jrpc2/handler"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/bootstrap"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestLSPLogRelayE2EWritesBackendLog(t *testing.T) {
	backendLogPath := filepath.Join(t.TempDir(), "backend.log")
	backendLog, err := os.OpenFile(backendLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open backend log: %v", err)
	}
	t.Cleanup(func() { _ = backendLog.Close() })

	backendLogger := slog.New(slog.NewTextHandler(backendLog, &slog.HandlerOptions{Level: slog.LevelDebug}))
	addr := startControlPlaneForLogRelayTest(t, mcpcontrol.NewHandlers(mcpcontrol.HandlerDeps{
		Registry: mcpcontrol.NewRegistry(),
		Logger:   backendLogger,
	}).Handlers)

	previousLogger := pkglogger.Get()
	pkglogger.ClearRelayHook()
	pkglogger.InitWithConsoleWriter(io.Discard)
	t.Cleanup(func() {
		pkglogger.ClearRelayHook()
		pkglogger.SetForTest(previousLogger)
	})

	client := bootstrap.New(bootstrap.Config{
		RPCAddr:             addr,
		InstanceID:          "lsp-log-e2e",
		BinaryName:          "mcp-lsp",
		ClientKind:          mcpdto.ClientKindLSP,
		ThreadID:            "thread-log-e2e",
		CapabilitiesOffered: []string{"tools/lsp"},
	})
	client.InstallLogRelay()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := client.Start(ctx); err != nil {
		t.Fatalf("bootstrap Start() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	pkglogger.Warn("lsp relay backend persistence e2e",
		pkglogger.FieldToolName, "grep",
		pkglogger.FieldComponent, "mcp-lsp",
	)

	assertBackendLogContains(t, backendLog, backendLogPath, []string{
		`msg="lsp relay backend persistence e2e"`,
		"source=mcp-control",
		"mcp_binary_name=mcp-lsp",
		"mcp_client_kind=lsp",
		"thread_id=thread-log-e2e",
		"tool_name=grep",
	})
}

func startControlPlaneForLogRelayTest(t *testing.T, methods handler.Map) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen control plane: %v", err)
	}
	done := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		_ = ln.Close()
		stat := jrpc2.NewServer(methods, &jrpc2.ServerOptions{}).Start(channel.Line(conn, conn)).WaitStatus()
		done <- stat.Err
	})
	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case <-done:
			wg.Wait()
		case <-time.After(time.Second):
			t.Error("control plane server did not stop")
		}
	})
	return ln.Addr().String()
}

func assertBackendLogContains(t *testing.T, file *os.File, path string, wants []string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		_ = file.Sync()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read backend log: %v", err)
		}
		text := string(data)
		missing := missingLogFragments(text, wants)
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("backend log missing %v\nlog:\n%s", missing, text)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func missingLogFragments(text string, wants []string) []string {
	var missing []string
	for _, want := range wants {
		if !strings.Contains(text, want) {
			missing = append(missing, want)
		}
	}
	return missing
}
