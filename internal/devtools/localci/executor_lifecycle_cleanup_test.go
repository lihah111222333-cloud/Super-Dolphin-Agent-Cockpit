package localci

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRunFreshContainerCancelledShardExitedHookTimeoutIsFailClosed(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request = canonicalShardRequest(t, request)
	runner.lifecycleCleanupTimeout = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	request.LifecycleHook = func(hookCtx context.Context, event FreshContainerLifecycleEvent) error {
		if event.Phase == FreshContainerPhaseStarted {
			cancel()
		}
		if event.Phase == FreshContainerPhaseExited {
			<-hookCtx.Done()
			return hookCtx.Err()
		}
		return nil
	}
	stub.request = request
	stub.waitForCancel = true
	defer cancel()

	result, err := runner.RunFreshContainer(ctx, request)
	assertCancelledLifecycleFailure(t, result, err, context.DeadlineExceeded)
}

func TestRunFreshContainerCancelledShardRemovedHookFailureIsFailClosed(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request = canonicalShardRequest(t, request)
	ctx, cancel := context.WithCancel(context.Background())
	hookFailure := errors.New("persist removed lifecycle failed")
	request.LifecycleHook = func(_ context.Context, event FreshContainerLifecycleEvent) error {
		if event.Phase == FreshContainerPhaseStarted {
			cancel()
		}
		if event.Phase == FreshContainerPhaseRemoved {
			return hookFailure
		}
		return nil
	}
	stub.request = request
	stub.waitForCancel = true
	defer cancel()

	result, err := runner.RunFreshContainer(ctx, request)
	assertCancelledLifecycleFailure(t, result, err, hookFailure)
}

func TestRunFreshContainerRetriesCancelledShardTerminalLifecycle(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request = canonicalShardRequest(t, request)
	ctx, cancel := context.WithCancel(context.Background())
	attempts := map[FreshContainerLifecyclePhase]int{}
	request.LifecycleHook = retryingTerminalLifecycleHook(cancel, attempts)
	stub.request = request
	stub.waitForCancel = true
	defer cancel()

	result, err := runner.RunFreshContainer(ctx, request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunFreshContainer() error = %v, want cancellation", err)
	}
	if !result.Container.Removed || result.RemovalProofDigest == "" {
		t.Fatalf("cancelled shard removal evidence = %#v", result)
	}
	assertRetriedTerminalLifecycle(t, attempts)
}

func retryingTerminalLifecycleHook(
	cancel context.CancelFunc,
	attempts map[FreshContainerLifecyclePhase]int,
) FreshContainerLifecycleHook {
	return func(_ context.Context, event FreshContainerLifecycleEvent) error {
		if event.Phase == FreshContainerPhaseStarted {
			cancel()
		}
		if event.Phase == FreshContainerPhaseExited || event.Phase == FreshContainerPhaseRemovalPending || event.Phase == FreshContainerPhaseRemoved {
			attempts[event.Phase]++
			if attempts[event.Phase] == 1 {
				return errors.New("transient lifecycle persistence failure")
			}
		}
		return nil
	}
}

func assertRetriedTerminalLifecycle(t *testing.T, attempts map[FreshContainerLifecyclePhase]int) {
	t.Helper()
	if attempts[FreshContainerPhaseExited] != 2 ||
		attempts[FreshContainerPhaseRemovalPending] != 2 ||
		attempts[FreshContainerPhaseRemoved] != 2 {
		t.Fatalf("terminal lifecycle attempts = %#v", attempts)
	}
}

