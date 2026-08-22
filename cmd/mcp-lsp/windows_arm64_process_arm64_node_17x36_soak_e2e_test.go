//go:build windows && arm64 && e2e

package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	node17x36FormalEnv   = "MCP_LSP_REAL_NODE_WINDOWS_ARM64_PROCESS_ARM64_17X36_SOAK_15M"
	node17x36PrecheckEnv = "MCP_LSP_REAL_NODE_WINDOWS_ARM64_PROCESS_ARM64_17X36_PRECHECK"
	node17x36EvidenceEnv = "MCP_LSP_REAL_NODE_WINDOWS_ARM64_PROCESS_ARM64_17X36_EVIDENCE_DIR"
	node17x36FormalIdle  = 15 * time.Minute
	// 17 个语言按顺序完成矩阵后才开始正式 idle；管理器预算必须覆盖最早语言的
	// 矩阵耗时和完整十五分钟观察窗，不能只比 formal idle 多两分钟。
	node17x36ManagerIdle  = 30 * time.Minute
	node17x36TestTimeout  = 45 * time.Minute
	node17x36PrecheckTime = 30 * time.Second
)

// TestWindowsARM64Node17RetainedProcessHandleExitCode 验证短生命周期句柄合同，不是正式 E2E。
func TestWindowsARM64Node17RetainedProcessHandleExitCode(t *testing.T) {
	if os.Getenv("MCP_LSP_REAL_NODE_WINDOWS_ARM64_PROCESS_ARM64_HANDLE_TEST") != "1" {
		t.Skip("retained handle contract test is opt-in")
	}
	command := exec.Command("cmd.exe", "/c", "exit", "7")
	if err := command.Start(); err != nil {
		t.Fatalf("start retained-handle contract child: %v", err)
	}
	handle, err := retainRealMCPProcessHandle(command.Process.Pid)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("retain child handle: %v", err)
	}
	identity := realMCPProcessIdentity{PID: command.Process.Pid, ProcessHandle: handle}
	if err := command.Wait(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("contract child wait: %v", err)
		}
	}
	code, exited, err := realMCPRetainedExitCode(identity)
	if err != nil || !exited || code != 7 {
		t.Fatalf("retained exit code=(%d,%t,%v), want (7,true,nil)", code, exited, err)
	}
	closeRealMCPProcessHandles(map[realMCPProcessKey]realMCPProcessIdentity{{PID: identity.PID}: identity})
	if _, _, err := realMCPRetainedExitCode(identity); err == nil {
		t.Fatal("closed retained handle unexpectedly remained usable")
	}
}

// TestWindowsARM64ProcessARM64Node17x36PrecheckE2E 只做三十秒以内的结构预检。
// 预检收据明确标记 NON_PASS，不能被误读成安装、36-action 或生命周期证明。
func TestWindowsARM64ProcessARM64Node17x36PrecheckE2E(t *testing.T) {
	if os.Getenv(node17x36PrecheckEnv) != "1" {
		t.Skipf("set %s=1 to run the non-pass Windows ARM64 Node 17x36 precheck", node17x36PrecheckEnv)
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("Node 17x36 precheck requires Windows ARM64 test process, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	ctx, cancel := context.WithTimeout(context.Background(), node17x36PrecheckTime)
	defer cancel()
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform for Node 17x36 precheck: %v", err)
	}
	if host.OS != installer.WindowsHostOSWindows || host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("Node 17x36 precheck requires native/process ARM64, got os=%q native=%q process=%q build=%d", host.OS, host.NativeArch, host.ProcessArch, host.WindowsBuild)
	}
	servers := realNodeServerCases()
	requireRealNodeServerCaseClosure(t, servers)
	if len(servers) != realMCPExpectedLanguageCount {
		t.Fatalf("Node 17x36 precheck server count=%d, want %d", len(servers), realMCPExpectedLanguageCount)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("Node 17x36 precheck exceeded its thirty-second budget: %v", err)
	}

	repoRoot := realNodeRepoRoot(t)
	evidenceDir := node17x36EvidenceDirectory(t, repoRoot)
	receiptPath := filepath.Join(evidenceDir, "node-windows-arm64-process-arm64-17x36-precheck.receipt")
	receipt := []string{
		"test=windows-arm64-process-arm64-node-17x36-precheck",
		"status=NON_PASS",
		"formal_lifecycle=not_run",
		"semantic_actions=not_run",
		"action_total=0",
		"zero_residual=not_proven",
		"absolute_path_markers=0",
		"reason=precheck_only;_this_receipt_is_not_a_semantic_or_lifecycle_pass",
		fmt.Sprintf("host_os=%s", host.OS),
		fmt.Sprintf("host_native_arch=%s", host.NativeArch),
		fmt.Sprintf("host_process_arch=%s", host.ProcessArch),
		fmt.Sprintf("host_windows_build=%d", host.WindowsBuild),
	}
	if err := node17x36WriteReceipt(receiptPath, receipt); err != nil {
		t.Fatalf("write Node 17x36 precheck receipt: %v", err)
	}
	t.Logf("Node 17x36 precheck receipt=%s status=NON_PASS; formal lifecycle was not run", receiptPath)
}

// TestWindowsARM64ProcessARM64Node17x36LockedPayloadPathE2E 锁定 payload.zip 位于
// 不可变 asset root，而不是 ready tree；该快速回归不联网、不安装，也不构成正式证明。
func TestWindowsARM64ProcessARM64Node17x36LockedPayloadPathE2E(t *testing.T) {
	const productRoot = `C:\proof-product`
	asset := installer.WindowsLockedAsset{
		Version:      "22.22.0",
		Architecture: "arm64",
		SHA256:       "AABBCCDD",
	}
	got := node17x36LockedPayloadPath(productRoot, asset)
	want := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir, "node-runtime", asset.Version, asset.Architecture, "aabbccdd", "payload.zip")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("locked Node payload path=%q, want asset-root path=%q", got, want)
	}
	if strings.Contains(strings.ToLower(filepath.ToSlash(got)), "/ready/") {
		t.Fatalf("locked Node payload path must not enter ready tree: %q", got)
	}
}

