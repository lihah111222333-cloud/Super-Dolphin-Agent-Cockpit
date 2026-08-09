//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"golang.org/x/sync/errgroup"
)

func TestRuntimeGoplsRootCohortRetiresPreBootButKeepsLiveCleanupEvidence(t *testing.T) {
	currentIdentity, err := hiddenexec.ProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatalf("read current process identity: %v", err)
	}
	config := runtimeDurableGoplsRootCohortTestConfig("reboot-boundary")
	state := runtimeServerDurableGoplsRootCohortState{}
	runtimeServerInitializeGoplsRootCohortState(&state, config)
	state.DrainStatus = runtimeGoplsRootCohortDrainCleanupPending
	state.IdleDeadlineUnixNano = 1
	state.DrainEpoch = 1
	state.OwnerPID = 999999
	state.OwnerStartIdentity = "1.0"
	state.OwnerMemberID = "pre-boot-current-member"
	state.OwnerJournalRevision = 1
	state.OwnerMemberGeneration = 1
	state.OwnerLeaseID = "pre-boot-current-lease"
	state.LastDrainError = "historical cleanup failure"
	state.DrainRetryUnixNano = 1
	state.PendingCleanups = []runtimeGoplsRootCohortCleanupEvidence{
		runtimeGoplsRootCohortCleanupEvidenceForBootTest("pre-boot-pending", "1.0"),
		runtimeGoplsRootCohortCleanupEvidenceForBootTest("current-boot-pending", currentIdentity),
	}
	state.PendingCleanups[1].OwnerPID = os.Getpid()

	if err := runtimeServerRetireStaleGoplsRootCohortCleanupEvidence(&state); err != nil {
		t.Fatalf("retire pre-boot cleanup evidence: %v", err)
	}
	if state.DrainStatus != runtimeGoplsRootCohortDrainActive || state.OwnerPID != 0 || state.OwnerStartIdentity != "" || state.OwnerLeaseID != "" {
		t.Fatalf("pre-boot current drain was not cleared: %+v", state)
	}
	if len(state.PendingCleanups) != 1 || state.PendingCleanups[0].Fence.LeaseID != "current-boot-pending" {
		t.Fatalf("pending cleanups = %+v, want only current-boot evidence", state.PendingCleanups)
	}
}

func TestRuntimeGoplsRootCohortRetiresKilledSameBootOwner(t *testing.T) {
	process := exec.Command("sleep", "30")
	if err := process.Start(); err != nil {
		t.Fatalf("start same-boot owner fixture: %v", err)
	}
	t.Cleanup(func() {
		if process.Process != nil {
			_ = process.Process.Kill()
		}
		_ = process.Wait()
	})
	ownerPID := process.Process.Pid
	ownerStart, err := hiddenexec.ProcessStartIdentity(ownerPID)
	if err != nil {
		t.Fatalf("capture same-boot owner fixture: %v", err)
	}
	if err := process.Process.Kill(); err != nil {
		t.Fatalf("kill same-boot owner fixture: %v", err)
	}
	if err := process.Wait(); err == nil {
		t.Fatal("killed same-boot owner fixture exited successfully")
	}

	state := runtimeGoplsRootCohortRecoveryTestState(t, "same-boot-killed-owner")
	state.PendingCleanups = []runtimeGoplsRootCohortCleanupEvidence{
		runtimeGoplsRootCohortCleanupEvidenceForBootTest("same-boot-killed-owner", ownerStart),
	}
	state.PendingCleanups[0].OwnerPID = ownerPID
	if err := runtimeServerRetireStaleGoplsRootCohortCleanupEvidence(&state); err != nil {
		t.Fatalf("retire killed same-boot owner: %v", err)
	}
	if len(state.PendingCleanups) != 0 {
		t.Fatalf("killed same-boot owner remained: %+v", state.PendingCleanups)
	}
}

func TestRuntimeGoplsRootCohortRetiresReusedPIDOwnerIdentity(t *testing.T) {
	currentIdentity, err := hiddenexec.ProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatalf("read current process identity: %v", err)
	}
	reusedIdentity := fmt.Sprintf("%d.0", time.Now().Add(time.Hour).Unix())
	if reusedIdentity == currentIdentity {
		t.Fatal("PID reuse fixture unexpectedly matches the current process identity")
	}
	state := runtimeGoplsRootCohortRecoveryTestState(t, "pid-reuse")
	state.PendingCleanups = []runtimeGoplsRootCohortCleanupEvidence{
		runtimeGoplsRootCohortCleanupEvidenceForBootTest("pid-reuse", reusedIdentity),
	}
	state.PendingCleanups[0].OwnerPID = os.Getpid()
	if err := runtimeServerRetireStaleGoplsRootCohortCleanupEvidence(&state); err != nil {
		t.Fatalf("retire reused PID owner: %v", err)
	}
	if len(state.PendingCleanups) != 0 {
		t.Fatalf("reused PID evidence remained: %+v", state.PendingCleanups)
	}
}

