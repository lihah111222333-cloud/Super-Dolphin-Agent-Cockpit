package turn

import (
	"github.com/anthropic-ai/super-agent-v3/internal/platform/statemachine"
)

// TurnState 是本地 turn 生命周期状态的命名字符串类型。
type TurnState string

// TurnTrigger 是本地 turn 生命周期触发器的命名字符串类型。
type TurnTrigger string

const (
	// TurnState 常量描述本地 tracker 的生命周期状态；终态不允许再向外转换。
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
	// TurnTrigger 常量描述状态机允许触发的生命周期动作。
	TriggerStart     TurnTrigger = "start"
	TriggerRun       TurnTrigger = "run"
	TriggerForce     TurnTrigger = "force"
	TriggerInterrupt TurnTrigger = "interrupt"
	TriggerAbort     TurnTrigger = "abort"
	TriggerComplete  TurnTrigger = "complete"
	TriggerFail      TurnTrigger = "fail"
	TriggerStall     TurnTrigger = "stall"
)

// permit 把命名 trigger/state 转为 statemachine 需要的普通字符串 Permit。
func permit(trigger TurnTrigger, dest TurnState) statemachine.Permit {
	return statemachine.Permit{Trigger: string(trigger), Dest: string(dest)}
}

// newTurnStateMachineConfig 定义 turn 生命周期状态机，终态不允许再向外转换。
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
					permit(TriggerFail, StateInterrupted),     // 中断后失败仍按 interrupted 收敛
					permit(TriggerComplete, StateInterrupted), // 中断后完成仍按 interrupted 收敛
					permit(TriggerStall, StateStalled),
				},
			},
			// 终态不再允许向外转换，避免迟到事件回滚已收敛状态。
			{Name: string(StateInterrupted)},
			{Name: string(StateCompleted)},
			{Name: string(StateFailed)},
			{Name: string(StateStalled)},
		},
	}
}
