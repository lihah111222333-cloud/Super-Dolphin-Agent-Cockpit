package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"golang.org/x/sync/errgroup"
)

func TestRuntimeDurableGoplsRootCohortSharesStateAcrossControllers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gopls auto daemon root cohorts are unsupported on Windows")
	}
	cacheRoot := t.TempDir()
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatalf("secure test cache root: %v", err)
	}
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)

	first, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(10 * time.Millisecond)
	if err != nil {
		t.Fatalf("new first durable controller: %v", err)
	}
	second, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(10 * time.Millisecond)
	if err != nil {
		t.Fatalf("new second durable controller: %v", err)
	}
	config := runtimeDurableGoplsRootCohortTestConfig("same")
	firstLease, err := first.AcquireLease(config)
	if err != nil {
		t.Fatalf("first AcquireLease: %v", err)
	}
	secondLease, err := second.AcquireLease(config)
	if err != nil {
		t.Fatalf("second AcquireLease: %v", err)
	}
	if firstLease.Fence() == secondLease.Fence() {
		t.Fatal("independent durable controllers reused the same member fence")
	}
	if err := second.ValidateFence(config, firstLease.Fence()); err != nil {
		t.Fatalf("second controller could not validate first fence: %v", err)
	}

	conflict := config
	conflict.EffectiveConfigDigest = "effective-conflict"
	if _, err := second.AcquireLease(conflict); !errors.Is(err, multilsp.ErrGoplsRootCohortConfigConflict) {
		t.Fatalf("conflicting immutable config error = %v, want ErrGoplsRootCohortConfigConflict", err)
	}

	if err := firstLease.ReleaseWithOwner(func() error { return nil }); err != nil {
		t.Fatalf("release first durable lease: %v", err)
	}
	if err := first.ValidateFence(config, firstLease.Fence()); !errors.Is(err, multilsp.ErrGoplsRootCohortFenceStale) {
		t.Fatalf("released first fence validation error = %v, want stale fence", err)
	}
	snapshot, ok := second.Snapshot(config)
	if !ok || snapshot.ActiveMembers != 1 || snapshot.State != multilsp.GoplsRootCohortStateAdmitted {
		t.Fatalf("snapshot after first release = (%+v, %v), want one admitted member", snapshot, ok)
	}
	if err := secondLease.ReleaseWithOwner(func() error { return nil }); err != nil {
		t.Fatalf("release second durable lease: %v", err)
	}
	snapshot, ok = second.Snapshot(config)
	if !ok || snapshot.ActiveMembers != 0 || snapshot.State != multilsp.GoplsRootCohortStateIdle {
		t.Fatalf("snapshot after all releases = (%+v, %v), want idle cohort", snapshot, ok)
	}
}

