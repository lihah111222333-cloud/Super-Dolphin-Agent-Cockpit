package turn

import (
	"github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
)

const (
	StatePreparing       = "preparing"
	StateRunning         = "running"
	StateForceCompleting = "force_completing"
	StateInterrupting    = "interrupting"
	StateInterrupted     = "interrupted"
	StateCompleted       = "completed"
	StateFailed          = "failed"
	StateStalled         = "stalled"
)

const (
	TriggerStart       = "start"
	TriggerRun         = "run"
	TriggerForce       = "force"
	TriggerInterrupt   = "interrupt"
	TriggerAbort       = "abort"
	TriggerComplete    = "complete"
	TriggerFail        = "fail"
	TriggerStall       = "stall"
)

func newTurnStateMachineConfig() statemachine.Config {
	return statemachine.Config{
		Initial: StatePreparing,
		States: []statemachine.StateConfig{
			{
				Name: StatePreparing,
				Permits: []statemachine.Permit{
					{Trigger: TriggerRun, Dest: StateRunning},
					{Trigger: TriggerComplete, Dest: StateCompleted},
					{Trigger: TriggerInterrupt, Dest: StateInterrupting},
					{Trigger: TriggerAbort, Dest: StateInterrupted},
					{Trigger: TriggerFail, Dest: StateFailed},
					{Trigger: TriggerStall, Dest: StateStalled},
				},
			},
			{
				Name: StateRunning,
				Permits: []statemachine.Permit{
					{Trigger: TriggerForce, Dest: StateForceCompleting},
					{Trigger: TriggerInterrupt, Dest: StateInterrupting},
					{Trigger: TriggerAbort, Dest: StateInterrupted},
					{Trigger: TriggerComplete, Dest: StateCompleted},
					{Trigger: TriggerFail, Dest: StateFailed},
					{Trigger: TriggerStall, Dest: StateStalled},
				},
			},
			{
				Name: StateForceCompleting,
				Permits: []statemachine.Permit{
					{Trigger: TriggerComplete, Dest: StateCompleted},
					{Trigger: TriggerFail, Dest: StateFailed},
					{Trigger: TriggerStall, Dest: StateStalled},
					{Trigger: TriggerAbort, Dest: StateInterrupted},
				},
			},
			{
				Name: StateInterrupting,
				Permits: []statemachine.Permit{
					{Trigger: TriggerAbort, Dest: StateInterrupted},
					{Trigger: TriggerFail, Dest: StateInterrupted}, // fail after interrupt is treated as interrupted
					{Trigger: TriggerComplete, Dest: StateInterrupted}, // complete after interrupt is interrupted
					{Trigger: TriggerStall, Dest: StateStalled},
				},
			},
			// Terminal states have no outward permits
			{Name: StateInterrupted},
			{Name: StateCompleted},
			{Name: StateFailed},
			{Name: StateStalled},
		},
	}
}
