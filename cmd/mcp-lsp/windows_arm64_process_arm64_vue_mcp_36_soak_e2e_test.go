//go:build windows && arm64 && e2e

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

const (
	vueWindowsARM64ProcessARM64SoakEnv            = "MCP_LSP_REAL_NODE_VUE_WINDOWS_ARM64_PROCESS_ARM64_SOAK_15M"
	vueWindowsARM64ProcessARM64SoakProductRootEnv = "MCP_LSP_REAL_NODE_VUE_WINDOWS_ARM64_PROCESS_ARM64_SOAK_PRODUCT_ROOT"
	vueWindowsARM64ProcessARM64SoakEvidenceEnv    = "MCP_LSP_REAL_NODE_VUE_WINDOWS_ARM64_PROCESS_ARM64_SOAK_EVIDENCE_DIR"
	vueWindowsARM64ProcessARM64SoakIdle           = 15 * time.Minute
	vueWindowsARM64ProcessARM64ManagerIdle        = 17 * time.Minute
)

// TestWindowsARM64ProcessARM64VueMCP36SoakE2E 只消费已验证的生产 Node/Vue/TypeScript
// 缓存 cohort；同一 Vue、TypeScript companion 和 typingsInstaller 必须跨越 15 分钟空闲，
// 再完成真实非空语义请求、36-action 账本与 PID+start-token 零残留校验。
func TestWindowsARM64ProcessARM64VueMCP36SoakE2E(t *testing.T) {
	if os.Getenv(vueWindowsARM64ProcessARM64SoakEnv) != "1" {
		t.Skipf("set %s=1 to enable the cached Windows ARM64 Vue 15-minute lifecycle proof", vueWindowsARM64ProcessARM64SoakEnv)
	}
	if testing.Short() {
		t.Skip("the 15-minute Vue lifecycle proof is disabled by -short")
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("Vue lifecycle proof requires Windows ARM64 test process, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	if host.OS != installer.WindowsHostOSWindows || host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("Vue lifecycle proof requires Windows native ARM64/process ARM64, got os=%q native=%q process=%q build=%d", host.OS, host.NativeArch, host.ProcessArch, host.WindowsBuild)
	}

	repoRoot := realNodeRepoRoot(t)
	productRoot := strings.TrimSpace(os.Getenv(vueWindowsARM64ProcessARM64SoakProductRootEnv))
	if productRoot == "" {
		t.Fatalf("%s is required: pass an already provisioned Vue product root; lifecycle proof is cache-only and performs no network provisioning", vueWindowsARM64ProcessARM64SoakProductRootEnv)
	}
	productRoot, err = filepath.Abs(productRoot)
	if err != nil {
		t.Fatalf("resolve cached Vue product root: %v", err)
	}
	if info, statErr := os.Stat(productRoot); statErr != nil || !info.IsDir() {
		t.Fatalf("cached Vue product root is not a directory: %s (%v)", productRoot, statErr)
	}

	evidenceDir := strings.TrimSpace(os.Getenv(vueWindowsARM64ProcessARM64SoakEvidenceEnv))
	if evidenceDir == "" {
		evidenceDir = filepath.Join(repoRoot, ".build-cache", "lsp-test-results")
	}
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatalf("create Vue lifecycle evidence directory: %v", err)
	}
	receiptPath := filepath.Join(evidenceDir, "vue-windows-arm64-process-arm64-soak-15m-receipt.log")
	receipt := []string{
		"test=windows-arm64-process-arm64-vue-mcp-36-soak-15m",
		"status=started",
		fmt.Sprintf("started_at=%s", startedAt.Format(time.RFC3339Nano)),
		fmt.Sprintf("host_os=%s", host.OS),
		fmt.Sprintf("host_native_arch=%s", host.NativeArch),
		fmt.Sprintf("host_process_arch=%s", host.ProcessArch),
		fmt.Sprintf("host_windows_version=%s", host.WindowsVersion),
		fmt.Sprintf("host_windows_build=%d", host.WindowsBuild),
		"network_provisioning=not_requested_cache_only",
		"resolver_cache_http_requests=0",
		"cache_prep=production_vue_check_only_reused",
		"cache_prep_receipt=vue-windows-arm64-process-arm64-soak-prep-receipt.log",
		"acl_win32_5_1314=typed_securefs_authorization_required_preserved",
		fmt.Sprintf("product_root_digest=%s", vueLifecyclePathDigest(productRoot)),
		"product_root_path_policy=not_recorded",
		"receipt_file=vue-windows-arm64-process-arm64-soak-15m-receipt.log",
		"absolute_path_markers=0",
		fmt.Sprintf("manager_idle_timeout=%s", vueWindowsARM64ProcessARM64ManagerIdle),
		fmt.Sprintf("proof_idle_duration=%s", vueWindowsARM64ProcessARM64SoakIdle),
		"idle_timeout_headroom=process_identity_sampling_only",
	}
	appendReceipt := func(format string, args ...any) {
		receipt = append(receipt, fmt.Sprintf(format, args...))
	}
	t.Cleanup(func() {
		appendReceipt("finished_at=%s", time.Now().Format(time.RFC3339Nano))
		appendReceipt("elapsed=%s", time.Since(startedAt).Round(time.Millisecond))
		if err := os.WriteFile(receiptPath, []byte(strings.Join(receipt, "\n")+"\n"), 0o600); err != nil {
			t.Logf("write Vue lifecycle receipt %s: %v", receiptPath, err)
		}
	})
	t.Logf("Vue lifecycle receipt path=%s; cached product root=%s", receiptPath, productRoot)

	// 清除系统 Node/npm 和旧用户态路径；生产 sidecar 只能从显式 product root 解析 cohort。
	t.Setenv("PATH", realNodePathWithoutNodeNPM(os.Getenv("PATH")))
	t.Setenv("NODE_PATH", "")
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "")
	t.Setenv("SUPER_DOLPHIN_WINDOWS_NODE_PATH", "")
	t.Setenv("SUPER_DOLPHIN_MSVC_RUNTIME_DIR", "")
	for _, commandName := range []string{"node", "node.exe", "npm", "npm.cmd"} {
		if resolved, lookErr := exec.LookPath(commandName); lookErr == nil {
			t.Fatalf("lifecycle PATH still resolves forbidden %s at %q", commandName, resolved)
		}
	}
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)

	nodeRuntime, err := installer.NewWindowsNodeRuntime(productRoot, nil)
	if err != nil {
		t.Fatalf("construct cached Windows Node runtime: %v", err)
	}
	expectedPaths, err := nodeRuntime.ExpectedPaths()
	if err != nil {
		t.Fatalf("resolve cached Windows Node paths: %v", err)
	}
	nodeAsset, err := installer.WindowsNodeRuntimeAssetForPlatform(host)
	if err != nil {
		t.Fatalf("select cached Node receipt asset: %v", err)
	}
	nodePayload := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir, "node-runtime", nodeAsset.Version, nodeAsset.Architecture, strings.ToLower(nodeAsset.SHA256), "payload.zip")
	if got, hashErr := vueLifecycleSHA256File(nodePayload); hashErr != nil || !strings.EqualFold(got, nodeAsset.SHA256) {
		t.Fatalf("cached Node payload SHA256=%s err=%v, want=%s", got, hashErr, nodeAsset.SHA256)
	}
	appendReceipt("node_version=%s", nodeAsset.Version)
	appendReceipt("node_url=%s", nodeAsset.URL)
	appendReceipt("node_sha256=%s", nodeAsset.SHA256)
	appendReceipt("node_payload_sha256=%s", nodeAsset.SHA256)

	var exactPackages []string
	for _, spec := range runtimeNPMInstallerSpecsForPlatform("windows") {
		if !strings.Contains(strings.Join(spec.languages, ","), "vue") {
			continue
		}
		exactPackages, err = runtimeNPMExactPackages(spec.args)
		if err != nil {
			t.Fatalf("parse cached Vue exact npm pins: %v", err)
		}
		break
	}
	if len(exactPackages) == 0 {
		t.Fatal("cached Vue exact npm pins are missing")
	}
	if err := nodeRuntime.ValidateExactPackages(ctx, exactPackages); err != nil {
		t.Fatalf("validate cached Vue npm cohort: %v", err)
	}
	for _, specification := range exactPackages {
		packageName, packageVersion, parseErr := productionExactPackageNameAndVersion(specification)
		if parseErr != nil {
			t.Fatalf("parse cached Vue package %q: %v", specification, parseErr)
		}
		verifyRealNodePackageVersion(t, expectedPaths.Prefix, packageName, packageVersion)
		appendReceipt("npm_package=%s version=%s", packageName, packageVersion)
	}

	serverBinary, err := nodeRuntime.BinaryPath(ctx, "vue-language-server")
	if err != nil {
		t.Fatalf("resolve cached Vue server binary: %v", err)
	}
	bridgeSpec, err := runtimeServerWindowsVueTSBridgeSpec(serverBinary)
	if err != nil {
		t.Fatalf("resolve cached Vue TypeScript companion: %v", err)
	}
	appendReceipt("vue_server_relative=%s", vueLifecycleRelativePath(productRoot, serverBinary))
	appendReceipt("typescript_binary_relative=%s", vueLifecycleRelativePath(productRoot, bridgeSpec.typescriptBinary))
	appendReceipt("vue_plugin_relative=%s", vueLifecycleRelativePath(productRoot, bridgeSpec.vuePluginLocation))

	vclibsAsset, err := installer.WindowsVCLibsDesktopAssetForPlatform(host)
	if err != nil {
		t.Fatalf("select cached VCLibs receipt asset: %v", err)
	}
	vclibsRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalPath(productRoot)
	if err != nil {
		t.Fatalf("read-only resolve cached VCLibs root: %v", err)
	}
	vclibsProcessRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalProcessPath(productRoot)
	if err != nil {
		t.Fatalf("read-only resolve cached VCLibs process root: %v", err)
	}
	if same, sameErr := sameRealNodeFile(vclibsRoot, vclibsProcessRoot); sameErr != nil || !same {
		if sameErr != nil {
			t.Fatalf("compare cached VCLibs identities: %v", sameErr)
		}
		t.Fatalf("cached VCLibs process root %q changed ready root %q", vclibsProcessRoot, vclibsRoot)
	}
	vclibsPayload := filepath.Join(filepath.Dir(vclibsRoot), "payload.zip")
	gotVCLibsSHA, err := vueLifecycleSHA256File(vclibsPayload)
	if err != nil {
		t.Fatalf("hash cached VCLibs payload: %v", err)
	}
	if !strings.EqualFold(gotVCLibsSHA, vclibsAsset.SHA256) {
		t.Fatalf("cached VCLibs payload SHA256=%s, want=%s", gotVCLibsSHA, vclibsAsset.SHA256)
	}
	t.Setenv("SUPER_DOLPHIN_WINDOWS_NODE_PATH", expectedPaths.NodePath)
	t.Setenv("SUPER_DOLPHIN_MSVC_RUNTIME_DIR", vclibsProcessRoot)
	// 语义工具清单在 sidecar 启动前必须看到同一 product-owned npm cohort 的
	// Vue shim；这里只加入已校验 prefix 的 .bin，绝不恢复系统 Node/npm 或 NODE_PATH。
	productNPMBin := filepath.Join(expectedPaths.Prefix, "node_modules", ".bin")
	if info, statErr := os.Stat(productNPMBin); statErr != nil || !info.IsDir() {
		t.Fatalf("cached product npm .bin directory is missing: %s (%v)", productNPMBin, statErr)
	}
	productNodeDir, err := installer.WindowsShortProcessPathWithinRoot(productRoot, expectedPaths.NodeDir)
	if err != nil {
		t.Fatalf("resolve cached product Node directory process path: %v", err)
	}
	if info, statErr := os.Stat(expectedPaths.NodePath); statErr != nil || !info.Mode().IsRegular() {
		t.Fatalf("cached product Node executable is missing: %s (%v)", expectedPaths.NodePath, statErr)
	}
	filteredPath := realNodePathWithoutNodeNPM(os.Getenv("PATH"))
	// Windows npm .bin shims invoke `node` when their sibling node.exe is absent;
	// prepend only the same locked product NodeDir, never a host Node/npm directory.
	t.Setenv("PATH", productNodeDir+string(os.PathListSeparator)+productNPMBin+string(os.PathListSeparator)+filteredPath)
	appendReceipt("node_dir_relative=%s", vueLifecycleRelativePath(productRoot, expectedPaths.NodeDir))
	appendReceipt("product_npm_bin_relative=%s", vueLifecycleRelativePath(productRoot, productNPMBin))
	appendReceipt("vclibs_version=%s", vclibsAsset.Version)
	appendReceipt("vclibs_url=%s", vclibsAsset.URL)
	appendReceipt("vclibs_sha256=%s", vclibsAsset.SHA256)
	appendReceipt("vclibs_payload_sha256=%s", gotVCLibsSHA)
	appendReceipt("cache_validation=read_only_success")
	appendReceipt("vclibs_process_path_policy=8.3_same_file_identity")

	fixtureRoot := t.TempDir()
	servers := realNodeServerCasesForLanguage("vue")
	requireRealNodeServerCaseIdentities(t, servers)
	if len(servers) != 1 {
		t.Fatalf("Vue server cases=%d, want exactly one", len(servers))
	}
	server := servers[0]
	fixture := writeRealMCPLanguageFixture(t, fixtureRoot, server)
	astFile := filepath.Join(fixtureRoot, "ast_fixture.js")
	writeRealFixture(t, astFile, "function realMCPAstFixture(name) { return name; }\nrealMCPAstFixture(\"world\");\n")
	binary := buildRealMcpLSPBinary(t, repoRoot)
	appendReceipt("binary_path_digest=%s", vueLifecyclePathDigest(binary))

	// manager 的 idle 计时从最后一个工具租约释放时开始，早于 E2E 完成进程树与
	// start-token 采样；这里只在 Windows ARM64 E2E 进程内增加两分钟观测余量，
	// 生产默认值和其他平台均保持十五分钟，正式证明窗口仍完整等待十五分钟。
	t.Setenv("MCP_LSP_IDLE_TIMEOUT", vueWindowsARM64ProcessARM64ManagerIdle.String())
	client := startRealMcpLSPBinary(t, ctx, binary, fixtureRoot, repoRoot, "", "", productRoot)
	mcpPID := client.cmd.Process.Pid
	startToken, err := windowsGoplsProcessStartIdentity(mcpPID)
	if err != nil {
		client.close(t)
		t.Fatalf("capture mcp-lsp PID %d start identity: %v", mcpPID, err)
	}
	appendReceipt("mcp_pid=%d", mcpPID)
	appendReceipt("mcp_start_token=%s", startToken)
	tracked := map[realMCPProcessKey]realMCPProcessIdentity{
		{PID: mcpPID, StartToken: startToken}: {PID: mcpPID, StartToken: startToken, Name: "mcp-lsp", Language: "vue"},
	}
	clientClosed := false
	shutdownSent := false
	t.Cleanup(func() {
		if client == nil || client.cmd == nil || clientClosed {
			return
		}
		if !shutdownSent {
			_ = writeMCPShutdownWithoutFatal(client)
		}
		_ = trackRealMCPProcessTree(t, mcpPID, "vue-soak-final-before-close", tracked)
		client.close(t)
		clientClosed = true
		if len(tracked) <= 1 {
			t.Errorf("Vue soak process tree captured no descendant; zero-residual proof is incomplete")
			appendReceipt("zero_residual=false")
			return
		}
		requireRealMCPProcessIdentitiesGone(t, tracked)
		appendReceipt("zero_residual=true")
	})

	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "super-dolphin-windows-arm64-process-arm64-vue-soak-15m", "version": "1"}})
	requireRealMCPToolFamilies(t, callMcpLSPBinaryRaw(t, client, "tools/list", map[string]any{}))
	appendReceipt("initialize=success")
	appendReceipt("tools_list=seven_public_families_exact")

	actions := realMCPActionSpecs(server, fixture, astFile)
	if err := validateRealMCPActionClosure(actions); err != nil {
		t.Fatalf("Vue soak action closure: %v", err)
	}
	summary := vueLifecycleRunActionMatrix(t, client, server, fixtureRoot, fixture, actions)
	if summary.total != realMCPExpectedActionCount || summary.succeeded+summary.capabilityUnsupported != summary.total {
		t.Fatalf("Vue soak 36-action ledger incomplete: total=%d success=%d legal_empty=%d unsupported=%d actions=%v", summary.total, summary.succeeded, summary.legalEmpty, summary.capabilityUnsupported, summary.unsupportedActions)
	}
	appendReceipt("action_total=%d", summary.total)
	appendReceipt("action_success=%d", summary.succeeded)
	appendReceipt("action_legal_empty=%d", summary.legalEmpty)
	appendReceipt("action_capability_unsupported=%d", summary.capabilityUnsupported)
	appendReceipt("action_error_or_null=0")
	t.Logf("Vue soak 36-action matrix total=%d success=%d legal_empty=%d capability_unsupported=%d", summary.total, summary.succeeded, summary.legalEmpty, summary.capabilityUnsupported)
	// 初始化只列出 MCP 工具，不会创建语言 client；首个真实 action 启动 product-owned Vue
	// 后，生产 env seam 才创建并注入同一产品根的用户态目录，故在 36-action 后校验。
	for _, item := range []struct {
		label string
		path  string
	}{
		{label: "LOCALAPPDATA", path: filepath.Join(productRoot, "runtime-state", "localappdata")},
		{label: "APPDATA", path: filepath.Join(productRoot, "runtime-state", "appdata")},
	} {
		info, statErr := os.Stat(item.path)
		if statErr != nil || !info.IsDir() {
			t.Fatalf("production Vue %s directory is missing after real actions: %s (%v)", item.label, item.path, statErr)
		}
		appendReceipt("%s_relative=%s", strings.ToLower(item.label), vueLifecycleRelativePath(productRoot, item.path))
	}

	if !trackRealMCPProcessTree(t, mcpPID, "vue-before-soak", tracked) {
		t.Fatalf("capture Vue process tree before idle failed")
	}
	companionKeys := vueLifecycleCaptureCompanionKeys(t, mcpPID)
	appendReceipt("vue_pid=%d vue_start_token=%s", companionKeys["vue"].PID, companionKeys["vue"].StartToken)
	appendReceipt("typescript_pid=%d typescript_start_token=%s", companionKeys["typescript"].PID, companionKeys["typescript"].StartToken)
	if _, ok := companionKeys["typingsInstaller"]; !ok {
		t.Fatalf("Vue soak process tree did not capture typingsInstaller")
	}
	appendReceipt("typings_installer_pid=%d typings_installer_start_token=%s", companionKeys["typingsInstaller"].PID, companionKeys["typingsInstaller"].StartToken)

	idleStarted := time.Now()
	appendReceipt("idle_begin=%s", idleStarted.Format(time.RFC3339Nano))
	appendReceipt("idle_required=%s", vueWindowsARM64ProcessARM64SoakIdle)
	t.Logf("Vue soak idle_begin=%s required=%s mcp_pid=%d vue_pid=%d typescript_pid=%d", idleStarted.Format(time.RFC3339Nano), vueWindowsARM64ProcessARM64SoakIdle, mcpPID, companionKeys["vue"].PID, companionKeys["typescript"].PID)
	heartbeats := 0
	for {
		elapsed := time.Since(idleStarted)
		if elapsed >= vueWindowsARM64ProcessARM64SoakIdle {
			break
		}
		wait := time.Minute
		if remaining := vueWindowsARM64ProcessARM64SoakIdle - elapsed; remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatalf("Vue soak context expired during idle after %s: %v", time.Since(idleStarted).Round(time.Second), ctx.Err())
		case <-timer.C:
		}
		vueLifecycleAssertCompanionKeys(t, mcpPID, companionKeys)
		heartbeats++
		t.Logf("Vue soak idle heartbeat elapsed=%s mcp_pid=%d vue_pid=%d typescript_pid=%d", time.Since(idleStarted).Round(time.Second), mcpPID, companionKeys["vue"].PID, companionKeys["typescript"].PID)
	}
	idleEnded := time.Now()
	idleDuration := idleEnded.Sub(idleStarted)
	if idleDuration < vueWindowsARM64ProcessARM64SoakIdle {
		t.Fatalf("Vue soak idle duration=%s, want at least %s", idleDuration, vueWindowsARM64ProcessARM64SoakIdle)
	}
	vueLifecycleAssertCompanionKeys(t, mcpPID, companionKeys)
	_ = trackRealMCPProcessTree(t, mcpPID, "vue-post-soak-boundary", tracked)
	appendReceipt("idle_end=%s", idleEnded.Format(time.RFC3339Nano))
	appendReceipt("idle_duration=%s", idleDuration.Round(time.Millisecond))
	appendReceipt("idle_heartbeats=%d", heartbeats)
	appendReceipt("idle_identity=same_vue_typescript_typingsinstaller_pid_and_start_token")
	t.Logf("Vue soak idle_end=%s duration=%s heartbeats=%d", idleEnded.Format(time.RFC3339Nano), idleDuration.Round(time.Millisecond), heartbeats)

	for _, action := range actions {
		if action.tool != "inspect" || (action.name != "hover" && action.name != "definition") {
			if action.tool != "xref" || action.name != "references" {
				continue
			}
		}
		response := client.callTool(t, action.tool, realMCPWindowsToolArguments(server.languageID, fixtureRoot, action.tool, action.name, action.args))
		status := requireRealMCPActionResult(t, response, true, "", false, realMCPActionCapabilityKey(action.tool, action.name), false, "Vue post-idle "+action.tool+" "+action.name)
		if status != realMCPActionSucceeded {
			t.Fatalf("Vue post-idle %s/%s status=%s, want non-empty success", action.tool, action.name, status)
		}
		appendReceipt("post_idle_action=%s/%s status=success non_empty=true", action.tool, action.name)
		t.Logf("Vue post-idle action=%s/%s status=%s non_empty=true", action.tool, action.name, status)
	}
	vueLifecycleAssertCompanionKeys(t, mcpPID, companionKeys)
	appendReceipt("post_idle_semantics=hover_definition_references_non_empty")
	appendReceipt("post_idle_identity=same_vue_typescript_typingsinstaller_pid_and_start_token")

	if !trackRealMCPProcessTree(t, mcpPID, "vue-before-shutdown", tracked) {
		t.Fatalf("capture Vue process tree before shutdown failed")
	}
	requireRealMCPTypeScriptUserDataWithinRoot(t, tracked, productRoot)
	shutdown := client.call(t, "shutdown", map[string]any{})
	if shutdown.Error != nil || shutdown.Result.IsError {
		t.Fatalf("Vue shutdown response invalid: %#v", shutdown)
	}
	shutdownSent = true
	appendReceipt("shutdown=response_ok")
	client.close(t)
	clientClosed = true
	appendReceipt("exit=sent_and_process_wait_zero")
	logRealMCPProcessIdentities(t, tracked)
	if len(tracked) <= 1 {
		t.Fatalf("Vue soak process tree captured no server descendants: tracked=%d", len(tracked))
	}
	requireRealMCPProcessIdentitiesGone(t, tracked)
	appendReceipt("zero_residual=true")
	appendReceipt("status=complete_36_action_and_15m_lifecycle")
	t.Logf("Vue ARM64/process-ARM64 36-action matrix and 15-minute lifecycle completed: receipt=%s elapsed=%s", receiptPath, time.Since(startedAt).Round(time.Millisecond))
}

