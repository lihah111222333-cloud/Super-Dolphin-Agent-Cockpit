package heterociexec

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
)

type commandExecutorRunner struct {
	mu       sync.Mutex
	calls    int
	commands []Command
	outcome  RunOutcome
	err      error
	started  chan struct{}
	release  chan struct{}
}

func (runner *commandExecutorRunner) Run(ctx context.Context, command Command) (RunOutcome, error) {
	runner.mu.Lock()
	runner.calls++
	runner.commands = append(runner.commands, command)
	started := runner.started
	release := runner.release
	outcome := runner.outcome
	err := runner.err
	runner.mu.Unlock()
	if started != nil {
		close(started)
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return RunOutcome{}, ctx.Err()
		}
	}
	return outcome, err
}

type commandExecutorReceiptStore struct {
	mu            sync.Mutex
	reads         []heterociworkflow.BatchID
	readDigests   []string
	claims        []string
	writes        []heterociworkflow.BatchResult
	readResult    heterociworkflow.BatchResult
	readFound     bool
	readErr       error
	readMisses    int
	claimErr      error
	claimOverride *bool
	claimed       map[string]bool
	claimDigests  map[string]string
	writeErr      error
	persistWrite  bool
	persistResult *heterociworkflow.BatchResult
}

type blockingReceiptStore struct {
	commandExecutorReceiptStore
	readStarted chan struct{}
	releaseRead chan struct{}
	readOnce    sync.Once
}

func (store *blockingReceiptStore) Read(ctx context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string) (heterociworkflow.BatchResult, bool, error) {
	store.readOnce.Do(func() { close(store.readStarted) })
	select {
	case <-store.releaseRead:
		return store.commandExecutorReceiptStore.Read(ctx, batch, commandDigest)
	case <-ctx.Done():
		return heterociworkflow.BatchResult{}, false, ctx.Err()
	}
}

func (store *commandExecutorReceiptStore) Claim(_ context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string) (bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := string(batch.BatchID) + "\x00" + batch.MaterialDigest
	store.claims = append(store.claims, key)
	if store.claimErr != nil {
		return false, store.claimErr
	}
	if store.claimOverride != nil {
		return *store.claimOverride, nil
	}
	if store.claimed == nil {
		store.claimed = make(map[string]bool)
	}
	if store.claimDigests == nil {
		store.claimDigests = make(map[string]string)
	}
	if previous, exists := store.claimDigests[key]; exists {
		if previous != commandDigest {
			return false, ErrCommandExecutor
		}
		return false, nil
	}
	if store.claimed[key] {
		return false, nil
	}
	store.claimed[key] = true
	store.claimDigests[key] = commandDigest
	return true, nil
}

func (store *commandExecutorReceiptStore) Read(_ context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string) (heterociworkflow.BatchResult, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.reads = append(store.reads, batch.BatchID)
	store.readDigests = append(store.readDigests, commandDigest)
	if len(store.reads) <= store.readMisses {
		return heterociworkflow.BatchResult{}, false, nil
	}
	return store.readResult, store.readFound, store.readErr
}

func (store *commandExecutorReceiptStore) WriteIfAbsent(_ context.Context, result heterociworkflow.BatchResult) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.writes = append(store.writes, result)
	if store.persistWrite {
		store.readResult = result
		if store.persistResult != nil {
			store.readResult = *store.persistResult
		}
		store.readFound = true
	}
	return store.writeErr
}

func TestCommandBatchExecutorRequiresExplicitPorts(t *testing.T) {
	validFactory := CommandFactory(func(heterociworkflow.ExecutionBatch) (Command, error) {
		return Command{Platform: heteroci.PlatformLinuxAMD64, Argv: []string{"tool"}}, nil
	})
	validRunner := &commandExecutorRunner{}
	validStore := &commandExecutorReceiptStore{}
	for _, test := range []struct {
		name    string
		factory CommandFactory
		runner  Runner
		store   ReceiptStore
	}{
		{name: "factory", runner: validRunner, store: validStore},
		{name: "runner", factory: validFactory, store: validStore},
		{name: "receipt store", factory: validFactory, runner: validRunner},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewCommandBatchExecutor(test.factory, test.runner, test.store); err == nil {
				t.Fatal("constructor accepted a missing explicit port")
			}
		})
	}
}