func TestRunFreshContainerRemovalPendingHookFailurePreventsDockerRemove(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	hookFailure := errors.New("persist removal intent failed")
	request.LifecycleHook = func(_ context.Context, event FreshContainerLifecycleEvent) error {
		if event.Phase == FreshContainerPhaseRemovalPending {
			return hookFailure
		}
		return nil
	}
	stub.request = request

	result, err := runner.RunFreshContainer(context.Background(), request)
	if !errors.Is(err, hookFailure) || result.Status != gate.ResultStatusInfraFailed || result.Container.Removed {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	for _, call := range stub.calls {
		if len(call) > 0 && call[0] == "rm" {
			t.Fatalf("docker remove ran without durable removal intent: %#v", stub.calls)
		}
	}
}

func TestFreshContainerLifecycleRejectsInvalidExitedAt(t *testing.T) {
	startedAt := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
	deadline := startedAt.Add(time.Minute)
	exitedAt := startedAt.Add(time.Second)
	base := FreshContainerResult{
		Status: gate.ResultStatusPassed, StartedAt: startedAt, Deadline: deadline,
		ExitedAt: exitedAt, CompletedAt: exitedAt.Add(time.Second),
	}
	tests := []struct {
		name   string
		phase  FreshContainerLifecyclePhase
		mutate func(*FreshContainerResult)
	}{
		{name: "exited missing", phase: FreshContainerPhaseExited, mutate: func(result *FreshContainerResult) { result.ExitedAt = time.Time{} }},
		{name: "removed missing", phase: FreshContainerPhaseRemoved, mutate: func(result *FreshContainerResult) { result.ExitedAt = time.Time{} }},
		{name: "removed timeout missing", phase: FreshContainerPhaseRemoved, mutate: func(result *FreshContainerResult) {
			result.Status = gate.ResultStatusTimeout
			result.ExitedAt = time.Time{}
		}},
		{name: "removed cancelled missing", phase: FreshContainerPhaseRemoved, mutate: func(result *FreshContainerResult) {
			result.Status = gate.ResultStatusCancelled
			result.ExitedAt = time.Time{}
		}},
		{name: "removed observed process missing", phase: FreshContainerPhaseRemoved, mutate: func(result *FreshContainerResult) {
			result.Status = gate.ResultStatusInfraFailed
			result.ExitCode = 137
			result.ExitedAt = time.Time{}
		}},
		{name: "exited completion before exit", phase: FreshContainerPhaseExited, mutate: func(result *FreshContainerResult) { result.CompletedAt = result.ExitedAt.Add(-time.Nanosecond) }},
		{name: "removed completion before exit", phase: FreshContainerPhaseRemoved, mutate: func(result *FreshContainerResult) { result.CompletedAt = result.ExitedAt.Add(-time.Nanosecond) }},
		{name: "timeout before deadline", phase: FreshContainerPhaseExited, mutate: func(result *FreshContainerResult) {
			result.Status = gate.ResultStatusTimeout
			result.ExitedAt = result.Deadline.Add(-time.Nanosecond)
		}},
		{name: "removed timeout before deadline", phase: FreshContainerPhaseRemoved, mutate: func(result *FreshContainerResult) {
			result.Status = gate.ResultStatusTimeout
			result.ExitedAt = result.Deadline.Add(-time.Nanosecond)
		}},
		{name: "prepared carries exit", phase: FreshContainerPhasePrepared, mutate: func(*FreshContainerResult) {}},
		{name: "creating carries exit", phase: FreshContainerPhaseCreating, mutate: func(*FreshContainerResult) {}},
		{name: "created carries exit", phase: FreshContainerPhaseCreated, mutate: func(*FreshContainerResult) {}},
		{name: "starting carries exit", phase: FreshContainerPhaseStarting, mutate: func(*FreshContainerResult) {}},
		{name: "started carries exit", phase: FreshContainerPhaseStarted, mutate: func(*FreshContainerResult) {}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := base
			test.mutate(&result)
			hookCalled := false
			request := FreshContainerRequest{LifecycleHook: func(context.Context, FreshContainerLifecycleEvent) error {
				hookCalled = true
				return nil
			}}
			if err := (&FreshContainerRunner{}).emitLifecycle(context.Background(), request, result, test.phase); err == nil {
				t.Fatal("invalid lifecycle exited_at was accepted")
			}
			if hookCalled {
				t.Fatal("invalid lifecycle reached persistence hook")
			}
		})
	}
}

func TestRecoverFreshContainerPreservesExitedAtAcrossTerminalLifecycle(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request.ContainerLabels = map[string]string{"test.recovery": "verified"}
	stub.request = request
	stub.waitCalls = 1
	startedAt := time.Date(2026, time.July, 16, 0, 0, 0, 0, time.UTC)
	exitedAt := startedAt.Add(time.Second)
	runner.now = func() time.Time { return exitedAt.Add(time.Second) }
	command, err := freshContainerCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	observed := make([]terminalLifecycleObservation, 0, 2)
	recovery := FreshContainerRecoveryRequest{
		ContainerID: testContainerID, ContainerLabels: request.ContainerLabels,
		ImageReference: request.Image.Registry + "@" + request.Image.PlatformManifestDigest,
		ConfigDigest:   request.Image.ConfigDigest, SourceSnapshotDir: request.SourceSnapshotDir,
		Command: command, Profile: request.Profile, GateID: request.GateID,
		StartedAt: startedAt, Deadline: startedAt.Add(executionTimeout(false)),
		LifecycleHook: observeTerminalLifecycle(&observed),
	}
	result, err := runner.RecoverFreshContainer(context.Background(), recovery)
	if err != nil {
		t.Fatalf("RecoverFreshContainer() error = %v", err)
	}
	if !result.ExitedAt.Equal(exitedAt) {
		t.Fatalf("recovered exited_at = %s, want %s", result.ExitedAt, exitedAt)
	}
	assertTerminalLifecycle(t, observed, result)
}

