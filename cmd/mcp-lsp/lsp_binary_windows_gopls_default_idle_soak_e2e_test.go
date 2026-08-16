//go:build windows && e2e

package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

const (
	windowsGoplsDefaultIdleSoak  = 15 * time.Minute
	windowsGoplsEarlyIdleProbe   = 14 * time.Minute
	windowsGoplsIdleExitDeadline = 2 * time.Minute
)

// TestMcpLSPBinaryWindowsGoplsDefault15mIdleReclaimsSharedDaemonE2E 验证默认闲置窗口与精确进程回收。
// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryWindowsGoplsDefault15mIdleReclaimsSharedDaemonE2E(t *testing.T) {
	roots, targets := writeRealGoplsLinkedWorktreeFixtures(t)
	fixture := t.TempDir()
	argsLog, cacheRoot := filepath.Join(fixture, "args.log"), filepath.Join(fixture, "cache")
	install := buildWindowsGoplsTestInstall(t)
	env := []string{
		"SUPER_DOLPHIN_LSP_BUNDLE_DIR=" + install.Bundle,
		"SUPER_DOLPHIN_LSP_MANIFEST=" + install.Manifest,
		"AGENT_LSP_SHARED_CACHE_DIR=" + cacheRoot,
		fakeGoplsArgsLogEnv + "=" + argsLog,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	var exact []windowsGoplsProvisionalIdentity
	registerWindowsGoplsExactCleanup(t, &exact)
	clients := startWindowsGoplsClients(t, ctx, install.Binary, roots, filepath.Dir(install.Gopls), env, targets)

	endpoint, daemonPID := requireWindowsGoplsTopologyWithIdle(
		t, waitForFakeGoplsInvocations(t, argsLog, 3), install.Gopls, windowsGoplsDefaultIdleSoak,
	)
	record := waitForWindowsGoplsBrokerRecord(t, cacheRoot)
	requireWindowsGoplsBrokerRecordWithIdle(t, record, install, endpoint, daemonPID, windowsGoplsDefaultIdleSoak)
	exact = []windowsGoplsProvisionalIdentity{
		{PID: record.OwnerPID, StartIdentity: record.OwnerStartIdentity},
		{PID: record.DaemonPID, StartIdentity: record.DaemonStartIdentity},
	}

	clients[0].close(t)
	result := clients[1].callTool(t, "completion", map[string]any{"pos": targets[1] + ":3:1"})
	requireMCPToolSuccess(t, clients[1], result, "Windows default-idle second forwarder after first close")
	clients[1].close(t)
	idleStarted := time.Now()
	time.Sleep(windowsGoplsEarlyIdleProbe)
	requireWindowsGoplsExactIdentitiesAlive(t, exact...)
	requireWindowsGoplsExactIdentitiesGone(t, windowsGoplsIdleExitDeadline, exact...)
	t.Logf("Windows shared gopls default idle lifecycle converged after %s", time.Since(idleStarted))
}

func requireWindowsGoplsExactIdentitiesAlive(t *testing.T, identities ...windowsGoplsProvisionalIdentity) {
	t.Helper()
	for _, identity := range identities {
		alive, err := windowsGoplsExactIdentityAlive(identity)
		if err != nil || !alive {
			t.Fatalf("Windows gopls exact process became idle before 14m: identity=%+v alive=%t err=%v", identity, alive, err)
		}
	}
}