func TestCommandBatchExecutorMapsExplicitSuccessAndWritesReceipt(t *testing.T) {
	batch := commandExecutorBatch()
	runner := &commandExecutorRunner{outcome: RunOutcome{ExitCode: 0}}
	store := &commandExecutorReceiptStore{}
	factoryCalls := 0
	executor, err := NewCommandBatchExecutor(
		CommandFactory(func(got heterociworkflow.ExecutionBatch) (Command, error) {
			factoryCalls++
			if got.BatchID != batch.BatchID || got.RunID != batch.RunID || got.Key != batch.Key {
				t.Fatalf("factory received drifted batch: %#v", got)
			}
			return Command{Platform: heteroci.PlatformLinuxAMD64, Argv: []string{"tool", "two words"}}, nil
		}),
		runner,
		store,
	)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor() error = %v", err)
	}
	got, err := executor.ExecuteBatch(context.Background(), batch)
	if err != nil {
		t.Fatalf("ExecuteBatch() error = %v", err)
	}
	assertCanonicalBatchResultWithArgv(t, batch, got, heteroci.LeaseStateSucceeded, []string{"tool", "two words"})
	if factoryCalls != 1 || runner.calls != 1 || len(store.writes) != 1 {
		t.Fatalf("calls factory=%d runner=%d writes=%d, want 1/1/1", factoryCalls, runner.calls, len(store.writes))
	}
	if !slices.Equal(store.writes[0].Outcomes, got.Outcomes) {
		t.Fatalf("written receipt outcomes = %#v, returned = %#v", store.writes[0].Outcomes, got.Outcomes)
	}
}

func TestCommandBatchExecutorMapsExplicitFailureExit(t *testing.T) {
	batch := commandExecutorBatch()
	executor, err := NewCommandBatchExecutor(
		CommandFactory(func(heterociworkflow.ExecutionBatch) (Command, error) {
			return Command{Platform: heteroci.PlatformLinuxAMD64, Argv: []string{"tool"}}, nil
		}),
		&commandExecutorRunner{outcome: RunOutcome{ExitCode: 17}},
		&commandExecutorReceiptStore{},
	)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor() error = %v", err)
	}
	got, err := executor.ExecuteBatch(context.Background(), batch)
	if err != nil {
		t.Fatalf("ExecuteBatch() error = %v", err)
	}
	assertCanonicalBatchResult(t, batch, got, heteroci.LeaseStateFailed)
}

func TestCommandBatchExecutorAcceptsFrozenPlatformEncodings(t *testing.T) {
	for _, test := range []struct {
		name     string
		platform heteroci.Platform
		runtime  heteroci.RuntimeKind
	}{
		{name: "linux", platform: heteroci.PlatformLinuxAMD64, runtime: heteroci.RuntimeUbuntuContainer},
		{name: "darwin", platform: heteroci.PlatformDarwinARM64, runtime: heteroci.RuntimeDarwinNative},
		{name: "windows", platform: heteroci.PlatformWindowsAMD64, runtime: heteroci.RuntimeWindowsNative},
	} {
		t.Run(test.name, func(t *testing.T) {
			batch := commandExecutorPlatformBatch(test.platform, test.runtime)
			command, err := EncodeCommand(test.platform, []string{"tool", "two words"})
			if err != nil {
				t.Fatalf("EncodeCommand() error = %v", err)
			}
			executor, err := NewCommandBatchExecutor(func(heterociworkflow.ExecutionBatch) (Command, error) { return command, nil }, &commandExecutorRunner{}, &commandExecutorReceiptStore{})
			if err != nil {
				t.Fatalf("NewCommandBatchExecutor() error = %v", err)
			}
			if _, err := executor.ExecuteBatch(context.Background(), batch); err != nil {
				t.Fatalf("ExecuteBatch(%s) error = %v", test.platform, err)
			}
		})
	}
}

