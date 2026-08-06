package agent

// AgentState 是 agent 生命周期状态的 wire 枚举类型。
type AgentState string

// AgentTrigger 是驱动 agent 状态机迁移的 wire 事件枚举类型。
type AgentTrigger string

// agent 生命周期状态常量。
const (
	StateProvisioning      AgentState = "provisioning"
	StateIdle              AgentState = "idle"
	StateTurnQueued        AgentState = "turn_queued"
	StateTurnStarting      AgentState = "turn_starting"
	StateTurnRunning       AgentState = "turn_running"
	StateAwaitingUserInput AgentState = "awaiting_user_input"
	StateRecovering        AgentState = "recovering"
	StateStopping          AgentState = "stopping"
	StateStopped           AgentState = "stopped"
	StateFailed            AgentState = "failed"
)

// agent 生命周期触发器常量。
const (
	TriggerLaunchSucceeded    AgentTrigger = "launch_succeeded"
	TriggerLaunchFailed       AgentTrigger = "launch_failed"
	TriggerTurnEnqueued       AgentTrigger = "turn_enqueued"
	TriggerTurnAccepted       AgentTrigger = "turn_accepted"
	TriggerTurnCompleted      AgentTrigger = "turn_completed"
	TriggerTurnAborted        AgentTrigger = "turn_aborted"
	TriggerUserInputRequested AgentTrigger = "user_input_requested"
	TriggerUserInputResolved  AgentTrigger = "user_input_resolved"
	TriggerRecoverRequested   AgentTrigger = "recover_requested"
	TriggerStopRequested      AgentTrigger = "stop_requested"
	TriggerProcessExited      AgentTrigger = "process_exited"
)

// StateDefinition 描述一个状态及其面向诊断/展示的说明。
type StateDefinition struct {
	Name        AgentState
	Description string
}

// TriggerDefinition 描述一个触发器及其面向诊断/展示的说明。
type TriggerDefinition struct {
	Name        AgentTrigger
	Description string
}

// TransitionDefinition 定义状态机允许的一条迁移边。
type TransitionDefinition struct {
	From    AgentState
	Trigger AgentTrigger
	To      AgentState
}

// StateDefinitions 返回 agent 状态机的状态说明快照。
func StateDefinitions() []StateDefinition {
	return []StateDefinition{
		{Name: StateProvisioning, Description: "Launching agent process and wiring runtime"},
		{Name: StateIdle, Description: "Ready to accept a new turn"},
		{Name: StateTurnQueued, Description: "Queued turn is waiting to start"},
		{Name: StateTurnStarting, Description: "Turn dispatch started and awaiting provider acceptance"},
		{Name: StateTurnRunning, Description: "Turn is actively executing"},
		{Name: StateAwaitingUserInput, Description: "Turn is blocked on user approval or input"},
		{Name: StateRecovering, Description: "Recovering process and replaying runtime state"},
		{Name: StateStopping, Description: "Stop requested and waiting for process exit"},
		{Name: StateStopped, Description: "Process exited after intentional stop"},
		{Name: StateFailed, Description: "Launch or runtime failed and needs recovery"},
	}
}

// TriggerDefinitions 返回 agent 状态机的触发器说明快照。
func TriggerDefinitions() []TriggerDefinition {
	return []TriggerDefinition{
		{Name: TriggerLaunchSucceeded, Description: "Launch or re-launch completed successfully"},
		{Name: TriggerLaunchFailed, Description: "Launch or re-launch failed"},
		{Name: TriggerTurnEnqueued, Description: "A turn was appended to the local queue"},
		{Name: TriggerTurnAccepted, Description: "Queued turn was accepted by the next lifecycle stage"},
		{Name: TriggerTurnCompleted, Description: "Turn completed successfully"},
		{Name: TriggerTurnAborted, Description: "Turn aborted or ended early"},
		{Name: TriggerUserInputRequested, Description: "Runtime requested explicit user input or approval"},
		{Name: TriggerUserInputResolved, Description: "Pending user input or approval was resolved"},
		{Name: TriggerRecoverRequested, Description: "Recovery flow was requested"},
		{Name: TriggerStopRequested, Description: "Stop flow was requested"},
		{Name: TriggerProcessExited, Description: "Underlying process exited"},
	}
}

