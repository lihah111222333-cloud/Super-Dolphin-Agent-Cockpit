//go:build windows && e2e

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

type windowsGoplsDurableLeaseSnapshot struct {
	path  string
	lease runtimeServerDurableGoplsRootCohortLease
}

type windowsGoplsCommittedCacheSnapshot struct {
	paths           []string
	cohortKeys      []string
	originalExists  bool
	enumerateErr    error
	cohortKeyErr    error
	originalStatErr error
	brokerAlive     bool
	brokerErr       error
	daemonAlive     bool
	daemonErr       error
}

const windowsGoplsStaleLeaseRecoveryIdle = 10 * time.Second

// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryWindowsCommittedDurableGoplsStaleLeaseRecoveryE2E(t *testing.T) {
	roots, targets := writeRealGoplsLinkedWorktreeFixtures(t)
	argsLog := filepath.Join(t.TempDir(), "gopls-args.log")
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	install := buildWindowsGoplsShortIdlePrecheckTestInstall(t)
	env := windowsGoplsSidecarEnv(t, install,
		fakeGoplsArgsLogEnv+"="+argsLog,
		"AGENT_LSP_SHARED_CACHE_DIR="+cacheRoot,
		"MCP_LSP_IDLE_TIMEOUT="+windowsGoplsStaleLeaseRecoveryIdle.String(),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	clientA := startWindowsGoplsMCPBinaryForTest(t, ctx, install.Binary, roots[0], filepath.Dir(install.Gopls), env)
	t.Cleanup(func() { clientA.close(t) })
	clientA.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	completionA := clientA.callTool(t, "completion", map[string]any{"pos": targets[0] + ":3:1"})
	requireMCPToolSuccess(t, clientA, completionA, "Windows committed stale-lease sidecar A completion")

	endpointA, daemonPIDA := requireSingleWindowsGoplsTopology(t, waitForFakeGoplsInvocations(t, argsLog, 2), install.Gopls, windowsGoplsStaleLeaseRecoveryIdle)
	recordPath, recordA := waitForWindowsGoplsCommittedRecordPath(t, cacheRoot)
	requireWindowsGoplsCommittedRecordIdentity(t, recordA, endpointA, daemonPIDA, windowsGoplsStaleLeaseRecoveryIdle)
	registerWindowsGoplsExactProcessCleanup(t, recordA)
	leaseA := waitForWindowsGoplsDurableLeaseCount(t, filepath.Dir(recordPath), 1, 5*time.Second)[0]
	stateA := readWindowsGoplsDurableState(t, filepath.Dir(recordPath))
	requireWindowsGoplsCommittedLease(t, leaseA.lease, stateA, recordA.ConfigDigest, clientA.cmd.Process.Pid)

	hardKillWindowsGoplsSidecarWithoutRelease(t, clientA, leaseA.lease.OwnerStartIdentity)
	stale := waitForWindowsGoplsDurableLeaseCount(t, filepath.Dir(recordPath), 1, time.Second)[0]
	requireWindowsGoplsStaleLeasePreserved(t, stale, leaseA)
	requireWindowsGoplsCommittedCacheSnapshot(t, cacheRoot, recordPath, recordA, "before sidecar B")

	clientB := startWindowsGoplsMCPBinaryForTest(t, ctx, install.Binary, roots[1], filepath.Dir(install.Gopls), env)
	t.Cleanup(func() { clientB.close(t) })
	clientB.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	completionB := clientB.callTool(t, "completion", map[string]any{"pos": targets[1] + ":3:1"})
	requireMCPToolSuccess(t, clientB, completionB, "Windows committed stale-lease sidecar B completion")
	requireWindowsGoplsCommittedCacheSnapshot(t, cacheRoot, recordPath, recordA, "after sidecar B completion")

	endpointB, daemonPIDB := requireWindowsGoplsTopologyWithIdle(t, waitForFakeGoplsInvocations(t, argsLog, 3), install.Gopls, windowsGoplsStaleLeaseRecoveryIdle)
	recordB := readWindowsGoplsBrokerRecord(t, recordPath)
	requireWindowsGoplsCommittedTopologyReused(t, recordA, recordB, endpointA, endpointB, daemonPIDA, daemonPIDB)
	leaseB := waitForWindowsGoplsDurableLeaseCount(t, filepath.Dir(recordPath), 1, 5*time.Second)[0]
	requireWindowsGoplsStaleLeaseReplaced(t, leaseA, leaseB, clientB.cmd.Process.Pid)

	clientB.close(t)
	waitForWindowsGoplsDurableLeaseCount(t, filepath.Dir(recordPath), 0, 5*time.Second)
	requireWindowsGoplsBrokerDaemonExactExit(t, recordA, windowsGoplsStaleLeaseRecoveryIdle+5*time.Second)
}