func TestRuntimeDurableGoplsRootCohortRotatesConfigAfterStaleOwnerIsProvenDead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gopls auto daemon root cohorts are unsupported on Windows")
	}
	cacheRoot := t.TempDir()
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatalf("secure test cache root: %v", err)
	}
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	controllerValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(10 * time.Millisecond)
	if err != nil {
		t.Fatalf("new durable controller: %v", err)
	}
	controller := controllerValue.(*runtimeServerDurableGoplsRootCohortController)
	oldConfig := runtimeDurableGoplsRootCohortTestConfig("stale-old")
	oldLease, err := controller.AcquireLease(oldConfig)
	if err != nil {
		t.Fatalf("old AcquireLease: %v", err)
	}
	dir := runtimeServerGoplsRootCohortDir(controller.root, oldConfig)
	leasePath := runtimeServerGoplsRootCohortLeasePath(dir, oldLease.Fence())
	leaseRecord, err := runtimeServerReadGoplsRootCohortLease(leasePath)
	if err != nil {
		t.Fatalf("read old lease: %v", err)
	}
	leaseRecord.OwnerPID = 1 << 30
	leaseRecord.OwnerStartIdentity = "proven-dead-owner"
	payload, err := json.Marshal(leaseRecord)
	if err != nil {
		t.Fatalf("marshal stale lease: %v", err)
	}
	if err := os.WriteFile(leasePath, payload, 0o600); err != nil {
		t.Fatalf("write stale lease: %v", err)
	}

	newConfig := oldConfig
	newConfig.CohortID = "sdmcp2-test-stale-new"
	newConfig.EffectiveConfigDigest = "effective-stale-new"
	newLease, err := controller.AcquireLease(newConfig)
	if err != nil {
		t.Fatalf("AcquireLease after stale owner config rotation: %v", err)
	}
	if newLease.Fence().Epoch <= oldLease.Fence().Epoch || newLease.Fence().MemberGeneration <= oldLease.Fence().MemberGeneration {
		t.Fatalf("rotated fence did not advance monotonically: old=%+v new=%+v", oldLease.Fence(), newLease.Fence())
	}
	if err := controller.ValidateFence(oldConfig, oldLease.Fence()); !errors.Is(err, multilsp.ErrGoplsRootCohortFenceStale) {
		t.Fatalf("old fence validation after rotation = %v, want ErrGoplsRootCohortFenceStale", err)
	}
	if _, err := os.Stat(leasePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale lease remains after config rotation: %v", err)
	}
	state, err := runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read rotated state: %v", err)
	}
	stored, err := state.configValue()
	if err != nil {
		t.Fatalf("decode rotated state config: %v", err)
	}
	if !storedEqualGoplsRootCohortConfig(stored, newConfig) {
		t.Fatalf("rotated state config = %#v, want %#v", stored, newConfig)
	}
	if err := newLease.Release(); err != nil {
		t.Fatalf("release new lease: %v", err)
	}
}

func TestRuntimeDurableGoplsRootCohortSerializesConcurrentConfigRotation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gopls auto daemon root cohorts are unsupported on Windows")
	}
	cacheRoot := t.TempDir()
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatalf("secure test cache root: %v", err)
	}
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	controller, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(10 * time.Millisecond)
	if err != nil {
		t.Fatalf("new durable controller: %v", err)
	}
	oldConfig := runtimeDurableGoplsRootCohortTestConfig("concurrent-old")
	oldLease, err := controller.AcquireLease(oldConfig)
	if err != nil {
		t.Fatalf("old AcquireLease: %v", err)
	}
	if err := oldLease.Release(); err != nil {
		t.Fatalf("release old lease: %v", err)
	}

	firstConfig := oldConfig
	firstConfig.CohortID = "sdmcp2-test-concurrent-first"
	firstConfig.EffectiveConfigDigest = "effective-concurrent-first"
	secondConfig := oldConfig
	secondConfig.CohortID = "sdmcp2-test-concurrent-second"
	secondConfig.EffectiveConfigDigest = "effective-concurrent-second"
	type result struct {
		config multilsp.GoplsRootCohortConfig
		lease  multilsp.GoplsRootCohortLease
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var workers errgroup.Group
	for _, config := range []multilsp.GoplsRootCohortConfig{firstConfig, secondConfig} {
		config := config
		workers.Go(func() error {
			<-start
			lease, err := controller.AcquireLease(config)
			results <- result{config: config, lease: lease, err: err}
			return nil
		})
	}
	close(start)
	if err := workers.Wait(); err != nil {
		t.Fatalf("wait concurrent rotations: %v", err)
	}
	firstResult := <-results
	secondResult := <-results
	var winner, loser result
	if firstResult.err == nil {
		winner, loser = firstResult, secondResult
	} else {
		winner, loser = secondResult, firstResult
	}
	if winner.err != nil {
		t.Fatalf("both concurrent rotations failed: first=%v second=%v", firstResult.err, secondResult.err)
	}
	if !errors.Is(loser.err, multilsp.ErrGoplsRootCohortConfigConflict) {
		t.Fatalf("losing concurrent rotation error = %v, want ErrGoplsRootCohortConfigConflict", loser.err)
	}
	if err := controller.ValidateFence(winner.config, winner.lease.Fence()); err != nil {
		t.Fatalf("winning concurrent rotation fence is invalid: %v", err)
	}
	if err := winner.lease.Release(); err != nil {
		t.Fatalf("release winning concurrent rotation lease: %v", err)
	}
}