func TestRuntimeGoplsRootCohortRejectsMalformedBootIdentity(t *testing.T) {
	config := runtimeDurableGoplsRootCohortTestConfig("malformed-reboot-boundary")
	state := runtimeServerDurableGoplsRootCohortState{}
	runtimeServerInitializeGoplsRootCohortState(&state, config)
	state.PendingCleanups = []runtimeGoplsRootCohortCleanupEvidence{
		runtimeGoplsRootCohortCleanupEvidenceForBootTest("malformed-owner", "not-a-start-token"),
	}
	if err := runtimeServerRetireStaleGoplsRootCohortCleanupEvidence(&state); err == nil {
		t.Fatal("malformed owner identity unexpectedly retired cleanup evidence")
	}
	if len(state.PendingCleanups) != 1 {
		t.Fatalf("malformed cleanup evidence mutated on failure: %+v", state.PendingCleanups)
	}
}

func TestRuntimeGoplsRootCohortMalformedEvidenceFailsAtomically(t *testing.T) {
	currentIdentity, err := hiddenexec.ProcessStartIdentity(os.Getpid())
	if err != nil {
		t.Fatalf("read current process identity: %v", err)
	}
	state := runtimeGoplsRootCohortRecoveryTestState(t, "atomic-malformed")
	state.PendingCleanups = []runtimeGoplsRootCohortCleanupEvidence{
		runtimeGoplsRootCohortCleanupEvidenceForBootTest("pre-boot-would-retire", "1.0"),
		runtimeGoplsRootCohortCleanupEvidenceForBootTest("malformed", "bad-token"),
		runtimeGoplsRootCohortCleanupEvidenceForBootTest("live", currentIdentity),
	}
	before := append([]runtimeGoplsRootCohortCleanupEvidence(nil), state.PendingCleanups...)
	if err := runtimeServerRetireStaleGoplsRootCohortCleanupEvidence(&state); err == nil {
		t.Fatal("mixed malformed evidence unexpectedly succeeded")
	}
	if fmt.Sprintf("%+v", state.PendingCleanups) != fmt.Sprintf("%+v", before) {
		t.Fatalf("cleanup evidence mutated before fail-fast: before=%+v after=%+v", before, state.PendingCleanups)
	}
}

func TestRuntimeGoplsRootCohortRejectsIncompleteCurrentOwnerEvidence(t *testing.T) {
	state := runtimeGoplsRootCohortRecoveryTestState(t, "incomplete-current-owner")
	state.OwnerPID = 0
	state.OwnerStartIdentity = fmt.Sprintf("%d.0", time.Now().Unix())
	if err := runtimeServerRetireStaleGoplsRootCohortCleanupEvidence(&state); err == nil {
		t.Fatal("incomplete current owner evidence unexpectedly retired")
	}
	if state.OwnerStartIdentity == "" {
		t.Fatal("incomplete current owner evidence was mutated on failure")
	}
}

func TestRuntimeGoplsRootCohortConcurrentAdmissionsRotateStaleJournalOnce(t *testing.T) {
	cacheRoot := t.TempDir()
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatalf("secure recovery cache root: %v", err)
	}
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	controllers := newConcurrentGoplsRecoveryControllers(t, 2)
	oldConfig := runtimeDurableGoplsRootCohortTestConfig("concurrent-old")
	newConfig := oldConfig
	newConfig.CohortID = "sdmcp2-test-concurrent-new"
	newConfig.EffectiveConfigDigest = "effective-concurrent-new"
	dir := writeConcurrentGoplsRecoveryState(t, cacheRoot, oldConfig)
	leases := acquireConcurrentGoplsRecoveryLeases(t, controllers, newConfig)
	cleanupConcurrentGoplsRecoveryLeases(t, leases)
	stored, err := runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read concurrent recovery state: %v", err)
	}
	if stored.Config.CohortID != newConfig.CohortID || len(stored.PendingCleanups) != 0 || stored.Epoch != 2 {
		t.Fatalf("concurrent recovery state = %+v, want one rotation to epoch 2", stored)
	}
}

func newConcurrentGoplsRecoveryControllers(t *testing.T, count int) []*runtimeServerDurableGoplsRootCohortController {
	t.Helper()
	controllers := make([]*runtimeServerDurableGoplsRootCohortController, 0, count)
	for range count {
		value, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(time.Hour)
		if err != nil {
			t.Fatalf("new concurrent recovery controller: %v", err)
		}
		controller := value.(*runtimeServerDurableGoplsRootCohortController)
		controllers = append(controllers, controller)
		t.Cleanup(func() { closeDurableGoplsRootCohortController(t, controller) })
	}
	return controllers
}

