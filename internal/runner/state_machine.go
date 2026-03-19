// Package runner implements the agent state machine and process lifecycle.
//
// V3 uses qmuntal/stateless for table-driven state management.
// V2 equivalent: 530 lines of switch/case in manager_event.go + 18 transition functions.
// V3: ~80 lines of declarative configuration.
package runner

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/qmuntal/stateless"
)

// Agent states
const (
	StateIdle     = "idle"
	StateThinking = "thinking"
	StateRunning  = "running"
	StatePaused   = "paused"
	StateStopped  = "stopped"
	StateError    = "error"
)

// Triggers (events that cause state transitions)
const (
	TriggerLaunch          = "launch"
	TriggerSubmit          = "submit"
	TriggerMessageDelta    = "message_delta"
	TriggerCommandBegin    = "command_begin"
	TriggerCommandEnd      = "command_end"
	TriggerTurnComplete    = "turn_complete"
	TriggerInterrupt       = "interrupt"
	TriggerStall           = "stall"
	TriggerResume          = "resume"
	TriggerStop            = "stop"
	TriggerError           = "error"
	TriggerRecover         = "recover"
)

// NewStateMachine creates a stateless FSM for an agent process.
//
// V2 equivalent code paths replaced:
//   - deriveNormalizedEventDecision()     (25 lines)
//   - errorEventStateDecision()           (8 lines)
//   - systemEventDecision()               (14 lines)
//   - threadStatusChangedDecision()       (26 lines)
//   - applyEventTypeOverrides()           (21 lines)
//   - applyTurnCompleteDecision()         (19 lines)
//   - updateEventState()                  (27 lines)
//   - effectiveState()                    (14 lines)
//   - reconcileStaleActiveState()         (18 lines)
//   Total replaced: ~172 lines of decision logic → ~60 lines of config
func NewStateMachine(proc *AgentProcess) *stateless.StateMachine {
	sm := stateless.NewStateMachineWithExternalStorage(
		func(_ context.Context) (stateless.State, error) {
			return proc.state, nil
		},
		func(_ context.Context, state stateless.State) error {
			proc.state = state.(string)
			if proc.logger != nil {
				proc.logger.Debug("state transition",
					slog.String("agent", proc.ID),
					slog.String("new_state", proc.state),
				)
			}
			return nil
		},
		stateless.FiringQueued,
	)

	// ── Idle: waiting for work ──
	sm.Configure(StateIdle).
		Permit(TriggerLaunch, StateThinking).
		Permit(TriggerSubmit, StateThinking).
		Permit(TriggerRecover, StateThinking)

	// ── Thinking: LLM is generating ──
	sm.Configure(StateThinking).
		OnEntry(func(_ context.Context, args ...any) error {
			// Side effect: emit turn started event
			return nil
		}).
		PermitReentry(TriggerMessageDelta). // self-loop: stay in Thinking
		Permit(TriggerCommandBegin, StateRunning).
		Permit(TriggerTurnComplete, StateIdle).
		Permit(TriggerInterrupt, StateStopped).
		Permit(TriggerStall, StatePaused).
		Permit(TriggerError, StateError).
		Permit(TriggerStop, StateStopped)

	// ── Running: executing a tool/command ──
	sm.Configure(StateRunning).
		Permit(TriggerCommandEnd, StateThinking).
		Permit(TriggerTurnComplete, StateIdle).
		Permit(TriggerInterrupt, StateStopped).
		Permit(TriggerError, StateError).
		Permit(TriggerStop, StateStopped)

	// ── Paused: stall detected ──
	sm.Configure(StatePaused).
		Permit(TriggerResume, StateThinking).
		Permit(TriggerInterrupt, StateStopped).
		Permit(TriggerStop, StateStopped).
		Permit(TriggerError, StateError)

	// ── Stopped: intentionally terminated ──
	sm.Configure(StateStopped).
		Permit(TriggerRecover, StateThinking).
		Permit(TriggerLaunch, StateThinking).
		OnEntry(func(_ context.Context, args ...any) error {
			// Side effect: cleanup resources
			return nil
		})

	// ── Error: unrecoverable failure ──
	sm.Configure(StateError).
		Permit(TriggerRecover, StateThinking).
		Permit(TriggerLaunch, StateThinking).
		Permit(TriggerStop, StateStopped)

	return sm
}

// AllStates returns all valid states for test enumeration.
func AllStates() []string {
	return []string{StateIdle, StateThinking, StateRunning, StatePaused, StateStopped, StateError}
}

// AllTriggers returns all valid triggers for test enumeration.
func AllTriggers() []string {
	return []string{
		TriggerLaunch, TriggerSubmit, TriggerMessageDelta, TriggerCommandBegin,
		TriggerCommandEnd, TriggerTurnComplete, TriggerInterrupt, TriggerStall,
		TriggerResume, TriggerStop, TriggerError, TriggerRecover,
	}
}

// StateLabel returns a human-readable label for a state.
func StateLabel(state string) string {
	labels := map[string]string{
		StateIdle:     "Idle",
		StateThinking: "Thinking",
		StateRunning:  "Running",
		StatePaused:   "Paused (stall)",
		StateStopped:  "Stopped",
		StateError:    "Error",
	}
	if l, ok := labels[state]; ok {
		return l
	}
	return fmt.Sprintf("Unknown(%s)", state)
}