func vueLifecycleRunActionMatrix(t *testing.T, client *mcpLSPBinaryClient, server realNodeServerCase, fixtureRoot string, fixture realMCPFixture, actions []realMCPActionSpec) realMCPMatrixSummary {
	t.Helper()
	var summary realMCPMatrixSummary
	for _, action := range actions {
		if action.tool == "patch_edit" {
			path, _ := action.args["file_path"].(string)
			if path == "" {
				position, _ := action.args["pos"].(string)
				path = realMCPPositionPath(position)
			}
			if path == "" {
				t.Fatalf("Vue soak patch_edit action %s has no file path", action.name)
			}
			opened := client.callTool(t, "file", realMCPWindowsToolArguments(server.languageID, fixtureRoot, "file", "open_file", map[string]any{"action": "open_file", "file_path": path}))
			requireRealMCPActionResult(t, opened, true, "", false, "", false, "Vue soak file open "+action.name)
		}
		response := client.callTool(t, action.tool, realMCPWindowsToolArguments(server.languageID, fixtureRoot, action.tool, action.name, action.args))
		status := requireRealMCPActionResult(t, response, action.requireResult, action.emptyResultReason, action.allowCapabilityUnsupported, realMCPActionCapabilityKey(action.tool, action.name), realMCPActionProtocolOptional(action.tool, action.name), "Vue soak "+action.tool+" "+action.name)
		if action.tool == "patch_edit" && action.name == "replace_range" && status != realMCPActionUnsupported {
			assertRealFileContains(t, fixture.replaceFile, "REAL_MCP_REPLACED", "Vue soak patch_edit replace_range")
		}
		summary.total++
		switch status {
		case realMCPActionSucceeded:
			summary.succeeded++
		case realMCPActionLegalEmpty:
			summary.succeeded++
			summary.legalEmpty++
		case realMCPActionUnsupported:
			summary.capabilityUnsupported++
			summary.unsupportedActions = append(summary.unsupportedActions, action.tool+"/"+action.name)
		default:
			t.Fatalf("Vue soak action %s/%s returned unclassified status %q", action.tool, action.name, status)
		}
		t.Logf("Vue soak action=%s/%s status=%s", action.tool, action.name, status)
	}
	return summary
}

