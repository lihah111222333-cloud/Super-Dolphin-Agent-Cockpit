package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

type localCodexHelper struct {
	logPath      string
	childPIDPath string
}

func TestTransportSpawnLocalWaitsForReady(t *testing.T) {
	helper := installLocalCodexHelper(t, "serve")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	transport, err := newTransport(ctx, "")
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	defer func() { _ = transport.Close() }()

	if !transport.local {
		t.Fatal("transport.local = false, want true")
	}
	if !transport.Running() {
		t.Fatal("transport.Running() = false, want true")
	}
	events := waitForHelperEvents(t, helper.logPath, 1, time.Second)
	if events[0] != "initialize" {
		t.Fatalf("first helper event = %q, want initialize; events=%v", events[0], events)
	}
}

func TestTransportCloseGracefullyStopsLocalProcess(t *testing.T) {
	helper := installLocalCodexHelper(t, "serve-with-child")

	transport, err := newTransport(context.Background(), "")
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	childPID := waitForHelperChildPID(t, helper.childPIDPath, time.Second)
	if err := transport.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	waitForProcessExit(t, childPID, 5*time.Second)
	events := waitForHelperEvents(t, helper.logPath, 2, 2*time.Second)
	if !containsEvent(events, "signal:terminated") {
		t.Fatalf("helper events = %v, want SIGTERM record", events)
	}
}

func TestTransportStartupFailureCleansUpOrphans(t *testing.T) {
	helper := installLocalCodexHelper(t, "fail-with-child")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := newTransport(ctx, "")
	if err == nil {
		t.Fatal("newTransport() error = nil, want startup failure")
	}
	if !strings.Contains(err.Error(), "helper startup failure") {
		t.Fatalf("newTransport() error = %v, want helper stderr", err)
	}
	childPID := waitForHelperChildPID(t, helper.childPIDPath, time.Second)
	waitForProcessExit(t, childPID, 5*time.Second)
}

func TestCodexHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CODEX_HELPER") != "1" {
		return
	}
	if err := runCodexHelperProcess(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func installLocalCodexHelper(t *testing.T, mode string) localCodexHelper {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	scriptPath := filepath.Join(dir, "codex")
	script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run '^TestCodexHelperProcess$' -- \"$@\"\n", exe)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", scriptPath, err)
	}
	helper := localCodexHelper{
		logPath:      filepath.Join(dir, "events.log"),
		childPIDPath: filepath.Join(dir, "child.pid"),
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GO_WANT_CODEX_HELPER", "1")
	t.Setenv("CODEX_HELPER_MODE", mode)
	t.Setenv("CODEX_HELPER_LOG", helper.logPath)
	t.Setenv("CODEX_HELPER_CHILD_PID_FILE", helper.childPIDPath)
	return helper
}

func runCodexHelperProcess() error {
	args := helperCommandArgs()
	mode := strings.TrimSpace(os.Getenv("CODEX_HELPER_MODE"))
	switch mode {
	case "serve":
		return runServeHelper(helperListenURL(args), false)
	case "serve-with-child":
		return runServeHelper(helperListenURL(args), true)
	case "fail-with-child":
		_, err := startHelperChild()
		fmt.Fprintln(os.Stderr, "helper startup failure")
		return errOrDefault(err, errors.New("helper startup failure"))
	default:
		return fmt.Errorf("unknown helper mode %q", mode)
	}
}

func helperCommandArgs() []string {
	for i, arg := range os.Args {
		if arg == "--" && i+1 < len(os.Args) {
			return os.Args[i+1:]
		}
	}
	return nil
}

func helperListenURL(args []string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--listen" {
			return args[i+1]
		}
	}
	return ""
}

func runServeHelper(listenURL string, withChild bool) error {
	if withChild {
		if _, err := startHelperChild(); err != nil {
			return err
		}
	}
	parsed, err := url.Parse(strings.TrimSpace(listenURL))
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", parsed.Host)
	if err != nil {
		return err
	}
	defer listener.Close()
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- http.Serve(listener, websocket.Handler(helperWSHandler))
	}()
	return waitForHelperSignal(listener, serveErr)
}

func waitForHelperSignal(listener net.Listener, serveErr chan error) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	select {
	case sig := <-signals:
		_ = appendHelperEvent("signal:" + helperSignalName(sig))
		_ = listener.Close()
		return nil
	case err := <-serveErr:
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	}
}

func helperWSHandler(conn *websocket.Conn) {
	defer conn.Close()
	for {
		var raw string
		if err := websocket.Message.Receive(conn, &raw); err != nil {
			return
		}
		var msg jsonRPCMessage
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			continue
		}
		method := strings.TrimSpace(msg.Method)
		if method != "" {
			_ = appendHelperEvent(method)
		}
		if len(msg.ID) == 0 {
			continue
		}
		resp, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(append([]byte(nil), msg.ID...)),
			"result":  map[string]any{"ok": true},
		})
		if err != nil {
			return
		}
		if err := websocket.Message.Send(conn, string(resp)); err != nil {
			return
		}
	}
}

func startHelperChild() (int, error) {
	child := exec.Command("/bin/sh", "-c", "exec sleep 60")
	if err := child.Start(); err != nil {
		return 0, err
	}
	if path := strings.TrimSpace(os.Getenv("CODEX_HELPER_CHILD_PID_FILE")); path != "" {
		if err := os.WriteFile(path, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			return 0, err
		}
	}
	return child.Process.Pid, nil
}

func appendHelperEvent(event string) error {
	path := strings.TrimSpace(os.Getenv("CODEX_HELPER_LOG"))
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintln(file, event)
	return err
}

func helperSignalName(sig os.Signal) string {
	if sig == syscall.SIGTERM {
		return "terminated"
	}
	if sig == syscall.SIGINT {
		return "interrupt"
	}
	return sig.String()
}

func errOrDefault(err error, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

func waitForHelperEvents(t *testing.T, path string, min int, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		events := readHelperEvents(t, path)
		if len(events) >= min {
			return events
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("helper events in %q did not reach %d entries", path, min)
	return nil
}

func readHelperEvents(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func waitForHelperChildPID(t *testing.T, path string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr != nil {
				t.Fatalf("Atoi(%q) error = %v", strings.TrimSpace(string(data)), convErr)
			}
			return pid
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("ReadFile(%q) error = %v", path, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("child pid file %q not created", path)
	return 0
}

func waitForProcessExit(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pid %d still running after %s", pid, timeout)
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func containsEvent(events []string, want string) bool {
	return slices.Contains(events, want)
}
