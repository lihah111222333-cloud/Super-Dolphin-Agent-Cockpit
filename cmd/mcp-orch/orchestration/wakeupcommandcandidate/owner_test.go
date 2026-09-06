package wakeupcommandcandidate

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type localFakeProvider struct{ calls int }

func (p *localFakeProvider) Launch(owner *Owner, permit DispatchPermit) error {
	if err := owner.ValidatePermit(permit); err != nil {
		return err
	}
	p.calls++
	return nil
}

func testCommand() WakeupCommand {
	return WakeupCommand{CommandID: "command-1", WakeupID: "wakeup-1", WakeupFence: "fence-1",
		Intent: Intent{Key: "wakeup-key-1", Digest: strings.Repeat("a", 64)}, AttemptNumber: 1}
}

func permitFromAttempt(attempt Attempt) DispatchPermit {
	return DispatchPermit{CommandID: attempt.CommandID, WakeupID: attempt.WakeupID, WakeupFence: attempt.WakeupFence,
		AttemptNumber: attempt.AttemptNumber, DispatchReceipt: attempt.DispatchReceipt, Permit: attempt.Permit, Nonce: attempt.Nonce}
}

func outcomeFor(attempt Attempt, outcome ProviderOutcome) OutcomeRequest {
	return OutcomeRequest{CommandID: attempt.CommandID, WakeupID: attempt.WakeupID, WakeupFence: attempt.WakeupFence, AttemptNumber: attempt.AttemptNumber,
		DispatchReceipt: attempt.DispatchReceipt, Permit: attempt.Permit, Nonce: attempt.Nonce, Outcome: outcome}
}

func TestAcceptFindRequiresExactReplay(t *testing.T) {
	t.Parallel()
	owner := NewOwner()
	command := testCommand()
	got, replay, err := owner.Accept(command)
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, command, got)
	got, replay, err = owner.Accept(command)
	require.NoError(t, err)
	require.True(t, replay)
	require.Equal(t, command, got)
	_, err = owner.Find(command.Intent)
	require.NoError(t, err)
	conflict := command
	conflict.Intent.Digest = strings.Repeat("b", 64)
	_, _, err = owner.Accept(conflict)
	require.ErrorIs(t, err, ErrDigestConflict)
	_, err = owner.Find(conflict.Intent)
	require.ErrorIs(t, err, ErrDigestConflict)
	bindingConflict := command
	bindingConflict.WakeupFence = "fence-old"
	_, _, err = owner.Accept(bindingConflict)
	require.ErrorIs(t, err, ErrReplayConflict)
}

func TestProviderPermitRequiresAcceptedCommand(t *testing.T) {
	owner := NewOwner()
	command := testCommand()
	provider := &localFakeProvider{}
	_, err := owner.IssueDispatch(command.CommandID, command.WakeupFence)
	require.ErrorIs(t, err, ErrNotFound)
	err = provider.Launch(owner, DispatchPermit{CommandID: command.CommandID, WakeupID: command.WakeupID,
		WakeupFence: command.WakeupFence, AttemptNumber: 1, DispatchReceipt: "forged-receipt", Permit: "forged-permit", Nonce: "forged-nonce"})
	require.ErrorIs(t, err, ErrPermitMismatch)
	require.Zero(t, provider.calls)
}

func TestConcurrentAcceptAndIssueHaveOneCanonicalAttempt(t *testing.T) {
	owner := NewOwner()
	command := testCommand()
	const workers = 16
	accepted := make(chan struct {
		command WakeupCommand
		replay  bool
		err     error
	}, workers)
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			got, replay, err := owner.Accept(command)
			accepted <- struct {
				command WakeupCommand
				replay  bool
				err     error
			}{got, replay, err}
		})
	}
	group.Wait()
	close(accepted)
	first := 0
	for result := range accepted {
		require.NoError(t, result.err)
		require.Equal(t, command, result.command)
		if !result.replay {
			first++
		}
	}
	require.Equal(t, 1, first)
	type attemptResult struct {
		attempt Attempt
		err     error
	}
	attempts := make(chan attemptResult, workers)
	for range workers {
		group.Go(func() {
			attempt, err := owner.IssueDispatch(command.CommandID, command.WakeupFence)
			attempts <- attemptResult{attempt: attempt, err: err}
		})
	}
	group.Wait()
	close(attempts)
	winners := 0
	for result := range attempts {
		if result.err == nil {
			winners++
			require.Equal(t, 1, result.attempt.AttemptNumber)
			require.NotEmpty(t, result.attempt.DispatchReceipt)
			continue
		}
		require.ErrorIs(t, result.err, ErrAttemptUnresolved)
		require.Zero(t, result.attempt)
	}
	require.Equal(t, 1, winners)
}

func TestRecordBeforeEffectMarkAfterEffect(t *testing.T) {
	owner := NewOwner()
	command := testCommand()
	_, _, err := owner.Accept(command)
	require.NoError(t, err)
	attempt, err := owner.IssueDispatch(command.CommandID, command.WakeupFence)
	require.NoError(t, err)
	require.Equal(t, PhaseEffectPending, attempt.Phase)
	require.Equal(t, OutcomePending, attempt.Outcome)
	require.NotEmpty(t, attempt.Permit)
	require.NotEmpty(t, attempt.Nonce)
	provider := &localFakeProvider{}
	require.NoError(t, provider.Launch(owner, permitFromAttempt(attempt)))
	require.Equal(t, 1, provider.calls)
	marked, err := owner.MarkProviderOutcome(outcomeFor(attempt, OutcomeAccepted))
	require.NoError(t, err)
	require.Equal(t, PhaseProviderAccepted, marked.Phase)
	replayed, err := owner.MarkProviderOutcome(outcomeFor(attempt, OutcomeAccepted))
	require.NoError(t, err)
	require.Equal(t, marked, replayed)
	_, err = owner.IssueDispatch(command.CommandID, command.WakeupFence)
	require.ErrorIs(t, err, ErrAttemptTerminal)
}