func TestStrictCommandEncoderRejectsWindowsCreateProcessOverflow(t *testing.T) {
	exact := strings.Repeat("a", windowsCommandLineMaxUTF16CodeUnits-len("tool "))
	if _, err := EncodeCommand(heteroci.PlatformWindowsAMD64, []string{"tool", exact}); err != nil {
		t.Fatalf("exact boundary rejected: %v", err)
	}
	if _, err := EncodeCommand(heteroci.PlatformWindowsAMD64, []string{"tool", exact + "a"}); err == nil {
		t.Fatal("Windows CreateProcess overflow was accepted")
	}
}

func TestCommandBatchExecutorUsesOnlySameBatchReceiptAfterRunnerError(t *testing.T) {
	batch := commandExecutorBatch()
	runnerErr := errors.New("runner outcome is ambiguous")
	receipt := canonicalBatchResult(batch, heteroci.LeaseStateSucceeded)
	store := &commandExecutorReceiptStore{readResult: receipt, readFound: true, readMisses: 1}
	runner := &commandExecutorRunner{err: runnerErr}
	executor, err := NewCommandBatchExecutor(commandExecutorFactory(), runner, store)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor() error = %v", err)
	}
	got, err := executor.ExecuteBatch(context.Background(), batch)
	if err != nil {
		t.Fatalf("ExecuteBatch() with receipt error = %v", err)
	}
	assertCanonicalBatchResult(t, batch, got, heteroci.LeaseStateSucceeded)
	if runner.calls != 1 || len(store.reads) != 2 || !allReceiptReadsMatch(store.reads, batch.BatchID) || len(store.writes) != 0 {
		t.Fatalf("runner=%d reads=%v writes=%d, want preflight and recovery reads of same batch and no write", runner.calls, store.reads, len(store.writes))
	}
}

func TestCommandBatchExecutorMissingReceiptIsUncertainAndNeverReruns(t *testing.T) {
	batch := commandExecutorBatch()
	runnerErr := errors.New("runner failed after launch")
	runner := &commandExecutorRunner{err: runnerErr}
	store := &commandExecutorReceiptStore{}
	executor, err := NewCommandBatchExecutor(commandExecutorFactory(), runner, store)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor() error = %v", err)
	}
	first, firstErr := executor.ExecuteBatch(context.Background(), batch)
	second, secondErr := executor.ExecuteBatch(context.Background(), batch)
	if !errors.Is(firstErr, heterociworkflow.ErrBatchReceiptRequired) || !errors.Is(secondErr, ErrCommandInFlight) {
		t.Fatalf("errors first=%v second=%v, want receipt-required then ErrCommandInFlight", firstErr, secondErr)
	}
	if !reflect.DeepEqual(first, heterociworkflow.BatchResult{}) || !reflect.DeepEqual(second, heterociworkflow.BatchResult{}) {
		t.Fatalf("uncertain results first=%#v second=%#v, want zero", first, second)
	}
	if runner.calls != 1 || len(store.reads) != 3 || !allReceiptReadsMatch(store.reads, batch.BatchID) {
		t.Fatalf("runner=%d reads=%v, want first preflight/recovery and second preflight only", runner.calls, store.reads)
	}
}

func TestCommandBatchExecutorReceiptAndCommandWriteErrorsFailFast(t *testing.T) {
	batch := commandExecutorBatch()
	writeErr := errors.New("receipt write failed")
	store := &commandExecutorReceiptStore{writeErr: writeErr}
	executor, err := NewCommandBatchExecutor(commandExecutorFactory(), &commandExecutorRunner{}, store)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor() error = %v", err)
	}
	if _, err := executor.ExecuteBatch(context.Background(), batch); !errors.Is(err, ErrReceiptWrite) || !errors.Is(err, writeErr) {
		t.Fatalf("receipt write error = %v, want ErrReceiptWrite and cause", err)
	}
	readErr := errors.New("receipt read failed")
	readStore := &commandExecutorReceiptStore{readErr: readErr}
	readRunner := &commandExecutorRunner{err: errors.New("ambiguous")}
	readExecutor, err := NewCommandBatchExecutor(commandExecutorFactory(), readRunner, readStore)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor(read) error = %v", err)
	}
	if _, err := readExecutor.ExecuteBatch(context.Background(), batch); !errors.Is(err, ErrReceiptRead) || !errors.Is(err, readErr) {
		t.Fatalf("receipt read error = %v, want ErrReceiptRead and cause", err)
	}
	if readRunner.calls != 0 {
		t.Fatalf("runner calls after preflight read error = %d, want zero", readRunner.calls)
	}
}

