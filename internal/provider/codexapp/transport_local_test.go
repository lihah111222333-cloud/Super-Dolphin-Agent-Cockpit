//go:build !windows

package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kelindar/event"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

type localCodexHelper struct {
	logPath      string
	childPIDPath string
}

func TestTransportConnectOnceCapsInboundFrame(t *testing.T) {
	t.Run("boundary", func(t *testing.T) { assertInboundFrameLimit(t, transportInboundFrameMaxBytes, false) })
	t.Run("oversized", func(t *testing.T) { assertInboundFrameLimit(t, transportInboundFrameMaxBytes+1, true) })
}

func assertInboundFrameLimit(t *testing.T, size int, wantErr bool) {
	t.Helper()
	server, serverWrite := newInboundFrameServer(size)
	defer server.Close()

	transport := &transport{serverURL: "ws" + strings.TrimPrefix(server.URL, "http")}
	if err := transport.connectOnce(context.Background()); err != nil {
		t.Fatalf("connectOnce() error = %v", err)
	}
	defer transport.closeSocket()
	ws := transport.currentWS()
	if ws == nil {
		t.Fatal("connectOnce() did not retain websocket")
	}
	if err := ws.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	_, payload, err := ws.ReadMessage()
	if (err != nil) != wantErr {
		t.Fatalf("ReadMessage() error = %v, wantErr %v", err, wantErr)
	}
	if !wantErr {
		assertInboundPayloadLength(t, payload, size)
	}
	if wantErr {
		transport.closeSocket()
	}
	assertInboundFrameServerWrite(t, <-serverWrite, wantErr)
}

func newInboundFrameServer(size int) (*httptest.Server, <-chan error) {
	serverWrite := make(chan error, 1)
	upgrader := websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			serverWrite <- err
			return
		}
		defer conn.Close()
		serverWrite <- conn.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", size)))
	}))
	return server, serverWrite
}

func assertInboundPayloadLength(t *testing.T, payload []byte, size int) {
	t.Helper()
	if len(payload) != size {
		t.Fatalf("ReadMessage() payload length = %d, want %d", len(payload), size)
	}
}

func assertInboundFrameServerWrite(t *testing.T, err error, allowFailure bool) {
	t.Helper()
	if err != nil && !allowFailure {
		t.Fatalf("server WriteMessage() error = %v", err)
	}
}