func TestRuntimeServerGoplsRootCohortConfigRotationAllowedStateMatrix(t *testing.T) {
	cleanActive := runtimeServerDurableGoplsRootCohortState{
		DrainStatus: runtimeGoplsRootCohortDrainActive,
	}
	cleanCompleted := cleanActive
	cleanCompleted.DrainStatus = runtimeGoplsRootCohortDrainCompleted
	cleanCompleted.CompletionReceipt = "completion-receipt"
	cleanCompleted.CompletionUnixNano = 1
	activeAfterCompletedDrain := cleanCompleted
	activeAfterCompletedDrain.DrainStatus = runtimeGoplsRootCohortDrainActive

	tests := []struct {
		name  string
		state *runtimeServerDurableGoplsRootCohortState
		want  bool
	}{
		{name: "nil", state: nil},
		{name: "clean active", state: &cleanActive, want: true},
		{name: "active after completed drain", state: &activeAfterCompletedDrain, want: true},
		{name: "clean completed with receipt", state: &cleanCompleted, want: true},
	}
	dirtyCases := []struct {
		name   string
		mutate func(*runtimeServerDurableGoplsRootCohortState)
	}{
		{name: "draining", mutate: func(state *runtimeServerDurableGoplsRootCohortState) {
			state.DrainStatus = runtimeGoplsRootCohortDrainDraining
		}},
		{name: "attempting", mutate: func(state *runtimeServerDurableGoplsRootCohortState) {
			state.DrainStatus = runtimeGoplsRootCohortDrainAttempting
		}},
		{name: "cleanup pending", mutate: func(state *runtimeServerDurableGoplsRootCohortState) {
			state.DrainStatus = runtimeGoplsRootCohortDrainCleanupPending
		}},
		{name: "idle deadline", mutate: func(state *runtimeServerDurableGoplsRootCohortState) { state.IdleDeadlineUnixNano = 1 }},
		{name: "drain epoch", mutate: func(state *runtimeServerDurableGoplsRootCohortState) { state.DrainEpoch = 1 }},
		{name: "owner pid", mutate: func(state *runtimeServerDurableGoplsRootCohortState) { state.OwnerPID = 1 }},
		{name: "owner start identity", mutate: func(state *runtimeServerDurableGoplsRootCohortState) { state.OwnerStartIdentity = "owner" }},
		{name: "owner member id", mutate: func(state *runtimeServerDurableGoplsRootCohortState) { state.OwnerMemberID = "member" }},
		{name: "owner journal revision", mutate: func(state *runtimeServerDurableGoplsRootCohortState) { state.OwnerJournalRevision = 1 }},
		{name: "owner member generation", mutate: func(state *runtimeServerDurableGoplsRootCohortState) { state.OwnerMemberGeneration = 1 }},
		{name: "owner lease id", mutate: func(state *runtimeServerDurableGoplsRootCohortState) { state.OwnerLeaseID = "lease" }},
		{name: "last drain error", mutate: func(state *runtimeServerDurableGoplsRootCohortState) { state.LastDrainError = "failed" }},
		{name: "drain retry", mutate: func(state *runtimeServerDurableGoplsRootCohortState) { state.DrainRetryUnixNano = 1 }},
		{name: "active completion receipt", mutate: func(state *runtimeServerDurableGoplsRootCohortState) { state.CompletionReceipt = "stale-receipt" }},
		{name: "active completion time", mutate: func(state *runtimeServerDurableGoplsRootCohortState) { state.CompletionUnixNano = 1 }},
		{name: "pending cleanup", mutate: func(state *runtimeServerDurableGoplsRootCohortState) {
			state.PendingCleanups = []runtimeGoplsRootCohortCleanupEvidence{{Status: runtimeGoplsRootCohortDrainCleanupPending}}
		}},
	}
	for _, tc := range dirtyCases {
		state := cleanActive
		tc.mutate(&state)
		tests = append(tests, struct {
			name  string
			state *runtimeServerDurableGoplsRootCohortState
			want  bool
		}{name: tc.name, state: &state})
	}
	completedWithoutReceipt := cleanCompleted
	completedWithoutReceipt.CompletionReceipt = ""
	tests = append(tests, struct {
		name  string
		state *runtimeServerDurableGoplsRootCohortState
		want  bool
	}{name: "completed without receipt", state: &completedWithoutReceipt})
	completedWithoutTime := cleanCompleted
	completedWithoutTime.CompletionUnixNano = 0
	tests = append(tests, struct {
		name  string
		state *runtimeServerDurableGoplsRootCohortState
		want  bool
	}{name: "completed without time", state: &completedWithoutTime})

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := runtimeServerGoplsRootCohortConfigRotationAllowed(tc.state); got != tc.want {
				t.Fatalf("rotation allowed = %v, want %v for state %+v", got, tc.want, tc.state)
			}
		})
	}
}

