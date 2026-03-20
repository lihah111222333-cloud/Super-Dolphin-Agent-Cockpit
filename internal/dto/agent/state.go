package agent

import (
	"fmt"
	"strings"
)

const (
	StateProvisioning      = "provisioning"
	StateIdle              = "idle"
	StateTurnQueued        = "turn_queued"
	StateTurnStarting      = "turn_starting"
	StateTurnRunning       = "turn_running"
	StateAwaitingUserInput = "awaiting_user_input"
	StateRecovering        = "recovering"
	StateStopping          = "stopping"
	StateStopped           = "stopped"
	StateFailed            = "failed"
)

const (
	TriggerLaunchSucceeded    = "launch_succeeded"
	TriggerLaunchFailed       = "launch_failed"
	TriggerTurnEnqueued       = "turn_enqueued"
	TriggerTurnAccepted       = "turn_accepted"
	TriggerTurnCompleted      = "turn_completed"
	TriggerTurnAborted        = "turn_aborted"
	TriggerUserInputRequested = "user_input_requested"
	TriggerUserInputResolved  = "user_input_resolved"
	TriggerRecoverRequested   = "recover_requested"
	TriggerStopRequested      = "stop_requested"
	TriggerProcessExited      = "process_exited"
)

type StateDefinition struct {
	Name        string
	Description string
}

type TriggerDefinition struct {
	Name        string
	Description string
}

type TransitionDefinition struct {
	From    string
	Trigger string
	To      string
}

var StateDefinitions = []StateDefinition{
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

var TriggerDefinitions = []TriggerDefinition{
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

var TransitionDefinitions = []TransitionDefinition{
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

func AllStates() []string {
	states := make([]string, 0, len(StateDefinitions))
	for _, definition := range StateDefinitions {
		states = append(states, definition.Name)
	}
	return states
}

func AllTriggers() []string {
	triggers := make([]string, 0, len(TriggerDefinitions))
	for _, definition := range TriggerDefinitions {
		triggers = append(triggers, definition.Name)
	}
	return triggers
}

func AllowedTriggers(state string) []string {
	triggers := make([]string, 0, 4)
	for _, transition := range TransitionDefinitions {
		if transition.From == state {
			triggers = append(triggers, transition.Trigger)
		}
	}
	return triggers
}

func StateLabel(state string) string {
	for _, definition := range StateDefinitions {
		if definition.Name != state {
			continue
		}
		parts := strings.Split(definition.Name, "_")
		for i, part := range parts {
			if part == "" {
				continue
			}
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
		return strings.Join(parts, " ")
	}
	return fmt.Sprintf("Unknown(%s)", state)
}