func writeConcurrentGoplsRecoveryState(t *testing.T, cacheRoot string, config multilsp.GoplsRootCohortConfig) string {
	t.Helper()
	dir := runtimeServerGoplsRootCohortDir(cacheRoot, config)
	if err := runtimeServerEnsurePrivateDescendant(cacheRoot, dir); err != nil {
		t.Fatalf("create concurrent recovery cohort dir: %v", err)
	}
	state := runtimeServerDurableGoplsRootCohortState{}
	runtimeServerInitializeGoplsRootCohortState(&state, config)
	state.JournalRevision, state.NextMemberGeneration, state.NextSequence = 1, 1, 1
	state.PendingCleanups = []runtimeGoplsRootCohortCleanupEvidence{
		runtimeGoplsRootCohortCleanupEvidenceForBootTest("concurrent-pre-boot", "1.0"),
	}
	if err := runtimeServerWriteGoplsRootCohortState(filepath.Join(dir, "state.json"), state); err != nil {
		t.Fatalf("write concurrent recovery state: %v", err)
	}
	return dir
}

func acquireConcurrentGoplsRecoveryLeases(t *testing.T, controllers []*runtimeServerDurableGoplsRootCohortController, config multilsp.GoplsRootCohortConfig) []multilsp.GoplsRootCohortLease {
	t.Helper()
	start := make(chan struct{})
	leases := make([]multilsp.GoplsRootCohortLease, len(controllers))
	var group errgroup.Group
	for index, controller := range controllers {
		index, controller := index, controller
		group.Go(func() error {
			<-start
			lease, err := controller.AcquireLease(config)
			leases[index] = lease
			return err
		})
	}
	close(start)
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent recovery admission: %v", err)
	}
	return leases
}

func cleanupConcurrentGoplsRecoveryLeases(t *testing.T, leases []multilsp.GoplsRootCohortLease) {
	t.Helper()
	for _, lease := range leases {
		lease := lease
		t.Cleanup(func() {
			if err := lease.Release(); err != nil {
				t.Errorf("release concurrent recovery lease: %v", err)
			}
		})
	}
}

func TestRuntimeGoplsRootCohortCorruptJournalFailsWithoutRewrite(t *testing.T) {
	cacheRoot := t.TempDir()
	if err := os.Chmod(cacheRoot, 0o700); err != nil {
		t.Fatalf("secure corrupt-journal cache root: %v", err)
	}
	t.Setenv(agentLSPSharedCacheDirEnv, cacheRoot)
	value, err := runtimeServerNewDurableGoplsRootCohortControllerWithDrainWindow(time.Hour)
	if err != nil {
		t.Fatalf("new corrupt-journal controller: %v", err)
	}
	controller := value.(*runtimeServerDurableGoplsRootCohortController)
	t.Cleanup(func() { closeDurableGoplsRootCohortController(t, controller) })
	config := runtimeDurableGoplsRootCohortTestConfig("corrupt-journal")
	dir := runtimeServerGoplsRootCohortDir(cacheRoot, config)
	if err := runtimeServerEnsurePrivateDescendant(cacheRoot, dir); err != nil {
		t.Fatalf("create corrupt-journal cohort dir: %v", err)
	}
	statePath := filepath.Join(dir, "state.json")
	payload := []byte(`{"schema_version":1,"unknown_partial_field":true}`)
	if err := os.WriteFile(statePath, payload, 0o600); err != nil {
		t.Fatalf("write corrupt journal: %v", err)
	}
	if _, err := controller.AcquireLease(config); err == nil {
		t.Fatal("corrupt journal unexpectedly admitted a lease")
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read corrupt journal after refusal: %v", err)
	}
	if string(after) != string(payload) {
		t.Fatalf("corrupt journal was rewritten: before=%q after=%q", payload, after)
	}
}

func runtimeGoplsRootCohortRecoveryTestState(t *testing.T, suffix string) runtimeServerDurableGoplsRootCohortState {
	t.Helper()
	state := runtimeServerDurableGoplsRootCohortState{}
	runtimeServerInitializeGoplsRootCohortState(&state, runtimeDurableGoplsRootCohortTestConfig(suffix))
	return state
}

func runtimeGoplsRootCohortCleanupEvidenceForBootTest(leaseID, ownerStartIdentity string) runtimeGoplsRootCohortCleanupEvidence {
	return runtimeGoplsRootCohortCleanupEvidence{
		Fence: runtimeServerDurableGoplsRootCohortFence{
			Epoch:            1,
			JournalRevision:  1,
			MemberID:         leaseID + "-member",
			MemberGeneration: 1,
			LeaseID:          leaseID,
		},
		IdleDeadlineUnixNano: 1,
		OwnerPID:             999999,
		OwnerStartIdentity:   ownerStartIdentity,
		Status:               runtimeGoplsRootCohortDrainCleanupPending,
		RetryUnixNano:        1,
	}
}
