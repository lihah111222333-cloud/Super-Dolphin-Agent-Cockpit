package turn

import (
	"github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
)

// TurnState is a named string type for turn lifecycle states.
type TurnState string

// TurnTrigger is a named string type for turn lifecycle triggers.
type TurnTrigger string

const (
	StatePreparing       TurnState = "preparing"
	StateRunning         TurnState = "running"
	StateForceCompleting TurnState = "force_completing"
	StateInterrupting    TurnState = "interrupting"
	StateInterrupted     TurnState = "interrupted"
	StateCompleted       TurnState = "completed"
	StateFailed          TurnState = "failed"
	StateStalled         TurnState = "stalled"
)

const (
	TriggerStart     TurnTrigger = "start"
	TriggerRun       TurnTrigger = "run"
	TriggerForce     TurnTrigger = "force"
	TriggerInterrupt TurnTrigger = "interrupt"
	TriggerAbort     TurnTrigger = "abort"
	TriggerComplete  TurnTrigger = "complete"
	TriggerFail      TurnTrigger = "fail"
	TriggerStall     TurnTrigger = "stall"
)

// permit is a shorthand that converts named trigger/state types to the
// plain-string Permit expected by the statemachine package.
func permit(trigger TurnTrigger, dest TurnState) statemachine.Permit {
	return statemachine.Permit{Trigger: string(trigger), Dest: string(dest)}
}

// newTurnStateMachineConfig 创建turn状态machine配置。
func newTurnStateMachineConfig() statemachine.Config {
	return statemachine.Config{
		Initial: string(StatePreparing),
		States: []statemachine.StateConfig{
			{
				Name: string(StatePreparing),
				Permits: []statemachine.Permit{
					permit(TriggerRun, StateRunning),
					permit(TriggerComplete, StateCompleted),
					permit(TriggerInterrupt, StateInterrupting),
					permit(TriggerAbort, StateInterrupted),
					permit(TriggerFail, StateFailed),
					permit(TriggerStall, StateStalled),
				},
			},
			{
				Name: string(StateRunning),
				Permits: []statemachine.Permit{
					permit(TriggerForce, StateForceCompleting),
					permit(TriggerInterrupt, StateInterrupting),
					permit(TriggerAbort, StateInterrupted),
					permit(TriggerComplete, StateCompleted),
					permit(TriggerFail, StateFailed),
					permit(TriggerStall, StateStalled),
				},
			},
			{
				Name: string(StateForceCompleting),
				Permits: []statemachine.Permit{
					permit(TriggerComplete, StateCompleted),
					permit(TriggerFail, StateFailed),
					permit(TriggerStall, StateStalled),
					permit(TriggerAbort, StateInterrupted),
				},
			},
			{
				Name: string(StateInterrupting),
				Permits: []statemachine.Permit{
					permit(TriggerAbort, StateInterrupted),
					permit(TriggerFail, StateInterrupted),     // fail after interrupt is treated as interrupted
					permit(TriggerComplete, StateInterrupted), // complete after interrupt is interrupted
					permit(TriggerStall, StateStalled),
				},
			},
			// Terminal states have no outward permits
			{Name: string(StateInterrupted)},
			{Name: string(StateCompleted)},
			{Name: string(StateFailed)},
			{Name: string(StateStalled)},
		},
	}
}
