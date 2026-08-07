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

func TestRuntimeDurableGoplsRootCohortCrossControllerAdmissionFencesOldCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("gopls auto daemon root cohorts are unsupported on Windows")
	}
	cacheRoot := t.TempDir()
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatalf("secure test cache root: %v", err)
	}
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	firstValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(40 * time.Millisecond)
	if err != nil {
		t.Fatalf("new first durable controller: %v", err)
	}
	secondValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(40 * time.Millisecond)
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
	oldClosed := make(chan struct{}, 1)
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
	if state.PendingCleanups[0].Fence.toValue() != lease.Fence() {
		t.Fatalf("old cleanup fence = %+v, want %v", state.PendingCleanups[0].Fence, lease.Fence())
	}
	if state.PendingCleanups[0].Status != runtimeGoplsRootCohortDrainCleanupPending {
		t.Fatalf("old cleanup status = %q, want cleanup_pending evidence", state.PendingCleanups[0].Status)
	}
	select {
	case <-oldClosed:
	case <-time.After(time.Second):
		t.Fatal("old owner did not clean its own forwarder at persisted deadline")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, err = runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
		if err == nil && len(state.PendingCleanups) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("read post-cleanup state: %v", err)
	}
	if len(state.PendingCleanups) != 0 {
		t.Fatalf("old cleanup evidence remained after owner callback: %+v", state.PendingCleanups)
	}
	if err := next.ReleaseWithOwner(func() error { return nil }); err != nil {
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
	firstValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(25 * time.Millisecond)
	if err != nil {
		t.Fatalf("new first durable controller: %v", err)
	}
	secondValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(25 * time.Millisecond)
	if err != nil {
		t.Fatalf("new second durable controller: %v", err)
	}
	first := firstValue.(*runtimeServerDurableGoplsRootCohortController)
	second := secondValue.(*runtimeServerDurableGoplsRootCohortController)
	config := runtimeDurableGoplsRootCohortTestConfig("cross-owner-unreachable")
	lease, err := first.AcquireLease(config)
	if err != nil {
		t.Fatalf("first AcquireLease: %v", err)
	}
	oldClosed := make(chan struct{}, 1)
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
	deadline := time.Now().Add(time.Second)
	var state *runtimeServerDurableGoplsRootCohortState
	for time.Now().Before(deadline) {
		state, err = runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
		if err == nil && len(state.PendingCleanups) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("read retained cleanup state: %v", err)
	}
	if state.DrainStatus != runtimeGoplsRootCohortDrainActive || len(state.PendingCleanups) != 1 {
		t.Fatalf("unreachable-owner state = %+v, want active new epoch plus pending evidence", state)
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
	case <-time.After(100 * time.Millisecond):
	}
	if err := next.ReleaseWithOwner(func() error { return nil }); err != nil {
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
	controllerValue, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(25 * time.Millisecond)
	if err != nil {
		t.Fatalf("new durable controller: %v", err)
	}
	controller := controllerValue.(*runtimeServerDurableGoplsRootCohortController)
	config := runtimeDurableGoplsRootCohortTestConfig("forwarder-close")
	lease, err := controller.AcquireLease(config)
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	forwarder := &durableForwarderCloseProbe{closed: make(chan struct{}, 1)}
	client := &goplsRootCohortClient{Client: forwarder, lease: &lease}
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
	if state.IdleDeadlineUnixNano <= time.Now().UnixNano() {
		t.Fatalf("idle deadline = %d, want future persisted deadline", state.IdleDeadlineUnixNano)
	}
	select {
	case <-forwarder.closed:
	case <-time.After(time.Second):
		t.Fatal("forwarder Close did not run at persisted deadline")
	}
	if got := forwarder.closes.Load(); got != 1 {
		t.Fatalf("forwarder Close calls after deadline = %d, want exactly one", got)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, err = runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
		if err == nil && state.DrainStatus == runtimeGoplsRootCohortDrainCompleted && state.CompletionReceipt != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
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
	if err := next.ReleaseWithOwner(func() error { return nil }); err != nil {
		t.Fatalf("release restart admission: %v", err)
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
