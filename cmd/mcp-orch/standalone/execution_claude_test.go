package standalone

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/claudewire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestClaudeExecutionRoutingContract(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.Providers[0].Name = "claude"
	e := NewExecution(cfg)
	req := contract.LaunchRequest{Cwd: cfg.WorkspaceRoot, Env: []string{"AGENT_PROVIDER=claude", "AGENT_MODEL=test-model"}}
	provider, model, err := e.launchProfile(req)
	if err != nil || provider.Name != "claude" || model != "test-model" {
		t.Fatalf("Claude profile = %#v, %q, %v", provider, model, err)
	}
	var received []turndto.TurnTerminalV2
	e.BindTerminalSink(func(_ context.Context, terminal turndto.TurnTerminalV2) { received = append(received, terminal) })
	session := &cliSession{threadID: "session", turnID: "turn"}
	handler := e.claudeEventHandler(session)
	testClaudeRejectedEvents(t, handler, session, &received)
	testClaudeInitFence(t, handler, session)
	handler(claudewire.Event{Type: "result", SessionID: "session", Subtype: "success", Result: "done"})
	if len(received) != 1 || received[0].PublicSummary != "done" || session.turnID != "" {
		t.Fatalf("terminal = %#v active=%q", received, session.turnID)
	}
	if err := turndto.ValidateTurnTerminalV2(received[0]); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeResultBeforeInitIsBufferedAndReplayedOnce(t *testing.T) {
	e := NewExecution(Config{})
	var received []turndto.TurnTerminalV2
	e.BindTerminalSink(func(_ context.Context, terminal turndto.TurnTerminalV2) { received = append(received, terminal) })
	session := &cliSession{threadID: "session", turnID: "turn"}
	handler := e.claudeEventHandler(session)
	result := claudewire.Event{Type: "result", SessionID: "session", UUID: "result-1", Subtype: "success", Result: "buffered"}
	handler(result)
	if len(received) != 0 || session.pendingClaudeResult == nil || session.turnID != "turn" {
		t.Fatalf("pre-init result was not bounded: received=%d pending=%v active=%q", len(received), session.pendingClaudeResult != nil, session.turnID)
	}
	handler(claudewire.Event{Type: "system", Subtype: "init", SessionID: "session"})
	if len(received) != 1 || received[0].PublicSummary != "buffered" || session.turnID != "" || session.pendingClaudeResult != nil {
		t.Fatalf("buffered result was not replayed exactly once: received=%#v active=%q pending=%v", received, session.turnID, session.pendingClaudeResult)
	}
	handler(result)
	if len(received) != 1 {
		t.Fatalf("duplicate Claude result was redelivered: %#v", received)
	}
}

// TestClaudeTerminalReceiptRetryPreservesCanonicalTruth mirrors the Codex
// receipt contract for Claude's root result stream: a failed sink keeps the
// service turn busy and exact result replay reuses one canonical EventID.
func TestClaudeTerminalReceiptRetryPreservesCanonicalTruth(t *testing.T) {
	e := NewExecution(Config{})
	firstErr := errors.New("Claude consumer temporarily unavailable")
	attempts := 0
	var received []turndto.TurnTerminalV2
	e.BindTerminalSinkWithReceipt(func(_ context.Context, terminal turndto.TurnTerminalV2) error {
		attempts++
		received = append(received, terminal)
		if attempts == 1 {
			return firstErr
		}
		return nil
	})
	session := &cliSession{threadID: "claude-session-retry", turnID: "claude-turn-retry", claudeInitialized: true}
	handler := e.claudeEventHandler(session)
	event := claudewire.Event{Type: "result", SessionID: session.threadID, UUID: "claude-result-retry", Subtype: "success", Result: "stable Claude output"}

	handler(event)
	pendingID := assertFailedClaudeReceipt(t, session, attempts, firstErr)

	handler(event)
	if attempts != 2 || len(received) != 2 || received[0].EventID != pendingID || received[1].EventID != pendingID {
		t.Fatalf("Claude receipt retry attempts=%d received=%#v pendingID=%q", attempts, received, pendingID)
	}
	assertSuccessfulClaudeReceiptCleanup(t, session)
}

func assertFailedClaudeReceipt(t *testing.T, session *cliSession, attempts int, firstErr error) string {
	t.Helper()
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.pendingTerminal == nil || !session.pendingTerminal.retryable || session.turnID != "claude-turn-retry" || session.completedTurnID != "" {
		t.Fatalf("failed Claude receipt state = pending:%v retryable:%v active:%q completed:%q", session.pendingTerminal != nil, session.pendingTerminal != nil && session.pendingTerminal.retryable, session.turnID, session.completedTurnID)
	}
	if attempts != 1 || !errors.Is(session.failed, firstErr) {
		t.Fatalf("first Claude receipt attempt=%d failed=%v, want one attempt and %v", attempts, session.failed, firstErr)
	}
	return session.pendingTerminal.terminal.EventID
}

func assertSuccessfulClaudeReceiptCleanup(t *testing.T, session *cliSession) {
	t.Helper()
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.pendingTerminal != nil || session.turnID != "" || session.completedTurnID != "claude-turn-retry" || session.failed != nil {
		t.Fatalf("successful Claude receipt cleanup = pending:%v active:%q completed:%q failed:%v", session.pendingTerminal != nil, session.turnID, session.completedTurnID, session.failed)
	}
}

// TestClaudeTerminalReceiptConflictDoesNotReplacePendingTruth rejects a second
// root result with the same service turn but a different provider UUID/result.
func TestClaudeTerminalReceiptConflictDoesNotReplacePendingTruth(t *testing.T) {
	e := NewExecution(Config{})
	want := errors.New("Claude consumer unavailable")
	attempts := 0
	e.BindTerminalSinkWithReceipt(func(context.Context, turndto.TurnTerminalV2) error {
		attempts++
		return want
	})
	session := &cliSession{threadID: "claude-session-conflict", turnID: "claude-turn-conflict", claudeInitialized: true}
	handler := e.claudeEventHandler(session)
	first := claudewire.Event{Type: "result", SessionID: session.threadID, UUID: "claude-result-first", Subtype: "success", Result: "first"}
	handler(first)
	session.mu.Lock()
	firstID := session.pendingTerminal.terminal.EventID
	session.mu.Unlock()
	conflict := first
	conflict.UUID, conflict.Result = "claude-result-conflict", "different"
	handler(conflict)
	if attempts != 1 {
		t.Fatalf("conflicting Claude terminal triggered another sink call: attempts=%d", attempts)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.pendingTerminal == nil || session.pendingTerminal.terminal.EventID != firstID || session.turnID != "claude-turn-conflict" {
		t.Fatalf("Claude conflict replaced pending truth: pending=%#v active=%q", session.pendingTerminal, session.turnID)
	}
}

func TestClaudeInitDefersReadinessUntilProcessAttachment(t *testing.T) {
	e := NewExecution(Config{})
	session := &cliSession{threadID: "session", readyCh: make(chan struct{}), claudeAttachPending: true}

	if pending, ok := e.acceptClaudeInit(session, claudewire.Event{Type: "system", Subtype: "init", SessionID: "session"}); !ok || pending != nil {
		t.Fatalf("acceptClaudeInit() = pending:%v ok:%v, want init accepted without pending result", pending, ok)
	}
	select {
	case <-session.readyCh:
		t.Fatal("readiness was signalled before Claude process attachment")
	default:
	}

	session.mu.Lock()
	session.claudeAttachPending = false
	session.mu.Unlock()
	session.signalReady(nil)
	select {
	case <-session.readyCh:
	default:
		t.Fatal("readiness was not signalled after Claude process attachment")
	}
}

func TestClaudeConflictingPreInitResultsFailClosed(t *testing.T) {
	e := NewExecution(Config{})
	var received []turndto.TurnTerminalV2
	e.BindTerminalSink(func(_ context.Context, terminal turndto.TurnTerminalV2) { received = append(received, terminal) })
	session := &cliSession{threadID: "session", turnID: "turn"}
	handler := e.claudeEventHandler(session)
	handler(claudewire.Event{Type: "result", SessionID: "session", UUID: "result-1", Subtype: "success", Result: "one"})
	handler(claudewire.Event{Type: "result", SessionID: "session", UUID: "result-2", Subtype: "success", Result: "two"})
	if session.failed == nil || len(received) != 0 {
		t.Fatalf("conflicting pre-init results were accepted: failed=%v received=%#v", session.failed, received)
	}
	handler(claudewire.Event{Type: "system", Subtype: "init", SessionID: "session"})
	if len(received) != 1 || received[0].Outcome != "failed" || received[0].PublicSummary != "" {
		t.Fatalf("conflicting pre-init result did not fail closed: %#v", received)
	}
}

func testClaudeRejectedEvents(t *testing.T, handler func(claudewire.Event), session *cliSession, received *[]turndto.TurnTerminalV2) {
	t.Helper()
	handler(claudewire.Event{Type: "result", SessionID: "foreign", Subtype: "success", Result: "wrong"})
	parent := "nested-agent"
	handler(claudewire.Event{Type: "result", SessionID: "session", ParentToolUseID: &parent, Subtype: "success", Result: "wrong"})
	if len(*received) != 0 || session.turnID != "turn" {
		t.Fatal("foreign/nested event changed current task")
	}
}

func testClaudeInitFence(t *testing.T, handler func(claudewire.Event), session *cliSession) {
	t.Helper()
	handler(claudewire.Event{Type: "system", Subtype: "init", SessionID: "foreign"})
	if session.claudeInitialized {
		t.Fatal("foreign system/init activated session")
	}
	handler(claudewire.Event{Type: "system", Subtype: "init", SessionID: "session"})
	if !session.claudeInitialized {
		t.Fatal("root system/init did not activate session")
	}
}

// TestClaudeArchiveDoesNotImpersonateStop 未实现归档时明确拒绝，不能偷偷停止任务并报告成功。
func TestClaudeArchiveDoesNotImpersonateStop(t *testing.T) {
	e := NewExecution(Config{})
	session := &cliSession{provider: Provider{Name: "claude"}, threadID: "thread", turnID: "active"}
	e.sessions["agent"] = session
	err := e.Archive(context.Background(), "agent")
	if !errors.Is(err, ErrExecutionNotReady) {
		t.Fatalf("archive error=%v, want unsupported capability", err)
	}
	var capabilityErr *contract.CapabilityError
	if !errors.As(err, &capabilityErr) || capabilityErr.Capability != providerdto.CapThreadArchive || capabilityErr.Driver != "claude" {
		t.Fatalf("archive capability error=%v, want typed thread_archive/claude error", err)
	}
	if session.stopped || session.turnID != "active" {
		t.Fatal("unsupported archive changed active task state")
	}
}

// TestClaudeUnsupportedLifecycleCapabilitiesAreTyped 验证未接通能力不会退化成空操作或新会话。
func TestClaudeUnsupportedLifecycleCapabilitiesAreTyped(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.Providers[0].Name = "claude"
	cfg.Providers[0].Sandbox = ""
	e := NewExecution(cfg)
	e.sessions["agent"] = &cliSession{provider: Provider{Name: "claude"}, threadID: "thread", turnID: "turn"}
	request := contract.LaunchRequest{AgentID: "child", Cwd: cfg.WorkspaceRoot,
		Env: []string{"AGENT_PROVIDER=claude", "AGENT_MODEL=test-model"}}
	tests := []struct {
		name, capability string
		call             func() error
	}{
		{name: "archive", capability: providerdto.CapThreadArchive, call: func() error {
			return e.Archive(context.Background(), "agent")
		}},
		{name: "interrupt", capability: providerdto.CapTurnInterrupt, call: func() error {
			return e.Interrupt(context.Background(), "agent", "test")
		}},
		{name: "fork", capability: providerdto.CapThreadFork, call: func() error {
			_, err := e.Fork(context.Background(), "agent", request)
			return err
		}},
		{name: "resume", capability: providerdto.CapThreadResume, call: func() error {
			return e.ActivatePersistedRuntime(context.Background(), orchestration.PersistedRuntimeActivation{
				AgentID: "agent", ThreadID: "thread", Provider: "claude", CWD: cfg.WorkspaceRoot,
			})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()
			if !errors.Is(err, ErrExecutionNotReady) {
				t.Fatalf("error=%v, want ErrExecutionNotReady", err)
			}
			var capabilityErr *contract.CapabilityError
			if !errors.As(err, &capabilityErr) || capabilityErr.Capability != test.capability || capabilityErr.Driver != "claude" {
				t.Fatalf("error=%v, want typed capability=%q driver=claude", err, test.capability)
			}
		})
	}
}