func TestCleanupUnprovedFreshContainerRemovedDoesNotFabricateExitedAt(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request.ContainerLabels = map[string]string{"test.cleanup": "unproved"}
	stub.request = request
	command, err := freshContainerCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	events := make([]FreshContainerLifecycleEvent, 0, 1)
	cleanup := FreshContainerCleanupRequest{
		ContainerID: testContainerID, ContainerLabels: request.ContainerLabels,
		ImageReference: request.Image.Registry + "@" + request.Image.PlatformManifestDigest,
		ConfigDigest:   request.Image.ConfigDigest, SourceSnapshotDir: request.SourceSnapshotDir,
		Command: command, Profile: request.Profile, GateID: request.GateID,
		LifecycleHook: func(_ context.Context, event FreshContainerLifecycleEvent) error {
			events = append(events, event)
			return nil
		},
	}
	result, err := runner.CleanupUnprovedFreshContainer(context.Background(), cleanup)
	if err != nil {
		t.Fatalf("CleanupUnprovedFreshContainer() error = %v", err)
	}
	if !result.Killed || !result.Container.Removed || result.Container.ContainerID != testContainerID ||
		result.Container.ResourceWitnessDigest == "" || !result.ExitedAt.IsZero() {
		t.Fatalf("unproved cleanup result = %#v", result)
	}
	assertPendingRemovedLifecycle(t, events)
}

func TestCleanupUnprovedFreshContainerProvesAbsentIdentity(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request.ContainerLabels = map[string]string{"test.cleanup": "absent"}
	stub.request = request
	command, err := freshContainerCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	events := make([]FreshContainerLifecycleEvent, 0, 1)
	cleanup := FreshContainerCleanupRequest{
		ContainerLabels: request.ContainerLabels,
		ImageReference:  request.Image.Registry + "@" + request.Image.PlatformManifestDigest,
		ConfigDigest:    request.Image.ConfigDigest, SourceSnapshotDir: request.SourceSnapshotDir,
		Command: command, Profile: request.Profile, GateID: request.GateID,
		LifecycleHook: func(_ context.Context, event FreshContainerLifecycleEvent) error {
			events = append(events, event)
			return nil
		},
	}
	result, err := runner.CleanupUnprovedFreshContainer(context.Background(), cleanup)
	if err != nil {
		t.Fatalf("CleanupUnprovedFreshContainer() error = %v", err)
	}
	assertCleanupAbsenceResult(t, result)
	assertCleanupAbsenceLifecycle(t, events)
	assertCleanupAbsenceDidNotMutateContainer(t, stub.calls)
}

func TestCreatingLifecyclePersistsOperationIdentityAcrossRestart(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request.ContainerLabels = map[string]string{"test.cleanup": "restart"}
	stub.request = request
	operationIdentity := mustFreshContainerOperationIdentity(t, request.ContainerLabels)
	var creating FreshContainerLifecycleEvent
	request.LifecycleHook = func(_ context.Context, event FreshContainerLifecycleEvent) error {
		creating = event
		return nil
	}
	if err := runner.emitLifecycle(context.Background(), request, FreshContainerResult{
		Status: gate.ResultStatusInfraFailed, ImageReference: request.Image.Registry + "@" + request.Image.PlatformManifestDigest, ExitCode: -1,
	}, FreshContainerPhaseCreating); err != nil {
		t.Fatalf("emit creating lifecycle: %v", err)
	}
	if creating.ContainerID != operationIdentity || !IsFreshContainerOperationIdentity(creating.ContainerID) {
		t.Fatalf("persisted creating lifecycle = %#v", creating)
	}
	cleanup := creatingCleanupRequest(t, request, creating.ContainerID)
	stub.psOutputs = []string{"", testContainerID + "\n"}
	result, err := runner.CleanupUnprovedFreshContainer(context.Background(), cleanup)
	if err != nil {
		t.Fatalf("CleanupUnprovedFreshContainer() error = %v", err)
	}
	if result.Container.ContainerID != testContainerID || !result.Killed || !result.Container.Removed || stub.psCalls != 3 {
		t.Fatalf("late create was not recovered exactly: result=%#v calls=%#v", result, stub.calls)
	}
}