func TestCommandBatchExecutorPreexistingReceiptSkipsRunner(t *testing.T) {
	batch := commandExecutorBatch()
	claimFalse := false
	store := &commandExecutorReceiptStore{readResult: canonicalBatchResult(batch, heteroci.LeaseStateSucceeded), readFound: true, claimOverride: &claimFalse}
	runner := &commandExecutorRunner{}
	executor, err := NewCommandBatchExecutor(commandExecutorFactory(), runner, store)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor() error = %v", err)
	}
	got, err := executor.ExecuteBatch(context.Background(), batch)
	if err != nil {
		t.Fatalf("ExecuteBatch(preexisting receipt) error = %v", err)
	}
	assertCanonicalBatchResult(t, batch, got, heteroci.LeaseStateSucceeded)
	if runner.calls != 0 || len(store.reads) != 1 || store.reads[0] != batch.BatchID || len(store.writes) != 0 {
		t.Fatalf("runner=%d reads=%v writes=%d, want receipt-only recovery", runner.calls, store.reads, len(store.writes))
	}
}

func TestCommandBatchExecutorWriteAckLostReadsExactReceipt(t *testing.T) {
	batch := commandExecutorBatch()
	writeErr := errors.New("receipt write ack lost")
	store := &commandExecutorReceiptStore{writeErr: writeErr, persistWrite: true}
	runner := &commandExecutorRunner{outcome: RunOutcome{ExitCode: 0}}
	executor, err := NewCommandBatchExecutor(commandExecutorFactory(), runner, store)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor() error = %v", err)
	}
	got, err := executor.ExecuteBatch(context.Background(), batch)
	if err != nil {
		t.Fatalf("ExecuteBatch(write ack lost) error = %v", err)
	}
	assertCanonicalBatchResult(t, batch, got, heteroci.LeaseStateSucceeded)
	if runner.calls != 1 || len(store.writes) != 1 || len(store.reads) != 2 || !allReceiptReadsMatch(store.reads, batch.BatchID) {
		t.Fatalf("runner=%d writes=%d reads=%v, want one run/write and exact recovery read", runner.calls, len(store.writes), store.reads)
	}
}

func TestCommandBatchExecutorWriteAckConflictFailsFast(t *testing.T) {
	batch := commandExecutorBatch()
	conflict := canonicalBatchResult(batch, heteroci.LeaseStateFailed)
	store := &commandExecutorReceiptStore{writeErr: errors.New("receipt write ack lost"), persistWrite: true, persistResult: &conflict}
	executor, err := NewCommandBatchExecutor(commandExecutorFactory(), &commandExecutorRunner{outcome: RunOutcome{ExitCode: 0}}, store)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor() error = %v", err)
	}
	if _, err := executor.ExecuteBatch(context.Background(), batch); !errors.Is(err, ErrReceiptWrite) || !errors.Is(err, heterociworkflow.ErrBatchResult) {
		t.Fatalf("write conflict error = %v, want ErrReceiptWrite and ErrBatchResult", err)
	}
}

func TestCommandBatchExecutorInvalidRunOutcomeCannotUseReceipt(t *testing.T) {
	batch := commandExecutorBatch()
	store := &commandExecutorReceiptStore{}
	runner := &commandExecutorRunner{outcome: RunOutcome{ExitCode: 0, Outcomes: []heterociworkflow.BatchOutcome{{UnitID: "unit-1", State: heteroci.LeaseStateFailed}}}}
	executor, err := NewCommandBatchExecutor(commandExecutorFactory(), runner, store)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor() error = %v", err)
	}
	if _, err := executor.ExecuteBatch(context.Background(), batch); err == nil || errors.Is(err, heterociworkflow.ErrBatchReceiptRequired) {
		t.Fatalf("invalid explicit outcome error = %v, want direct fail-fast", err)
	}
	if _, found, err := executor.ReadBatchReceipt(context.Background(), batch); err == nil || found {
		t.Fatalf("ReadBatchReceipt after invalid outcome = found:%t err:%v, want same fail-fast", found, err)
	}
	if runner.calls != 1 || len(store.reads) != 1 {
		t.Fatalf("runner=%d reads=%v, invalid outcome must not consult recovery receipt", runner.calls, store.reads)
	}
}