func requireWindowsGoplsStaleLeasePreserved(t *testing.T, stale, original windowsGoplsDurableLeaseSnapshot) {
	t.Helper()
	if stale.path != original.path || stale.lease.Fence != original.lease.Fence {
		t.Fatalf("sidecar A stale lease changed before recovery: path=%q want=%q", stale.path, original.path)
	}
}

func requireWindowsGoplsCommittedCacheSnapshot(t *testing.T, cacheRoot, recordPath string, want windowsGoplsBrokerRecordV2, phase string) {
	t.Helper()
	snapshot := inspectWindowsGoplsCommittedCache(cacheRoot, recordPath, want)
	snapshotErr := windowsGoplsCommittedCacheSnapshotErr(snapshot)
	t.Logf("Windows gopls committed cache %s: record_count=%d cohort_keys=%v original_exists=%t enumerate_error=%t key_error=%t stat_error=%t broker=%s alive=%t error=%t daemon=%s alive=%t error=%t",
		phase, len(snapshot.paths), snapshot.cohortKeys, snapshot.originalExists,
		snapshot.enumerateErr != nil, snapshot.cohortKeyErr != nil, snapshot.originalStatErr != nil,
		formatWindowsGoplsIdentity(windowsGoplsProvisionalIdentity{PID: want.OwnerPID, StartIdentity: want.OwnerStartIdentity}), snapshot.brokerAlive, snapshot.brokerErr != nil,
		formatWindowsGoplsIdentity(windowsGoplsProvisionalIdentity{PID: want.DaemonPID, StartIdentity: want.DaemonStartIdentity}), snapshot.daemonAlive, snapshot.daemonErr != nil)
	if snapshotErr != nil {
		t.Fatal("inspect Windows gopls committed cache snapshot")
	}
	if !windowsGoplsCommittedCacheRetainedOriginal(snapshot, recordPath) {
		t.Fatalf("Windows gopls committed cache %s did not retain one original record: record_count=%d cohort_keys=%v original_exists=%t",
			phase, len(snapshot.paths), snapshot.cohortKeys, snapshot.originalExists)
	}
	record, err := runtimeServerReadWindowsGoplsDaemonRecord(snapshot.paths[0])
	t.Logf("Windows gopls committed record %s: owner=%s daemon=%s endpoint_match=%t config_match=%t idle=%s read_error=%t",
		phase,
		formatWindowsGoplsIdentity(windowsGoplsProvisionalIdentity{PID: record.OwnerPID, StartIdentity: record.OwnerStartIdentity}),
		formatWindowsGoplsIdentity(windowsGoplsProvisionalIdentity{PID: record.DaemonPID, StartIdentity: record.DaemonStartIdentity}),
		record.Endpoint == want.Endpoint, record.ConfigDigest == want.ConfigDigest,
		time.Duration(record.IdleTimeoutNanos), err != nil)
	if err != nil {
		t.Fatal("read unique Windows gopls committed record")
	}
	if !windowsGoplsCommittedRecordMatches(record, want) {
		t.Fatalf("Windows gopls committed cache %s did not preserve the original record", phase)
	}
	if !windowsGoplsCommittedCacheSnapshotLive(snapshot) {
		t.Fatalf("Windows gopls committed cache %s did not preserve the original live topology", phase)
	}
}