// TestNode17x36ReusableProductRoot 验证显式复用根必须是现有绝对目录，并且不会被测试修改。
func TestNode17x36ReusableProductRoot(t *testing.T) {
	t.Run("unset keeps private-root mode", func(t *testing.T) {
		t.Setenv(realNodeWindowsReuseProductRootEnv, "")
		got, reused, err := node17x36ReusableProductRoot()
		if err != nil {
			t.Fatalf("node17x36ReusableProductRoot() error = %v", err)
		}
		if got != "" || reused {
			t.Fatalf("node17x36ReusableProductRoot() = (%q, %t), want (empty, false)", got, reused)
		}
	})

	t.Run("existing absolute root is reused", func(t *testing.T) {
		productRoot := t.TempDir()
		sentinel := filepath.Join(productRoot, "reuse-sentinel")
		if err := os.WriteFile(sentinel, []byte("must remain"), 0o600); err != nil {
			t.Fatalf("write reuse sentinel: %v", err)
		}
		t.Setenv(realNodeWindowsReuseProductRootEnv, productRoot)
		got, reused, err := node17x36ReusableProductRoot()
		if err != nil {
			t.Fatalf("node17x36ReusableProductRoot() error = %v", err)
		}
		if filepath.Clean(got) != filepath.Clean(productRoot) || !reused {
			t.Fatalf("node17x36ReusableProductRoot() = (%q, %t), want (%q, true)", got, reused, productRoot)
		}
		if _, err := os.Stat(sentinel); err != nil {
			t.Fatalf("reuse sentinel was changed: %v", err)
		}
	})

	t.Run("relative root fails fast", func(t *testing.T) {
		t.Setenv(realNodeWindowsReuseProductRootEnv, "relative-product-root")
		if _, _, err := node17x36ReusableProductRoot(); err == nil {
			t.Fatal("node17x36ReusableProductRoot() accepted a relative root")
		}
	})

	t.Run("missing root fails fast", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing-product-root")
		t.Setenv(realNodeWindowsReuseProductRootEnv, missing)
		if _, _, err := node17x36ReusableProductRoot(); err == nil {
			t.Fatal("node17x36ReusableProductRoot() accepted a missing root")
		}
	})
}

// node17x36ReusableProductRoot 解析显式复用根；未设置时返回空根和 false。
func node17x36ReusableProductRoot() (string, bool, error) {
	productRoot := strings.TrimSpace(os.Getenv(realNodeWindowsReuseProductRootEnv))
	if productRoot == "" {
		return "", false, nil
	}
	productRoot = filepath.Clean(productRoot)
	if !filepath.IsAbs(productRoot) {
		return "", true, fmt.Errorf("%s must be an absolute Windows product root: %q", realNodeWindowsReuseProductRootEnv, productRoot)
	}
	info, err := os.Stat(productRoot)
	if err != nil {
		return "", true, fmt.Errorf("stat reusable Windows product root %q: %w", productRoot, err)
	}
	if !info.IsDir() {
		return "", true, fmt.Errorf("reusable Windows product root %q is not a directory", productRoot)
	}
	return productRoot, true, nil
}