// TestIssueDispatchReplayBeforeOutcomeIsFailClosed is the RED/GREEN lock for
// the crash window between the first provider call and its durable outcome
// mark.  The old implementation returned the same pending permit on replay,
// allowing a second provider call; the candidate now returns only an
// unresolved error and leaves the original attempt available to Reconcile.
func TestIssueDispatchReplayBeforeOutcomeIsFailClosed(t *testing.T) {
	owner := NewOwner()
	command := testCommand()
	_, _, err := owner.Accept(command)
	require.NoError(t, err)

	first, err := owner.IssueDispatch(command.CommandID, command.WakeupFence)
	require.NoError(t, err)
	provider := &localFakeProvider{}
	require.NoError(t, provider.Launch(owner, permitFromAttempt(first)))

	replay, err := owner.IssueDispatch(command.CommandID, command.WakeupFence)
	require.ErrorIs(t, err, ErrAttemptUnresolved)
	require.Zero(t, replay)
	// A caller that obeys the error cannot invoke the provider with a replayed
	// permit.  The original attempt remains the only reconciliation handle.
	reconciled, err := owner.Reconcile(command.CommandID, first.AttemptNumber)
	require.NoError(t, err)
	require.Equal(t, first, reconciled)
	require.Equal(t, 1, provider.calls)
}

func TestStaleFenceHasZeroLocalProviderEffects(t *testing.T) {
	owner := NewOwner()
	command := testCommand()
	_, _, err := owner.Accept(command)
	require.NoError(t, err)
	provider := &localFakeProvider{}
	_, err = owner.IssueDispatch(command.CommandID, "old-fence")
	require.ErrorIs(t, err, ErrStaleFence)
	require.Zero(t, provider.calls)
	attempt, err := owner.IssueDispatch(command.CommandID, command.WakeupFence)
	require.NoError(t, err)
	err = provider.Launch(owner, DispatchPermit{CommandID: attempt.CommandID, WakeupID: attempt.WakeupID,
		WakeupFence: "old-fence", AttemptNumber: 1, DispatchReceipt: attempt.DispatchReceipt, Permit: attempt.Permit, Nonce: attempt.Nonce})
	require.ErrorIs(t, err, ErrPermitMismatch)
	require.Zero(t, provider.calls)
}

func TestReclaimAndUnknownOnlyReconcileOriginalAttempt(t *testing.T) {
	owner := NewOwner()
	command := testCommand()
	_, _, err := owner.Accept(command)
	require.NoError(t, err)
	attempt, err := owner.IssueDispatch(command.CommandID, command.WakeupFence)
	require.NoError(t, err)
	provider := &localFakeProvider{}
	require.NoError(t, provider.Launch(owner, permitFromAttempt(attempt)))
	reclaimed, err := owner.Reclaim(command.CommandID, command.WakeupFence)
	require.NoError(t, err)
	require.Equal(t, PhaseOutcomeUnknown, reclaimed.Phase)
	require.Equal(t, 1, reclaimed.AttemptNumber)
	_, err = owner.IssueDispatch(command.CommandID, command.WakeupFence)
	require.ErrorIs(t, err, ErrAttemptUnresolved)
	queried, err := owner.Reconcile(command.CommandID, 1)
	require.NoError(t, err)
	require.Equal(t, reclaimed, queried)
	resolved, err := owner.ResolveUnknown(outcomeFor(reclaimed, OutcomeAccepted))
	require.NoError(t, err)
	require.Equal(t, PhaseProviderAccepted, resolved.Phase)
	require.Equal(t, 1, provider.calls)
}

func TestUnknownAndDefinitiveFailureAreBoundToOriginalPermit(t *testing.T) {
	owner := NewOwner()
	command := testCommand()
	_, _, err := owner.Accept(command)
	require.NoError(t, err)
	attempt, err := owner.IssueDispatch(command.CommandID, command.WakeupFence)
	require.NoError(t, err)
	unknown, err := owner.MarkProviderOutcome(outcomeFor(attempt, OutcomeUnknown))
	require.NoError(t, err)
	require.Equal(t, PhaseOutcomeUnknown, unknown.Phase)
	_, err = owner.ResolveUnknown(outcomeFor(unknown, OutcomeUnknown))
	require.ErrorIs(t, err, ErrInvalidOutcome)
	failed, err := owner.ResolveUnknown(outcomeFor(unknown, OutcomeDefinitiveFailure))
	require.NoError(t, err)
	require.Equal(t, PhaseProviderFailed, failed.Phase)
	require.Equal(t, OutcomeDefinitiveFailure, failed.Outcome)
	bad := outcomeFor(attempt, OutcomeAccepted)
	bad.Permit = "forged"
	_, err = owner.ResolveUnknown(bad)
	require.ErrorIs(t, err, ErrPermitMismatch)
}