func TestRuntimeDurableGoplsRootCohortIdleDrainCompletionAndAdmissionCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gopls auto daemon root cohorts are unsupported on Windows")
	}
	t.Run("completion receipt", func(t *testing.T) {
		runDurableGoplsRootCohortCompletionReceipt(t)
	})
	t.Run("new admission cancels pending drain", func(t *testing.T) {
		runDurableGoplsRootCohortAdmissionCancel(t)
	})
}

// runDurableGoplsRootCohortCompletionReceipt 覆盖 idle drain 完成与 completion receipt 持久化。
func runDurableGoplsRootCohortCompletionReceipt(t *testing.T) {
	t.Helper()
	cacheRoot := t.TempDir()
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatalf("secure test cache root: %v", err)
	}
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	drainWindow := time.Hour
	controllerValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(drainWindow)
	if err != nil {
		t.Fatalf("new durable controller: %v", err)
	}
	controller := controllerValue.(*runtimeServerDurableGoplsRootCohortController)
	t.Cleanup(func() { closeDurableGoplsRootCohortController(t, controller) })
	config := runtimeDurableGoplsRootCohortTestConfig("drain-complete")
	lease, err := controller.AcquireLease(config)
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	closed := make(chan struct{}, 1)
	releaseStarted := time.Now()
	if err := lease.ReleaseWithOwner(func() error {
		closed <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("ReleaseWithOwner: %v", err)
	}
	dir := runtimeServerGoplsRootCohortDir(controller.root, config)
	state, err := runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read draining state: %v", err)
	}
	if state.DrainStatus != runtimeGoplsRootCohortDrainDraining || state.IdleDeadlineUnixNano < releaseStarted.Add(drainWindow).UnixNano() {
		t.Fatalf("draining state = %+v, want durable deadline from release start", state)
	}
	select {
	case <-closed:
		t.Fatal("idle drain owner ran before explicit admission cancellation")
	default:
	}
	if err := controller.cancelPendingDrainForAdmission(config); err != nil {
		t.Fatalf("explicit idle drain completion: %v", err)
	}
	select {
	case <-closed:
	default:
		t.Fatal("explicit idle drain completion did not execute owner")
	}
	state, err = runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read completed state: %v", err)
	}
	if state.DrainStatus != runtimeGoplsRootCohortDrainCompleted || state.CompletionReceipt == "" || state.CompletionUnixNano == 0 {
		t.Fatalf("completed drain state = %+v, want completion receipt", state)
	}
	if state.IdleDeadlineUnixNano != 0 {
		t.Fatalf("completed drain retained idle deadline %d", state.IdleDeadlineUnixNano)
	}
	next, err := controller.AcquireLease(config)
	if err != nil {
		t.Fatalf("subsequent admission after drain completion: %v", err)
	}
	if next.Fence().Epoch <= lease.Fence().Epoch {
		t.Fatalf("subsequent admission epoch = %d, want > completed epoch %d", next.Fence().Epoch, lease.Fence().Epoch)
	}
	if err := next.Release(); err != nil {
		t.Fatalf("release subsequent lease: %v", err)
	}
}