func TestCommandBatchExecutorRejectsBatchCommandAndOutcomeDrift(t *testing.T) {
	batch := commandExecutorBatch()
	for _, test := range []struct {
		name    string
		mutate  func(*heterociworkflow.ExecutionBatch)
		factory CommandFactory
	}{
		{name: "batch id", mutate: func(candidate *heterociworkflow.ExecutionBatch) {
			candidate.BatchID = "0000000000000000000000000000000000000000000000000000000000000000"
		}, factory: commandExecutorFactory()},
		{name: "platform", mutate: func(*heterociworkflow.ExecutionBatch) {}, factory: func(heterociworkflow.ExecutionBatch) (Command, error) {
			return Command{Platform: heteroci.PlatformWindowsAMD64, Argv: []string{"tool"}}, nil
		}},
		{name: "argv", mutate: func(*heterociworkflow.ExecutionBatch) {}, factory: func(heterociworkflow.ExecutionBatch) (Command, error) {
			return Command{Platform: heteroci.PlatformLinuxAMD64, Argv: []string{"tool", "bad\x00"}}, nil
		}},
		{name: "assignment", mutate: func(candidate *heterociworkflow.ExecutionBatch) { candidate.Assignments[0].NodeID = "other-node" }, factory: commandExecutorFactory()},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := batch
			test.mutate(&candidate)
			runner := &commandExecutorRunner{}
			executor, err := NewCommandBatchExecutor(test.factory, runner, &commandExecutorReceiptStore{})
			if err != nil {
				t.Fatalf("NewCommandBatchExecutor() error = %v", err)
			}
			if _, err := executor.ExecuteBatch(context.Background(), candidate); err == nil {
				t.Fatal("ExecuteBatch accepted drifted batch/command")
			}
			if runner.calls != 0 {
				t.Fatalf("runner calls = %d, want zero on validation failure", runner.calls)
			}
		})
	}
}

func TestCommandBatchExecutorConcurrentCallsExecuteOnce(t *testing.T) {
	batch := commandExecutorBatch()
	runner := &commandExecutorRunner{started: make(chan struct{}), release: make(chan struct{})}
	executor, err := NewCommandBatchExecutor(commandExecutorFactory(), runner, &commandExecutorReceiptStore{})
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor() error = %v", err)
	}
	firstResult := make(chan error, 1)
	secondResult := make(chan error, 1)
	var group sync.WaitGroup
	group.Go(func() { _, callErr := executor.ExecuteBatch(context.Background(), batch); firstResult <- callErr })
	<-runner.started
	group.Go(func() { _, callErr := executor.ExecuteBatch(context.Background(), batch); secondResult <- callErr })
	select {
	case secondErr := <-secondResult:
		if !errors.Is(secondErr, ErrCommandInFlight) {
			t.Fatalf("concurrent second ExecuteBatch error = %v, want ErrCommandInFlight", secondErr)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent second ExecuteBatch waited for the first runner")
	}
	close(runner.release)
	group.Wait()
	if firstErr := <-firstResult; firstErr != nil {
		t.Fatalf("first ExecuteBatch error = %v", firstErr)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want exactly one", runner.calls)
	}
}

