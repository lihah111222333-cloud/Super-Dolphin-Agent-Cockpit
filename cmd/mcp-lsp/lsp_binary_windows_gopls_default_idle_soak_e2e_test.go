//go:build windows && e2e

package main

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const (
	windowsGoplsDefaultIdleSoak  = 15 * time.Minute
	windowsGoplsEarlyIdleProbe   = 14 * time.Minute
	windowsGoplsIdleBoundarySafe = time.Second
	windowsGoplsIdleExitDeadline = 2 * time.Minute
)

// TestMcpLSPBinaryWindowsGoplsDefault15mIdleReclaimsSharedDaemonE2E 验证默认闲置窗口与精确进程回收。
// 手工运行必须显式传入 go test -timeout=20m 或更大预算；Go 默认 10m 会在 14m 存活探针前误杀测试驱动。
// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryWindowsGoplsDefault15mIdleReclaimsSharedDaemonE2E(t *testing.T) {
	roots, targets := writeRealGoplsLinkedWorktreeFixtures(t)
	fixture := t.TempDir()
	argsLog, cacheRoot := filepath.Join(fixture, "args.log"), filepath.Join(fixture, "cache")
	install := buildWindowsGoplsProductionTestInstall(t)
	env := []string{
		"SUPER_DOLPHIN_LSP_BUNDLE_DIR=" + install.Bundle,
		"SUPER_DOLPHIN_LSP_MANIFEST=" + install.Manifest,
		"AGENT_LSP_SHARED_CACHE_DIR=" + cacheRoot,
		fakeGoplsArgsLogEnv + "=" + argsLog,
		"MCP_LSP_FAKE_GOPLS_RSS_CHILD=1",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	var exact []windowsGoplsProvisionalIdentity
	registerWindowsGoplsExactCleanup(t, &exact, cacheRoot)
	clients := startWindowsGoplsClients(t, ctx, install.Binary, roots, filepath.Dir(install.Gopls), env, targets)
	sidecars := requireWindowsGoplsSidecarIdentities(t, clients)

	invocations := waitForFakeGoplsInvocations(t, argsLog, 4)
	endpoint, daemonPID, childPID, forwarders := requireWindowsGoplsDefaultIdleTopology(
		t, invocations, install.Gopls, windowsGoplsDefaultIdleSoak,
	)
	record := waitForWindowsGoplsBrokerRecord(t, cacheRoot)
	requireWindowsGoplsBrokerRecordWithIdle(t, record, install, endpoint, daemonPID, windowsGoplsDefaultIdleSoak)
	childIdentity := requireWindowsGoplsStartIdentity(t, childPID)
	durable := []windowsGoplsProvisionalIdentity{
		{PID: record.OwnerPID, StartIdentity: record.OwnerStartIdentity},
		{PID: record.DaemonPID, StartIdentity: record.DaemonStartIdentity},
		childIdentity,
	}
	exact = append(exact, durable...)
	exact = append(exact, forwarders...)
	exact = append(exact, sidecars...)

	clients[0].close(t)
	result := clients[1].callTool(t, "completion", map[string]any{"pos": targets[1] + ":3:1"})
	requireMCPToolSuccess(t, clients[1], result, "Windows default-idle second forwarder after first close")
	idleStarted := time.Now()
	clients[1].close(t)

	// 14m 探针必须验证仍存活的 durable broker、daemon 和 RSS child；forwarder/sidecar 已按设计关闭。
	time.Sleep(windowsGoplsEarlyIdleProbe)
	requireWindowsGoplsExactIdentitiesAlive(t, durable...)

	// 在 15m 到期前保留 1s 调度余量，再次确认 durable 进程没有提前退出。
	if wait := windowsGoplsDefaultIdleSoak - windowsGoplsIdleBoundarySafe - time.Since(idleStarted); wait > 0 {
		time.Sleep(wait)
	}
	requireWindowsGoplsExactIdentitiesAlive(t, durable...)
	if wait := windowsGoplsDefaultIdleSoak - time.Since(idleStarted); wait > 0 {
		time.Sleep(wait)
	}
	requireWindowsGoplsExactIdentitiesGone(t, windowsGoplsIdleExitDeadline, exact...)
	elapsed := time.Since(idleStarted)
	if elapsed < windowsGoplsDefaultIdleSoak {
		t.Fatalf("Windows shared gopls default idle lifecycle converged before 15m: elapsed=%s", elapsed)
	}
	t.Logf("Windows shared gopls default idle lifecycle converged after %s", elapsed)
}

func requireWindowsGoplsDefaultIdleTopology(t *testing.T, invocations [][]string, executable string, idle time.Duration) (string, int, int, []windowsGoplsProvisionalIdentity) {
	t.Helper()
	topology, childPID := splitWindowsGoplsRSSChildInvocation(t, invocations)
	endpoint, daemonPID := requireWindowsGoplsTopologyWithIdle(t, topology, executable, idle)
	_, forwarderArgs := splitWindowsGoplsInvocations(topology)
	forwarders := make([]windowsGoplsProvisionalIdentity, 0, len(forwarderArgs))
	for _, args := range forwarderArgs {
		pid, err := strconv.Atoi(windowsGoplsArg(args, "pid="))
		if err != nil || pid <= 1 {
			t.Fatalf("invalid Windows gopls forwarder PID in %v: %v", args, err)
		}
		forwarders = append(forwarders, requireWindowsGoplsStartIdentity(t, pid))
	}
	return endpoint, daemonPID, childPID, forwarders
}

func requireWindowsGoplsSidecarIdentities(t *testing.T, clients []*mcpLSPBinaryClient) []windowsGoplsProvisionalIdentity {
	t.Helper()
	identities := make([]windowsGoplsProvisionalIdentity, 0, len(clients))
	for index, client := range clients {
		if client == nil || client.cmd == nil || client.cmd.Process == nil || client.cmd.Process.Pid <= 1 {
			t.Fatalf("Windows mcp-lsp sidecar %d has no live process identity", index)
		}
		identities = append(identities, requireWindowsGoplsStartIdentity(t, client.cmd.Process.Pid))
	}
	return identities
}

func requireWindowsGoplsExactIdentitiesAlive(t *testing.T, identities ...windowsGoplsProvisionalIdentity) {
	t.Helper()
	for _, identity := range identities {
		alive, err := windowsGoplsExactIdentityAlive(identity)
		if err != nil || !alive {
			t.Fatalf("Windows gopls exact process became idle before required boundary: identity=%+v alive=%t err=%v", identity, alive, err)
		}
	}
}
