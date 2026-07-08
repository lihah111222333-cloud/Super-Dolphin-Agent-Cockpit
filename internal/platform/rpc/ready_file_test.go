package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerRunWritesRPCReadyFile(t *testing.T) {
	readyPath := filepath.Join(t.TempDir(), "runtime", "rpc-ready.json")
	sessionToken := "sd-ready-test-token"
	t.Setenv(controlRPCReadyFileEnv, readyPath)
	t.Setenv(controlRPCSessionTokenEnv, sessionToken)
	t.Setenv(legacyControlRPCSessionTokenEnv, "")
	t.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "owner")
	t.Setenv("SUPER_DOLPHIN_ENTRYPOINT", "agent-runtime")
	t.Setenv(controlRPCAddrEnv, "127.0.0.1:0")

	server := newTestServer()
	cancel, done := startRPCRunnerForTest(t, server.Run)

	event, raw := waitRPCReadyFile(t, readyPath, done)
	stopRPCRunnerForReadyFileTest(t, cancel, done)

	assertRPCReadyEvent(t, event)
	assertReadyFileDoesNotLeakToken(t, raw, sessionToken)
}

func assertRPCReadyEvent(t *testing.T, event rpcReadyEvent) {
	t.Helper()
	if event.Event != "core.ready" {
		t.Fatalf("event = %q, want core.ready", event.Event)
	}
	assertConcreteRPCAddr(t, event.RPCAddr)
	if got := os.Getenv(controlRPCAddrEnv); got != event.RPCAddr {
		t.Fatalf("%s = %q, want ready rpc_addr %q", controlRPCAddrEnv, got, event.RPCAddr)
	}
	if event.SessionTokenEnv != controlRPCSessionTokenEnv {
		t.Fatalf("session_token_env = %q, want %q", event.SessionTokenEnv, controlRPCSessionTokenEnv)
	}
	if event.PID != os.Getpid() {
		t.Fatalf("pid = %d, want %d", event.PID, os.Getpid())
	}
	if event.ProcessRole != "owner" {
		t.Fatalf("process_role = %q, want owner", event.ProcessRole)
	}
	if event.Entrypoint != "agent-runtime" {
		t.Fatalf("entrypoint = %q, want agent-runtime", event.Entrypoint)
	}
	if event.EmittedAt.IsZero() {
		t.Fatal("emitted_at is zero")
	}
}

func assertConcreteRPCAddr(t *testing.T, addr string) {
	t.Helper()
	if addr == "" || strings.HasSuffix(addr, ":0") {
		t.Fatalf("rpc_addr = %q, want concrete listener addr", addr)
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("rpc_addr %q is not host:port: %v", addr, err)
	}
	if port == "0" {
		t.Fatalf("rpc_addr port = 0, want concrete listener port")
	}
}

func assertReadyFileDoesNotLeakToken(t *testing.T, raw []byte, sessionToken string) {
	t.Helper()
	if strings.Contains(string(raw), sessionToken) {
		t.Fatalf("ready file leaked session token: %s", raw)
	}
}

func stopRPCRunnerForReadyFileTest(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestServerRunReadyFileRequiresCanonicalSessionToken(t *testing.T) {
	readyPath := filepath.Join(t.TempDir(), "rpc-ready.json")
	t.Setenv(controlRPCReadyFileEnv, readyPath)
	t.Setenv(controlRPCSessionTokenEnv, "")
	t.Setenv(legacyControlRPCSessionTokenEnv, "")

	err := newTestServer().Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), controlRPCSessionTokenEnv) {
		t.Fatalf("Run() error = %v, want missing %s", err, controlRPCSessionTokenEnv)
	}
	assertNoReadyFile(t, readyPath)
}

func TestServerRunReadyFileRejectsLegacyOnlySessionToken(t *testing.T) {
	readyPath := filepath.Join(t.TempDir(), "rpc-ready.json")
	t.Setenv(controlRPCReadyFileEnv, readyPath)
	t.Setenv(controlRPCSessionTokenEnv, "")
	t.Setenv(legacyControlRPCSessionTokenEnv, "sd-legacy-token")

	err := newTestServer().Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), controlRPCSessionTokenEnv) {
		t.Fatalf("Run() error = %v, want canonical %s requirement", err, controlRPCSessionTokenEnv)
	}
	assertNoReadyFile(t, readyPath)
}

func TestPublishRPCReadyFileRejectsRelativePath(t *testing.T) {
	err := publishRPCReadyFile("relative/rpc-ready.json", "127.0.0.1:49152", time.Now())
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("publishRPCReadyFile() error = %v, want absolute path validation", err)
	}
}

func TestMaybePublishRPCReadyFileRequiresInheritedSessionToken(t *testing.T) {
	readyPath := filepath.Join(t.TempDir(), "rpc-ready.json")
	t.Setenv(controlRPCReadyFileEnv, readyPath)

	err := maybePublishRPCReadyFile("127.0.0.1:49152", false)
	if err == nil || !strings.Contains(err.Error(), controlRPCSessionTokenEnv) {
		t.Fatalf("maybePublishRPCReadyFile() error = %v, want inherited %s requirement", err, controlRPCSessionTokenEnv)
	}
	assertNoReadyFile(t, readyPath)
}

func waitRPCReadyFile(t *testing.T, path string, done <-chan error) (rpcReadyEvent, []byte) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if event, raw, ok := readRPCReadyFileIfPresent(t, path); ok {
			return event, raw
		}
		requireRPCRunnerStillRunning(t, done)
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for ready file %s", path)
	return rpcReadyEvent{}, nil
}

func readRPCReadyFileIfPresent(t *testing.T, path string) (rpcReadyEvent, []byte, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return rpcReadyEvent{}, nil, false
	}
	if err != nil {
		t.Fatalf("read ready file: %v", err)
	}
	var event rpcReadyEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("decode ready file: %v\n%s", err, raw)
	}
	return event, raw, true
}

func requireRPCRunnerStillRunning(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("Run() returned before ready file: %v", err)
	default:
	}
}

func assertNoReadyFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("ready file %s exists, want no file", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat ready file %s: %v", path, err)
	}
}
