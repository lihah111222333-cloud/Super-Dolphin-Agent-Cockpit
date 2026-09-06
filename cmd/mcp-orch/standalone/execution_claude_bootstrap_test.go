package standalone

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/claudewire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// TestClaudeFirstTurnProtocolContract 聚合首输入、真实初始化和终态回执的窄协议回归。
func TestClaudeFirstTurnProtocolContract(t *testing.T) {
	t.Run("first-input-bootstrap", TestClaudeFirstTurnBootstrapsInit)
	t.Run("result-before-init", TestClaudeResultBeforeInitIsBufferedAndReplayedOnce)
	t.Run("init-before-attachment", TestClaudeInitDefersReadinessUntilProcessAttachment)
	t.Run("terminal-receipt-retry", TestClaudeTerminalReceiptRetryPreservesCanonicalTruth)
}

// TestClaudeFirstTurnBootstrapsInit 通过真实子进程锁住先输入后 init 的官方生命周期，控制通道代替时间猜测。
func TestClaudeFirstTurnBootstrapsInit(t *testing.T) {
	requireUnixProbe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	fixture := newClaudeBootstrapFixture(t, ctx)
	turnID := fixture.submitFirstInput(t, ctx)
	fixture.confirmInit(t, ctx)
	fixture.confirmResult(t, ctx, turnID)
}

type claudeBootstrapFixture struct {
	execution *Execution
	session   *cliSession
	terminals chan turndto.TurnTerminalV2
	events    chan claudewire.Event
	control   net.Conn
}

func newClaudeBootstrapFixture(t *testing.T, ctx context.Context) *claudeBootstrapFixture {
	t.Helper()
	listener, deadline := newClaudeBootstrapListener(t, ctx)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fixture := writeProbeFixture(t, "#!/bin/sh\nexport ORCH_CLAUDE_BOOTSTRAP="+strconv.Quote(listener.Addr().String())+"\nexec "+strconv.Quote(binary)+" -test.run=^TestClaudeBootstrapChild$ -- \"$@\"\n")
	identity, err := uuid.NewRandom()
	if err != nil {
		t.Fatal(err)
	}
	session := &cliSession{threadID: identity.String(), provider: Provider{Name: "claude"}, readyCh: make(chan struct{})}
	execution := NewExecution(Config{})
	execution.sessions["agent"] = session
	terminals := make(chan turndto.TurnTerminalV2, 2)
	execution.BindTerminalSink(func(_ context.Context, terminal turndto.TurnTerminalV2) { terminals <- terminal })
	events := make(chan claudewire.Event, 4)
	handler := execution.claudeEventHandler(session)
	process, err := claudewire.StartWithExitContext(ctx, fixture, t.TempDir(), "fixture", session.threadID, "", os.Environ(), func(event claudewire.Event) { handler(event); events <- event }, contract.CLIProcessExitBinding{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := process.Close(); err != nil {
			t.Errorf("close child: %v", err)
		}
	})
	session.mu.Lock()
	session.claude = process
	session.activated = true
	session.mu.Unlock()
	control, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := control.Close(); err != nil {
			t.Errorf("close control: %v", err)
		}
	})
	if err := control.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(control)
	if !scanner.Scan() || scanner.Text() != "awaiting-user" {
		t.Fatal("child did not reach pre-input barrier")
	}
	return &claudeBootstrapFixture{execution: execution, session: session, terminals: terminals, events: events, control: control}
}

func (f *claudeBootstrapFixture) submitFirstInput(t *testing.T, ctx context.Context) string {
	t.Helper()
	execution, session, terminals, events := f.execution, f.session, f.terminals, f.events
	assertClaudeBootstrapNotReady(t, session, terminals)
	sub := contract.TurnSubmission{AgentID: "agent", Inputs: []turndto.InputItem{{Type: "text", Content: "first-input"}}}
	turnID, err := execution.SubmitTurn(ctx, sub)
	if err != nil {
		t.Fatalf("first user input must bootstrap system/init: %v", err)
	}
	if turnID == "" {
		t.Fatal("first input missing turn identity")
	}
	event := awaitClaudeBootstrapEvent(t, ctx, events)
	if event.SessionID == session.threadID || event.Type != "system" {
		t.Fatal("expected controlled wrong-session init")
	}
	assertClaudeBootstrapNotReady(t, session, terminals)
	if _, err := execution.SubmitTurn(ctx, sub); err == nil {
		t.Fatal("second input accepted while first turn active before init")
	}
	return turnID
}