func TestCreatingCleanupSingleAbsenceDoesNotProveRemoved(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request.ContainerLabels = map[string]string{"test.cleanup": "single-absence"}
	stub.request = request
	cleanup := creatingCleanupRequest(t, request, mustFreshContainerOperationIdentity(t, request.ContainerLabels))
	stub.psOutputs = []string{"", testContainerID + "\n"}
	result, err := runner.CleanupUnprovedFreshContainer(context.Background(), cleanup)
	if err != nil {
		t.Fatalf("CleanupUnprovedFreshContainer() error = %v", err)
	}
	if result.Container.ContainerID != testContainerID || !result.Killed || !result.Container.Removed || stub.psCalls != 3 {
		t.Fatalf("single absence finalized creating cleanup: result=%#v calls=%#v", result, stub.calls)
	}
}

func TestCreatingCleanupRequiresStableAbsenceBeforeRemoved(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request.ContainerLabels = map[string]string{"test.cleanup": "stable-absence"}
	stub.request = request
	operationIdentity := mustFreshContainerOperationIdentity(t, request.ContainerLabels)
	cleanup := creatingCleanupRequest(t, request, operationIdentity)
	stub.psOutputs = []string{"", "", ""}
	events := make([]FreshContainerLifecycleEvent, 0, 1)
	cleanup.LifecycleHook = func(_ context.Context, event FreshContainerLifecycleEvent) error {
		events = append(events, event)
		return nil
	}
	result, err := runner.CleanupUnprovedFreshContainer(context.Background(), cleanup)
	if err != nil {
		t.Fatalf("CleanupUnprovedFreshContainer() error = %v", err)
	}
	if result.Container.ContainerID != "" || !result.Container.Removed || stub.psCalls != creatingAbsenceProofs {
		t.Fatalf("creating absence proof = result=%#v calls=%#v", result, stub.calls)
	}
	if len(events) != 1 || events[0].Phase != FreshContainerPhaseRemoved || events[0].ContainerID != operationIdentity {
		t.Fatalf("stable absence lifecycle = %#v", events)
	}
	assertCleanupAbsenceDidNotMutateContainer(t, stub.calls)
}

func TestCreatingCleanupRejectsOperationIdentityLabelDrift(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request.ContainerLabels = map[string]string{"test.cleanup": "expected"}
	stub.request = request
	cleanup := creatingCleanupRequest(t, request, mustFreshContainerOperationIdentity(t, map[string]string{"test.cleanup": "drifted"}))
	if _, err := runner.CleanupUnprovedFreshContainer(context.Background(), cleanup); err == nil {
		t.Fatal("creating cleanup accepted an operation identity derived from different labels")
	}
	if len(stub.calls) != 0 {
		t.Fatalf("drifted creating identity reached Docker: %#v", stub.calls)
	}
}

func TestCreatingCleanupFindsContainerAppearingDuringFinalAbsenceProbe(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request.ContainerLabels = map[string]string{"test.cleanup": "delayed-third-probe"}
	stub.request = request
	cleanup := creatingCleanupRequest(t, request, mustFreshContainerOperationIdentity(t, request.ContainerLabels))
	stub.psOutputs = []string{"", "", testContainerID + "\n"}
	result, err := runner.CleanupUnprovedFreshContainer(context.Background(), cleanup)
	if err != nil {
		t.Fatalf("CleanupUnprovedFreshContainer() error = %v", err)
	}
	if result.Container.ContainerID != testContainerID || !result.Killed || !result.Container.Removed || stub.psCalls != creatingAbsenceProofs+1 {
		t.Fatalf("third-probe delayed container was not removed: result=%#v calls=%#v", result, stub.calls)
	}
}

func creatingCleanupRequest(t *testing.T, request FreshContainerRequest, operationIdentity string) FreshContainerCleanupRequest {
	t.Helper()
	command, err := freshContainerCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	return FreshContainerCleanupRequest{
		ContainerID: operationIdentity, ContainerLabels: request.ContainerLabels,
		ImageReference: request.Image.Registry + "@" + request.Image.PlatformManifestDigest,
		ConfigDigest:   request.Image.ConfigDigest, SourceSnapshotDir: request.SourceSnapshotDir,
		Command: command, Profile: request.Profile, GateID: request.GateID,
	}
}