// TestWindowsARM64ProcessARM64Node17x36SoakE2E 先通过生产 EnsureInstalledDetailed
// 建立空私有产品根中的锁定 Node/npm cohort，再在同一 production MCP 进程中完成
// 17x36 action、十五分钟 idle、真实非空 LSP 请求和 PID+start-token 零残留证明。
func TestWindowsARM64ProcessARM64Node17x36SoakE2E(t *testing.T) {
	realMCPObservedExitSet.Store(false)
	if os.Getenv(node17x36FormalEnv) != "1" {
		t.Skipf("set %s=1 to run the Windows ARM64 Node 17x36 lifecycle proof", node17x36FormalEnv)
	}
	if testing.Short() {
		t.Skip("the Node 17x36 lifecycle proof is disabled by -short")
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("Node 17x36 lifecycle proof requires Windows ARM64 test process, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), node17x36TestTimeout)
	defer cancel()
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform for Node 17x36 lifecycle proof: %v", err)
	}
	if host.OS != installer.WindowsHostOSWindows || host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("Node 17x36 lifecycle proof requires native/process ARM64, got os=%q native=%q process=%q build=%d", host.OS, host.NativeArch, host.ProcessArch, host.WindowsBuild)
	}

	repoRoot := realNodeRepoRoot(t)
	servers := realNodeServerCases()
	requireRealNodeServerCaseClosure(t, servers)
	if len(servers) != realMCPExpectedLanguageCount {
		t.Fatalf("Node 17x36 lifecycle server count=%d, want %d", len(servers), realMCPExpectedLanguageCount)
	}
	productRoot, reuseProductRoot, err := node17x36ReusableProductRoot()
	if err != nil {
		t.Fatalf("resolve reusable Node 17x36 product root: %v", err)
	}
	productRootMode := "private_empty"
	if reuseProductRoot {
		productRootMode = "reused_existing"
	}

	evidenceDir := node17x36EvidenceDirectory(t, repoRoot)
	receiptPath := filepath.Join(evidenceDir, "node-windows-arm64-process-arm64-17x36-soak.receipt")
	receiptBase := []string{
		"test=windows-arm64-process-arm64-node-17x36-soak-15m",
		"formal_lifecycle=required",
		"action_total=not_started",
		"absolute_path_markers=0",
		fmt.Sprintf("product_root_mode=%s", productRootMode),
		"http_download_install_observation=required",
		"http_download_install_observation_scope=go_default_transport_only",
		"npm_child_network_observation=not_observed",
		"acl_win32_5_1314=typed_authorization_required_only;acl_changes=none",
		fmt.Sprintf("started_at=%s", startedAt.Format(time.RFC3339Nano)),
		fmt.Sprintf("host_os=%s", host.OS),
		fmt.Sprintf("host_native_arch=%s", host.NativeArch),
		fmt.Sprintf("host_process_arch=%s", host.ProcessArch),
		fmt.Sprintf("host_windows_version=%s", host.WindowsVersion),
		fmt.Sprintf("host_windows_build=%d", host.WindowsBuild),
		fmt.Sprintf("manager_idle_timeout=%s", node17x36ManagerIdle),
		fmt.Sprintf("formal_idle_required=%s", node17x36FormalIdle),
	}
	initialReceipt := append([]string{}, receiptBase...)
	initialReceipt = append(initialReceipt, "status=started")
	if err := node17x36WriteReceipt(receiptPath, initialReceipt); err != nil {
		t.Fatalf("write initial Node 17x36 receipt: %v", err)
	}
	var completedReceipt []string
	var tracked map[realMCPProcessKey]realMCPProcessIdentity
	var client *mcpLSPBinaryClient
	lifecycleReceiptReady := false
	t.Cleanup(func() {
		finalReceipt := append([]string{}, receiptBase...)
		if lifecycleReceiptReady && !t.Failed() {
			finalReceipt = append(finalReceipt, completedReceipt...)
		} else {
			finalReceipt = append(finalReceipt, "status=NON_PASS", "zero_residual=not_proven", "failure=see_test_log_for_first_root_cause")
			if pid, code, ok := realMCPObservedExit(); ok {
				finalReceipt = append(finalReceipt, fmt.Sprintf("child_exit_pid=%d", pid), fmt.Sprintf("child_exit_code=%d", code))
			} else {
				finalReceipt = append(finalReceipt, "child_exit_code=unavailable_without_retained_process_handle")
			}
			finalReceipt = append(finalReceipt, "job_or_recycler_reason=unavailable_in_current_observer")
			if client != nil {
				stderr := client.stderr.String()
				stderrDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(stderr)))
				tail := stderr
				if len(tail) > 2048 {
					tail = tail[len(tail)-2048:]
				}
				tailDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(tail)))
				finalReceipt = append(finalReceipt, "mcp_stderr_sha256="+stderrDigest, fmt.Sprintf("mcp_stderr_tail_bytes=%d", len(tail)), "mcp_stderr_tail_sha256="+tailDigest)
			}
			finalReceipt = append(finalReceipt, node17x36IdentityReceiptLines(tracked)...)
		}
		if err := node17x36WriteReceipt(receiptPath, finalReceipt); err != nil {
			t.Errorf("write failed Node 17x36 receipt: %v", err)
		}
	})

	if !reuseProductRoot {
		productRoot, err = os.MkdirTemp("", "sd-node-production-windows-arm64-process-arm64-17x36-")
		if err != nil {
			t.Fatalf("create empty private Node 17x36 product root: %v", err)
		}
		t.Cleanup(func() {
			if err := removeRealWindowsProductRoot(productRoot); err != nil {
				t.Errorf("remove Node 17x36 product root: %v", err)
			}
		})
		if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
			t.Fatalf("restrict empty private Node 17x36 product root: %v", err)
		}
	} else {
		t.Logf("reusing existing Node 17x36 product root without cleanup: %s", productRoot)
	}

	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "")
	t.Setenv("NODE_PATH", "")
	t.Setenv("SUPER_DOLPHIN_WINDOWS_NODE_PATH", "")
	t.Setenv("SUPER_DOLPHIN_MSVC_RUNTIME_DIR", "")
	t.Setenv("MCP_LSP_IDLE_TIMEOUT", node17x36ManagerIdle.String())
	t.Setenv("PATH", realNodePathWithoutNodeNPM(os.Getenv("PATH")))
	for _, commandName := range []string{"node", "node.exe", "npm", "npm.cmd"} {
		if resolved, lookErr := exec.LookPath(commandName); lookErr == nil {
			t.Fatalf("production Node 17x36 PATH still resolves %s at %q", commandName, resolved)
		}
	}
	cacheBefore, err := node17x36CacheEntryCount(productRoot)
	if err != nil {
		t.Fatalf("inspect empty Node 17x36 product cache: %v", err)
	}
	if !reuseProductRoot && cacheBefore != 0 {
		t.Fatalf("Node 17x36 product cache was not empty before production installation: entries=%d", cacheBefore)
	}

	previousHTTPTransport := http.DefaultTransport
	if previousHTTPTransport == nil {
		t.Fatal("Node 17x36 HTTP observation requires a default transport")
	}
	httpObserver := &node17x36HTTPObserver{base: previousHTTPTransport}
	http.DefaultTransport = httpObserver
	httpTransportRestored := false
	defer func() {
		if !httpTransportRestored {
			http.DefaultTransport = previousHTTPTransport
		}
	}()

	provider := setupInstaller()
	installCtx, installCancel := context.WithTimeout(ctx, node17x36TestTimeout)
	defer installCancel()
	node17x36Results := make(map[string]installer.InstallResult, len(servers))
	node17x36Configs := make(map[string]installer.InstallerConfig, len(servers))
	statusCounts := make(map[installer.InstallStatus]int)
	installLines := make([]string, 0, len(servers))
	for _, server := range servers {
		result, installErr := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(installCtx), server.languageID)
		if installErr != nil {
			t.Fatalf("production EnsureInstalledDetailed(%s) failed: %v", server.languageID, installErr)
		}
		if result.Status == installer.InstallStatusInstalledFallback {
			t.Fatalf("production EnsureInstalledDetailed(%s) used forbidden PATH fallback: %#v", server.languageID, result)
		}
		if _, rootErr := installer.WindowsShortProcessPathWithinRoot(productRoot, result.Path); rootErr != nil {
			t.Fatalf("production Node 17x36 %s escaped product root: %v", server.languageID, rootErr)
		}
		cfg, ok := provider.ConfigForLanguage(server.languageID)
		if !ok {
			t.Fatalf("production installer config disappeared for language %s", server.languageID)
		}
		relative, relErr := filepath.Rel(productRoot, result.Path)
		if relErr != nil || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatalf("production Node 17x36 %s result path is not relative to product root: %q (%v)", server.languageID, result.Path, relErr)
		}
		node17x36Results[server.languageID] = result
		node17x36Configs[server.languageID] = cfg
		statusCounts[result.Status]++
		installLines = append(installLines, fmt.Sprintf("install.%s.status=%s;binary=%s;relative_path=%s", server.languageID, result.Status, cfg.BinaryName, filepath.ToSlash(relative)))
	}
	http.DefaultTransport = previousHTTPTransport
	httpTransportRestored = true
	installHTTP := httpObserver.Snapshot()
	if reuseProductRoot {
		if installHTTP.Requests != 0 || installHTTP.Attempts != 0 || installHTTP.Responses != 0 || installHTTP.ActiveResponses != 0 || installHTTP.TransportErrors != 0 || installHTTP.RedirectResponses != 0 || installHTTP.SuccessfulResponses != 0 || installHTTP.FailedResponses != 0 {
			t.Fatalf("reused Node 17x36 product root performed installation HTTP work: requests=%d attempts=%d responses=%d active=%d transport_errors=%d redirects=%d successes=%d failed=%d", installHTTP.Requests, installHTTP.Attempts, installHTTP.Responses, installHTTP.ActiveResponses, installHTTP.TransportErrors, installHTTP.RedirectResponses, installHTTP.SuccessfulResponses, installHTTP.FailedResponses)
		}
	} else if installHTTP.Requests <= 0 || installHTTP.Attempts != installHTTP.Requests || installHTTP.TransportErrors != 0 || installHTTP.Responses != installHTTP.Requests || installHTTP.SuccessfulResponses <= 0 || installHTTP.FailedResponses != 0 {
		t.Fatalf("Node 17x36 HTTP install observation failed: requests=%d attempts=%d responses=%d transport_errors=%d redirects=%d successes=%d failed=%d", installHTTP.Requests, installHTTP.Attempts, installHTTP.Responses, installHTTP.TransportErrors, installHTTP.RedirectResponses, installHTTP.SuccessfulResponses, installHTTP.FailedResponses)
	}

	nodeRuntime, err := installer.NewWindowsNodeRuntime(productRoot, nil)
	if err != nil {
		t.Fatalf("construct locked Windows Node runtime after production installation: %v", err)
	}
	expectedPaths, err := nodeRuntime.ExpectedPaths()
	if err != nil {
		t.Fatalf("resolve locked Windows Node/npm cohort paths: %v", err)
	}
	for _, server := range servers {
		result := node17x36Results[server.languageID]
		cfg := node17x36Configs[server.languageID]
		expectedBinary, pathErr := nodeRuntime.BinaryPath(installCtx, cfg.BinaryName)
		if pathErr != nil {
			t.Fatalf("resolve locked binary path for %s: %v", server.languageID, pathErr)
		}
		if filepath.Clean(result.Path) != filepath.Clean(expectedBinary) {
			t.Fatalf("production Node 17x36 %s path=%q, want exact locked cohort path=%q", server.languageID, result.Path, expectedBinary)
		}
	}

	allPackages := make([]string, 0)
	seenPackages := make(map[string]struct{})
	for _, spec := range runtimeNPMInstallerSpecsForPlatform("windows") {
		packages, packageErr := runtimeNPMExactPackages(spec.args)
		if packageErr != nil {
			t.Fatalf("parse locked Windows npm package pins: %v", packageErr)
		}
		for _, specification := range packages {
			if _, seen := seenPackages[specification]; seen {
				continue
			}
			seenPackages[specification] = struct{}{}
			allPackages = append(allPackages, specification)
		}
	}
	if err := nodeRuntime.ValidateExactPackages(installCtx, allPackages); err != nil {
		t.Fatalf("validate exact Windows Node/npm cohort after 17 EnsureInstalledDetailed calls: %v", err)
	}
	asset, err := installer.WindowsNodeRuntimeAssetForPlatform(host)
	if err != nil {
		t.Fatalf("select locked Windows Node asset for receipt: %v", err)
	}
	// payload.zip 属于不可变 asset root，不在 ready tree 内；按锁定 manifest 字段构造路径，
	// 避免从 node.exe 的嵌套目录层级反推并把 ready 误当成资产根目录。
	nodePayload := node17x36LockedPayloadPath(productRoot, asset)
	if gotSHA, hashErr := sha256File(nodePayload); hashErr != nil {
		t.Fatalf("hash locked Windows Node payload path=%q ready_node=%q: %v", nodePayload, expectedPaths.NodePath, hashErr)
	} else if !strings.EqualFold(gotSHA, asset.SHA256) {
		t.Fatalf("locked Windows Node payload SHA256=%s, want %s", gotSHA, asset.SHA256)
	}
	vclibsAsset, err := installer.WindowsVCLibsDesktopAssetForPlatform(host)
	if err != nil {
		t.Fatalf("select locked Windows VCLibs asset for receipt: %v", err)
	}
	vclibsRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalPath(productRoot)
	if err != nil {
		t.Fatalf("resolve locked Windows VCLibs ready root: %v", err)
	}
	vclibsProcessRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalProcessPath(productRoot)
	if err != nil {
		t.Fatalf("resolve locked Windows VCLibs process root: %v", err)
	}
	if same, sameErr := sameRealNodeFile(vclibsRoot, vclibsProcessRoot); sameErr != nil {
		t.Fatalf("compare Windows VCLibs ready/process identity: %v", sameErr)
	} else if !same {
		t.Fatalf("Windows VCLibs process root %q changed ready identity %q", vclibsProcessRoot, vclibsRoot)
	}
	vclibsPayload := filepath.Join(filepath.Dir(vclibsRoot), "payload.zip")
	if gotSHA, hashErr := sha256File(vclibsPayload); hashErr != nil {
		t.Fatalf("hash locked Windows VCLibs payload: %v", hashErr)
	} else if !strings.EqualFold(gotSHA, vclibsAsset.SHA256) {
		t.Fatalf("locked Windows VCLibs payload SHA256=%s, want %s", gotSHA, vclibsAsset.SHA256)
	}
	t.Setenv("SUPER_DOLPHIN_MSVC_RUNTIME_DIR", vclibsProcessRoot)
	t.Setenv("SUPER_DOLPHIN_WINDOWS_NODE_PATH", expectedPaths.NodePath)
	if vueResult, ok := node17x36Results["vue"]; ok {
		if bridgeSpec, bridgeErr := runtimeServerWindowsVueTSBridgeSpec(vueResult.Path); bridgeErr != nil {
			t.Fatalf("resolve production Vue TypeScript bridge cohort: %v", bridgeErr)
		} else {
			t.Logf("production Vue bridge uses locked TypeScript binary=%s plugin=%s", filepath.Base(bridgeSpec.typescriptBinary), filepath.Base(bridgeSpec.vuePluginLocation))
		}
	}
	cacheAfter, err := node17x36CacheEntryCount(productRoot)
	if err != nil {
		t.Fatalf("inspect Node 17x36 product cache after installation: %v", err)
	}
	t.Logf("production Node 17x36 install host=%s/%s/%s build=%d node_version=%s node_sha256=%s vclibs_version=%s vclibs_sha256=%s cache_before=%d cache_after=%d ensure_installed_calls=%d status_counts=%v http_requests=%d http_attempts=%d http_responses=%d http_redirects=%d http_successes=%d http_errors=%d",
		host.OS, host.NativeArch, host.ProcessArch, host.WindowsBuild, asset.Version, asset.SHA256, vclibsAsset.Version, vclibsAsset.SHA256, cacheBefore, cacheAfter, len(servers), statusCounts, installHTTP.Requests, installHTTP.Attempts, installHTTP.Responses, installHTTP.RedirectResponses, installHTTP.SuccessfulResponses, installHTTP.TransportErrors)

	binary := buildRealMcpLSPBinary(t, repoRoot)
	fixtureRoot := t.TempDir()
	registerRealMCPTempRootCleanup(t, fixtureRoot)
	astFile := filepath.Join(fixtureRoot, "ast_fixture.js")
	writeRealFixture(t, astFile, "function realMCPAstFixture(name) { return name; }\nrealMCPAstFixture(\"world\");\n")
	client = startRealMcpLSPBinary(t, ctx, binary, fixtureRoot, repoRoot, "", "", productRoot)
	mcpPID := client.cmd.Process.Pid
	tracked = make(map[realMCPProcessKey]realMCPProcessIdentity)
	mcpStart, err := windowsGoplsProcessStartIdentity(mcpPID)
	if err != nil {
		t.Fatalf("capture Node 17x36 MCP PID %d start identity: %v", mcpPID, err)
	}
	tracked[realMCPProcessKey{PID: mcpPID, StartToken: mcpStart}] = realMCPProcessIdentity{PID: mcpPID, StartToken: mcpStart, Name: "mcp-lsp", Language: "mcp-lsp"}
	shutdownSent := false
	postIdleCount := 0
	defer func() {
		if trackRealMCPProcessTree(t, mcpPID, "final-before-close", tracked) {
			requireRealMCPTypeScriptUserDataWithinRoot(t, tracked, productRoot)
			logRealMCPProcessIdentities(t, tracked)
		}
		exitSent := node17x36CloseWithExitProof(t, client)
		if !shutdownSent {
			t.Log("Node 17x36 MCP did not receive protocol shutdown because the test failed before the final lifecycle gate")
		}
		requireRealMCPProcessIdentitiesGone(t, tracked)
		if shutdownSent && exitSent && !t.Failed() {
			completedReceipt = append(completedReceipt,
				"exit=sent",
				"zero_residual=proven",
				fmt.Sprintf("finished_at=%s", time.Now().Format(time.RFC3339Nano)),
				fmt.Sprintf("elapsed=%s", time.Since(startedAt).Round(time.Millisecond)),
			)
			lifecycleReceiptReady = true
			t.Logf("Node 17x36 formal lifecycle receipt=%s elapsed=%s matrix=612 post_idle=%d zero_residual=proven", receiptPath, time.Since(startedAt), postIdleCount)
		}
	}()

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}})
	requireRealMCPToolFamilies(t, callMcpLSPBinaryRaw(t, client, "tools/list", map[string]any{}))
	matrix, _, actionsByLanguage, actionLedger := node17x36RunActionMatrix(t, client, mcpPID, fixtureRoot, astFile, servers, tracked)
	if matrix.total != realMCPExpectedLanguageCount*realMCPExpectedActionCount || matrix.succeeded+matrix.capabilityUnsupported != matrix.total {
		t.Fatalf("Node 17x36 matrix accounting failed: total=%d success=%d legal_empty=%d capability_unsupported=%d", matrix.total, matrix.succeeded, matrix.legalEmpty, matrix.capabilityUnsupported)
	}
	if len(tracked) <= 1 {
		t.Fatalf("Node 17x36 process tree captured no language-server descendant: tracked=%d", len(tracked))
	}
	node17x36RequireLanguageIdentities(t, tracked, servers)
	baseline := node17x36ProcessKeys(tracked)

	idleStarted := time.Now()
	idleDeadline := idleStarted.Add(node17x36FormalIdle)
	for {
		remaining := time.Until(idleDeadline)
		if remaining <= 0 {
			break
		}
		if remaining > time.Minute {
			remaining = time.Minute
		}
		time.Sleep(remaining)
		if !trackRealMCPProcessTree(t, mcpPID, "idle-heartbeat", tracked) {
			t.Fatalf("Node 17x36 idle heartbeat could not capture the process tree")
		}
		node17x36AssertProcessKeysUnchanged(t, baseline, tracked, "idle-heartbeat")
		t.Logf("Node 17x36 idle heartbeat elapsed=%s identities=%d", time.Since(idleStarted).Round(time.Second), len(tracked))
	}
	idleElapsed := time.Since(idleStarted)
	if idleElapsed < node17x36FormalIdle {
		t.Fatalf("Node 17x36 formal idle elapsed=%s, want at least %s", idleElapsed, node17x36FormalIdle)
	}

	for _, server := range servers {
		action, ok := node17x36PostIdleAction(actionsByLanguage[server.languageID])
		if !ok {
			t.Fatalf("%s has no required non-empty LSP-backed post-idle action", server.languageID)
		}
		requestArgs := realMCPWindowsToolArguments(server.languageID, fixtureRoot, action.tool, action.name, action.args)
		response := client.callTool(t, action.tool, requestArgs)
		status := requireRealMCPActionResult(t, response, true, "", action.allowCapabilityUnsupported, realMCPActionCapabilityKey(action.tool, action.name), realMCPActionProtocolOptional(action.tool, action.name), server.languageID+" post-idle "+action.tool+"/"+action.name)
		if status != realMCPActionSucceeded {
			t.Fatalf("%s post-idle action %s/%s was not a real non-empty semantic success: %s", server.languageID, action.tool, action.name, status)
		}
		postIdleCount++
		if !trackRealMCPProcessTree(t, mcpPID, "post-idle-"+server.languageID, tracked) {
			t.Fatalf("%s post-idle process tree capture failed", server.languageID)
		}
		node17x36AssertProcessKeysUnchanged(t, baseline, tracked, "post-idle-"+server.languageID)
	}
	if postIdleCount != realMCPExpectedLanguageCount {
		t.Fatalf("Node 17x36 post-idle semantic action count=%d, want %d", postIdleCount, realMCPExpectedLanguageCount)
	}

	client.call(t, "shutdown", map[string]any{})
	shutdownSent = true
	completedReceipt = append(completedReceipt,
		"status=PASS",
		fmt.Sprintf("action_total=%d", realMCPExpectedLanguageCount*realMCPExpectedActionCount),
		fmt.Sprintf("success_including_legal_empty=%d", matrix.succeeded),
		fmt.Sprintf("legal_empty=%d", matrix.legalEmpty),
		fmt.Sprintf("semantic_success=%d", matrix.succeeded-matrix.legalEmpty),
		fmt.Sprintf("capability_unsupported=%d", matrix.capabilityUnsupported),
		"runtime_failure=0",
		"null_result=0",
		fmt.Sprintf("formal_idle_elapsed=%s", idleElapsed),
		fmt.Sprintf("post_idle_non_empty_actions=%d", postIdleCount),
		"shutdown=sent",
		fmt.Sprintf("cache_entries_before=%d", cacheBefore),
		fmt.Sprintf("cache_entries_after=%d", cacheAfter),
		fmt.Sprintf("ensure_installed_calls=%d", len(servers)),
		fmt.Sprintf("http_requests=%d", installHTTP.Requests),
		fmt.Sprintf("http_attempts=%d", installHTTP.Attempts),
		fmt.Sprintf("http_responses=%d", installHTTP.Responses),
		fmt.Sprintf("http_redirect_responses=%d", installHTTP.RedirectResponses),
		fmt.Sprintf("http_successful_responses=%d", installHTTP.SuccessfulResponses),
		fmt.Sprintf("http_transport_errors=%d", installHTTP.TransportErrors),
		fmt.Sprintf("http_failed_responses=%d", installHTTP.FailedResponses),
		"verified_locked_payloads=2",
		fmt.Sprintf("node_version=%s", asset.Version),
		fmt.Sprintf("node_url=%s", asset.URL),
		fmt.Sprintf("node_sha256=%s", asset.SHA256),
		fmt.Sprintf("vclibs_version=%s", vclibsAsset.Version),
		fmt.Sprintf("vclibs_url=%s", vclibsAsset.URL),
		fmt.Sprintf("vclibs_sha256=%s", vclibsAsset.SHA256),
		fmt.Sprintf("mcp_pid=%d;mcp_start=%s", mcpPID, mcpStart),
	)
	for _, status := range []installer.InstallStatus{installer.InstallStatusPathFound, installer.InstallStatusInstalledPath, installer.InstallStatusInstalledFallback} {
		completedReceipt = append(completedReceipt, fmt.Sprintf("install_status_count.%s=%d", status, statusCounts[status]))
	}
	completedReceipt = append(completedReceipt, actionLedger...)
	completedReceipt = append(completedReceipt, installLines...)
	completedReceipt = append(completedReceipt, node17x36IdentityReceiptLines(tracked)...)
	t.Logf("Node 17x36 semantic and idle gates passed; deferred shutdown close and zero-residual proof remain pending receipt=%s", receiptPath)
}

