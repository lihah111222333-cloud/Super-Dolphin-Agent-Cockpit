package bootstrap

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	jrpcserver "github.com/creachadair/jrpc2/server"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestInstallLogRelaySendsLoggerRecordsThroughControlPlane(t *testing.T) {
	logRuntime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	logRuntime.InitWithConsoleWriter(io.Discard)
	t.Cleanup(logRuntime.ClearRelayHook)

	got := make(chan mcpdto.LogNotify, 1)
	local := jrpcserver.NewLocal(handler.Map{
		mcpdto.MethodLog: platformrpc.StrictHandler(func(_ context.Context, req mcpdto.LogNotify) (map[string]bool, error) {
			got <- req
			return map[string]bool{"ok": true}, nil
		}),
	}, &jrpcserver.LocalOptions{Server: &jrpc2.ServerOptions{}})
	defer local.Close()

	client := mustNewClient(t, Config{
		InstanceID: "inst-1",
		BinaryName: "mcp-lsp",
		ClientKind: mcpdto.ClientKindLSP,
		ThreadID:   "thread-1",
	})
	client.conn = local.Client
	client.lease = mcpdto.LeaseKey{InstanceID: "inst-1", Generation: 7}
	client.InstallLogRelay(logRuntime)

	logRuntime.Get().Warn("relay bridge test", "foo", "bar")

	select {
	case req := <-got:
		if req.InstanceID != "inst-1" || req.Generation != 7 {
			t.Fatalf("lease = %s/%d, want inst-1/7", req.InstanceID, req.Generation)
		}
		if req.Level != "WARN" {
			t.Fatalf("level = %q, want WARN", req.Level)
		}
		if req.Message != "relay bridge test" {
			t.Fatalf("message = %q, want relay bridge test", req.Message)
		}
		if req.Fields["foo"] != "bar" {
			t.Fatalf("fields[foo] = %#v, want bar", req.Fields["foo"])
		}
		if req.Fields["binary_name"] != "mcp-lsp" {
			t.Fatalf("fields[binary_name] = %#v, want mcp-lsp", req.Fields["binary_name"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ctl/log notify")
	}
}

func TestInstallLogRelayFallsBackToDedicatedFileWhenRPCUnavailable(t *testing.T) {
	logRuntime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	logRuntime.InitWithConsoleWriter(io.Discard)
	t.Cleanup(logRuntime.ClearRelayHook)

	dir := t.TempDir()
	t.Setenv(logFallbackDirEnv, dir)
	client := mustNewClient(t, Config{
		InstanceID: "inst-offline",
		BinaryName: "mcp-lsp",
		ClientKind: mcpdto.ClientKindLSP,
	})
	client.InstallLogRelay(logRuntime)

	logRuntime.Get().Warn("relay offline test", "foo", "bar")

	path := filepath.Join(dir, "mcp-lsp-"+time.Now().Format("2006-01-02")+".log")
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fallback log: %v", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatalf("fallback log is empty: %v", scanner.Err())
	}
	var record localLogFallbackRecord
	if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
		t.Fatalf("decode fallback record: %v", err)
	}
	if record.Message != "relay offline test" {
		t.Fatalf("message = %q, want relay offline test", record.Message)
	}
	if record.Level != "WARN" {
		t.Fatalf("level = %q, want WARN", record.Level)
	}
	if record.Fields["foo"] != "bar" {
		t.Fatalf("fields[foo] = %#v, want bar", record.Fields["foo"])
	}
}
