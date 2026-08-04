//go:build e2e && (darwin || linux)

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/processprobe"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

const (
	// source harness 仅在本机固定 Homebrew 工具路径；测试启动前会解析 symlink 并复核根身份，版本/摘要由运行日志保留。
	idleRealNodePath       = "/opt/homebrew/bin/node"
	idleRealTypeScriptPath = "/opt/homebrew/bin/typescript-language-server"
	idleRealTimeout        = 1 * time.Second
	idleRecyclerScan       = 30 * time.Second
)

// TestMcpLSPBinaryRealTypeScriptLeaseSurvivesThenRecyclesExactTree_E2E 验证生产空闲契约。
// 它使用 cohort 台账中的权威 PID/启动身份和只读 PPID 图，不按进程名判断。
func TestMcpLSPBinaryRealTypeScriptLeaseSurvivesThenRecyclesExactTree_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real idle lifecycle E2E in short mode")
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native process identity E2E is supported only on Darwin/Linux")
	}
	resolvedNodePath := requireIdleRealTool(t, idleRealNodePath)
	resolvedTypeScriptPath := requireIdleRealTool(t, idleRealTypeScriptPath)

	root := t.TempDir()
	target, _ := writeRealTypeScriptToolsFixture(t, root)
	cacheDir := t.TempDir()
	if err := os.Chmod(cacheDir, 0o700); err != nil {
		t.Fatalf("chmod exact resource cohort cache root: %v", err)
	}
	binary := buildMcpLSPBinaryForTest(t)
	ctx, cancel := platformconfig.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, filepath.Dir(idleRealTypeScriptPath), []string{
		"MCP_LSP_IDLE_TIMEOUT=" + idleRealTimeout.String(),
		"AGENT_LSP_SHARED_CACHE_DIR=" + cacheDir,
	})
	defer client.close(t)

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	structure := client.callTool(t, "structure", map[string]any{
		"action":      "document_symbol",
		"file_path":   target,
		"language_id": "typescript",
	})
	requireMCPToolSuccess(t, client, structure, "real TypeScript idle lifecycle warmup")
	t.Logf("real TypeScript source harness: node=%s typescript_language_server=%s", resolvedNodePath, resolvedTypeScriptPath)
	exerciseIdleRealTypeScriptLease(t, client, root, target, cacheDir, filepath.Base(resolvedNodePath))
}

// exerciseIdleRealTypeScriptLease 冻结精确 owner tree，验证 lease 保活后再释放回收。
func exerciseIdleRealTypeScriptLease(t *testing.T, client *mcpLSPBinaryClient, root, target, cacheDir, rootExecutable string) {
	clientPID, err := waitForIdleRealClientPID(client.cmd.Process.Pid, 10*time.Second)
	if err != nil {
		t.Fatalf("real TypeScript client PID was not uniquely observable below sidecar: %v", err)
	}
	nativeTree := captureAndStopIdleRealTree(t, client.cmd.Process.Pid, clientPID, rootExecutable)
	callLoop := startIdleRealCallLoop(client, root, target)
	continued := false
	defer func() {
		if !continued {
			_ = continueIdleNativeTree(nativeTree)
		}
		_ = callLoop.drain()
	}()
	activeMember, err := waitForIdleRealActiveMember(cacheDir, client.cmd.Process.Pid, clientPID, idleRecyclerScan+10*time.Second)
	if err != nil {
		_ = continueIdleNativeTree(nativeTree)
		continued = true
		drainErr := callLoop.drain()
		t.Fatalf("real TypeScript request did not expose an active lease before completion: %v (last_call_error=%v) stderr=%s", err, drainErr, client.stderrString())
	}
	if activeMember.ActiveLeases < 1 {
		t.Fatalf("active member has no lease: %#v", activeMember)
	}
	t.Logf("real TypeScript active lease observed: owner_pid=%d client_pid=%d active_leases=%d updated_at_unix_nano=%d last_activity_unix_nano=%d", client.cmd.Process.Pid, clientPID, activeMember.ActiveLeases, activeMember.UpdatedAtUnixNano, activeMember.LastActivityUnixNano)
	assertIdleRealLeaseStable(t, client, callLoop, cacheDir, client.cmd.Process.Pid, clientPID, activeMember)
	if err := continueIdleNativeTree(nativeTree); err != nil {
		t.Fatalf("continue exact TypeScript client tree root=%d: %v", clientPID, err)
	}
	continued = true
	if callErr := callLoop.drain(); callErr != nil {
		t.Fatalf("real TypeScript held hover request did not complete cleanly after exact tree SIGCONT: %v stderr=%s", callErr, client.stderrString())
	}
	assertIdleRealLeaseRecycled(t, client, cacheDir, client.cmd.Process.Pid, activeMember, nativeTree)
}