func mustFreshContainerOperationIdentity(t *testing.T, labels map[string]string) string {
	t.Helper()
	identity, err := FreshContainerOperationIdentity(labels)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func assertCleanupAbsenceResult(t *testing.T, result FreshContainerResult) {
	t.Helper()
	if !result.Container.Removed || result.RemovalProofDigest == "" || !result.ExitedAt.IsZero() {
		t.Fatalf("absence cleanup result = %#v", result)
	}
}

func assertCleanupAbsenceLifecycle(t *testing.T, events []FreshContainerLifecycleEvent) {
	t.Helper()
	if len(events) != 1 || events[0].Phase != FreshContainerPhaseRemoved || events[0].ContainerID != "" {
		t.Fatalf("absence cleanup lifecycle = %#v", events)
	}
}

func assertCleanupAbsenceDidNotMutateContainer(t *testing.T, calls [][]string) {
	t.Helper()
	for _, call := range calls {
		if len(call) > 0 && (call[0] == "kill" || call[0] == "rm" || call[0] == "inspect") {
			t.Fatalf("absent container was mutated or inspected: %#v", calls)
		}
	}
}

func assertPendingRemovedLifecycle(t *testing.T, events []FreshContainerLifecycleEvent) {
	t.Helper()
	if len(events) != 2 || events[0].Phase != FreshContainerPhaseRemovalPending ||
		events[1].Phase != FreshContainerPhaseRemoved || !events[1].ExitedAt.IsZero() {
		t.Fatalf("unproved cleanup lifecycle = %#v", events)
	}
}

func TestCleanupUnprovedFreshContainerReplaysPendingRemovalAfterCrash(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request.ContainerLabels = map[string]string{"test.cleanup": "pending-removal"}
	stub.request = request
	command, err := freshContainerCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	events := make([]FreshContainerLifecycleEvent, 0, 1)
	cleanup := FreshContainerCleanupRequest{
		ContainerID: testContainerID, ContainerLabels: request.ContainerLabels,
		ImageReference: request.Image.Registry + "@" + request.Image.PlatformManifestDigest,
		ConfigDigest:   request.Image.ConfigDigest, SourceSnapshotDir: request.SourceSnapshotDir,
		Command: command, Profile: request.Profile, GateID: request.GateID, RemovalPending: true,
		LifecycleHook: func(_ context.Context, event FreshContainerLifecycleEvent) error {
			events = append(events, event)
			return nil
		},
	}
	result, err := runner.CleanupUnprovedFreshContainer(context.Background(), cleanup)
	if err != nil {
		t.Fatalf("CleanupUnprovedFreshContainer() error = %v", err)
	}
	assertPendingRemovalReplay(t, result, events, stub.calls)
}

func assertPendingRemovalReplay(
	t *testing.T,
	result FreshContainerResult,
	events []FreshContainerLifecycleEvent,
	calls [][]string,
) {
	t.Helper()
	if !result.Container.Removed || result.RemovalProofDigest == "" || len(events) != 1 || events[0].Phase != FreshContainerPhaseRemoved {
		t.Fatalf("pending removal replay result=%#v events=%#v", result, events)
	}
	for _, call := range calls {
		if len(call) > 0 && (call[0] == "kill" || call[0] == "rm") {
			t.Fatalf("already-removed container was acted on again: %#v", calls)
		}
	}
}

func TestCleanupUnprovedFreshContainerPendingRemovalRejectsIdentityDriftWithoutDockerMutation(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request.ContainerLabels = map[string]string{"test.cleanup": "original"}
	stub.request = request
	stub.containerID = testContainerID
	stub.psOutput = testContainerID + "\n"
	command, err := freshContainerCommand(request)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := FreshContainerCleanupRequest{
		ContainerID: testContainerID, ContainerLabels: map[string]string{"test.cleanup": "drifted"},
		ImageReference: request.Image.Registry + "@" + request.Image.PlatformManifestDigest,
		ConfigDigest:   request.Image.ConfigDigest, SourceSnapshotDir: request.SourceSnapshotDir,
		Command: command, Profile: request.Profile, GateID: request.GateID, RemovalPending: true,
	}
	if _, err := runner.CleanupUnprovedFreshContainer(context.Background(), cleanup); err == nil {
		t.Fatal("pending removal recovery accepted a container with drifted identity")
	}
	for _, call := range stub.calls {
		if len(call) > 0 && (call[0] == "kill" || call[0] == "rm") {
			t.Fatalf("identity-drifted container was mutated: %#v", stub.calls)
		}
	}
}

type terminalLifecycleObservation struct {
	event    FreshContainerLifecycleEvent
	deadline time.Time
}

func assertCleanupWithoutObservedExit(t *testing.T, events []FreshContainerLifecycleEvent, before FreshContainerLifecyclePhase) {
	t.Helper()
	beforeIndex, removedIndex := -1, -1
	for index, event := range events {
		switch event.Phase {
		case before:
			beforeIndex = index
		case FreshContainerPhaseExited:
			t.Fatalf("cleanup emitted exited lifecycle without trusted inspect: %#v", events)
		case FreshContainerPhaseRemoved:
			removedIndex = index
			if !event.ExitedAt.IsZero() {
				t.Fatalf("cleanup fabricated removed exited_at %s", event.ExitedAt)
			}
		}
	}
	if beforeIndex < 0 || removedIndex <= beforeIndex {
		t.Fatalf("cleanup lifecycle order = %#v", events)
	}
}

func observeCancelledTerminalLifecycle(cancel context.CancelFunc, observed *[]terminalLifecycleObservation) FreshContainerLifecycleHook {
	observeTerminal := observeTerminalLifecycle(observed)
	return func(ctx context.Context, event FreshContainerLifecycleEvent) error {
		if event.Phase == FreshContainerPhaseStarted {
			if !event.ExitedAt.IsZero() {
				return errors.New("started lifecycle carried exited_at")
			}
			cancel()
			return nil
		}
		return observeTerminal(ctx, event)
	}
}

func observeTerminalLifecycle(observed *[]terminalLifecycleObservation) FreshContainerLifecycleHook {
	return func(ctx context.Context, event FreshContainerLifecycleEvent) error {
		if event.Phase != FreshContainerPhaseExited && event.Phase != FreshContainerPhaseRemoved {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("terminal lifecycle inherited cancellation: %w", err)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > freshContainerLifecycleCleanupTimeout {
			return errors.New("terminal lifecycle cleanup deadline is invalid")
		}
		*observed = append(*observed, terminalLifecycleObservation{event: event, deadline: deadline})
		return nil
	}
}

func assertTerminalLifecycle(t *testing.T, observed []terminalLifecycleObservation, result FreshContainerResult) {
	t.Helper()
	assertTerminalLifecycleOrder(t, observed)
	assertTerminalLifecycleEvidence(t, observed, result)
}

func assertTerminalLifecycleOrder(t *testing.T, observed []terminalLifecycleObservation) {
	t.Helper()
	if len(observed) != 2 || observed[0].event.Phase != FreshContainerPhaseExited || observed[1].event.Phase != FreshContainerPhaseRemoved {
		t.Fatalf("terminal lifecycle order = %#v", observed)
	}
	if !observed[1].deadline.After(observed[0].deadline) {
		t.Fatalf("terminal lifecycle cleanup deadlines were reused: %#v", observed)
	}
}

func assertTerminalLifecycleEvidence(t *testing.T, observed []terminalLifecycleObservation, result FreshContainerResult) {
	t.Helper()
	exited, removed := observed[0].event, observed[1].event
	if !exited.ExitedAt.Equal(result.ExitedAt) || exited.CompletedAt.Before(exited.ExitedAt) ||
		exited.ExitCode != result.ExitCode || exited.RemovalProofDigest != "" {
		t.Fatalf("exited lifecycle evidence = %#v", exited)
	}
	if removed.ExitedAt != exited.ExitedAt || removed.CompletedAt != exited.CompletedAt ||
		removed.ExitCode != exited.ExitCode || removed.RemovalProofDigest != result.RemovalProofDigest {
		t.Fatalf("removed lifecycle evidence = %#v, exited = %#v", removed, exited)
	}
}

func assertCancelledLifecycleFailure(t *testing.T, result FreshContainerResult, runErr error, want error) {
	t.Helper()
	if !errors.Is(runErr, want) || result.Status != gate.ResultStatusInfraFailed || !result.Killed || !result.Container.Removed {
		t.Fatalf("cancelled lifecycle failure result = %#v, err = %v", result, runErr)
	}
	if len(result.PlanGateResults) != 0 || result.GateResult != nil {
		t.Fatalf("cancelled lifecycle failure accepted gate results: %#v", result.PlanGateResults)
	}
}