// runDurableGoplsRootCohortAdmissionCancel 覆盖新 admission 对 pending drain 的显式取消。
func runDurableGoplsRootCohortAdmissionCancel(t *testing.T) {
	t.Helper()
	cacheRoot := t.TempDir()
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatalf("secure test cache root: %v", err)
	}
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	drainWindow := time.Hour
	controllerValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(drainWindow)
	if err != nil {
		t.Fatalf("new durable controller: %v", err)
	}
	controller := controllerValue.(*runtimeServerDurableGoplsRootCohortController)
	t.Cleanup(func() { closeDurableGoplsRootCohortController(t, controller) })
	config := runtimeDurableGoplsRootCohortTestConfig("drain-cancel")
	first, err := controller.AcquireLease(config)
	if err != nil {
		t.Fatalf("first AcquireLease: %v", err)
	}
	closed := make(chan struct{}, 1)
	releaseStarted := time.Now()
	if err := first.ReleaseWithOwner(func() error {
		closed <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("first ReleaseWithOwner: %v", err)
	}
	dir := runtimeServerGoplsRootCohortDir(controller.root, config)
	second, err := controller.AcquireLease(config)
	if err != nil {
		t.Fatalf("new admission: %v", err)
	}
	if second.Fence().Epoch <= first.Fence().Epoch {
		t.Fatalf("new admission fence epoch = %d, want > %d after drain cancellation", second.Fence().Epoch, first.Fence().Epoch)
	}
	select {
	case <-closed:
	default:
		t.Fatal("new admission did not synchronously drain the prior owner")
	}
	state, err := runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read admission-cancel state: %v", err)
	}
	if state.DrainStatus != runtimeGoplsRootCohortDrainActive || state.IdleDeadlineUnixNano != 0 || len(state.PendingCleanups) != 0 || state.CompletionReceipt == "" {
		t.Fatalf("admission-cancel state = %+v, want active epoch with completion receipt", state)
	}
	if state.CompletionUnixNano < releaseStarted.UnixNano() {
		t.Fatalf("completion timestamp = %d, before release start %d", state.CompletionUnixNano, releaseStarted.UnixNano())
	}
	if err := second.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
}