func TestCommandBatchExecutorConflictsBeforeBlockedReceiptRead(t *testing.T) {
	batch := commandExecutorBatch()
	store := &blockingReceiptStore{readStarted: make(chan struct{}), releaseRead: make(chan struct{})}
	runner := &commandExecutorRunner{outcome: RunOutcome{ExitCode: 0}}
	executor, err := NewCommandBatchExecutor(commandExecutorFactory(), runner, store)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor() error = %v", err)
	}
	firstDone := make(chan error, 1)
	var group sync.WaitGroup
	group.Go(func() {
		_, firstErr := executor.ExecuteBatch(context.Background(), batch)
		firstDone <- firstErr
	})
	<-store.readStarted
	secondDone := make(chan error, 1)
	group.Go(func() {
		_, secondErr := executor.ExecuteBatch(context.Background(), batch)
		secondDone <- secondErr
	})
	select {
	case secondErr := <-secondDone:
		if !errors.Is(secondErr, ErrCommandInFlight) {
			t.Fatalf("second ExecuteBatch error = %v, want ErrCommandInFlight", secondErr)
		}
	case <-time.After(500 * time.Millisecond):
		close(store.releaseRead)
		t.Fatal("second ExecuteBatch waited on the blocked receipt read")
	}
	store.commandExecutorReceiptStore.mu.Lock()
	readCount := len(store.commandExecutorReceiptStore.reads)
	store.commandExecutorReceiptStore.mu.Unlock()
	if readCount != 0 {
		t.Fatalf("receipt Read calls while first read blocked = %d, want zero delegated reads", readCount)
	}
	close(store.releaseRead)
	if firstErr := <-firstDone; firstErr != nil {
		t.Fatalf("first ExecuteBatch error = %v", firstErr)
	}
	group.Wait()
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want exactly one", runner.calls)
	}
}

func TestCommandBatchExecutorSharedStoreClaimsAcrossInstances(t *testing.T) {
	batch := commandExecutorBatch()
	store := &commandExecutorReceiptStore{persistWrite: true}
	runner := &commandExecutorRunner{started: make(chan struct{}), release: make(chan struct{}), outcome: RunOutcome{ExitCode: 0}}
	first, err := NewCommandBatchExecutor(commandExecutorFactory(), runner, store)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor(first) error = %v", err)
	}
	second, err := NewCommandBatchExecutor(commandExecutorFactory(), runner, store)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor(second) error = %v", err)
	}
	firstDone := make(chan error, 1)
	var group sync.WaitGroup
	group.Go(func() {
		_, firstErr := first.ExecuteBatch(context.Background(), batch)
		firstDone <- firstErr
	})
	<-runner.started
	if _, secondErr := second.ExecuteBatch(context.Background(), batch); !errors.Is(secondErr, ErrCommandInFlight) {
		t.Fatalf("second ExecuteBatch while first is running = %v, want ErrCommandInFlight", secondErr)
	}
	close(runner.release)
	group.Wait()
	if firstErr := <-firstDone; firstErr != nil {
		t.Fatalf("first ExecuteBatch() error = %v", firstErr)
	}
	recovered, recoverErr := second.ExecuteBatch(context.Background(), batch)
	if recoverErr != nil {
		t.Fatalf("second receipt recovery error = %v", recoverErr)
	}
	assertCanonicalBatchResult(t, batch, recovered, heteroci.LeaseStateSucceeded)
	if runner.calls != 1 {
		t.Fatalf("shared-store runner calls = %d, want exactly one", runner.calls)
	}
}

func TestCommandBatchExecutorSharedStoreRejectsCommandDigestDrift(t *testing.T) {
	batch := commandExecutorBatch()
	store := &commandExecutorReceiptStore{}
	runner := &commandExecutorRunner{started: make(chan struct{}), release: make(chan struct{}), outcome: RunOutcome{ExitCode: 0}}
	first, err := NewCommandBatchExecutor(commandExecutorFactory(), runner, store)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor(first) error = %v", err)
	}
	differentFactory := CommandFactory(func(heterociworkflow.ExecutionBatch) (Command, error) {
		return Command{Platform: heteroci.PlatformLinuxAMD64, Argv: []string{"other-tool"}}, nil
	})
	second, err := NewCommandBatchExecutor(differentFactory, runner, store)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor(second) error = %v", err)
	}
	firstDone := make(chan error, 1)
	var group sync.WaitGroup
	group.Go(func() {
		_, firstErr := first.ExecuteBatch(context.Background(), batch)
		firstDone <- firstErr
	})
	<-runner.started
	if _, secondErr := second.ExecuteBatch(context.Background(), batch); !errors.Is(secondErr, ErrReceiptClaim) {
		t.Fatalf("second command-drift ExecuteBatch() = %v, want ErrReceiptClaim", secondErr)
	}
	close(runner.release)
	group.Wait()
	if firstErr := <-firstDone; firstErr != nil {
		t.Fatalf("first ExecuteBatch() error = %v", firstErr)
	}
	if runner.calls != 1 {
		t.Fatalf("command-drift runner calls = %d, want exactly one", runner.calls)
	}
}

