package runner_test

import (
	"context"
	"testing"

	"github.com/anthropic/super-agent-v3/internal/runner"
)

// TestStateMachineHappyPath verifies the basic launch → think → run → complete cycle.
func TestStateMachineHappyPath(t *testing.T) {
	proc := &runner.AgentProcess{ID: "test-1", ThreadID: "t-1"}
	proc.SetState(runner.StateIdle)
	sm := runner.NewStateMachine(proc)
	ctx := context.Background()

	steps := []struct {
		trigger string
		want    string
	}{
		{runner.TriggerLaunch, runner.StateThinking},
		{runner.TriggerCommandBegin, runner.StateRunning},
		{runner.TriggerCommandEnd, runner.StateThinking},
		{runner.TriggerTurnComplete, runner.StateIdle},
	}

	for _, step := range steps {
		if err := sm.FireCtx(ctx, step.trigger); err != nil {
			t.Fatalf("Fire(%s) failed: %v", step.trigger, err)
		}
		got := proc.State()
		if got != step.want {
			t.Fatalf("after %s: got state %q, want %q", step.trigger, got, step.want)
		}
	}
	t.Log("Happy path: idle → thinking → running → thinking → idle ✅")
}

// TestStateMachineInvalidTransition verifies that invalid transitions return an error.
func TestStateMachineInvalidTransition(t *testing.T) {
	proc := &runner.AgentProcess{ID: "test-2", ThreadID: "t-2"}
	proc.SetState(runner.StateIdle)
	sm := runner.NewStateMachine(proc)
	ctx := context.Background()

	// Cannot command_begin from Idle
	err := sm.FireCtx(ctx, runner.TriggerCommandBegin)
	if err == nil {
		t.Fatal("expected error for invalid transition Idle → command_begin")
	}
	t.Logf("correctly rejected: %v", err)
}

// TestStateMachineErrorRecovery verifies error → recover → thinking cycle.
func TestStateMachineErrorRecovery(t *testing.T) {
	proc := &runner.AgentProcess{ID: "test-3", ThreadID: "t-3"}
	proc.SetState(runner.StateThinking)
	sm := runner.NewStateMachine(proc)
	ctx := context.Background()

	if err := sm.FireCtx(ctx, runner.TriggerError); err != nil {
		t.Fatalf("Fire(error) failed: %v", err)
	}
	if proc.State() != runner.StateError {
		t.Fatalf("expected Error state, got %s", proc.State())
	}

	if err := sm.FireCtx(ctx, runner.TriggerRecover); err != nil {
		t.Fatalf("Fire(recover) failed: %v", err)
	}
	if proc.State() != runner.StateThinking {
		t.Fatalf("expected Thinking state after recovery, got %s", proc.State())
	}
	t.Log("Error recovery: thinking → error → thinking ✅")
}

// TestAllStatesAndTriggers verifies enumeration helpers.
func TestAllStatesAndTriggers(t *testing.T) {
	states := runner.AllStates()
	if len(states) != 6 {
		t.Fatalf("expected 6 states, got %d", len(states))
	}
	triggers := runner.AllTriggers()
	if len(triggers) != 12 {
		t.Fatalf("expected 12 triggers, got %d", len(triggers))
	}
	t.Logf("States: %v", states)
	t.Logf("Triggers: %v", triggers)
}
