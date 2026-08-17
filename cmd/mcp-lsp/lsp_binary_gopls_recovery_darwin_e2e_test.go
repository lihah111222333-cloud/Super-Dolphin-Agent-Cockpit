//go:build darwin && e2e

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

type goplsRecoveryE2EFixture struct {
	ctx        context.Context
	goplsPath  string
	binary     string
	runtimeDir string
	cacheRoot  string
	root       string
	target     string
	baseEnv    []string
}

// TestMcpLSPBinaryRecoversPreBootGoplsCleanupJournalAfterRestart_E2E 锁定整机
// 重启后旧 cleanup journal 不得永久阻断下一配置代际。
func TestMcpLSPBinaryRecoversPreBootGoplsCleanupJournalAfterRestart_E2E(t *testing.T) {
	fixture := newGoplsRecoveryE2EFixture(t, "reboot")
	first := fixture.start(t, "pre_reboot_gopls_generation")
	oldDaemon := fixture.requireStructure(t, first, "pre-reboot document_symbol")
	killMcpLSPBinaryClientAbruptly(t, first)
	killGoplsDaemonProcessForE2E(t, fixture.goplsPath, fixture.runtimeDir, oldDaemon)
	fixture.appendCleanup(t, 999999, "1.0", "pre-reboot-lease")
	fixture.requireNewGeneration(t, "post_reboot_gopls_generation", oldDaemon.PID, "post-reboot first document_symbol")
}

// TestMcpLSPBinaryRecoversKilledSameBootGoplsCleanupOwner_E2E 锁定同一次开机内
// 手动终止 exact sidecar 与 daemon 后，已失效 owner journal 可被身份复核并退役。
func TestMcpLSPBinaryRecoversKilledSameBootGoplsCleanupOwner_E2E(t *testing.T) {
	fixture := newGoplsRecoveryE2EFixture(t, "same-boot-kill")
	first := fixture.start(t, "before_manual_kill_generation")
	oldDaemon := fixture.requireStructure(t, first, "before manual kill document_symbol")
	ownerPID := first.cmd.Process.Pid
	ownerStart, err := hiddenexec.ProcessStartIdentity(ownerPID)
	if err != nil {
		t.Fatalf("capture same-boot sidecar identity: %v", err)
	}
	killMcpLSPBinaryClientAbruptly(t, first)
	killGoplsDaemonProcessForE2E(t, fixture.goplsPath, fixture.runtimeDir, oldDaemon)
	fixture.appendCleanup(t, ownerPID, ownerStart, "same-boot-killed-owner-lease")
	fixture.requireNewGeneration(t, "after_manual_kill_generation", oldDaemon.PID, "same-boot killed-owner recovery")
}

// TestMcpLSPBinaryReattachesAfterManualSidecarKillSameGeneration_E2E 证明只杀
// sidecar 时，同配置新 sidecar 可重新接入仍存活的真实 gopls daemon。
func TestMcpLSPBinaryReattachesAfterManualSidecarKillSameGeneration_E2E(t *testing.T) {
	fixture := newGoplsRecoveryE2EFixture(t, "sidecar-kill")
	first := fixture.start(t, "same_generation")
	daemon := fixture.requireStructure(t, first, "before sidecar kill document_symbol")
	killMcpLSPBinaryClientAbruptly(t, first)
	second := fixture.start(t, "same_generation")
	reused := fixture.requireStructure(t, second, "after sidecar kill document_symbol")
	if reused.PID != daemon.PID {
		t.Fatalf("same generation daemon PID changed after sidecar kill: before=%d after=%d", daemon.PID, reused.PID)
	}
}

// TestMcpLSPBinaryRestartsAfterManualGoplsDaemonKill_E2E 证明 sidecar 仍存活但
// daemon 被终止时，下一次真实工具调用会重建服务而不是永久留下部分工具可用。
func TestMcpLSPBinaryRestartsAfterManualGoplsDaemonKill_E2E(t *testing.T) {
	fixture := newGoplsRecoveryE2EFixture(t, "daemon-kill")
	client := fixture.start(t, "same_generation")
	oldDaemon := fixture.requireStructure(t, client, "before daemon kill document_symbol")
	killGoplsDaemonProcessForE2E(t, fixture.goplsPath, fixture.runtimeDir, oldDaemon)
	newDaemon := fixture.requireStructure(t, client, "first document_symbol after daemon kill")
	if newDaemon.PID == oldDaemon.PID {
		t.Fatalf("manual daemon kill reused terminated PID %d", newDaemon.PID)
	}
}