func windowsGoplsCommittedCacheSnapshotErr(snapshot windowsGoplsCommittedCacheSnapshot) error {
	return errors.Join(snapshot.enumerateErr, snapshot.cohortKeyErr, snapshot.originalStatErr, snapshot.brokerErr, snapshot.daemonErr)
}

func windowsGoplsCommittedCacheRetainedOriginal(snapshot windowsGoplsCommittedCacheSnapshot, recordPath string) bool {
	if len(snapshot.paths) != 1 {
		return false
	}
	return snapshot.originalExists && windowsGoplsPathsEqual(snapshot.paths[0], recordPath)
}

func windowsGoplsCommittedCacheSnapshotLive(snapshot windowsGoplsCommittedCacheSnapshot) bool {
	return snapshot.brokerAlive && snapshot.daemonAlive
}

func inspectWindowsGoplsCommittedCache(cacheRoot, recordPath string, record windowsGoplsBrokerRecordV2) windowsGoplsCommittedCacheSnapshot {
	paths, enumerateErr := windowsGoplsBrokerRecordPaths(cacheRoot)
	cohortKeys, cohortKeyErr := windowsGoplsRelativeCohortKeys(cacheRoot, paths)
	originalExists, originalStatErr := windowsGoplsPathExists(recordPath)
	broker, daemon := windowsGoplsBrokerDaemonIdentities(record)
	brokerAlive, brokerErr := windowsGoplsExactIdentityAlive(broker)
	daemonAlive, daemonErr := windowsGoplsExactIdentityAlive(daemon)
	return windowsGoplsCommittedCacheSnapshot{
		paths: paths, cohortKeys: cohortKeys, originalExists: originalExists,
		enumerateErr: enumerateErr, cohortKeyErr: cohortKeyErr, originalStatErr: originalStatErr,
		brokerAlive: brokerAlive, brokerErr: brokerErr, daemonAlive: daemonAlive, daemonErr: daemonErr,
	}
}

func windowsGoplsRelativeCohortKeys(cacheRoot string, paths []string) ([]string, error) {
	keys := make([]string, 0, len(paths))
	for _, path := range paths {
		relative, err := filepath.Rel(cacheRoot, filepath.Dir(path))
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, errors.Join(err, errors.New("Windows gopls daemon record escaped cache root"))
		}
		keys = append(keys, filepath.ToSlash(relative))
	}
	return keys, nil
}

func windowsGoplsPathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func windowsGoplsPathsEqual(first, second string) bool {
	return strings.EqualFold(filepath.Clean(first), filepath.Clean(second))
}

func windowsGoplsCommittedRecordMatches(record runtimeServerWindowsGoplsDaemonRecord, want windowsGoplsBrokerRecordV2) bool {
	return record.OwnerPID == want.OwnerPID && record.OwnerStartIdentity == want.OwnerStartIdentity &&
		record.DaemonPID == want.DaemonPID && record.DaemonStartIdentity == want.DaemonStartIdentity &&
		record.Endpoint == want.Endpoint && record.ConfigDigest == want.ConfigDigest &&
		record.IdleTimeoutNanos == want.IdleTimeoutNanos
}

func requireWindowsGoplsCommittedTopologyReused(
	t *testing.T,
	previous, current windowsGoplsBrokerRecordV2,
	previousEndpoint, currentEndpoint string,
	previousDaemonPID, currentDaemonPID int,
) {
	t.Helper()
	if currentEndpoint != previousEndpoint || currentDaemonPID != previousDaemonPID || current.OwnerPID != previous.OwnerPID ||
		current.DaemonPID != previous.DaemonPID || current.Endpoint != previous.Endpoint {
		t.Fatalf("sidecar B did not reuse committed broker/daemon/endpoint: endpoint_reused=%t daemon_reused=%t broker_reused=%t",
			currentEndpoint == previousEndpoint && current.Endpoint == previous.Endpoint,
			currentDaemonPID == previousDaemonPID && current.DaemonPID == previous.DaemonPID,
			current.OwnerPID == previous.OwnerPID)
	}
}

