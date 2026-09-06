package standalone

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestCLIExecutionInputAndTerminalContract(t *testing.T) {
	cfg := validTestConfig(t)
	e := NewExecution(cfg)
	req := contract.LaunchRequest{AgentID: "test-agent", Cwd: cfg.WorkspaceRoot, Env: []string{"AGENT_PROVIDER=codex", "AGENT_MODEL=test-model"}}
	if _, _, err := e.launchProfile(req); err != nil {
		t.Fatal(err)
	}
	req.Cwd = filepath.Dir(cfg.WorkspaceRoot)
	if _, _, err := e.launchProfile(req); err == nil {
		t.Fatal("outside workspace accepted")
	}
	t.Run("canonical_terminal", func(t *testing.T) { testCLITerminal(t, e) })
	t.Run("no_silent_input_loss", testCLIInput)
}

func testCLITerminal(t *testing.T, e *Execution) {
	t.Helper()
	var received turndto.TurnTerminalV2
	e.BindTerminalSink(func(_ context.Context, terminal turndto.TurnTerminalV2) { received = terminal })
	for _, status := range []string{"completed", "interrupted", "failed"} {
		event := codexEvent{ThreadID: "thread-1"}
		event.Turn.ID, event.Turn.Status = "turn-1", status
		if err := e.deliverTerminal(event, strings.Repeat("界", 5000), "interrupt-1"); err != nil {
			t.Fatal(err)
		}
		if err := turndto.ValidateTurnTerminalV2(received); err != nil {
			t.Fatalf("%s terminal invalid: %v", status, err)
		}
	}
}

// TestCLITerminalReceiptSinkPropagatesConsumerFailure 锁定 provider→orchestration
// 终态消费者失败时的 fail-closed 行为，避免错误被 void sink 静默吞掉。
func TestCLITerminalReceiptSinkPropagatesConsumerFailure(t *testing.T) {
	e := NewExecution(Config{})
	want := errors.New("terminal receipt unavailable")
	e.BindTerminalSinkWithReceipt(func(context.Context, turndto.TurnTerminalV2) error { return want })
	session := &cliSession{threadID: "thread-1", turnID: "turn-1", output: "done"}
	event := codexEvent{ThreadID: "thread-1"}
	event.Turn.ID, event.Turn.Status = "turn-1", "completed"
	e.handleCodexEvent(session, "turn/completed", event)
	if !errors.Is(session.failed, want) {
		t.Fatalf("terminal sink failure = %v, want %v", session.failed, want)
	}
}

func testCLIInput(t *testing.T) {
	t.Helper()
	valid := contract.TurnSubmission{Inputs: []turndto.InputItem{{Type: "text", Content: "hello"}}}
	if _, err := codexInput(valid); err != nil {
		t.Fatal(err)
	}
	valid.SelectedSkills = []string{"unsupported-skill"}
	if _, err := codexInput(valid); err == nil {
		t.Fatal("skill silently discarded")
	}
}