func TestCommandBatchExecutorReadBatchReceiptUsesRequestedID(t *testing.T) {
	batch := commandExecutorBatch()
	store := &commandExecutorReceiptStore{readResult: canonicalBatchResult(batch, heteroci.LeaseStateSucceeded), readFound: true}
	executor, err := NewCommandBatchExecutor(commandExecutorFactory(), &commandExecutorRunner{}, store)
	if err != nil {
		t.Fatalf("NewCommandBatchExecutor() error = %v", err)
	}
	if _, found, err := executor.ReadBatchReceipt(context.Background(), batch); err != nil || !found {
		t.Fatalf("ReadBatchReceipt() = found:%t err:%v, want found", found, err)
	}
	if len(store.reads) != 1 || store.reads[0] != batch.BatchID {
		t.Fatalf("receipt reads = %v, want [%s]", store.reads, batch.BatchID)
	}
}

func commandExecutorFactory() CommandFactory {
	return func(heterociworkflow.ExecutionBatch) (Command, error) {
		return Command{Platform: heteroci.PlatformLinuxAMD64, Argv: []string{"tool"}}, nil
	}
}

func commandExecutorBatch() heterociworkflow.ExecutionBatch {
	return commandExecutorPlatformBatch(heteroci.PlatformLinuxAMD64, heteroci.RuntimeUbuntuContainer)
}

func commandExecutorPlatformBatch(platform heteroci.Platform, runtime heteroci.RuntimeKind) heterociworkflow.ExecutionBatch {
	key := heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: "node-1"}
	unit := heteroci.ExecutionUnit{UnitID: "unit-1", TargetPlatform: platform, RequiredRuntime: runtime, EstimatedDurationMS: 10, CPUMilli: 1000, MemoryBytes: 4 << 30}
	assignment := heteroci.RouteAssignment{UnitID: unit.UnitID, Executor: heteroci.ExecutorKindLocalNode, NodeID: key.NodeID, Endpoint: "https://node-1.invalid/runner", ProbeIdentity: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", ProbeBootID: "boot-1"}
	batchID, err := heterociworkflow.DeriveBatchID("run-1", key, 0, []string{unit.UnitID})
	if err != nil {
		// archguard:ignore panic_count -- 固定批次夹具必须生成合法 batch ID，失败表示测试数据编程错误。
		panic(err)
	}
	decisionDigest := commandExecutorDigest('2')
	started := heteroci.StartedRoutingLease{
		Fence: heteroci.OwnerFence{
			Scope:      heteroci.RoutingOwnerScope(commandExecutorDigest('0')),
			OwnerID:    heteroci.RoutingOwnerID(strings.Repeat("a", 64)),
			OwnerEpoch: 1,
			Revision:   1,
		},
		Lease: heteroci.RoutingLease{
			SchemaVersion:  heteroci.RoutingLeaseSchemaVersion,
			Authority:      heteroci.AuthorityLocalNonAuthoritative,
			RunID:          "run-1",
			DecisionDigest: decisionDigest,
			UnitID:         unit.UnitID,
			Executor:       assignment.Executor,
			NodeID:         assignment.NodeID,
			Endpoint:       assignment.Endpoint,
			ProbeIdentity:  assignment.ProbeIdentity,
			ProbeBootID:    assignment.ProbeBootID,
			State:          heteroci.LeaseStateStarted,
		},
		Occupancy: heteroci.StartedRoutingOccupancy{
			OwnerEpoch:     1,
			RunID:          "run-1",
			UnitID:         unit.UnitID,
			DecisionDigest: decisionDigest,
		},
	}
	batch := heterociworkflow.ExecutionBatch{BatchID: batchID, RunID: "run-1", Key: key, Wave: 0, Units: []heteroci.ExecutionUnit{unit}, Assignments: []heteroci.RouteAssignment{assignment}, Started: []heteroci.StartedRoutingLease{started}}
	batch.MaterialDigest, err = heterociworkflow.DeriveExecutionMaterialDigest(batch)
	if err != nil {
		// archguard:ignore panic_count -- 固定批次夹具必须生成材料摘要，失败表示测试数据编程错误。
		panic(err)
	}
	return batch
}

