//go:build windows && e2e

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const windowsGoplsPressureIdle = 10 * time.Second

// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryWindowsGoplsRootCohortPressureReclaimsZeroLeaseJobE2E(t *testing.T) {
	roots, targets := writeRealGoplsLinkedWorktreeFixtures(t)
	fixture := t.TempDir()
	argsLog, cacheRoot := filepath.Join(fixture, "args.log"), filepath.Join(fixture, "cache")
	install := buildWindowsGoplsShortIdlePrecheckTestInstall(t)
	env := windowsGoplsSidecarEnv(t, install, fakeGoplsArgsLogEnv+"="+argsLog,
		"AGENT_LSP_SHARED_CACHE_DIR="+cacheRoot, "AGENT_LSP_GO_RSS_LIMIT_MB=1",
		"MCP_LSP_FAKE_GOPLS_RSS_CHILD=1", "MCP_LSP_IDLE_TIMEOUT="+windowsGoplsPressureIdle.String())
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var exact []windowsGoplsProvisionalIdentity
	registerWindowsGoplsExactCleanup(t, &exact)
	clients := startWindowsGoplsClients(t, ctx, install.Binary, roots, filepath.Dir(install.Gopls), env, targets)

	record := waitForWindowsGoplsBrokerRecord(t, cacheRoot)
	childPID, forwarders := requireWindowsGoplsPressureTopology(
		t, waitForFakeGoplsInvocations(t, argsLog, 4), install.Gopls, record,
	)
	exact = []windowsGoplsProvisionalIdentity{
		{PID: record.OwnerPID, StartIdentity: record.OwnerStartIdentity},
		{PID: record.DaemonPID, StartIdentity: record.DaemonStartIdentity},
		requireWindowsGoplsStartIdentity(t, childPID),
	}
	requireWindowsGoplsBrokerProcessIdentities(t, record, record.DaemonPID)
	resourcePath, receipt := waitForWindowsGoplsRootResourceReceipt(t, cacheRoot)
	requireWindowsGoplsRootResourceContract(t, resourcePath, receipt, record, childPID, forwarders)

	clients[0].close(t)
	result := clients[1].callTool(t, "completion", map[string]any{"pos": targets[1] + ":3:1"})
	requireMCPToolSuccess(t, clients[1], result, "Windows gopls pressure after first sidecar closed")
	requireWindowsProcessAlive(t, record.OwnerPID, "gopls broker")
	requireWindowsProcessAlive(t, record.DaemonPID, "gopls daemon")
	requireWindowsProcessAlive(t, childPID, "gopls RSS child")
	assertWindowsGoplsJobRSSObservation(
		t, queryWindowsGoplsJobRSS(t, record, record.ObservationCapability), record, childPID, forwarders,
	)

	clients[1].close(t)
	requireWindowsGoplsZeroDurableLeases(t, resourcePath)
	requireWindowsGoplsPressureConvergence(t, record.ObservationEndpoint, exact)
	requireWindowsGoplsPressureReclaimEvidence(t, resourcePath, record)
}

func requireWindowsGoplsPressureTopology(t *testing.T, invocations [][]string, executable string, record windowsGoplsBrokerRecordV2) (int, map[int]bool) {
	t.Helper()
	topology, childPID := splitWindowsGoplsRSSChildInvocation(t, invocations)
	daemon, forwarderArgs := splitWindowsGoplsInvocations(topology)
	requireWindowsGoplsInvocationExecutable(t, daemon, executable, "daemon")
	daemonPID, err := strconv.Atoi(windowsGoplsArg(daemon, "pid="))
	endpoint := "tcp;" + windowsGoplsArg(daemon, "-listen=tcp;")
	if err != nil || daemonPID != record.DaemonPID || endpoint != record.Endpoint ||
		windowsGoplsArg(daemon, "-listen.timeout=") != windowsGoplsPressureIdle.String() ||
		time.Duration(record.IdleTimeoutNanos) != windowsGoplsPressureIdle {
		t.Fatalf("Windows gopls pressure daemon contract mismatch: args=%v record=%+v err=%v", daemon, record, err)
	}
	requireWindowsGoplsForwarders(t, forwarderArgs, endpoint, executable)
	return childPID, windowsGoplsForwarderPIDSet(t, forwarderArgs)
}

func requireWindowsGoplsZeroDurableLeases(t *testing.T, resourcePath string) {
	t.Helper()
	dir := filepath.Dir(resourcePath)
	state, err := runtimeServerReadGoplsRootCohortState(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read Windows gopls pressure state after last close: %v", err)
	}
	active, err := runtimeServerCountGoplsRootCohortLeases(dir, state.ConfigDigest)
	if err != nil || active != 0 {
		t.Fatalf("Windows gopls durable active leases after last close = %d, err=%v", active, err)
	}
}

func requireWindowsGoplsPressureReclaimEvidence(t *testing.T, resourcePath string, record windowsGoplsBrokerRecordV2) {
	t.Helper()
	var receipt runtimeServerWindowsGoplsRootResourceReceipt
	if err := runtimeServerReadGoplsRootCohortJSON(resourcePath, &receipt, 32*1024); err != nil {
		t.Fatalf("read Windows gopls reclaim evidence: %v", err)
	}
	if receipt.SchemaVersion != runtimeServerWindowsGoplsRootResourceSchema || receipt.ActiveLeases != 0 ||
		receipt.Decision != runtimeServerWindowsGoplsRootResourceReclaimed || receipt.RSSBytes <= receipt.RSSLimitBytes ||
		receipt.DaemonPID != record.DaemonPID || receipt.DaemonStartIdentity != record.DaemonStartIdentity {
		t.Fatalf("Windows gopls reclaim evidence = %+v; want zero-lease over-limit reclaim", receipt)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(resourcePath), "daemon.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Windows gopls daemon record after reclaim err=%v; want not-exist", err)
	}
}

func requireWindowsGoplsPressureConvergence(t *testing.T, endpoint string, identities []windowsGoplsProvisionalIdentity) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		observerAlive := windowsGoplsPressureObserverAlive(t, endpoint)
		allGone := true
		for _, identity := range identities {
			alive, err := windowsGoplsExactIdentityAlive(identity)
			if err != nil {
				t.Fatalf("inspect Windows gopls pressure identity %+v: %v", identity, err)
			}
			allGone = allGone && !alive
		}
		if !observerAlive && allGone {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Windows gopls pressure Job did not converge within 1s: observer_alive=%t identities=%+v", observerAlive, identities)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func windowsGoplsPressureObserverAlive(t *testing.T, endpoint string) bool {
	t.Helper()
	connection, err := net.DialTimeout("tcp4", strings.TrimPrefix(endpoint, "tcp;"), 50*time.Millisecond)
	if err != nil {
		return false
	}
	if err := connection.Close(); err != nil {
		t.Fatalf("close Windows gopls pressure observer probe: %v", err)
	}
	return true
}