// node17x36LockedPayloadPath 根据生产 cache 契约返回锁定 Node 归档路径。
// ready 只保存解包结果；payload.zip 始终是其同级 asset root 的直接子项。
func node17x36LockedPayloadPath(productRoot string, asset installer.WindowsLockedAsset) string {
	return filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir, "node-runtime", asset.Version, asset.Architecture, strings.ToLower(asset.SHA256), "payload.zip")
}

// node17x36RunActionMatrix 复用 realMCPActionSpecs 和现有结果合同，不复制 36-action 清单。
func node17x36RunActionMatrix(t *testing.T, client *mcpLSPBinaryClient, mcpPID int, fixtureRoot, astFile string, servers []realNodeServerCase, tracked map[realMCPProcessKey]realMCPProcessIdentity) (realMCPMatrixSummary, map[string]realMCPFixture, map[string][]realMCPActionSpec, []string) {
	t.Helper()
	var matrix realMCPMatrixSummary
	fixtures := make(map[string]realMCPFixture, len(servers))
	actionsByLanguage := make(map[string][]realMCPActionSpec, len(servers))
	actionLedger := make([]string, 0, len(servers))
	for _, server := range servers {
		fixture := writeRealMCPLanguageFixture(t, fixtureRoot, server)
		actions := realMCPActionSpecs(server, fixture, astFile)
		if err := validateRealMCPActionClosure(actions); err != nil {
			t.Fatalf("%s action closure: %v", server.languageID, err)
		}
		fixtures[server.languageID] = fixture
		actionsByLanguage[server.languageID] = actions
		var languageSummary realMCPMatrixSummary
		for actionIndex, action := range actions {
			actionOrdinal := actionIndex + 1
			actionStarted := time.Now()
			t.Logf("Node 17x36 action start language=%s ordinal=%d/%d tool=%s name=%s require_non_empty=%t allow_capability_unsupported=%t", server.languageID, actionOrdinal, realMCPExpectedActionCount, action.tool, action.name, action.requireResult, action.allowCapabilityUnsupported)
			requestArgs := realMCPWindowsToolArguments(server.languageID, fixtureRoot, action.tool, action.name, action.args)
			response := client.callTool(t, action.tool, requestArgs)
			status := requireRealMCPActionResult(t, response, action.requireResult, action.emptyResultReason, action.allowCapabilityUnsupported, realMCPActionCapabilityKey(action.tool, action.name), realMCPActionProtocolOptionalForServer(server, action.tool, action.name), server.languageID+" "+action.tool+"/"+action.name)
			t.Logf("Node 17x36 action done language=%s ordinal=%d/%d tool=%s name=%s duration=%s status=%s", server.languageID, actionOrdinal, realMCPExpectedActionCount, action.tool, action.name, time.Since(actionStarted).Round(time.Millisecond), status)
			languageSummary.total++
			switch status {
			case realMCPActionSucceeded:
				languageSummary.succeeded++
			case realMCPActionLegalEmpty:
				languageSummary.succeeded++
				languageSummary.legalEmpty++
			case realMCPActionUnsupported:
				languageSummary.capabilityUnsupported++
				languageSummary.unsupportedActions = append(languageSummary.unsupportedActions, action.tool+"/"+action.name)
			default:
				t.Fatalf("%s returned unclassified status %q", action.tool+"/"+action.name, status)
			}
		}
		if languageSummary.total != realMCPExpectedActionCount || languageSummary.succeeded+languageSummary.capabilityUnsupported != languageSummary.total {
			t.Fatalf("%s action accounting total=%d success=%d legal_empty=%d capability_unsupported=%d", server.languageID, languageSummary.total, languageSummary.succeeded, languageSummary.legalEmpty, languageSummary.capabilityUnsupported)
		}
		t.Logf("Node 17x36 language=%s total=%d success=%d legal_empty=%d capability_unsupported=%d unsupported_actions=%v", server.languageID, languageSummary.total, languageSummary.succeeded, languageSummary.legalEmpty, languageSummary.capabilityUnsupported, languageSummary.unsupportedActions)
		actionLedger = append(actionLedger, fmt.Sprintf("language.%s.total=%d;success_including_legal_empty=%d;legal_empty=%d;semantic_success=%d;capability_unsupported=%d;runtime_failure=0;null_result=0;unsupported_actions=%s", server.languageID, languageSummary.total, languageSummary.succeeded, languageSummary.legalEmpty, languageSummary.succeeded-languageSummary.legalEmpty, languageSummary.capabilityUnsupported, strings.Join(languageSummary.unsupportedActions, ",")))
		matrix.total += languageSummary.total
		matrix.succeeded += languageSummary.succeeded
		matrix.legalEmpty += languageSummary.legalEmpty
		matrix.capabilityUnsupported += languageSummary.capabilityUnsupported
		matrix.unsupportedActions = append(matrix.unsupportedActions, languageSummary.unsupportedActions...)
		if !trackRealMCPProcessTree(t, mcpPID, server.languageID, tracked) {
			t.Fatalf("%s process tree capture failed", server.languageID)
		}
	}
	return matrix, fixtures, actionsByLanguage, actionLedger
}

