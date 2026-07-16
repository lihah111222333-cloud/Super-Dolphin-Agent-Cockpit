package appupdaterecovery

import (
	"context"
	"fmt"
	"slices"

	"github.com/qmuntal/stateless"
)

type transitionSpec struct {
	from    State
	trigger Trigger
	to      State
}

var transactionTransitions = []transitionSpec{
	{from: StatePrepared, trigger: TriggerRetainBackup, to: StateBackupPending},
	{from: StateBackupPending, trigger: TriggerBackupRetained, to: StateBackupRetained},
	{from: StateBackupRetained, trigger: TriggerInstallCandidate, to: StateInstallPending},
	{from: StateBackupRetained, trigger: TriggerRollbackRequested, to: StateRollbackPending},
	{from: StateInstallPending, trigger: TriggerCandidateInstalled, to: StateProbation},
	{from: StateInstallPending, trigger: TriggerRollbackRequested, to: StateRollbackPending},
	{from: StateProbation, trigger: TriggerHealthy, to: StateCommitPending},
	{from: StateProbation, trigger: TriggerRollbackRequested, to: StateRollbackPending},
	{from: StateCommitPending, trigger: TriggerCommitCompleted, to: StateCommitted},
	{from: StateRollbackPending, trigger: TriggerRollbackCompleted, to: StateRolledBack},
}

func newStateMachine(initial State) *stateless.StateMachine {
	machine := stateless.NewStateMachineWithMode(initial, stateless.FiringQueued)
	for _, spec := range transactionTransitions {
		machine.Configure(spec.from).Permit(spec.trigger, spec.to)
	}
	for _, state := range allStates() {
		machine.Configure(state)
	}
	return machine
}

func nextState(current State, trigger Trigger) (State, error) {
	machine := newStateMachine(current)
	if err := machine.FireCtx(context.Background(), trigger); err != nil {
		return "", fmt.Errorf("update transaction transition %s on %s: %w", trigger, current, err)
	}
	state, err := machine.State(context.Background())
	if err != nil {
		return "", fmt.Errorf("read update transaction state: %w", err)
	}
	next, ok := state.(State)
	if !ok {
		return "", fmt.Errorf("update transaction state has unexpected type %T", state)
	}
	return next, nil
}

func allStates() []State {
	return []State{
		StatePrepared,
		StateBackupPending,
		StateBackupRetained,
		StateInstallPending,
		StateProbation,
		StateCommitPending,
		StateCommitted,
		StateRollbackPending,
		StateRolledBack,
	}
}

func allTriggers() []Trigger {
	return []Trigger{
		TriggerRetainBackup,
		TriggerBackupRetained,
		TriggerInstallCandidate,
		TriggerCandidateInstalled,
		TriggerHealthy,
		TriggerCommitCompleted,
		TriggerRollbackRequested,
		TriggerRollbackCompleted,
	}
}

func isKnownState(candidate State) bool {
	return slices.Contains(allStates(), candidate)
}

func trustStateFor(state State) TrustState {
	switch state {
	case StateCommitted:
		return TrustCommitted
	case StateRolledBack:
		return TrustRolledBack
	default:
		return TrustPending
	}
}