// captureAndStopIdleRealTree 捕获并冻结全部可观测后代，根身份还要匹配固定 node 工具。
func captureAndStopIdleRealTree(t *testing.T, ownerPID, clientPID int, rootExecutable string) []idleNativeIdentity {
	t.Helper()
	nativeTree, err := captureIdleNativeTree(clientPID)
	if err != nil {
		t.Fatalf("capture exact TypeScript native process tree root=%d: %v", clientPID, err)
	}
	if len(nativeTree) == 0 {
		t.Fatalf("exact TypeScript native process tree root=%d was empty", clientPID)
	}
	if nativeTree[0].Executable != rootExecutable {
		t.Fatalf("exact TypeScript native root executable=%q, want resolved source harness executable=%q", nativeTree[0].Executable, rootExecutable)
	}
	t.Logf("real TypeScript exact owner tree before lease: sidecar_pid=%d client_pid=%d members=%s", ownerPID, clientPID, formatIdleNativeTree(nativeTree))
	if err := stopIdleNativeTree(nativeTree); err != nil {
		t.Fatalf("stop exact TypeScript client tree root=%d before active request: %v", clientPID, err)
	}
	return nativeTree
}

// assertIdleRealLeaseStable 验证 recycler 观察到活动 lease 后，短窗内不会误杀 exact tree。
func assertIdleRealLeaseStable(t *testing.T, client *mcpLSPBinaryClient, callLoop *idleRealCallLoop, cacheDir string, ownerPID, clientPID int, activeMember resourceCohortE2EMember) {
	t.Helper()
	time.Sleep(2 * time.Second)
	if callLoop.completedBeforeStableWindow() {
		t.Fatalf("active lease request completed before SIGCONT: active_member=%#v stderr=%s", activeMember, client.stderrString())
	}
	probe, probeErr := processprobe.Probe(context.Background(), clientPID)
	if probeErr != nil || !probe.Alive() {
		t.Fatalf("active lease did not keep exact client alive after active recycler scan: probe=%#v err=%v active_member=%#v call_completed=%v stderr=%s", probe, probeErr, activeMember, callLoop.completedBeforeStableWindow(), client.stderrString())
	}
	stillActive, found, err := findIdleRealMember(cacheDir, ownerPID, clientPID)
	if err != nil || !found {
		t.Fatalf("active lease member disappeared after active recycler scan: found=%v err=%v active_member=%#v call_completed=%v stderr=%s", found, err, activeMember, callLoop.completedBeforeStableWindow(), client.stderrString())
	}
	if stillActive.ActiveLeases < 1 || activeMember.ActiveLeases < 1 {
		t.Fatalf("active lease count dropped while exact client was stopped: initial=%#v current=%#v call_completed=%v stderr=%s", activeMember, stillActive, callLoop.completedBeforeStableWindow(), client.stderrString())
	}
}

// assertIdleRealLeaseRecycled 验证释放后 cohort 台账和 exact native identity 都消失。
func assertIdleRealLeaseRecycled(t *testing.T, client *mcpLSPBinaryClient, cacheDir string, ownerPID int, activeMember resourceCohortE2EMember, nativeTree []idleNativeIdentity) {
	t.Helper()
	if err := waitForIdleRealMemberGone(cacheDir, ownerPID, activeMember, 40*time.Second); err != nil {
		t.Fatalf("real TypeScript cohort member was not removed after release: %v stderr=%s", err, client.stderrString())
	}
	if err := waitForIdleNativeTreeGone(nativeTree, 40*time.Second); err != nil {
		t.Fatalf("real TypeScript exact owner tree was not fully reclaimed: %v stderr=%s", err, client.stderrString())
	}
	t.Logf("real TypeScript exact owner tree reclaimed: root=%d/start=%q members=%s", activeMember.ClientPID, activeMember.ClientStartIdentity, formatIdleNativeTree(nativeTree))
}