// node17x36PostIdleAction 选择已有合同中必需且有真实 LSP 结果的公开动作。
func node17x36PostIdleAction(actions []realMCPActionSpec) (realMCPActionSpec, bool) {
	for _, action := range actions {
		if !action.requireResult || action.allowCapabilityUnsupported {
			continue
		}
		switch action.tool {
		case "inspect", "xref", "structure", "completion":
			return action, true
		}
	}
	return realMCPActionSpec{}, false
}

// node17x36ProcessKeys 返回 PID+start-token 快照；生命周期中任何变化都必须失败。
func node17x36ProcessKeys(tracked map[realMCPProcessKey]realMCPProcessIdentity) map[realMCPProcessKey]struct{} {
	keys := make(map[realMCPProcessKey]struct{}, len(tracked))
	for key := range tracked {
		keys[key] = struct{}{}
	}
	return keys
}

// node17x36AssertProcessKeysUnchanged 检查 idle/post-idle 没有退出、启动或 PID 复用。
// tracked 是累计快照，因此除比较键集合外，还必须实时验证每个基线 PID+start 仍存活。
func node17x36AssertProcessKeysUnchanged(t *testing.T, baseline map[realMCPProcessKey]struct{}, tracked map[realMCPProcessKey]realMCPProcessIdentity, phase string) {
	t.Helper()
	current := node17x36ProcessKeys(tracked)
	if len(current) != len(baseline) {
		t.Fatalf("Node 17x36 %s process identity count changed from %d to %d", phase, len(baseline), len(current))
	}
	for key := range baseline {
		identity, ok := tracked[key]
		if !ok {
			t.Fatalf("Node 17x36 %s lost PID=%d start=%s", phase, key.PID, key.StartToken)
		}
		alive, err := processAliveForE2E(key.PID)
		if err != nil {
			t.Fatalf("Node 17x36 %s inspect PID=%d start=%s language=%s: %v", phase, key.PID, key.StartToken, identity.Language, err)
		}
		if !alive {
			code, exited, exitErr := realMCPRetainedExitCode(identity)
			if exitErr != nil {
				t.Fatalf("Node 17x36 %s baseline process exited PID=%d start=%s language=%s; exit_code=unavailable: %v", phase, key.PID, key.StartToken, identity.Language, exitErr)
			}
			if exited {
				recordRealMCPObservedExit(key.PID, code)
				t.Fatalf("Node 17x36 %s baseline process exited PID=%d start=%s language=%s exit_code=%d", phase, key.PID, key.StartToken, identity.Language, code)
			}
			t.Fatalf("Node 17x36 %s baseline process exited PID=%d start=%s language=%s; exit_code=still_active", phase, key.PID, key.StartToken, identity.Language)
		}
		start, err := windowsGoplsProcessStartIdentity(key.PID)
		if err != nil {
			t.Fatalf("Node 17x36 %s read live PID=%d start identity language=%s: %v", phase, key.PID, identity.Language, err)
		}
		if start != key.StartToken {
			t.Fatalf("Node 17x36 %s PID reuse detected PID=%d baseline_start=%s current_start=%s language=%s", phase, key.PID, key.StartToken, start, identity.Language)
		}
	}
}