func requireWindowsGoplsStaleLeaseReplaced(t *testing.T, previous, current windowsGoplsDurableLeaseSnapshot, ownerPID int) {
	t.Helper()
	if current.path == previous.path || current.lease.OwnerPID != ownerPID {
		t.Fatalf("sidecar B lease did not replace stale sidecar A lease: same_path=%t owner_pid=%d want=%d",
			current.path == previous.path, current.lease.OwnerPID, ownerPID)
	}
	requireWindowsGoplsFenceMonotonic(t, previous.lease.Fence, current.lease.Fence)
	if _, err := os.Lstat(previous.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sidecar A stale lease was not retired: %v", err)
	}
}

func requireSingleWindowsGoplsTopology(t *testing.T, invocations [][]string, executable string, idle time.Duration) (string, int) {
	t.Helper()
	daemon, forwarders := splitWindowsGoplsInvocations(invocations)
	endpoint, daemonPID := requireWindowsGoplsDaemonInvocationWithIdle(t, daemon, executable, idle)
	if len(forwarders) != 1 || windowsGoplsArg(forwarders[0], "-remote=") != endpoint {
		t.Fatalf("initial Windows gopls topology has %d forwarders, want one on committed endpoint", len(forwarders))
	}
	requireWindowsGoplsInvocationExecutable(t, forwarders[0], executable, "forwarder")
	return endpoint, daemonPID
}