type idleRealCallLoop struct {
	stop         chan struct{}
	result       chan error
	completed    chan struct{}
	completeOnce sync.Once
	wg           sync.WaitGroup
	drained      bool
	lastErr      error
}

// startIdleRealCallLoop 启动会持有 manager lease 的顺序 JSON-RPC 请求并显式等待其结束。
func startIdleRealCallLoop(client *mcpLSPBinaryClient, root, target string) *idleRealCallLoop {
	loop := &idleRealCallLoop{stop: make(chan struct{}), result: make(chan error, 1), completed: make(chan struct{})}
	loop.wg.Add(1)
	runtimesafe.SafeGo(context.Background(), nil, "mcp-lsp-real-idle-call-loop", func(context.Context) {
		defer loop.wg.Done()
		var lastErr error
		for {
			select {
			case <-loop.stop:
				loop.result <- lastErr
				return
			default:
			}
			_, lastErr = callIdleBinaryNoFail(client, "tools/call", map[string]any{
				"name": "inspect",
				"arguments": map[string]any{
					"action":      "hover",
					"pos":         target + ":1:17",
					"language_id": "typescript",
				},
				"_cwd":            root,
				"_workspaceRoots": []string{root},
			})
			loop.completeOnce.Do(func() { close(loop.completed) })
		}
	})
	return loop
}

func (loop *idleRealCallLoop) completedBeforeStableWindow() bool {
	select {
	case <-loop.completed:
		return true
	default:
		return false
	}
}

// drain 等待 call loop 完整退出并缓存最后一次调用错误。
func (loop *idleRealCallLoop) drain() error {
	if loop.drained {
		return loop.lastErr
	}
	close(loop.stop)
	loop.wg.Wait()
	loop.lastErr = <-loop.result
	loop.drained = true
	return loop.lastErr
}

// TestMcpLSPBinaryStdioRemainsAlivePastCanonicalIdleTimeout_E2E 验证 LSP 空闲策略
// 不会变成 MCP stdio 的静默退出计时器，并检查显式 exit 与 EOF 两条路径。
func TestMcpLSPBinaryStdioRemainsAlivePastCanonicalIdleTimeout_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stdio idle lifecycle E2E in short mode")
	}
	resolvedNodePath := requireIdleRealTool(t, idleRealNodePath)
	resolvedTypeScriptPath := requireIdleRealTool(t, idleRealTypeScriptPath)
	t.Logf("real TypeScript stdio source harness: node=%s typescript_language_server=%s", resolvedNodePath, resolvedTypeScriptPath)
	root := t.TempDir()
	target, _ := writeRealTypeScriptToolsFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	ctx, cancel := platformconfig.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, filepath.Dir(idleRealTypeScriptPath), []string{
		"MCP_LSP_IDLE_TIMEOUT=" + idleRealTimeout.String(),
	})
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	warm := client.callTool(t, "structure", map[string]any{
		"action":      "document_symbol",
		"file_path":   target,
		"language_id": "typescript",
	})
	requireMCPToolSuccess(t, client, warm, "stdio idle lifecycle warmup")
	time.Sleep(idleRealTimeout + 2*time.Second)
	if probe, err := processprobe.Probe(context.Background(), client.cmd.Process.Pid); err != nil || !probe.Alive() {
		t.Fatalf("mcp-lsp stdio sidecar exited during LSP idle window: probe=%#v err=%v stderr=%s", probe, err, client.stderrString())
	}
	toolsList := client.call(t, "tools/list", nil)
	if toolsList.Error != nil {
		t.Fatalf("tools/list after canonical idle timeout: %v; stderr=%s", toolsList.Error, client.stderrString())
	}
	closeIdleBinaryStrict(t, client) // 显式 exit 后必须是零退出码。

	eofClient := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, filepath.Dir(idleRealTypeScriptPath), []string{
		"MCP_LSP_IDLE_TIMEOUT=" + idleRealTimeout.String(),
	})
	eofClient.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	closeIdleBinaryEOFStrict(t, eofClient)
}