func TestRuntimeDurableGoplsRootCohortDrainFailureRetainsEvidenceAndRetries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gopls auto daemon root cohorts are unsupported on Windows")
	}
	cacheRoot := t.TempDir()
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatalf("secure test cache root: %v", err)
	}
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	controllerValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(10 * time.Millisecond)
	if err != nil {
		t.Fatalf("new durable controller: %v", err)
	}
	controller := controllerValue.(*runtimeServerDurableGoplsRootCohortController)
	controller.drainRetry = 10 * time.Millisecond
	config := runtimeDurableGoplsRootCohortTestConfig("drain-retry")
	lease, err := controller.AcquireLease(config)
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	var attempts atomic.Int32
	if err := lease.ReleaseWithOwner(func() error {
		if attempts.Add(1) == 1 {
			return errors.New("synthetic owner shutdown failure")
		}
		return nil
	}); err != nil {
		t.Fatalf("ReleaseWithOwner: %v", err)
	}
	dir := runtimeServerGoplsRootCohortDir(controller.root, config)
	deadline := time.Now().Add(time.Second)
	var state *runtimeServerDurableGoplsRootCohortState
	for time.Now().Before(deadline) {
		state, err = runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
		if err == nil && state.DrainStatus == runtimeGoplsRootCohortDrainCleanupPending {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("read cleanup-pending state: %v", err)
	}
	if state.DrainStatus != runtimeGoplsRootCohortDrainCleanupPending || state.OwnerLeaseID == "" || state.LastDrainError == "" {
		t.Fatalf("cleanup-pending state = %+v, want owner evidence and error", state)
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, err = runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
		if err == nil && state.DrainStatus == runtimeGoplsRootCohortDrainCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil || state.DrainStatus != runtimeGoplsRootCohortDrainCompleted || attempts.Load() < 2 || state.CompletionReceipt == "" {
		t.Fatalf("retry completion state = (%+v, %v), attempts=%d", state, err, attempts.Load())
	}
}

func TestRuntimeDurableGoplsRootCohortCrossControllerAdmissionFencesOldCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gopls auto daemon root cohorts are unsupported on Windows")
	}
	cacheRoot := t.TempDir()
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatalf("secure test cache root: %v", err)
	}
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	drainWindow := time.Hour
	// keep scheduler asleep while the test inspects durable evidence
	firstValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(drainWindow)
	if err != nil {
		t.Fatalf("new first durable controller: %v", err)
	}
	secondValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(drainWindow)
	if err != nil {
		t.Fatalf("new second durable controller: %v", err)
	}
	first := firstValue.(*runtimeServerDurableGoplsRootCohortController)
	second := secondValue.(*runtimeServerDurableGoplsRootCohortController)
	config := runtimeDurableGoplsRootCohortTestConfig("cross-owner")
	t.Cleanup(func() {
		closeDurableGoplsRootCohortController(t, first)
		closeDurableGoplsRootCohortController(t, second)
	})
	lease, err := first.AcquireLease(config)
	if err != nil {
		t.Fatalf("first AcquireLease: %v", err)
	}
	oldClosed := make(chan struct{}, 1)
	releaseStarted := time.Now()
	if err := lease.ReleaseWithOwner(func() error {
		oldClosed <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("first ReleaseWithOwner: %v", err)
	}
	next, err := second.AcquireLease(config)
	if err != nil {
		t.Fatalf("cross-controller admission error = %v, want atomic fenced re-admission", err)
	}
	if next.Fence().Epoch <= lease.Fence().Epoch {
		t.Fatalf("cross-controller admission epoch = %d, want > old epoch %d", next.Fence().Epoch, lease.Fence().Epoch)
	}
	dir := runtimeServerGoplsRootCohortDir(first.root, config)
	state, err := runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read fenced state: %v", err)
	}
	if state.DrainStatus != runtimeGoplsRootCohortDrainActive || len(state.PendingCleanups) != 1 {
		t.Fatalf("fenced state = %+v, want active new epoch with one old cleanup evidence", state)
	}
	if state.PendingCleanups[0].IdleDeadlineUnixNano < releaseStarted.Add(drainWindow).UnixNano() {
		t.Fatalf("old cleanup deadline = %d, want deadline from release start %d", state.PendingCleanups[0].IdleDeadlineUnixNano, releaseStarted.Add(drainWindow).UnixNano())
	}
	if state.PendingCleanups[0].Fence.toValue() != lease.Fence() {
		t.Fatalf("old cleanup fence = %+v, want %v", state.PendingCleanups[0].Fence, lease.Fence())
	}
	if state.PendingCleanups[0].Status != runtimeGoplsRootCohortDrainCleanupPending {
		t.Fatalf("old cleanup status = %q, want cleanup_pending evidence", state.PendingCleanups[0].Status)
	}
	select {
	case <-oldClosed:
		t.Fatal("old owner ran before explicit admission cancellation")
	default:
	}
	if err := first.cancelPendingDrainForAdmission(config); err != nil {
		t.Fatalf("explicit old-owner drain completion: %v", err)
	}
	select {
	case <-oldClosed:
	default:
		t.Fatal("explicit old-owner drain completion did not execute callback")
	}
	state, err = runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read post-cleanup state: %v", err)
	}
	if len(state.PendingCleanups) != 0 {
		t.Fatalf("old cleanup evidence remained after owner callback: %+v", state.PendingCleanups)
	}
	if state.DrainStatus != runtimeGoplsRootCohortDrainActive || state.CompletionReceipt == "" {
		t.Fatalf("post-cleanup state = %+v, want active new epoch with completion receipt", state)
	}
	if err := next.Release(); err != nil {
		t.Fatalf("release new epoch: %v", err)
	}
}