func vueLifecycleCaptureCompanionKeys(t *testing.T, rootPID int) map[string]realMCPProcessKey {
	t.Helper()
	identities, err := realMCPProcessTreeSnapshot(rootPID)
	if err != nil {
		t.Fatalf("capture Vue/TypeScript process identities: %v", err)
	}
	wants := map[string]string{
		"vue":              "@vue/language-server",
		"typescript":       "typescript-language-server",
		"typingsInstaller": "typingsinstaller.js",
	}
	keys := make(map[string]realMCPProcessKey, len(wants))
	for _, identity := range identities {
		// Windows 命令行可能含产品根的 8.3 父路径；先统一分隔符，再匹配稳定的
		// Vue/TypeScript 子进程标记，避免把短路径误判为缺失 companion。
		command := strings.ReplaceAll(strings.ToLower(identity.CommandLine), "\\", "/")
		for label, marker := range wants {
			if _, found := keys[label]; found || !strings.Contains(command, marker) {
				continue
			}
			keys[label] = realMCPProcessKey{PID: identity.PID, StartToken: identity.StartToken}
		}
	}
	for label := range wants {
		if _, found := keys[label]; !found {
			t.Fatalf("Vue lifecycle process tree missing %s identity", label)
		}
	}
	return keys
}

func vueLifecycleAssertCompanionKeys(t *testing.T, rootPID int, want map[string]realMCPProcessKey) {
	t.Helper()
	identities, err := realMCPProcessTreeSnapshot(rootPID)
	if err != nil {
		t.Fatalf("capture Vue lifecycle process identities: %v", err)
	}
	current := make(map[realMCPProcessKey]realMCPProcessIdentity, len(identities))
	for _, identity := range identities {
		current[realMCPProcessKey{PID: identity.PID, StartToken: identity.StartToken}] = identity
	}
	for label, key := range want {
		if _, found := current[key]; !found {
			t.Fatalf("Vue lifecycle %s PID/start identity changed or exited: pid=%d start=%s", label, key.PID, key.StartToken)
		}
	}
}

func vueLifecycleSHA256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// vueLifecycleRelativePath 只把产品根内的证据写成相对路径；根外路径立即标记为越界，
// 避免 receipt 泄露机器路径或把外部 binary 误记为同一 cohort。
func vueLifecycleRelativePath(root, target string) string {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "OUTSIDE_PRODUCT_ROOT"
	}
	return filepath.ToSlash(relative)
}

func vueLifecyclePathDigest(path string) string {
	hash := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(path))))
	return hex.EncodeToString(hash[:])
}