// node17x36RequireLanguageIdentities 证明每个 language action 都观察到真实后代进程。
func node17x36RequireLanguageIdentities(t *testing.T, tracked map[realMCPProcessKey]realMCPProcessIdentity, servers []realNodeServerCase) {
	t.Helper()
	for _, server := range servers {
		found := false
		for _, identity := range tracked {
			for _, language := range strings.Split(identity.Language, ",") {
				if language == server.languageID {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			t.Fatalf("Node 17x36 captured no PID+start identity for language %s", server.languageID)
		}
	}
}

// node17x36IdentityReceiptLines 写 PID/start、父 PID/start 和命令摘要，不写原始命令行路径。
func node17x36IdentityReceiptLines(tracked map[realMCPProcessKey]realMCPProcessIdentity) []string {
	identities := make([]realMCPProcessIdentity, 0, len(tracked))
	for _, identity := range tracked {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].PID != identities[j].PID {
			return identities[i].PID < identities[j].PID
		}
		return identities[i].StartToken < identities[j].StartToken
	})
	lines := make([]string, 0, len(identities))
	for _, identity := range identities {
		name := filepath.Base(filepath.ToSlash(identity.Name))
		language := strings.NewReplacer("\r", "_", "\n", "_", ";", "_").Replace(identity.Language)
		lines = append(lines, fmt.Sprintf("process.pid=%d;start=%s;parent_pid=%d;parent_start=%s;name=%s;command_sha256=%s;language=%s", identity.PID, identity.StartToken, identity.ParentPID, identity.ParentStartToken, name, identity.CommandSHA256, language))
	}
	return lines
}