func (f *claudeBootstrapFixture) confirmInit(t *testing.T, ctx context.Context) {
	t.Helper()
	execution, session, terminals, events, control := f.execution, f.session, f.terminals, f.events, f.control
	sub := contract.TurnSubmission{AgentID: "agent", Inputs: []turndto.InputItem{{Type: "text", Content: "first-input"}}}
	sendClaudeBootstrapGate(t, control, "init")
	event := awaitClaudeBootstrapEvent(t, ctx, events)
	if !event.IsSystemInit(session.threadID) {
		t.Fatal("matching init not delivered")
	}
	if err := execution.WaitForSessionReady(ctx, "agent", time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.SubmitTurn(ctx, sub); err == nil {
		t.Fatal("second input accepted while first turn active after init")
	}
	select {
	case <-terminals:
		t.Fatal("terminal published before result gate")
	default:
	}
}

func (f *claudeBootstrapFixture) confirmResult(t *testing.T, ctx context.Context, turnID string) {
	t.Helper()
	terminals, events, control := f.terminals, f.events, f.control
	sendClaudeBootstrapGate(t, control, "result")
	awaitClaudeBootstrapEvent(t, ctx, events)
	select {
	case terminal := <-terminals:
		if terminal.TurnID != turnID || terminal.PublicSummary != "bootstrap-complete" {
			t.Fatalf("terminal identity/content mismatch: %#v", terminal)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-terminals:
		t.Fatal("duplicate terminal")
	default:
	}
}

func assertClaudeBootstrapNotReady(t *testing.T, session *cliSession, terminals <-chan turndto.TurnTerminalV2) {
	t.Helper()
	session.mu.Lock()
	initialized := session.claudeInitialized
	session.mu.Unlock()
	if initialized {
		t.Fatal("initialized before matching system/init")
	}
	select {
	case <-session.readyCh:
		t.Fatal("ready before matching system/init")
	default:
	}
	select {
	case <-terminals:
		t.Fatal("terminal before matching system/init")
	default:
	}
}

func awaitClaudeBootstrapEvent(t *testing.T, ctx context.Context, events <-chan claudewire.Event) claudewire.Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return claudewire.Event{}
	}
}

func sendClaudeBootstrapGate(t *testing.T, control net.Conn, command string) {
	t.Helper()
	if _, err := fmt.Fprintln(control, command); err != nil {
		t.Fatal(err)
	}
}

// TestClaudeBootstrapChild 在读取且校验首条 user 之前没有任何 stdout 输出；每个初始化/结果阶段均由独立控制连接放行。
func TestClaudeBootstrapChild(t *testing.T) {
	address := os.Getenv("ORCH_CLAUDE_BOOTSTRAP")
	if address == "" {
		return
	}
	control, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		os.Exit(51)
	}
	if err := control.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
		os.Exit(52)
	}
	sessionID := claudeBootstrapChildSession(control)
	input := bufio.NewScanner(os.Stdin)
	if !input.Scan() {
		os.Exit(55)
	}
	requestID := readClaudeBootstrapChildUser(input.Bytes(), sessionID)
	writeClaudeBootstrapChildEvents(control, sessionID, requestID)
	// 持续读取时若意外出现第二条 user，立即以故障退出，而不是模拟第二轮。
	if input.Scan() {
		os.Exit(63)
	}
	os.Exit(0)
}

func claudeBootstrapChildSession(control net.Conn) string {
	sessionID := ""
	for i, arg := range os.Args {
		if arg == "--session-id" && i+1 < len(os.Args) {
			sessionID = os.Args[i+1]
		}
	}
	if _, err := uuid.Parse(sessionID); err != nil {
		os.Exit(53)
	}
	if _, err := fmt.Fprintln(control, "awaiting-user"); err != nil {
		os.Exit(54)
	}
	return sessionID
}

func readClaudeBootstrapChildUser(frame []byte, sessionID string) string {
	var user struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		UUID      string `json:"uuid"`
		Message   struct {
			Role    string               `json:"role"`
			Content []claudewire.Content `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(frame, &user) != nil || user.Type != "user" || user.SessionID != sessionID || user.Message.Role != "user" || len(user.Message.Content) != 1 || user.Message.Content[0].Text != "first-input" {
		os.Exit(56)
	}
	if _, err := uuid.Parse(user.UUID); err != nil {
		os.Exit(57)
	}
	return user.UUID
}

func writeClaudeBootstrapChildEvents(control net.Conn, sessionID, requestID string) {
	output := json.NewEncoder(os.Stdout)
	if output.Encode(claudewire.Event{Type: "system", Subtype: "init", SessionID: "wrong-session"}) != nil {
		os.Exit(58)
	}
	gates := bufio.NewScanner(control)
	if !gates.Scan() || gates.Text() != "init" {
		os.Exit(59)
	}
	if output.Encode(claudewire.Event{Type: "system", Subtype: "init", SessionID: sessionID}) != nil {
		os.Exit(60)
	}
	if !gates.Scan() || gates.Text() != "result" {
		os.Exit(61)
	}
	if output.Encode(claudewire.Event{Type: "result", Subtype: "success", SessionID: sessionID, UUID: requestID, Result: "bootstrap-complete"}) != nil {
		os.Exit(62)
	}
}

func newClaudeBootstrapListener(t *testing.T, ctx context.Context) (*net.TCPListener, time.Time) {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
	})
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("bootstrap context requires a deadline")
	}
	if err := listener.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	return listener, deadline
}
