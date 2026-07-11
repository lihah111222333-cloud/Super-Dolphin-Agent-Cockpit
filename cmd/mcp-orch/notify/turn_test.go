package notify

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// aliasFromMap returns a resolver that looks agent ids up in a static
// table. Any other id resolves to empty — the plan-compliant drop.
func aliasFromMap(m map[string]string) AgentAliasResolver {
	return func(agentID, _ string) string { return m[strings.TrimSpace(agentID)] }
}

func newCompletedEvent(agentID, threadID, turnID string, success bool, status string) turndto.TurnCompleted {
	return turndto.TurnCompleted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader:  shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: threadID}, AgentID: agentID},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: turnID},
		},
		Success: success,
		Status:  status,
		Result:  "hello",
	}
}

func TestTurnNotifierDefaultsToDropAll(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{}
	tn := NewTurnNotifier(slog.Default(), rec, nil)
	tn.OnTurnCompleted(context.Background(), newCompletedEvent("a", "t", "turn-1", true, "completed"))
	tn.OnTurnInterrupted(context.Background(), turndto.TurnInterrupted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader:  shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: "t"}, AgentID: "a"},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: "x"},
		},
		Reason: "cancel",
	})
	tn.OnThreadStopped(context.Background(), threaddto.Stopped{ThreadID: "t", AgentID: "a"})
	if rec.len() != 0 {
		t.Fatalf("default resolver must drop all events, got %d enqueues", rec.len())
	}
	if tn.Metrics().Skipped != 3 {
		t.Fatalf("Skipped = %d, want 3", tn.Metrics().Skipped)
	}
}

func TestTurnNotifierResolvesAndEnqueuesTurnCompleted(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{}
	resolver := aliasFromMap(map[string]string{"agent-1": "slack.agent"})
	tn := NewTurnNotifier(slog.Default(), rec, resolver)

	tn.OnTurnCompleted(context.Background(), newCompletedEvent("agent-1", "t", "turn-1", true, "completed"))
	if rec.len() != 1 {
		t.Fatalf("want 1 enqueue, got %d", rec.len())
	}
	got := rec.reqs[0]
	if got.ChannelAlias != "slack.agent" {
		t.Fatalf("alias = %q", got.ChannelAlias)
	}
	if got.Message.Level != contract.NotifyLevelInfo {
		t.Fatalf("success completed must map to info level, got %q", got.Message.Level)
	}
	if !strings.Contains(got.Message.Title, "Turn completed") {
		t.Fatalf("title = %q", got.Message.Title)
	}
	if !strings.Contains(got.Message.Body, "agent-1") {
		t.Fatalf("body missing agent id: %q", got.Message.Body)
	}
}

func TestTurnNotifierMapsFailedToErrorLevel(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{}
	tn := NewTurnNotifier(slog.Default(), rec, aliasFromMap(map[string]string{"a": "slack.agent"}))
	tn.OnTurnCompleted(context.Background(), turndto.TurnCompleted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: "t"}, AgentID: "a"},
		},
		Success: false,
		Status:  "failed",
		Error:   "boom",
	})
	if rec.reqs[0].Message.Level != contract.NotifyLevelError {
		t.Fatalf("level = %q, want error", rec.reqs[0].Message.Level)
	}
}

func TestTurnNotifierInterruptedIsWarn(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{}
	tn := NewTurnNotifier(slog.Default(), rec, aliasFromMap(map[string]string{"a": "slack.agent"}))
	tn.OnTurnInterrupted(context.Background(), turndto.TurnInterrupted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader:  shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: "t"}, AgentID: "a"},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: "turn-1"},
		},
		Reason: "user cancelled",
	})
	if rec.len() != 1 {
		t.Fatal("interrupted event must enqueue")
	}
	req := rec.reqs[0]
	if req.Message.Level != contract.NotifyLevelWarn {
		t.Fatalf("interrupted must be warn level; got %q", req.Message.Level)
	}
	if !strings.Contains(req.Message.Body, "user cancelled") {
		t.Fatalf("interrupted body missing reason: %q", req.Message.Body)
	}
}

func TestTurnNotifierStoppedThreadEnqueues(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{}
	tn := NewTurnNotifier(slog.Default(), rec, aliasFromMap(map[string]string{"a": "slack.agent"}))
	tn.OnThreadStopped(context.Background(), threaddto.Stopped{ThreadID: "t", AgentID: "a", Reason: "process_exited"})
	if rec.len() != 1 {
		t.Fatalf("want 1 enqueue for stopped thread, got %d", rec.len())
	}
	if !strings.Contains(rec.reqs[0].Message.Title, "Agent stopped") {
		t.Fatalf("title mismatch: %q", rec.reqs[0].Message.Title)
	}
}

func TestTurnNotifierCountsEnqueueErrors(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{fail: errors.New("queue full")}
	tn := NewTurnNotifier(slog.Default(), rec, aliasFromMap(map[string]string{"a": "slack.agent"}))
	tn.OnTurnCompleted(context.Background(), newCompletedEvent("a", "t", "turn-1", true, "completed"))
	m := tn.Metrics()
	if m.EnqueueErrors != 1 || m.Enqueued != 0 {
		t.Fatalf("metrics = %+v, want EnqueueErrors=1 Enqueued=0", m)
	}
}

func TestIsNegativeStatusMapping(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"failed", "FAILED", "error", " Error "} {
		if !isNegativeStatus(s) {
			t.Errorf("isNegativeStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "completed", "interrupted", "aborted", "stalled"} {
		if isNegativeStatus(s) {
			t.Errorf("isNegativeStatus(%q) = true, want false", s)
		}
	}
}