func waitForWindowsGoplsCommittedRecordPath(t *testing.T, cacheRoot string) (string, windowsGoplsBrokerRecordV2) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		paths, err := windowsGoplsBrokerRecordPaths(cacheRoot)
		if err != nil {
			t.Fatalf("find committed Windows gopls broker record: %v", err)
		}
		if len(paths) == 1 {
			return paths[0], readWindowsGoplsBrokerRecord(t, paths[0])
		}
		if len(paths) > 1 || time.Now().After(deadline) {
			t.Fatalf("committed Windows gopls broker record count = %d, want one", len(paths))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func requireWindowsGoplsCommittedRecordIdentity(t *testing.T, record windowsGoplsBrokerRecordV2, endpoint string, daemonPID int, idle time.Duration) {
	t.Helper()
	if record.SchemaVersion != runtimeServerWindowsGoplsDaemonSchema || record.ConfigDigest == "" ||
		record.Endpoint != endpoint || record.OwnerPID <= 1 || record.DaemonPID != daemonPID ||
		record.OwnerPID == record.DaemonPID || record.OwnerStartIdentity == "" || record.DaemonStartIdentity == "" ||
		record.IdleTimeoutNanos != idle.Nanoseconds() {
		t.Fatalf("committed Windows gopls record identity invalid: schema=%d config_set=%t endpoint_match=%t owner_pid=%d daemon_pid=%d idle=%s want=%s",
			record.SchemaVersion, record.ConfigDigest != "", record.Endpoint == endpoint, record.OwnerPID, record.DaemonPID,
			time.Duration(record.IdleTimeoutNanos), idle)
	}
}

func registerWindowsGoplsExactProcessCleanup(t *testing.T, record windowsGoplsBrokerRecordV2) {
	t.Helper()
	broker, daemon := windowsGoplsBrokerDaemonIdentities(record)
	t.Cleanup(func() {
		cleanupWindowsGoplsExactProcess(t, broker, "broker")
		cleanupWindowsGoplsExactProcess(t, daemon, "daemon")
	})
}

func windowsGoplsBrokerDaemonIdentities(record windowsGoplsBrokerRecordV2) (windowsGoplsProvisionalIdentity, windowsGoplsProvisionalIdentity) {
	return windowsGoplsProvisionalIdentity{PID: record.OwnerPID, StartIdentity: record.OwnerStartIdentity},
		windowsGoplsProvisionalIdentity{PID: record.DaemonPID, StartIdentity: record.DaemonStartIdentity}
}

func requireWindowsGoplsBrokerDaemonExactExit(t *testing.T, record windowsGoplsBrokerRecordV2, timeout time.Duration) {
	t.Helper()
	broker, daemon := windowsGoplsBrokerDaemonIdentities(record)
	deadline := time.Now().Add(timeout)
	for {
		brokerAlive, brokerErr := windowsGoplsExactIdentityAlive(broker)
		daemonAlive, daemonErr := windowsGoplsExactIdentityAlive(daemon)
		if brokerErr != nil || daemonErr != nil {
			t.Fatalf("inspect Windows gopls idle convergence: broker=%s alive=%t err=%v daemon=%s alive=%t err=%v",
				formatWindowsGoplsIdentity(broker), brokerAlive, brokerErr,
				formatWindowsGoplsIdentity(daemon), daemonAlive, daemonErr)
		}
		if !brokerAlive && !daemonAlive {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Windows gopls topology did not converge after %s: broker=%s alive=%t daemon=%s alive=%t",
				timeout, formatWindowsGoplsIdentity(broker), brokerAlive,
				formatWindowsGoplsIdentity(daemon), daemonAlive)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func cleanupWindowsGoplsExactProcess(t *testing.T, identity windowsGoplsProvisionalIdentity, label string) {
	t.Helper()
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(identity.PID))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return
	}
	if err != nil {
		t.Errorf("open exact Windows gopls %s cleanup target %s: %v", label, formatWindowsGoplsIdentity(identity), err)
		return
	}
	defer closeWindowsGoplsSidecarHandle(t, handle)
	if err := terminateWindowsGoplsExactHandle(handle, identity.PID, identity.StartIdentity); err != nil {
		t.Errorf("cleanup exact Windows gopls %s %s: %v", label, formatWindowsGoplsIdentity(identity), err)
	}
}

func waitForWindowsGoplsDurableLeaseCount(t *testing.T, dir string, want int, timeout time.Duration) []windowsGoplsDurableLeaseSnapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		snapshots, err := readWindowsGoplsDurableLeases(dir)
		if err == nil && len(snapshots) == want {
			return snapshots
		}
		if time.Now().After(deadline) {
			t.Fatalf("durable Windows gopls lease count did not converge: got=%d want=%d err=%v", len(snapshots), want, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readWindowsGoplsDurableLeases(dir string) ([]windowsGoplsDurableLeaseSnapshot, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var snapshots []windowsGoplsDurableLeaseSnapshot
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "lease-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		lease, err := runtimeServerReadGoplsRootCohortLease(path)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, windowsGoplsDurableLeaseSnapshot{path: path, lease: lease})
	}
	return snapshots, nil
}

func readWindowsGoplsDurableState(t *testing.T, dir string) *runtimeServerDurableGoplsRootCohortState {
	t.Helper()
	state, err := runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read committed Windows gopls durable state: %v", err)
	}
	return state
}

func requireWindowsGoplsCommittedLease(t *testing.T, lease runtimeServerDurableGoplsRootCohortLease, state *runtimeServerDurableGoplsRootCohortState, configDigest string, ownerPID int) {
	t.Helper()
	if lease.ConfigDigest != configDigest || lease.ConfigDigest != state.ConfigDigest || lease.OwnerPID != ownerPID ||
		lease.Fence.Epoch != state.Epoch || lease.Fence.JournalRevision != state.JournalRevision {
		t.Fatalf("Windows gopls lease was not committed with state: config_match=%t owner_pid=%d want=%d epoch=%d/%d revision=%d/%d",
			lease.ConfigDigest == configDigest && lease.ConfigDigest == state.ConfigDigest, lease.OwnerPID, ownerPID,
			lease.Fence.Epoch, state.Epoch, lease.Fence.JournalRevision, state.JournalRevision)
	}
}

func hardKillWindowsGoplsSidecarWithoutRelease(t *testing.T, client *mcpLSPBinaryClient, expectedStartIdentity string) {
	t.Helper()
	command, closeJob := takeWindowsGoplsSidecarForHardKill(t, client, expectedStartIdentity)
	jobClosed := false
	defer closeWindowsGoplsSidecarJobAfterFailure(t, closeJob, &jobClosed)

	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(command.Process.Pid))
	if err != nil {
		t.Fatalf("open exact Windows gopls sidecar for hard kill: %v", err)
	}
	defer closeWindowsGoplsSidecarHandle(t, handle)
	if err := terminateWindowsGoplsExactHandle(handle, command.Process.Pid, expectedStartIdentity); err != nil {
		t.Fatal(err)
	}
	if err := reapWindowsGoplsSidecar(command, client); err != nil {
		t.Fatalf("reap exact Windows gopls sidecar: %v", err)
	}
	if err := closeJob(); err != nil {
		t.Fatalf("close Windows gopls sidecar Job: %v", err)
	}
	jobClosed = true
}