func TestTransportSpawnLocalWaitsForReady(t *testing.T) {
	helper := installLocalCodexHelper(t, "serve")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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

func TestTransportSpawnLocalWaitsPastLegacyRetryWindow(t *testing.T) {
	helper := installLocalCodexHelper(t, "serve-after-delay")
	t.Setenv("CODEX_HELPER_START_DELAY_MS", "4800")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	transport, err := newTransport(ctx, "")
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	defer func() { _ = transport.Close() }()

	if !transport.local {
		t.Fatal("transport.local = false, want true")
	}
	events := waitForHelperEvents(t, helper.logPath, 1, 2*time.Second)
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

func TestSessionShutdownClosingStillStopsLocalProcess(t *testing.T) {
	helper := installLocalCodexHelper(t, "serve-with-child")

	transport, err := newTransport(context.Background(), "")
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	childPID := waitForHelperChildPID(t, helper.childPIDPath, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		transport: transport,
		ctx:       ctx,
		cancel:    cancel,
		turns:     map[string]*turnHandle{},
	}
	if err := s.shutdownSession(true); err != nil {
		t.Fatalf("shutdownSession() error = %v", err)
	}

	waitForProcessExit(t, childPID, 5*time.Second)
	if got := transport.currentProcess(); got != nil {
		t.Fatalf("currentProcess() = %v, want nil", got)
	}
	events := waitForHelperEvents(t, helper.logPath, 3, 2*time.Second)
	if !containsEvent(events, "shutdown") {
		t.Fatalf("helper events = %v, want graceful shutdown notification", events)
	}
	if !containsEvent(events, "signal:terminated") {
		t.Fatalf("helper events = %v, want SIGTERM record", events)
	}
}

func TestSessionForceStopClosingStillKillsLocalProcess(t *testing.T) {
	helper := installLocalCodexHelper(t, "serve-with-child")

	transport, err := newTransport(context.Background(), "")
	if err != nil {
		t.Fatalf("newTransport() error = %v", err)
	}
	childPID := waitForHelperChildPID(t, helper.childPIDPath, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{
		transport: transport,
		ctx:       ctx,
		cancel:    cancel,
		turns:     map[string]*turnHandle{},
	}
	if err := s.shutdownSession(false); err != nil {
		t.Fatalf("shutdownSession(false) error = %v", err)
	}

	waitForProcessExit(t, childPID, 5*time.Second)
	if got := transport.currentProcess(); got != nil {
		t.Fatalf("currentProcess() = %v, want nil", got)
	}
}

func TestWatchLocalProcessIgnoresClosingExit(t *testing.T) {
	proc := &localProcess{
		guard:      &processGuard{},
		done:       make(chan struct{}),
		stderrDone: make(chan struct{}),
	}
	transport := &transport{process: proc}
	transport.closing.Store(true)
	close(proc.done)
	close(proc.stderrDone)

	transport.watchLocalProcess(proc)

	if got := transport.currentProcess(); got != nil {
		t.Fatalf("currentProcess() = %v, want nil", got)
	}
	if err := transport.processFailure(); err != nil {
		t.Fatalf("processFailure() = %v, want nil during closing", err)
	}
}

func TestTransportStartupFailureCleansUpOrphans(t *testing.T) {
	helper := installLocalCodexHelper(t, "fail-with-child")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

func TestTransportDispatchReadMessage_CodexRolloutFramesDispatchToolLifecycle(t *testing.T) {
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	endCh := make(chan tooldto.ToolCallEnd, 1)
	cancelBegin := event.Subscribe(bus, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	defer cancelBegin()
	cancelEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { endCh <- ev })
	defer cancelEnd()

	ctx := context.Background()
	s := newInboundTestSession(ctx, nil, &ServerManager{})
	s.dispatcher = dispatcher
	transport := &transport{}
	handler := func(ctx context.Context, resp Responder, msg RawMessage) {
		s.onInboundMessage(ctx, resp, msg)
	}

	transport.dispatchReadMessage(ctx, []byte(`{
		"timestamp":"2026-05-21T13:49:04.055Z",
		"type":"response_item",
		"payload":{
			"type":"function_call",
			"name":"file",
			"namespace":"mcp__lsp__",
			"arguments":"{\"action\":\"read_file\",\"file_path\":\"smoke.go\"}",
			"call_id":"call-file"
		}
	}`), handler)
	begin := waitToolCallBegin(t, beginCh)
	if begin.CallID != "call-file" || begin.ToolName != "mcp__lsp__file" {
		t.Fatalf("ToolCallBegin = %+v, want call-file/mcp__lsp__file", begin)
	}

	transport.dispatchReadMessage(ctx, []byte(`{
		"timestamp":"2026-05-21T13:49:04.057Z",
		"type":"event_msg",
		"payload":{
			"type":"mcp_tool_call_end",
			"call_id":"call-file",
			"invocation":{"server":"lsp","tool":"file","arguments":{"action":"read_file","file_path":"smoke.go"}},
			"duration":{"secs":0,"nanos":2062541},
			"result":{"Ok":{"content":[{"type":"text","text":"\" 1: package main\\n\""}],"structuredContent":{"value":" 1: package main\n"},"isError":false}}
		}
	}`), handler)
	end := waitToolCallEnd(t, endCh)
	if end.CallID != "call-file" || end.ToolName != "mcp__lsp__file" || !end.Success {
		t.Fatalf("ToolCallEnd = %+v, want successful call-file/mcp__lsp__file", end)
	}
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
	if helperIsAppServerHelp(args) {
		return nil
	}
	mode := strings.TrimSpace(os.Getenv("CODEX_HELPER_MODE"))
	switch mode {
	case "serve":
		return runServeHelper(helperListenURL(args), false)
	case "serve-after-delay":
		if delay := helperStartDelay(); delay > 0 {
			time.Sleep(delay)
		}
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

func helperIsAppServerHelp(args []string) bool {
	return len(args) >= 2 && args[0] == codexAppServerCommand && args[1] == "--help"
}

func helperStartDelay() time.Duration {
	raw := strings.TrimSpace(os.Getenv("CODEX_HELPER_START_DELAY_MS"))
	if raw == "" {
		return 0
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
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
	if err := announceHelperListenURL(listener); err != nil {
		return err
	}
	upgrader := &websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	serveErr := make(chan error, 1)
	var serveWG sync.WaitGroup
	serveWG.Go(func() {
		serveErr <- http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			helperWSHandler(conn)
		}))
	})
	return waitForHelperSignal(listener, serveErr)
}

func announceHelperListenURL(listener net.Listener) error {
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("unexpected listener addr %T", listener.Addr())
	}
	serverURL := fmt.Sprintf("ws://127.0.0.1:%d", addr.Port)
	_, err := fmt.Fprintf(
		os.Stderr,
		"codex app-server (WebSockets)\n  listening on: %s\n  readyz: http://127.0.0.1:%d/readyz\n  healthz: http://127.0.0.1:%d/healthz\n  note: binds localhost only (use SSH port-forwarding for remote access)\n",
		serverURL,
		addr.Port,
		addr.Port,
	)
	return err
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
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var msg jsonRPCMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
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
		if err := conn.WriteMessage(websocket.TextMessage, resp); err != nil {
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