// closeIdleBinaryStrict 执行显式 exit，并把非零退出（包括 CleanupPending）变成测试失败。
func closeIdleBinaryStrict(t *testing.T, client *mcpLSPBinaryClient) {
	t.Helper()
	if client == nil || client.cmd == nil {
		return
	}
	cmd := client.cmd
	client.cmd = nil
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "exit"})
	if _, err := client.stdin.Write(append(raw, '\n')); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("write stdio exit: %v; stderr=%s", err, client.stderrString())
	}
	if err := client.stdin.Close(); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("close stdio stdin after exit: %v; stderr=%s", err, client.stderrString())
	}
	if waitErr := waitIdleBinaryProcess(cmd, "explicit exit"); waitErr != nil && !errors.Is(waitErr, os.ErrProcessDone) {
		t.Fatalf("mcp-lsp explicit exit returned non-zero: %v; stderr=%s", waitErr, client.stderrString())
	}
}

// closeIdleBinaryEOFStrict 关闭 stdio 写端并校验 EOF 的 exact 进程退出。
func closeIdleBinaryEOFStrict(t *testing.T, client *mcpLSPBinaryClient) {
	t.Helper()
	if client == nil || client.cmd == nil {
		return
	}
	cmd := client.cmd
	client.cmd = nil
	if err := client.stdin.Close(); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("close mcp-lsp stdio stdin for EOF path: %v", err)
	}
	if waitErr := waitIdleBinaryProcess(cmd, "stdio EOF"); waitErr != nil && !errors.Is(waitErr, os.ErrProcessDone) {
		t.Fatalf("mcp-lsp stdio EOF exit returned non-zero: %v; stderr=%s", waitErr, client.stderrString())
	}
}

// waitIdleBinaryProcess 用 runtimesafe.SafeGo 等待单一 cmd.Wait，并在超时后先杀死再 join。
func waitIdleBinaryProcess(cmd *exec.Cmd, label string) error {
	if cmd == nil {
		return errors.New(label + ": command is nil")
	}
	done := make(chan error, 1)
	var waitWG sync.WaitGroup
	waitWG.Add(1)
	runtimesafe.SafeGo(context.Background(), nil, "mcp-lsp-"+label+"-wait", func(context.Context) {
		defer waitWG.Done()
		done <- cmd.Wait()
	})
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case waitErr := <-done:
		waitWG.Wait()
		return waitErr
	case <-timer.C:
		_ = cmd.Process.Kill()
		waitWG.Wait()
		return fmt.Errorf("%s did not complete within 5s", label)
	}
}

func requireIdleRealTool(t *testing.T, path string) string {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
		t.Skipf("N/V: exact real tool %s is unavailable: %v", path, err)
	}
	if resolved, err := filepath.EvalSymlinks(path); err != nil || resolved == "" {
		t.Skipf("N/V: exact real tool %s has no resolvable native target: %v", path, err)
	} else {
		return resolved
	}
	return ""
}