func newGoplsRecoveryE2EFixture(t *testing.T, label string) *goplsRecoveryE2EFixture {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Darwin gopls recovery E2E in short mode")
	}
	goplsPath, err := exec.LookPath("gopls")
	if err != nil {
		t.Fatalf("gopls is required for recovery E2E: %v", err)
	}
	roots, targets := writeRealGoplsLinkedWorktreeFixtures(t)
	runtimeDir, err := os.MkdirTemp("/tmp", "mcp-lsp-gopls-"+label+"-")
	if err != nil {
		t.Fatalf("create gopls recovery runtime dir: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	t.Cleanup(func() {
		killAllGoplsDaemonProcessesForE2E(t, goplsPath, runtimeDir)
		if err := os.RemoveAll(runtimeDir); err != nil {
			t.Errorf("remove gopls recovery runtime dir: %v", err)
		}
	})
	cacheRoot := filepath.Join(runtimeDir, "lsp-resource")
	return &goplsRecoveryE2EFixture{
		ctx: ctx, goplsPath: goplsPath, binary: buildMcpLSPBinaryForTest(t),
		runtimeDir: runtimeDir, cacheRoot: cacheRoot, root: roots[0], target: targets[0],
		baseEnv: []string{
			"XDG_RUNTIME_DIR=" + runtimeDir,
			"AGENT_LSP_SHARED_CACHE_DIR=" + cacheRoot,
			"AGENT_LSP_GO_RSS_LIMIT_MB=384",
			"GOWORK=off",
			"MCP_LSP_IDLE_TIMEOUT=" + realGoplsRemoteListenTimeout.String(),
		},
	}
}

func (f *goplsRecoveryE2EFixture) start(t *testing.T, generation string) *mcpLSPBinaryClient {
	t.Helper()
	env := append(slices.Clone(f.baseEnv), "GOFLAGS=-tags="+generation)
	client := startMcpLSPBinaryForTestWithEnv(t, f.ctx, f.binary, f.root, filepath.Dir(f.goplsPath), env)
	t.Cleanup(func() { client.close(t) })
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	return client
}

func (f *goplsRecoveryE2EFixture) requireStructure(t *testing.T, client *mcpLSPBinaryClient, label string) goplsDaemonProcess {
	t.Helper()
	result := client.callTool(t, "structure", map[string]any{"action": "document_symbol", "file_path": f.target})
	requireMCPToolSuccess(t, client, result, label)
	return waitForSingleGoplsRecoveryDaemon(t, client, f.goplsPath, f.runtimeDir)
}

func waitForSingleGoplsRecoveryDaemon(t *testing.T, client *mcpLSPBinaryClient, goplsPath, runtimeDir string) goplsDaemonProcess {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		processes := requireGoplsDaemonProcesses(t, goplsPath, runtimeDir)
		if len(processes) == 1 {
			return processes[0]
		}
		if len(processes) > 1 || time.Now().After(deadline) {
			probe := queryGoplsDaemon(t, goplsPath, runtimeDir, client.cmd.Dir)
			t.Fatalf("gopls recovery daemon processes for runtime %s = %#v, want exactly one; endpoint_running=%t endpoint_err=%v endpoint_output=%s stderr=%s",
				runtimeDir, processes, probe.Running, probe.Err, probe.Output, client.stderrString())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (f *goplsRecoveryE2EFixture) appendCleanup(t *testing.T, ownerPID int, ownerStart, leaseID string) {
	t.Helper()
	statePath := requireSingleGoplsRootCohortStatePath(t, f.cacheRoot)
	state, err := runtimeServerReadGoplsRootCohortState(statePath)
	if err != nil {
		t.Fatalf("read recovery cohort state: %v", err)
	}
	now := time.Now().UnixNano()
	state.PendingCleanups = append(state.PendingCleanups, runtimeGoplsRootCohortCleanupEvidence{
		Fence: runtimeServerDurableGoplsRootCohortFence{
			Epoch: state.Epoch, JournalRevision: state.JournalRevision, MemberID: leaseID + "-member",
			MemberGeneration: state.NextMemberGeneration, LeaseID: leaseID,
		},
		IdleDeadlineUnixNano: now, OwnerPID: ownerPID, OwnerStartIdentity: ownerStart,
		Status: runtimeGoplsRootCohortDrainCleanupPending, LastError: "historical cleanup pending", RetryUnixNano: now,
	})
	if err := runtimeServerWriteGoplsRootCohortState(statePath, *state); err != nil {
		t.Fatalf("write recovery cleanup journal fixture: %v", err)
	}
}

func (f *goplsRecoveryE2EFixture) requireNewGeneration(t *testing.T, generation string, oldPID int, label string) {
	t.Helper()
	next := f.start(t, generation)
	newDaemon := f.requireStructure(t, next, label)
	if newDaemon.PID == oldPID {
		t.Fatalf("recovery reused terminated daemon PID %d", newDaemon.PID)
	}
}

func requireSingleGoplsRootCohortStatePath(t *testing.T, cacheRoot string) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(cacheRoot, "gopls-root-cohorts", "*", "state.json"))
	if err != nil {
		t.Fatalf("glob gopls root cohort state: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("gopls root cohort states under %s = %v, want exactly one", cacheRoot, paths)
	}
	return paths[0]
}