// node17x36CacheEntryCount 只统计产品根内 locked asset cache 的直接 cohort 数量。
func node17x36CacheEntryCount(productRoot string) (int, error) {
	cacheRoot := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir)
	entries, err := os.ReadDir(cacheRoot)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

// node17x36EvidenceDirectory 返回本地证据目录；收据内容本身不落绝对机器路径。
func node17x36EvidenceDirectory(t *testing.T, repoRoot string) string {
	t.Helper()
	evidenceDir := strings.TrimSpace(os.Getenv(node17x36EvidenceEnv))
	if evidenceDir == "" {
		evidenceDir = filepath.Join(repoRoot, ".build-cache", "lsp-test-results")
	}
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatalf("create Node 17x36 evidence directory: %v", err)
	}
	return evidenceDir
}

func node17x36WriteReceipt(path string, lines []string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("Node 17x36 receipt path is empty")
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// node17x36HTTPCounts 只记录生产安装的 HTTP 生命周期计数，不保存 URL、请求头、内容或机器路径。
type node17x36HTTPCounts struct {
	Requests            int64
	Attempts            int64
	Responses           int64
	ActiveResponses     int64
	TransportErrors     int64
	RedirectResponses   int64
	SuccessfulResponses int64
	FailedResponses     int64
}

// node17x36HTTPObserver 包裹生产安装使用的默认 transport；原子计数允许并行 npm 下载，
// RoundTrip 不读取请求内容，增强日志不会泄露供应链 URL、凭据或本机路径。
type node17x36HTTPObserver struct {
	base                http.RoundTripper
	requests            atomic.Int64
	attempts            atomic.Int64
	responses           atomic.Int64
	activeResponses     atomic.Int64
	transportErrors     atomic.Int64
	redirectResponses   atomic.Int64
	successfulResponses atomic.Int64
	failedResponses     atomic.Int64
}

func (o *node17x36HTTPObserver) RoundTrip(request *http.Request) (*http.Response, error) {
	if o == nil || o.base == nil {
		return nil, errors.New("Node 17x36 HTTP observer transport is unavailable")
	}
	o.requests.Add(1)
	o.attempts.Add(1)
	response, err := o.base.RoundTrip(request)
	if err != nil {
		o.transportErrors.Add(1)
		return nil, err
	}
	o.responses.Add(1)
	o.activeResponses.Add(1)
	response.Body = node17x36ObservedResponseBody{ReadCloser: response.Body, active: &o.activeResponses, closed: &atomic.Bool{}}
	switch {
	case response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < 400:
		o.redirectResponses.Add(1)
	case response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices:
		o.successfulResponses.Add(1)
	default:
		o.failedResponses.Add(1)
	}
	return response, nil
}

func (o *node17x36HTTPObserver) Snapshot() node17x36HTTPCounts {
	if o == nil {
		return node17x36HTTPCounts{}
	}
	return node17x36HTTPCounts{
		Requests:            o.requests.Load(),
		Attempts:            o.attempts.Load(),
		Responses:           o.responses.Load(),
		ActiveResponses:     o.activeResponses.Load(),
		TransportErrors:     o.transportErrors.Load(),
		RedirectResponses:   o.redirectResponses.Load(),
		SuccessfulResponses: o.successfulResponses.Load(),
		FailedResponses:     o.failedResponses.Load(),
	}
}

type node17x36ObservedResponseBody struct {
	io.ReadCloser
	active *atomic.Int64
	closed *atomic.Bool
}

func (b node17x36ObservedResponseBody) Close() error {
	if b.active != nil && (b.closed == nil || b.closed.CompareAndSwap(false, true)) {
		b.active.Add(-1)
	}
	if b.ReadCloser == nil {
		return nil
	}
	return b.ReadCloser.Close()
}

// node17x36CloseWithExitProof 发送完整 exit notification、关闭 stdin 并等待 MCP 自行退出；
// 写失败、非零退出、超时强杀或 process owner 释放失败都会把正式证明标记为 NON_PASS。
func node17x36CloseWithExitProof(t *testing.T, client *mcpLSPBinaryClient) bool {
	t.Helper()
	if client == nil || client.cmd == nil || client.stdin == nil {
		t.Error("Node 17x36 close requires a live MCP client and stdin")
		return false
	}
	cmd := client.cmd
	client.cmd = nil
	closeHook := client.closeHook
	client.closeHook = nil
	defer func() {
		if closeHook == nil {
			return
		}
		if err := closeHook(); err != nil {
			t.Errorf("release Node 17x36 MCP process owner: %v", err)
		}
	}()

	exitNotification := []byte("{\"jsonrpc\":\"2.0\",\"method\":\"exit\"}\n")
	written, err := client.stdin.Write(exitNotification)
	if err != nil || written != len(exitNotification) {
		t.Errorf("send Node 17x36 MCP exit notification: bytes=%d/%d error=%v", written, len(exitNotification), err)
		_ = client.stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return false
	}
	if err := client.stdin.Close(); err != nil {
		t.Errorf("close Node 17x36 MCP stdin after exit: %v", err)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return false
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("Node 17x36 MCP exited non-zero after shutdown/exit: %v; stderr=%s", err, client.stderrString())
			return false
		}
		return true
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		err := <-done
		t.Errorf("Node 17x36 MCP required kill after shutdown/exit timeout: wait=%v stderr=%s", err, client.stderrString())
		return false
	}
}