// waitForIdleRealClientPID 通过 sidecar 的直接子进程关系唯一确定已 warm 的 LSP client。
// 这里不读取或匹配进程名；工具已可用且 warm 成功后，身份不唯一必须让 E2E 失败。
func waitForIdleRealClientPID(ownerPID int, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		parents, err := readIdleNativeParents()
		if err != nil {
			return 0, err
		}
		children := parents[ownerPID]
		if len(children) == 1 {
			if _, probeErr := processprobe.Probe(context.Background(), children[0]); probeErr == nil {
				return children[0], nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 0, fmt.Errorf("sidecar PID %d has no unique live direct child", ownerPID)
}

// waitForIdleRealActiveMember 等待台账观察到至少一个活动 lease。
func waitForIdleRealActiveMember(cacheDir string, ownerPID, clientPID int, timeout time.Duration) (resourceCohortE2EMember, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		member, found, err := findIdleRealMember(cacheDir, ownerPID, clientPID)
		if err != nil {
			return resourceCohortE2EMember{}, err
		}
		if found && member.ActiveLeases >= 1 {
			return member, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return resourceCohortE2EMember{}, fmt.Errorf("timed out waiting for active lease owner_pid=%d client_pid=%d", ownerPID, clientPID)
}

// findIdleRealMember 只按 owner/client PID 和精确 language id 查找成员。
func findIdleRealMember(cacheDir string, ownerPID, clientPID int) (resourceCohortE2EMember, bool, error) {
	snapshot, err := readResourceCohortE2ESnapshot(cacheDir)
	if err != nil {
		return resourceCohortE2EMember{}, false, err
	}
	var found resourceCohortE2EMember
	foundCount := 0
	for _, member := range snapshot.Members {
		if member.OwnerPID != ownerPID || (clientPID != 0 && member.ClientPID != clientPID) {
			continue
		}
		if member.LanguageID != "typescript" && member.LanguageID != "javascript" {
			continue
		}
		found = member
		foundCount++
	}
	if foundCount > 1 {
		return resourceCohortE2EMember{}, false, fmt.Errorf("owner_pid=%d has %d ambiguous JS/TS members", ownerPID, foundCount)
	}
	return found, foundCount == 1, nil
}

// waitForIdleRealMemberGone 等待旧 PID/start identity 的 cohort 成员消失。
func waitForIdleRealMemberGone(cacheDir string, ownerPID int, old resourceCohortE2EMember, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		member, found, err := findIdleRealMember(cacheDir, ownerPID, old.ClientPID)
		if err != nil {
			return err
		}
		if !found || member.ClientStartIdentity != old.ClientStartIdentity {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("exact member owner_pid=%d client_pid=%d start=%q remained", ownerPID, old.ClientPID, old.ClientStartIdentity)
}

type idleNativeIdentity struct {
	PID        int
	ParentPID  int
	PGID       string
	SessionID  string
	Start      string
	Executable string
}

// stopIdleNativeTree 以 root 先行的顺序冻结当前测试拥有的全部进程身份。
func stopIdleNativeTree(tree []idleNativeIdentity) error {
	for index, identity := range tree {
		if err := verifyIdleNativeIdentity(identity, index == 0); err != nil {
			return err
		}
		if err := syscall.Kill(identity.PID, syscall.SIGSTOP); err != nil {
			return fmt.Errorf("stop pid=%d start=%q: %w", identity.PID, identity.Start, err)
		}
	}
	return nil
}

// continueIdleNativeTree 以 descendants 先行的顺序恢复被冻结的 owner tree。
func continueIdleNativeTree(tree []idleNativeIdentity) error {
	var firstErr error
	for index := len(tree) - 1; index >= 0; index-- {
		identity := tree[index]
		if err := verifyIdleNativeIdentity(identity, index == 0); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := syscall.Kill(identity.PID, syscall.SIGCONT); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("continue pid=%d start=%q: %w", identity.PID, identity.Start, err)
			}
		}
	}
	return firstErr
}

// verifyIdleNativeIdentity 在每次 signal 前复核 PID、启动身份、会话和进程组；根还必须保持 executable。
func verifyIdleNativeIdentity(identity idleNativeIdentity, root bool) error {
	snapshot, err := processprobe.Probe(context.Background(), identity.PID)
	if err != nil || !snapshot.Alive() {
		return fmt.Errorf("identity recheck pid=%d start=%q failed: snapshot=%#v err=%v", identity.PID, identity.Start, snapshot, err)
	}
	if snapshot.PID() != identity.PID || snapshot.StartIdentity() != identity.Start ||
		snapshot.SessionID() != identity.SessionID || snapshot.ProcessGroupID() != identity.PGID {
		return fmt.Errorf("identity mismatch before signal pid=%d expected_start=%q/session=%s/pgid=%s got_start=%q/session=%s/pgid=%s",
			identity.PID, identity.Start, identity.SessionID, identity.PGID,
			snapshot.StartIdentity(), snapshot.SessionID(), snapshot.ProcessGroupID())
	}
	if root && snapshot.Executable() != identity.Executable {
		return fmt.Errorf("root executable mismatch before signal pid=%d expected=%q got=%q", identity.PID, identity.Executable, snapshot.Executable())
	}
	return nil
}

// captureIdleNativeTree 通过只读 PPID 图记录根和全部可观测后代身份。
func captureIdleNativeTree(rootPID int) ([]idleNativeIdentity, error) {
	parents, err := readIdleNativeParents()
	if err != nil {
		return nil, err
	}
	groups, err := readIdleNativeGroups()
	if err != nil {
		return nil, err
	}
	rootSnapshot, err := processprobe.Probe(context.Background(), rootPID)
	if err != nil {
		return nil, err
	}
	queue := append([]int{rootPID}, groups[idleNativeGroupKey(rootSnapshot)]...)
	seen := map[int]struct{}{}
	result := make([]idleNativeIdentity, 0, 4)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}
		snapshot, probeErr := processprobe.Probe(context.Background(), pid)
		if probeErr != nil {
			if pid == rootPID {
				return nil, probeErr
			}
			continue
		}
		result = append(result, idleNativeIdentity{
			PID: pid, ParentPID: snapshot.ParentPID(), PGID: snapshot.ProcessGroupID(),
			SessionID: snapshot.SessionID(), Start: snapshot.StartIdentity(), Executable: snapshot.Executable(),
		})
		queue = append(queue, parents[pid]...)
	}
	return result, nil
}