func canonicalBatchResult(batch heterociworkflow.ExecutionBatch, state heteroci.RoutingLeaseState) heterociworkflow.BatchResult {
	outcomes := make([]heterociworkflow.BatchOutcome, len(batch.Units))
	for index, unit := range batch.Units {
		outcomes[index] = heterociworkflow.BatchOutcome{UnitID: unit.UnitID, State: state}
	}
	command, err := EncodeCommand(batch.Units[0].TargetPlatform, []string{"tool"})
	if err != nil {
		// archguard:ignore panic_count -- 固定命令夹具必须通过规范编码，失败表示测试数据编程错误。
		panic(err)
	}
	commandDigest, err := DeriveCommandDigest(command)
	if err != nil {
		// archguard:ignore panic_count -- 固定命令夹具必须生成摘要，失败表示测试数据编程错误。
		panic(err)
	}
	return heterociworkflow.BatchResult{BatchID: batch.BatchID, RunID: batch.RunID, Executor: batch.Key, MaterialDigest: batch.MaterialDigest, CommandDigest: commandDigest, Authority: heteroci.AuthorityLocalNonAuthoritative, Status: heterociworkflow.ResultStatusNotVerified, Outcomes: outcomes}
}

func assertCanonicalBatchResult(t *testing.T, batch heterociworkflow.ExecutionBatch, result heterociworkflow.BatchResult, state heteroci.RoutingLeaseState) {
	assertCanonicalBatchResultWithArgv(t, batch, result, state, []string{"tool"})
}

func assertCanonicalBatchResultWithArgv(t *testing.T, batch heterociworkflow.ExecutionBatch, result heterociworkflow.BatchResult, state heteroci.RoutingLeaseState, argv []string) {
	t.Helper()
	want, err := expectedBatchResult(batch, state, argv)
	if err != nil {
		t.Fatalf("expected BatchResult error = %v", err)
	}
	assertBatchResultEqual(t, result, want)
}

func expectedBatchResult(batch heterociworkflow.ExecutionBatch, state heteroci.RoutingLeaseState, argv []string) (heterociworkflow.BatchResult, error) {
	command, err := EncodeCommand(batch.Units[0].TargetPlatform, argv)
	if err != nil {
		return heterociworkflow.BatchResult{}, err
	}
	commandDigest, err := DeriveCommandDigest(command)
	if err != nil {
		return heterociworkflow.BatchResult{}, err
	}
	outcomes := make([]heterociworkflow.BatchOutcome, len(batch.Units))
	for index, unit := range batch.Units {
		outcomes[index] = heterociworkflow.BatchOutcome{UnitID: unit.UnitID, State: state}
	}
	return heterociworkflow.BatchResult{BatchID: batch.BatchID, RunID: batch.RunID, Executor: batch.Key, MaterialDigest: batch.MaterialDigest, CommandDigest: commandDigest, Authority: heteroci.AuthorityLocalNonAuthoritative, Status: heterociworkflow.ResultStatusNotVerified, Outcomes: outcomes}, nil
}

func assertBatchResultEqual(t *testing.T, result, want heterociworkflow.BatchResult) {
	t.Helper()
	if result.BatchID != want.BatchID || result.RunID != want.RunID || result.Executor != want.Executor || result.MaterialDigest != want.MaterialDigest || result.CommandDigest != want.CommandDigest || result.Authority != want.Authority || result.Status != want.Status || !slices.Equal(result.Outcomes, want.Outcomes) {
		t.Fatalf("BatchResult = %#v, want %#v", result, want)
	}
}

func commandExecutorDigest(character byte) string {
	return "sha256:" + strings.Repeat(string(character), 64)
}

func allReceiptReadsMatch(values []heterociworkflow.BatchID, want heterociworkflow.BatchID) bool {
	for _, value := range values {
		if value != want {
			return false
		}
	}
	return len(values) != 0
}

var _ heterociworkflow.BatchExecutor = (*CommandBatchExecutor)(nil)
var _ heterociworkflow.BatchReceiptReader = (*CommandBatchExecutor)(nil)