func TestRuntimeDurableGoplsRootCohortUnreachableOldOwnerRetainsCleanupEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gopls auto daemon root cohorts are unsupported on Windows")
	}
	cacheRoot := t.TempDir()
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatalf("secure test cache root: %v", err)
	}
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	drainWindow := time.Hour
	firstValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(drainWindow)
	if err != nil {
		t.Fatalf("new first durable controller: %v", err)
	}
	secondValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(drainWindow)
	if err != nil {
		t.Fatalf("new second durable controller: %v", err)
	}
	first := firstValue.(*runtimeServerDurableGoplsRootCohortController)
	second := secondValue.(*runtimeServerDurableGoplsRootCohortController)
	config := runtimeDurableGoplsRootCohortTestConfig("cross-owner-unreachable")
	t.Cleanup(func() {
		closeDurableGoplsRootCohortController(t, first)
		closeDurableGoplsRootCohortController(t, second)
	})
	lease, err := first.AcquireLease(config)
	if err != nil {
		t.Fatalf("first AcquireLease: %v", err)
	}
	oldClosed := make(chan struct{}, 1)
	releaseStarted := time.Now()
	if err := lease.ReleaseWithOwner(func() error {
		oldClosed <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("first ReleaseWithOwner: %v", err)
	}
	// Simulate the old sidecar disappearing before its in-memory callback can
	// run. The durable record remains, but the new sidecar has no authority to
	// invoke that callback.
	first.drainMu.Lock()
	delete(first.pendingOwner, config.RepositoryInstanceProof.CanonicalRootDigest)
	first.drainMu.Unlock()
	next, err := second.AcquireLease(config)
	if err != nil {
		t.Fatalf("new admission with unreachable old owner: %v", err)
	}
	if next.Fence().Epoch <= lease.Fence().Epoch {
		t.Fatalf("new epoch = %d, want > old epoch %d", next.Fence().Epoch, lease.Fence().Epoch)
	}
	dir := runtimeServerGoplsRootCohortDir(second.root, config)
	state, err := runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read retained cleanup state: %v", err)
	}
	if state.DrainStatus != runtimeGoplsRootCohortDrainActive || len(state.PendingCleanups) != 1 {
		t.Fatalf("unreachable-owner state = %+v, want active new epoch plus pending evidence", state)
	}
	if state.PendingCleanups[0].IdleDeadlineUnixNano < releaseStarted.Add(drainWindow).UnixNano() {
		t.Fatalf("unreachable-owner deadline = %d, want deadline from release start %d", state.PendingCleanups[0].IdleDeadlineUnixNano, releaseStarted.Add(drainWindow).UnixNano())
	}
	if state.PendingCleanups[0].Status != runtimeGoplsRootCohortDrainCleanupPending {
		t.Fatalf("unreachable-owner cleanup status = %q, want cleanup_pending evidence", state.PendingCleanups[0].Status)
	}
	if snapshot, ok := second.Snapshot(config); !ok || snapshot.State != multilsp.GoplsRootCohortStateCleanupPending || snapshot.ActiveMembers != 1 {
		t.Fatalf("snapshot = (%+v, %v), want cleanup_pending projection with new member", snapshot, ok)
	}
	select {
	case <-oldClosed:
		t.Fatal("new sidecar or stale worker invoked unreachable old owner callback")
	default:
	}
	if err := next.Release(); err != nil {
		t.Fatalf("release new epoch: %v", err)
	}
}