// readIdleNativeParents 读取一次平台 ps 父子快照，不解析进程名。
func readIdleNativeParents() (map[int][]int, error) {
	output, err := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		return nil, err
	}
	parents := make(map[int][]int)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if pidErr != nil || parentErr != nil || pid <= 1 || parent <= 0 {
			continue
		}
		parents[parent] = append(parents[parent], pid)
	}
	return parents, nil
}

// readIdleNativeGroups 读取独立 session/PGID 中的全部 PID，补足 PPID 图无法覆盖的 detached child。
func readIdleNativeGroups() (map[string][]int, error) {
	output, err := exec.Command("ps", "-axo", "pid=,pgid=,sess=").Output()
	if err != nil {
		return nil, err
	}
	groups := make(map[string][]int)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		if pidErr != nil || pid <= 1 {
			continue
		}
		groups[fields[1]+"/"+fields[2]] = append(groups[fields[1]+"/"+fields[2]], pid)
	}
	return groups, nil
}

// idleNativeGroupKey 将 processprobe 的 session/PGID 字段合成为只读分组键。
func idleNativeGroupKey(snapshot processprobe.Snapshot) string {
	return snapshot.ProcessGroupID() + "/" + snapshot.SessionID()
}

// waitForIdleNativeTreeGone 验证每个旧 PID/start identity 均不再存活。
func waitForIdleNativeTreeGone(tree []idleNativeIdentity, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		remaining := make([]idleNativeIdentity, 0, len(tree))
		for _, identity := range tree {
			snapshot, err := processprobe.Probe(context.Background(), identity.PID)
			if err == nil && snapshot.Alive() && snapshot.StartIdentity() == identity.Start {
				remaining = append(remaining, identity)
			}
		}
		if len(remaining) == 0 {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("remaining exact identities: %s", formatIdleNativeTree(tree))
}

// formatIdleNativeTree 输出便于复核的原生身份证据。
func formatIdleNativeTree(tree []idleNativeIdentity) string {
	parts := make([]string, 0, len(tree))
	for _, identity := range tree {
		parts = append(parts, fmt.Sprintf("pid=%d/ppid=%d/start=%q/pgid=%s/sid=%s/exec=%s", identity.PID, identity.ParentPID, identity.Start, identity.PGID, identity.SessionID, identity.Executable))
	}
	return strings.Join(parts, ";")
}

// callIdleBinaryNoFail 为异步 lease 测试提供不依赖 testing.T 的调用通道。
func callIdleBinaryNoFail(client *mcpLSPBinaryClient, method string, params map[string]any) (mcpLSPBinaryResponse, error) {
	request := map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": method, "params": params}
	raw, err := json.Marshal(request)
	if err != nil {
		return mcpLSPBinaryResponse{}, err
	}
	if _, err := client.stdin.Write(append(raw, '\n')); err != nil {
		return mcpLSPBinaryResponse{}, err
	}
	line, err := client.stdout.ReadBytes('\n')
	if err != nil {
		return mcpLSPBinaryResponse{}, err
	}
	var response mcpLSPBinaryResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return mcpLSPBinaryResponse{}, err
	}
	if response.Error != nil {
		return response, fmt.Errorf("JSON-RPC %d: %s", response.Error.Code, response.Error.Message)
	}
	return response, nil
}
