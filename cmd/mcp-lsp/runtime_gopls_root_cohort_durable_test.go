package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
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

func TestRuntimeDurableGoplsRootCohortIdleDrainCompletionAndAdmissionCancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gopls auto daemon root cohorts are unsupported on Windows")
	}
	t.Run("completion receipt", func(t *testing.T) {
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
		config := runtimeDurableGoplsRootCohortTestConfig("drain-complete")
		lease, err := controller.AcquireLease(config)
		if err != nil {
			t.Fatalf("AcquireLease: %v", err)
		}
		closed := make(chan struct{}, 1)
		if err := lease.ReleaseWithOwner(func() error {
			closed <- struct{}{}
			return nil
		}); err != nil {
			t.Fatalf("ReleaseWithOwner: %v", err)
		}
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("idle drain owner did not execute")
		}
		dir := runtimeServerGoplsRootCohortDir(controller.root, config)
		var state *runtimeServerDurableGoplsRootCohortState
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			state, err = runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
			if err == nil && state.DrainStatus == runtimeGoplsRootCohortDrainCompleted {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
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
		if err := next.ReleaseWithOwner(func() error { return nil }); err != nil {
			t.Fatalf("release subsequent lease: %v", err)
		}
	})

	t.Run("new admission cancels pending drain", func(t *testing.T) {
		cacheRoot := t.TempDir()
		if err := os.Chmod(cacheRoot, 0o700); err != nil {
			t.Fatalf("secure test cache root: %v", err)
		}
		t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
		controllerValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(time.Second)
		if err != nil {
			t.Fatalf("new durable controller: %v", err)
		}
		controller := controllerValue.(*runtimeServerDurableGoplsRootCohortController)
		config := runtimeDurableGoplsRootCohortTestConfig("drain-cancel")
		first, err := controller.AcquireLease(config)
		if err != nil {
			t.Fatalf("first AcquireLease: %v", err)
		}
		closed := make(chan struct{}, 1)
		if err := first.ReleaseWithOwner(func() error {
			closed <- struct{}{}
			return nil
		}); err != nil {
			t.Fatalf("first ReleaseWithOwner: %v", err)
		}
		second, err := controller.AcquireLease(config)
		if err != nil {
			t.Fatalf("new admission: %v", err)
		}
		if second.Fence().Epoch <= first.Fence().Epoch {
			t.Fatalf("new admission fence epoch = %d, want > %d after drain cancellation", second.Fence().Epoch, first.Fence().Epoch)
		}
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("new admission did not synchronously drain the prior owner")
		}
		if err := second.ReleaseWithOwner(func() error { return nil }); err != nil {
			t.Fatalf("second ReleaseWithOwner: %v", err)
		}
	})
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

func TestRuntimeDurableGoplsRootCohortCrossControllerOwnerUnavailableStaysPending(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gopls auto daemon root cohorts are unsupported on Windows")
	}
	cacheRoot := t.TempDir()
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatalf("secure test cache root: %v", err)
	}
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	firstValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(time.Second)
	if err != nil {
		t.Fatalf("new first durable controller: %v", err)
	}
	secondValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(time.Second)
	if err != nil {
		t.Fatalf("new second durable controller: %v", err)
	}
	first := firstValue.(*runtimeServerDurableGoplsRootCohortController)
	second := secondValue.(*runtimeServerDurableGoplsRootCohortController)
	config := runtimeDurableGoplsRootCohortTestConfig("cross-owner")
	lease, err := first.AcquireLease(config)
	if err != nil {
		t.Fatalf("first AcquireLease: %v", err)
	}
	if err := lease.ReleaseWithOwner(func() error { return nil }); err != nil {
		t.Fatalf("first ReleaseWithOwner: %v", err)
	}
	if _, err := second.AcquireLease(config); !errors.Is(err, multilsp.ErrGoplsRootCohortDrainCleanupPending) {
		t.Fatalf("cross-controller admission error = %v, want CleanupPending while owner callback is unreachable", err)
	}
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