// TransitionDefinitions 返回 agent 状态机的允许迁移快照。
// 调用方应通过 AllowedTriggers 查询合法触发器，不要在 UI 或 store 中复制规则。
func TransitionDefinitions() []TransitionDefinition {
	return []TransitionDefinition{
		{From: StateProvisioning, Trigger: TriggerLaunchSucceeded, To: StateIdle},
		{From: StateProvisioning, Trigger: TriggerLaunchFailed, To: StateFailed},
		{From: StateIdle, Trigger: TriggerTurnEnqueued, To: StateTurnQueued},
		{From: StateIdle, Trigger: TriggerRecoverRequested, To: StateRecovering},
		{From: StateIdle, Trigger: TriggerStopRequested, To: StateStopping},
		{From: StateIdle, Trigger: TriggerProcessExited, To: StateFailed},
		{From: StateTurnQueued, Trigger: TriggerTurnAccepted, To: StateTurnStarting},
		{From: StateTurnQueued, Trigger: TriggerRecoverRequested, To: StateRecovering},
		{From: StateTurnQueued, Trigger: TriggerStopRequested, To: StateStopping},
		{From: StateTurnQueued, Trigger: TriggerProcessExited, To: StateFailed},
		{From: StateTurnStarting, Trigger: TriggerTurnCompleted, To: StateIdle},
		{From: StateTurnStarting, Trigger: TriggerTurnAccepted, To: StateTurnRunning},
		{From: StateTurnStarting, Trigger: TriggerRecoverRequested, To: StateRecovering},
		{From: StateTurnStarting, Trigger: TriggerStopRequested, To: StateStopping},
		{From: StateTurnStarting, Trigger: TriggerProcessExited, To: StateFailed},
		{From: StateTurnRunning, Trigger: TriggerTurnCompleted, To: StateIdle},
		{From: StateTurnRunning, Trigger: TriggerTurnAborted, To: StateIdle},
		{From: StateTurnRunning, Trigger: TriggerUserInputRequested, To: StateAwaitingUserInput},
		{From: StateTurnRunning, Trigger: TriggerRecoverRequested, To: StateRecovering},
		{From: StateTurnRunning, Trigger: TriggerProcessExited, To: StateFailed},
		{From: StateTurnRunning, Trigger: TriggerStopRequested, To: StateStopping},
		{From: StateAwaitingUserInput, Trigger: TriggerUserInputResolved, To: StateTurnRunning},
		{From: StateAwaitingUserInput, Trigger: TriggerTurnAborted, To: StateIdle},
		{From: StateAwaitingUserInput, Trigger: TriggerRecoverRequested, To: StateRecovering},
		{From: StateAwaitingUserInput, Trigger: TriggerProcessExited, To: StateFailed},
		{From: StateAwaitingUserInput, Trigger: TriggerStopRequested, To: StateStopping},
		{From: StateRecovering, Trigger: TriggerLaunchSucceeded, To: StateIdle},
		{From: StateRecovering, Trigger: TriggerLaunchFailed, To: StateFailed},
		{From: StateStopping, Trigger: TriggerProcessExited, To: StateStopped},
		{From: StateStopped, Trigger: TriggerRecoverRequested, To: StateRecovering},
		{From: StateStopped, Trigger: TriggerLaunchSucceeded, To: StateIdle},
		{From: StateFailed, Trigger: TriggerRecoverRequested, To: StateRecovering},
		{From: StateFailed, Trigger: TriggerStopRequested, To: StateStopping},
	}
}

// AllowedTriggers 返回指定状态下允许触发的迁移事件。
func AllowedTriggers(state AgentState) []AgentTrigger {
	triggers := make([]AgentTrigger, 0, 4)
	for _, transition := range TransitionDefinitions() {
		if transition.From == state {
			triggers = append(triggers, transition.Trigger)
		}
	}
	return triggers
}

// AllowedTriggersStr 接收普通字符串状态并返回字符串触发器列表。
// 该 helper 面向 statemachine 边界的诊断和错误消息，避免调用方重复做类型转换。
func AllowedTriggersStr(state string) []string {
	triggers := AllowedTriggers(AgentState(state))
	result := make([]string, len(triggers))
	for i, t := range triggers {
		result[i] = string(t)
	}
	return result
}