func takeWindowsGoplsSidecarForHardKill(t *testing.T, client *mcpLSPBinaryClient, expectedStartIdentity string) (*exec.Cmd, func() error) {
	t.Helper()
	if client == nil {
		t.Fatal("Windows gopls sidecar hard-kill client is nil")
	}
	if client.cmd == nil {
		t.Fatal("Windows gopls sidecar hard-kill process is nil")
	}
	if client.closeHook == nil {
		t.Fatal("Windows gopls sidecar hard-kill Job owner is nil")
	}
	if expectedStartIdentity == "" {
		t.Fatal("Windows gopls sidecar hard-kill start identity is empty")
	}
	command, closeJob := client.cmd, client.closeHook
	client.cmd, client.closeHook = nil, nil
	return command, closeJob
}

func closeWindowsGoplsSidecarJobAfterFailure(t *testing.T, closeJob func() error, closed *bool) {
	t.Helper()
	if *closed {
		return
	}
	if err := closeJob(); err != nil {
		t.Errorf("close Windows gopls sidecar Job after failed hard kill: %v", err)
	}
}

func closeWindowsGoplsSidecarHandle(t *testing.T, handle windows.Handle) {
	t.Helper()
	if err := windows.CloseHandle(handle); err != nil {
		t.Errorf("close exact Windows gopls sidecar handle: %v", err)
	}
}

func terminateWindowsGoplsExactHandle(handle windows.Handle, pid int, expectedStartIdentity string) error {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return fmt.Errorf("query exact Windows gopls sidecar start identity: %w", err)
	}
	startIdentity := strconv.FormatUint(uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime), 10)
	if startIdentity != expectedStartIdentity {
		return fmt.Errorf("refuse Windows gopls sidecar hard kill after start identity changed: pid=%d", pid)
	}
	if err := windows.TerminateProcess(handle, 1); err != nil {
		return fmt.Errorf("terminate exact Windows gopls sidecar: %w", err)
	}
	status, err := windows.WaitForSingleObject(handle, uint32((5*time.Second)/time.Millisecond))
	if err != nil {
		return fmt.Errorf("wait exact Windows gopls sidecar termination: %w", err)
	}
	if status != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("wait exact Windows gopls sidecar termination: status=%d", status)
	}
	return nil
}

func reapWindowsGoplsSidecar(command *exec.Cmd, client *mcpLSPBinaryClient) error {
	if err := client.stdin.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return fmt.Errorf("close terminated Windows gopls sidecar stdin: %w", err)
	}
	err := command.Wait()
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		return nil
	}
	return err
}

func requireWindowsGoplsFenceMonotonic(t *testing.T, previous, current runtimeServerDurableGoplsRootCohortFence) {
	t.Helper()
	if current.Epoch < previous.Epoch || current.JournalRevision <= previous.JournalRevision ||
		current.MemberGeneration <= previous.MemberGeneration {
		t.Fatalf("Windows gopls recovery fence was not monotonic: epoch=%d->%d revision=%d->%d generation=%d->%d",
			previous.Epoch, current.Epoch, previous.JournalRevision, current.JournalRevision,
			previous.MemberGeneration, current.MemberGeneration)
	}
}