func TestRuntimeGoplsRootCohortClientDelaysForwarderCloseUntilDurableDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gopls auto daemon root cohorts are unsupported on Windows")
	}
	cacheRoot := t.TempDir()
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatalf("secure test cache root: %v", err)
	}
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	drainWindow := time.Hour
	controllerValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(drainWindow)
	if err != nil {
		t.Fatalf("new durable controller: %v", err)
	}
	controller := controllerValue.(*runtimeServerDurableGoplsRootCohortController)
	config := runtimeDurableGoplsRootCohortTestConfig("forwarder-close")
	t.Cleanup(func() { closeDurableGoplsRootCohortController(t, controller) })
	lease, err := controller.AcquireLease(config)
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	forwarder := &durableForwarderCloseProbe{closed: make(chan struct{}, 1)}
	client := &goplsRootCohortClient{Client: forwarder, lease: &lease}
	releaseStarted := time.Now()
	if err := client.Close(); err != nil {
		t.Fatalf("goplsRootCohortClient.Close: %v", err)
	}
	if got := forwarder.closes.Load(); got != 0 {
		t.Fatalf("forwarder Close calls before persisted deadline = %d, want 0", got)
	}
	dir := runtimeServerGoplsRootCohortDir(controller.root, config)
	state, err := runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read draining state: %v", err)
	}
	if state.DrainStatus != runtimeGoplsRootCohortDrainDraining || state.IdleDeadlineUnixNano < releaseStarted.Add(drainWindow).UnixNano() {
		t.Fatalf("draining state = %+v, want durable deadline from release start", state)
	}
	select {
	case <-forwarder.closed:
		t.Fatal("forwarder Close ran before explicit admission cancellation")
	default:
	}
	if err := controller.cancelPendingDrainForAdmission(config); err != nil {
		t.Fatalf("explicit forwarder drain completion: %v", err)
	}
	select {
	case <-forwarder.closed:
	default:
		t.Fatal("explicit forwarder drain completion did not close forwarder")
	}
	if got := forwarder.closes.Load(); got != 1 {
		t.Fatalf("forwarder Close calls after explicit completion = %d, want exactly one", got)
	}
	state, err = runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read completion receipt: %v", err)
	}
	if state.DrainStatus != runtimeGoplsRootCohortDrainCompleted || state.CompletionReceipt == "" {
		t.Fatalf("completion state = %+v, want receipt (daemon listen self-exit remains N/V)", state)
	}
	next, err := controller.AcquireLease(config)
	if err != nil {
		t.Fatalf("subsequent admission after completion: %v", err)
	}
	if next.Fence().Epoch <= lease.Fence().Epoch {
		t.Fatalf("restart admission epoch = %d, want > %d", next.Fence().Epoch, lease.Fence().Epoch)
	}
	if err := next.Release(); err != nil {
		t.Fatalf("release restart admission: %v", err)
	}
}

// closeDurableGoplsRootCohortController 通过生产 Close 完成测试 teardown，并暴露任何 pending owner 错误。
func closeDurableGoplsRootCohortController(t testing.TB, c *runtimeServerDurableGoplsRootCohortController) {
	t.Helper()
	if c == nil {
		return
	}
	if err := c.Close(); err != nil {
		t.Errorf("close durable gopls root cohort controller: %v", err)
	}
}

type durableForwarderCloseProbe struct {
	multilsp.Client
	closes atomic.Int32
	closed chan struct{}
}

func (p *durableForwarderCloseProbe) Close() error {
	if p.closes.Add(1) == 1 {
		p.closed <- struct{}{}
	}
	return nil
}

func runtimeDurableGoplsRootCohortTestConfig(suffix string) multilsp.GoplsRootCohortConfig {
	proof := multilsp.GoplsRepositoryInstanceProof{
		CanonicalRootDigest: "canonical-root-digest",
		FilesystemIdentity:  "dev:1:ino:2",
		GitMarkerDigest:     "git-marker-digest",
		InstanceNonce:       "instance-nonce",
	}
	return multilsp.GoplsRootCohortConfig{
		CohortID:                "sdmcp2-test-" + suffix,
		RepositoryInstanceProof: proof,
		EffectiveConfigDigest:   "effective-" + suffix,
	}
}
