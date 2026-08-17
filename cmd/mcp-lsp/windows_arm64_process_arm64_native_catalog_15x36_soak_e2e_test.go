//go:build windows && arm64 && e2e

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
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
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	nativeCatalog15x36KotlinProbeEnv           = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_KOTLIN_PROBE"
	nativeCatalog15x36KotlinProbeTimeout       = 6 * time.Minute
	nativeCatalog15x36FormalEnv                = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_15X36_E2E"
	nativeCatalog15x36PrecheckEnv              = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_15X36_PRECHECK"
	nativeCatalog15x36CProbeEnv                = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_C_PROBE"
	nativeCatalog15x36TransportEnvBisectionEnv = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_TRANSPORT_ENV_BISECTION"
	nativeCatalog15x36TransportEnvVariantEnv   = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_TRANSPORT_ENV_VARIANT"
	nativeCatalog15x36TerraformProbeEnv        = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_TERRAFORM_PROBE"
	nativeCatalog15x36TerraformProbeRootEnv    = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_TERRAFORM_PROBE_ROOT"
	nativeCatalog15x36RustProbeEnv             = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_RUST_PROBE"
	nativeCatalog15x36RustProbeRootEnv         = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_RUST_PROBE_ROOT"
	nativeCatalog15x36EvidenceEnv              = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_15X36_EVIDENCE_DIR"
	nativeCatalog15x36CacheOnlyRootEnv         = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_CACHE_ONLY_ROOT"
	nativeCatalog15x36CacheOnlyPrecheckEnv     = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_CACHE_ONLY_PRECHECK"
	nativeCatalog15x36DownloadOnlyEnv          = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_DOWNLOAD_INSTALL_ONLY"
	nativeCatalog15x36DownloadRootEnv          = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_DOWNLOAD_PRODUCT_ROOT"
	nativeCatalog15x36PrewarmEnv               = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_LOCAL_PAYLOAD_PREWARM"
	nativeCatalog15x36PrewarmSourcesEnv        = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_PREWARM_SOURCE_ROOTS"
	nativeCatalog15x36PrewarmTargetEnv         = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_PREWARM_TARGET_ROOT"
	nativeCatalog15x36PrewarmEvidenceEnv       = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_PREWARM_EVIDENCE_DIR"
	nativeCatalog15x36MixedPrepareEnv          = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_MIXED_PREPARE"
	nativeCatalog15x36ImportReadyEnv           = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_IMPORT_READY"
	nativeCatalog15x36MixedTargetEnv           = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_MIXED_TARGET_ROOT"
	nativeCatalog15x36MixedEvidenceEnv         = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_MIXED_EVIDENCE_DIR"
	nativeCatalog15x36MixedSourcesEnv          = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_MIXED_SOURCE_ROOTS"
	nativeCatalog15x36FormalIdle               = 15 * time.Minute
	// 正式矩阵串行创建多个合法 workspace：早期 dir_fallback client 可能在后续
	// clangd_project client 建立前就开始计时。测试专用 30 分钟窗口必须覆盖串行
	// 动作阶段与全局 15 分钟 idle soak；不改变生产默认 idle 或 recycler 下限。
	nativeCatalog15x36ManagerIdle     = 30 * time.Minute
	nativeCatalog15x36PrecheckIdle    = 30 * time.Second
	nativeCatalog15x36PrecheckTimeout = 6 * time.Minute
	// C probe 获批超过 600 秒，用于覆盖冷安装与真实 stdio；4 分钟只适合 NON_PASS 预检。
	nativeCatalog15x36CProbeTimeout         = 30 * time.Minute
	nativeCatalog15x36ProtoHoverEmptyReason = "proto buf 只允许精确 OK total=0/unit=hover 与 HINT no hover info available 的合法空结果"
	// 正式上限覆盖多项冷下载、540 个真实 action、15 分钟 idle 与清理；它不是
	// 生命周期时长，不能缩短 formalIdle，也不能被六分钟 NON_PASS 预检替代。
	nativeCatalog15x36FormalTimeout   = 3 * time.Hour
	nativeCatalog15x36ExpectedActions = 15 * realMCPExpectedActionCount
)

// TestWindowsARM64ProcessARM64NativeCatalogKotlinProbeE2E 只复现 Kotlin
// initialize/open_file 的短边界；它不是 15×36 或生命周期 PASS 证明。
// 每个阶段记录耗时、context 状态、MCP PID/start 和 stderr 摘要，避免把
// JSON-RPC cancelled 错误误判为 capability_unsupported 或合法空结果。
func TestWindowsARM64ProcessARM64NativeCatalogKotlinProbeE2E(t *testing.T) {
	if os.Getenv(nativeCatalog15x36KotlinProbeEnv) != "1" {
		t.Skipf("set %s=1 for the bounded NON_PASS Kotlin initialize probe", nativeCatalog15x36KotlinProbeEnv)
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("Kotlin probe requires windows/arm64, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	if host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("Kotlin probe requires ARM64/ARM64, got native=%q process=%q", host.NativeArch, host.ProcessArch)
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), nativeCatalog15x36KotlinProbeTimeout)
	defer cancel()
	productRoot, err := os.MkdirTemp("", "sd-node-production-windows-native-kotlin-probe-")
	if err != nil {
		t.Fatalf("create Kotlin probe product root: %v", err)
	}
	t.Cleanup(func() {
		if err := removeRealWindowsProductRoot(productRoot); err != nil {
			t.Errorf("remove Kotlin probe product root: %v", err)
		}
	})
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict Kotlin probe product root: %v", err)
	}
	repoRoot := realNodeRepoRoot(t)
	evidenceDir := strings.TrimSpace(os.Getenv(nativeCatalog15x36EvidenceEnv))
	if evidenceDir == "" {
		evidenceDir = filepath.Join(repoRoot, ".build-cache", "codex-native-kotlin-probe")
	}
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatalf("create Kotlin probe evidence directory: %v", err)
	}
	receiptPath := filepath.Join(evidenceDir, "windows-arm64-process-arm64-native-kotlin-probe.receipt")
	wirePath := filepath.Join(evidenceDir, "windows-arm64-process-arm64-native-kotlin-probe.wire.log")
	_ = os.Remove(receiptPath)
	_ = os.Remove(wirePath)
	receipt := []string{
		"test=windows-arm64-process-arm64-native-kotlin-probe",
		"status=started",
		"native_arch=arm64",
		"process_arch=arm64",
		fmt.Sprintf("windows_version=%s", host.WindowsVersion),
		fmt.Sprintf("windows_build=%d", host.WindowsBuild),
		"formal=false",
		"precheck=NON_PASS_precheck",
		"absolute_path_markers=0",
		"acl_win32_5_1314=typed_authorization_required_only;acl_changes=none",
	}
	if err := node17x36WriteReceipt(receiptPath, receipt); err != nil {
		t.Fatalf("write Kotlin probe receipt: %v", err)
	}
	var tracked map[realMCPProcessKey]realMCPProcessIdentity
	var shutdownSent bool
	var result installer.InstallResult
	var machine uint16
	writePhase := func(phase, detail string) {
		_ = nativeCatalog15x36WriteWire(wirePath, fmt.Sprintf("phase=%s;elapsed=%s;%s", phase, time.Since(started).Round(time.Millisecond), detail))
	}
	writePhase("started", "context_timeout=6m")
	finalize := func(status string, client *mcpLSPBinaryClient, mcpStart string, serverPath string, machine uint16, exitCode string) {
		stderr := ""
		pid := 0
		if client != nil {
			stderr = client.stderr.String()
			if client.cmd != nil && client.cmd.Process != nil {
				pid = client.cmd.Process.Pid
			}
		}
		lines := append([]string{}, receipt...)
		lines = append(lines,
			"status="+status,
			fmt.Sprintf("mcp_pid=%d;mcp_start_present=%t", pid, mcpStart != ""),
			fmt.Sprintf("server_basename=%s", filepath.Base(serverPath)),
			fmt.Sprintf("server_command_sha256=%x", sha256.Sum256([]byte(filepath.Base(serverPath)))),
			fmt.Sprintf("server_pe_machine=0x%04x", machine),
			"start_attempted=true",
			fmt.Sprintf("child_observed=%t", len(tracked) > 1),
			"initialize_request_context_err="+fmt.Sprint(ctx.Err()),
			"exit_code="+exitCode,
			fmt.Sprintf("stderr_bytes=%d;stderr_sha256=%x", len(stderr), sha256.Sum256([]byte(stderr))),
			fmt.Sprintf("shutdown_sent=%t", shutdownSent),
			fmt.Sprintf("zero_residual=%t", !t.Failed()),
			fmt.Sprintf("elapsed=%s", time.Since(started).Round(time.Millisecond)),
		)
		for i, event := range nativeCatalog15x36KotlinStderrEvents(stderr) {
			lines = append(lines, fmt.Sprintf("stderr_event.%02d=%s", i+1, event))
		}
		_ = node17x36WriteReceipt(receiptPath, lines)
		writePhase("closed", fmt.Sprintf("status=%s;stderr_events=%d", status, len(nativeCatalog15x36KotlinStderrEvents(stderr))))
	}
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", repoRoot)
	t.Setenv("APPDATA", "")
	logPhase := func(phase string, client *mcpLSPBinaryClient, mcpStart string) {
		stderr := ""
		pid := 0
		if client != nil {
			stderr = client.stderr.String()
		}
		if client != nil && client.cmd != nil && client.cmd.Process != nil {
			pid = client.cmd.Process.Pid
		}
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(stderr)))
		tail := stderr
		if len(tail) > 256 {
			tail = tail[len(tail)-256:]
		}
		t.Logf("kotlin_probe phase=%s elapsed=%s ctx_err=%v pid=%d start_present=%t stderr_bytes=%d stderr_sha256=%s stderr_tail_bytes=%d", phase, time.Since(started).Round(time.Millisecond), ctx.Err(), pid, mcpStart != "", len(stderr), digest, len(tail))
	}
	provider := setupInstaller()
	logPhase("before_ensure_installed", nil, "")
	result, err = provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), "kotlin")
	if err != nil {
		writePhase("ensure_installed_error", "context_err="+fmt.Sprint(ctx.Err()))
		logPhase("ensure_installed_error", nil, "")
		t.Fatalf("ensure ARM64 Kotlin server: %v", err)
	}
	if result.Status == installer.InstallStatusInstalledFallback || strings.TrimSpace(result.Path) == "" {
		t.Fatalf("Kotlin probe received invalid product-owned result: status=%s path_present=%t", result.Status, strings.TrimSpace(result.Path) != "")
	}
	var machineErr error
	machine, machineErr = nativeCatalog15x36PEMachine(result.Path)
	if machineErr != nil {
		t.Fatalf("inspect Kotlin server PE: %v", machineErr)
	}
	receipt = append(receipt, fmt.Sprintf("server_relative_basename=%s;server_status=%s;server_arch=%s", filepath.Base(result.Path), result.Status, installer.WindowsHostArchARM64))
	writePhase("installed", fmt.Sprintf("server=%s;pe_machine=0x%04x", filepath.Base(result.Path), machine))
	server := nativeCatalog15x36ServerCases()[11]
	fixtureRoot := t.TempDir()
	fixture := writeRealMCPLanguageFixture(t, fixtureRoot, server)
	binary := buildRealMcpLSPBinary(t, repoRoot)
	client := startRealMcpLSPBinary(t, ctx, binary, fixtureRoot, repoRoot, "", "", productRoot)
	mcpPID := client.cmd.Process.Pid
	mcpStart, err := windowsGoplsProcessStartIdentity(mcpPID)
	if err != nil {
		t.Fatalf("capture Kotlin probe MCP PID+start: %v", err)
	}
	tracked = map[realMCPProcessKey]realMCPProcessIdentity{{PID: mcpPID, StartToken: mcpStart}: {PID: mcpPID, StartToken: mcpStart, Name: "mcp-lsp", Language: "mcp-lsp"}}
	defer func() {
		_ = trackRealMCPProcessTree(t, mcpPID, "kotlin-probe-final", tracked)
		if client != nil {
			shutdownSent = node17x36CloseWithExitProof(t, client)
		}
		requireRealMCPProcessIdentitiesGone(t, tracked)
		logPhase("closed", client, mcpStart)
		exitCode := "not_observed"
		if client != nil && client.cmd != nil && client.cmd.ProcessState != nil {
			exitCode = fmt.Sprintf("%d", client.cmd.ProcessState.ExitCode())
		}
		finalize("NON_PASS_precheck", client, mcpStart, result.Path, machine, exitCode)
		t.Logf("kotlin_probe result=NON_PASS_precheck shutdown_sent=%t zero_residual=%t", shutdownSent, !t.Failed())
	}()
	logPhase("mcp_started", client, mcpStart)
	initialize := client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}})
	if initialize.Error != nil {
		writePhase("initialize_error", fmt.Sprintf("jsonrpc_code=%d;context_err=%v", initialize.Error.Code, ctx.Err()))
		logPhase("initialize_error", client, mcpStart)
		t.Fatalf("Kotlin initialize failed: json_rpc=%v text=%q", initialize.Error, initialize.Result.ContentText())
	}
	if err := nativeCatalog15x36Notify(client, "notifications/initialized", map[string]any{}); err != nil {
		t.Fatalf("Kotlin initialized notification: %v", err)
	}
	logPhase("initialized", client, mcpStart)
	response := client.callTool(t, "file", realMCPWindowsToolArguments("kotlin", fixtureRoot, "file", "open_file", map[string]any{"action": "open_file", "file_path": fixture.targetFile}))
	if response.Result.IsError || strings.TrimSpace(response.Result.ContentText()) == "" {
		writePhase("open_file_error", fmt.Sprintf("context_err=%v;response_sha256=%x", ctx.Err(), sha256.Sum256([]byte(response.Result.ContentText()))))
		logPhase("open_file_error", client, mcpStart)
		t.Fatalf("Kotlin open_file was not non-empty semantic success: text=%q", response.Result.ContentText())
	}
	logPhase("open_file_success", client, mcpStart)
	shutdown := client.call(t, "shutdown", map[string]any{})
	if shutdown.Error != nil {
		t.Fatalf("Kotlin shutdown failed: %v", shutdown.Error)
	}
}

// nativeCatalog15x36KotlinStderrEvents 只保存 stderr 事件的类别、错误码和摘要，
// 不把 launcher 输出、源码或绝对路径写入 receipt。
func nativeCatalog15x36KotlinStderrEvents(stderr string) []string {
	lines := strings.Split(stderr, "\\n")
	events := make([]string, 0, 30)
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "" || (!strings.Contains(lower, "error") && !strings.Contains(lower, "cancel") && !strings.Contains(lower, "initial")) {
			continue
		}
		phase := "stderr"
		if strings.Contains(lower, "initial") {
			phase = "initialize"
		} else if strings.Contains(lower, "cancel") {
			phase = "cancel"
		} else if strings.Contains(lower, "error") {
			phase = "error"
		}
		code := "none"
		for _, candidate := range []string{"-32800", "-32603", "-32001", "122"} {
			if strings.Contains(lower, candidate) {
				code = candidate
				break
			}
		}
		events = append(events, fmt.Sprintf("phase=%s;code=%s;hash=%x", phase, code, sha256.Sum256([]byte(line))))
	}
	if len(events) > 30 {
		events = events[len(events)-30:]
	}
	return events
}

var nativeCatalog15x36LanguageIDs = []string{
	"c", "cpp", "objective-c", "objective-cpp", "mq4", "mq5", "mqh", "mql", "mql4", "mql5",
	"proto", "kotlin", "dart", "terraform", "rust",
}

// nativeCatalog15x36ExpectedProducts 是任务固定语言闭包与生产 catalog 产品 ID 的
// 交叉守卫；资产版本、URL、SHA 和 binary path 仍只从 installer.WindowsLSPCatalog 读取。
var nativeCatalog15x36ExpectedProducts = map[string]installer.WindowsLSPProduct{
	"c": installer.WindowsLSPProductClangd, "cpp": installer.WindowsLSPProductClangd,
	"objective-c": installer.WindowsLSPProductClangd, "objective-cpp": installer.WindowsLSPProductClangd,
	"mq4": installer.WindowsLSPProductClangd, "mq5": installer.WindowsLSPProductClangd,
	"mqh": installer.WindowsLSPProductClangd, "mql": installer.WindowsLSPProductClangd,
	"mql4": installer.WindowsLSPProductClangd, "mql5": installer.WindowsLSPProductClangd,
	"proto":     installer.WindowsLSPProductBuf,
	"kotlin":    installer.WindowsLSPProductKotlin,
	"dart":      installer.WindowsLSPProductDart,
	"terraform": installer.WindowsLSPProductTerraform,
	"rust":      installer.WindowsLSPProductRustAnalyzer,
}

// TestWindowsARM64ProcessARM64NativeCatalog15x36Closure 是无网络的 catalog、fixture
// 和 15×36 合同回归；它不调用 EnsureInstalledDetailed，也不构成安装或生命周期 PASS。
func TestWindowsARM64ProcessARM64NativeCatalog15x36Closure(t *testing.T) {
	if nativeCatalog15x36FormalTimeout < 2*time.Hour {
		t.Fatalf("native catalog formal timeout=%s is too short for cold install plus lifecycle proof", nativeCatalog15x36FormalTimeout)
	}
	if err := nativeCatalog15x36ValidateCatalog(); err != nil {
		t.Fatal(err)
	}
	servers := nativeCatalog15x36ServerCases()
	if len(servers) != len(nativeCatalog15x36LanguageIDs) {
		t.Fatalf("native catalog server count=%d, want %d", len(servers), len(nativeCatalog15x36LanguageIDs))
	}
	root := t.TempDir()
	astFile := filepath.Join(root, "ast_fixture.js")
	writeRealFixture(t, astFile, "function nativeCatalogAst(name) { return name; }\nnativeCatalogAst(\"world\");\n")
	for _, server := range servers {
		fixture := writeRealMCPLanguageFixture(t, root, server)
		nativeCatalog15x36PrepareClangdFixture(t, root, server)
		requireRealMCPFixturePositions(t, fixture, server)
		actions := nativeCatalog15x36ActionSpecs(server, fixture, astFile)
		if err := validateRealMCPActionClosure(actions); err != nil {
			t.Fatalf("%s 36-action closure: %v", server.languageID, err)
		}
		for _, action := range actions {
			if action.tool == "inspect" && action.name == "hover" && server.languageID != "proto" && !action.requireResult {
				t.Fatalf("%s hover must have an explicit non-empty semantic contract", server.languageID)
			}
			if action.tool == "inspect" && action.name == "hover" && server.languageID == "proto" && (action.requireResult || action.emptyResultReason != nativeCatalog15x36ProtoHoverEmptyReason) {
				t.Fatalf("proto hover must use the strict legal-empty contract")
			}
		}
	}
	if got := len(nativeCatalog15x36LanguageIDs) * realMCPExpectedActionCount; got != nativeCatalog15x36ExpectedActions {
		t.Fatalf("native catalog action closure=%d, want %d", got, nativeCatalog15x36ExpectedActions)
	}
}

// TestWindowsARM64ProcessARM64NativeCatalog15x36PrecheckE2E 的预检只验证平台和
// 合同，收据明确 NON_PASS；即使启用也不能被当作冷安装、语义或 15 分钟证明。
func TestWindowsARM64ProcessARM64NativeCatalog15x36PrecheckE2E(t *testing.T) {
	if os.Getenv(nativeCatalog15x36PrecheckEnv) != "1" {
		t.Skipf("set %s=1 for the bounded NON_PASS native catalog precheck", nativeCatalog15x36PrecheckEnv)
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("native catalog precheck requires windows/arm64, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if err := nativeCatalog15x36ValidateCatalog(); err != nil {
		t.Fatal(err)
	}
	repoRoot := realNodeRepoRoot(t)
	evidenceDir := nativeCatalog15x36EvidenceDirectory(t, repoRoot)
	receipt := filepath.Join(evidenceDir, "windows-arm64-process-arm64-native-catalog-15x36-precheck.receipt")
	lines := []string{
		"test=windows-arm64-process-arm64-native-catalog-15x36",
		"status=NON_PASS",
		"precheck=true",
		"install=not_run",
		"semantic_actions=not_run",
		"action_total=0",
		"formal_idle=not_run",
		fmt.Sprintf("precheck_idle=%s", nativeCatalog15x36PrecheckIdle),
		fmt.Sprintf("precheck_timeout=%s", nativeCatalog15x36PrecheckTimeout),
		"zero_residual=not_proven",
		"absolute_path_markers=0",
		"reason=bounded_precheck_is_not_a_formal_proof",
	}
	if err := node17x36WriteReceipt(receipt, lines); err != nil {
		t.Fatalf("write native catalog precheck receipt: %v", err)
	}
	t.Logf("native catalog precheck receipt=%s status=NON_PASS", receipt)
}

// TestWindowsARM64ProcessARM64NativeCatalogClangdCProbeE2E 是诊断用短 probe，
// 只验证产品私有 ARM64 clangd 的 stdio initialize/file/open_file/关闭边界。
// 未显式开启时跳过；它不能替代 15×36 或 15 分钟正式证明。
func TestWindowsARM64ProcessARM64NativeCatalogClangdCProbeE2E(t *testing.T) {
	probeLanguage := "c"
	probeEnv := nativeCatalog15x36CProbeEnv
	probeRootEnv := ""
	if os.Getenv(nativeCatalog15x36TerraformProbeEnv) == "1" {
		probeLanguage = "terraform"
		probeEnv = nativeCatalog15x36TerraformProbeEnv
		probeRootEnv = nativeCatalog15x36TerraformProbeRootEnv
	} else if os.Getenv(nativeCatalog15x36RustProbeEnv) == "1" {
		probeLanguage = "rust"
		probeEnv = nativeCatalog15x36RustProbeEnv
		probeRootEnv = nativeCatalog15x36RustProbeRootEnv
	}
	if os.Getenv(probeEnv) != "1" {
		t.Skipf("set %s=1 for the bounded NON_PASS native language probe", probeEnv)
	}
	probeTimeout := nativeCatalog15x36CProbeTimeout
	if probeLanguage == "terraform" || probeLanguage == "rust" {
		// Terraform 单语言根因探针只覆盖真实 stdio 与 definition，不是生命周期证明。
		probeTimeout = 6 * time.Minute
	}
	if probeTimeout < 15*time.Minute && probeLanguage != "terraform" && probeLanguage != "rust" {
		t.Fatalf("native clangd C probe timeout=%s is below the approved 15-minute diagnostic boundary", nativeCatalog15x36CProbeTimeout)
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("native clangd C probe requires windows/arm64, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	if host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("native clangd C probe requires ARM64/ARM64, got native=%q process=%q", host.NativeArch, host.ProcessArch)
	}
	productRoot := strings.TrimSpace(os.Getenv(probeRootEnv))
	if productRoot == "" {
		productRoot, err = os.MkdirTemp("", "sd-node-production-windows-native-c-probe-")
		if err != nil {
			t.Fatalf("create native clangd C probe product root: %v", err)
		}
		t.Cleanup(func() {
			if err := removeRealWindowsProductRoot(productRoot); err != nil {
				t.Errorf("remove native clangd C probe product root: %v", err)
			}
		})
	} else if !filepath.IsAbs(productRoot) {
		t.Fatalf("%s product root must be absolute", probeLanguage)
	}
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict native clangd C probe product root: %v", err)
	}
	repoRoot := realNodeRepoRoot(t)
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", repoRoot)
	t.Setenv("APPDATA", "")
	t.Setenv("MCP_LSP_IDLE_TIMEOUT", nativeCatalog15x36ManagerIdle.String())
	provider := setupInstaller()
	if strings.TrimSpace(os.Getenv(probeRootEnv)) != "" && (probeLanguage == "terraform" || probeLanguage == "rust") {
		previousTransport := httpDefaultTransportForNativeCatalog()
		if previousTransport == nil {
			t.Fatal("Terraform cache-only probe requires an HTTP transport")
		}
		setHTTPDefaultTransportForNativeCatalog(&nativeCatalog15x36NoNetworkTransport{base: previousTransport})
		t.Cleanup(func() { setHTTPDefaultTransportForNativeCatalog(previousTransport) })
	}
	if _, err := installer.DetectWindowsHostPlatform(); err != nil {
		t.Fatalf("formal-equivalent Windows host detection: %v", err)
	}
	if err := nativeCatalog15x36ValidateCatalog(); err != nil {
		t.Fatalf("formal-equivalent catalog validation: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	result, err := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), probeLanguage)
	if err != nil {
		t.Fatalf("ensure ARM64 clangd for C probe: %v", err)
	}
	if result.Status == installer.InstallStatusInstalledFallback || strings.TrimSpace(result.Path) == "" {
		t.Fatalf("C probe received invalid product-owned clangd result: status=%s path_present=%t", result.Status, strings.TrimSpace(result.Path) != "")
	}
	var server realNodeServerCase
	for _, candidate := range nativeCatalog15x36ServerCases() {
		if candidate.languageID == probeLanguage {
			server = candidate
			break
		}
	}
	if server.languageID == "" {
		t.Fatalf("native probe language %q is not in the locked catalog", probeLanguage)
	}
	fixtureRoot := t.TempDir()
	fixture := writeRealMCPLanguageFixture(t, fixtureRoot, server)
	if probeLanguage == "terraform" {
		cliPath, err := installer.ResolveWindowsTerraformCLIPath(productRoot)
		if err != nil {
			t.Fatalf("resolve product-owned Terraform CLI companion for format probe: %v", err)
		}
		format := exec.CommandContext(ctx, cliPath, "fmt", "-check", "-no-color", fixture.targetFile)
		if output, err := format.CombinedOutput(); err != nil {
			safeOutput := strings.ReplaceAll(string(output), fixtureRoot, "<fixture-root>")
			t.Fatalf("product-owned Terraform CLI fmt probe failed: %v (output bytes=%d): %s", err, len(output), safeOutput)
		}
	}
	binary := buildRealMcpLSPBinary(t, repoRoot)
	client := startRealMcpLSPBinary(t, ctx, binary, fixtureRoot, repoRoot, "", "", productRoot)
	mcpPID := client.cmd.Process.Pid
	mcpStart, err := windowsGoplsProcessStartIdentity(mcpPID)
	if err != nil {
		t.Fatalf("capture C probe MCP PID+start: %v", err)
	}
	tracked := map[realMCPProcessKey]realMCPProcessIdentity{{PID: mcpPID, StartToken: mcpStart}: {PID: mcpPID, StartToken: mcpStart, Name: "mcp-lsp", Language: "mcp-lsp"}}
	defer func() {
		_ = trackRealMCPProcessTree(t, mcpPID, "c-probe-final", tracked)
		if !node17x36CloseWithExitProof(t, client) {
			t.Errorf("C probe did not send verified exit")
		}
		requireRealMCPProcessIdentitiesGone(t, tracked)
	}()
	initialize := client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}})
	if initialize.Error != nil {
		t.Fatalf("C probe initialize failed: %s", initialize.Result.ContentText())
	}
	if err := nativeCatalog15x36Notify(client, "notifications/initialized", map[string]any{}); err != nil {
		t.Fatalf("C probe initialized notification: %v", err)
	}
	response := client.callTool(t, "file", realMCPWindowsToolArguments(probeLanguage, fixtureRoot, "file", "open_file", map[string]any{
		"action":    "open_file",
		"file_path": fixture.targetFile,
	}))
	requireRealMCPActionResult(t, response, true, "", false, "", false, "native language file/open_file probe")
	if probeLanguage == "terraform" {
		definition := client.callTool(t, "inspect", realMCPWindowsToolArguments(probeLanguage, fixtureRoot, "inspect", "definition", map[string]any{
			"action": "definition",
			"pos":    fixture.semanticPosition,
		}))
		requireRealMCPActionResult(t, definition, true, "", false, "", false, "native terraform inspect/definition probe")
	} else if probeLanguage == "rust" {
		hover := client.callTool(t, "inspect", realMCPWindowsToolArguments(probeLanguage, fixtureRoot, "inspect", "hover", map[string]any{
			"action": "hover",
			"pos":    fixture.semanticPosition,
		}))
		requireRealMCPActionResult(t, hover, true, "", false, "", false, "native rust inspect/hover probe")
		definition := client.callTool(t, "inspect", realMCPWindowsToolArguments(probeLanguage, fixtureRoot, "inspect", "definition", map[string]any{
			"action": "definition",
			"pos":    fixture.semanticPosition,
		}))
		requireRealMCPActionResult(t, definition, true, "", false, "", false, "native rust inspect/definition probe")
		completion := client.callTool(t, "completion", realMCPWindowsToolArguments(probeLanguage, fixtureRoot, "completion", "completion", map[string]any{
			"pos": fixture.completionPosition,
		}))
		requireRealMCPActionResult(t, completion, false, "", true, "completion", false, "native rust completion probe")
		format := client.callTool(t, "patch_edit", realMCPWindowsToolArguments(probeLanguage, fixtureRoot, "patch_edit", "format", map[string]any{
			"action":    "format",
			"file_path": fixture.formatFile,
		}))
		requireRealMCPActionResult(t, format, false, "", true, "format", false, "native rust format probe")
	}
}

// TestWindowsARM64ProcessARM64NativeCatalogClangdWarmInitPrecheckE2E 先在同一私有
// product root 完成 15 个 catalog 产品安装，再用三个独立 MCP 进程只做 C initialize/
// open_file/退出。它是六分钟内的 NON_PASS_precheck，用于区分 clangd 启动瞬态，绝不替代正式证明。
// nativeCatalog15x36ApplyTransportEnvVariant 仅用于正式等价 C 诊断，记录环境形状而不改变生产启动语义。
// 四轮由独立 go test 进程执行：baseline 覆盖受控目录，其他轮次分别继承指定环境变量。
func nativeCatalog15x36ApplyTransportEnvVariant(t *testing.T, root, repoRoot, variant string) {
	t.Helper()
	if variant == "" {
		variant = "baseline"
	}
	if variant != "baseline" && variant != "inherit_userdata" && variant != "inherit_temp" && variant != "no_vclibs_path" {
		t.Fatalf("unknown transport environment bisection variant %q", variant)
	}
	set := func(name, value string) { t.Setenv(name, value) }
	if variant != "inherit_userdata" {
		set("APPDATA", filepath.Join(root, "userdata", "roaming"))
		set("LOCALAPPDATA", filepath.Join(root, "userdata", "local"))
	}
	if variant != "inherit_temp" {
		tmp := filepath.Join(root, "tmp")
		set("TEMP", tmp)
		set("TMP", tmp)
	}
	if variant != "no_vclibs_path" {
		vclibs, err := installer.ResolveWindowsVCLibsDesktopAppLocalProcessPath(root)
		if err != nil {
			t.Fatalf("resolve verified VCLibs process root for env diagnostic: %v", err)
		}
		set("SUPER_DOLPHIN_MSVC_RUNTIME_DIR", vclibs)
		set("PATH", vclibs+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	set("SUPER_DOLPHIN_HOME", root)
	set("PROJECT_ROOT", "")
	set("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", repoRoot)
	set("MCP_LSP_IDLE_TIMEOUT", nativeCatalog15x36ManagerIdle.String())
}

// nativeCatalog15x36WriteTransportEnvSummary 只写字段名、长度、摘要和类别，禁止写环境值。
func nativeCatalog15x36WriteTransportEnvSummary(wirePath, variant string) {
	names := []string{"SUPER_DOLPHIN_HOME", "PROJECT_ROOT", "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "PATH", "SUPER_DOLPHIN_MSVC_RUNTIME_DIR", "MCP_LSP_IDLE_TIMEOUT"}
	lines := []string{"transport_env_variant=" + variant, "transport_env_value_policy=metadata_only"}
	for _, name := range names {
		value, present := os.LookupEnv(name)
		class := "unset"
		if present {
			switch {
			case value == "":
				class = "empty"
			case name == "PATH":
				class = "path_list"
			case name == "MCP_LSP_IDLE_TIMEOUT":
				class = "duration"
			case strings.Contains(name, "ROOT") || strings.HasSuffix(name, "DATA") || name == "TEMP" || name == "TMP" || name == "SUPER_DOLPHIN_MSVC_RUNTIME_DIR":
				class = "absolute_path"
			default:
				class = "nonempty"
			}
		}
		digest := sha256.Sum256([]byte(value))
		lines = append(lines, fmt.Sprintf("transport_env.%s.present=%t;bytes=%d;sha256=%x;class=%s", name, present, len([]byte(value)), digest, class))
	}
	for _, line := range lines {
		_ = nativeCatalog15x36WriteWire(wirePath, line)
	}
}

// nativeCatalog15x36ApplyLimitVariant 只在诊断子进程中移除资源覆盖，禁止绕过进程所有权和 kill-on-close。
func nativeCatalog15x36ApplyLimitVariant(t *testing.T, variant string) {
	t.Helper()
	var names []string
	switch variant {
	case "no_process_rss":
		names = []string{"AGENT_LSP_PRIMARY_RSS_LIMIT_MB", "AGENT_LSP_SECONDARY_RSS_LIMIT_MB", "AGENT_LSP_GO_RSS_LIMIT_MB", "AGENT_LSP_RSS_LIMIT_MB"}
	case "no_cohort_rss":
		names = []string{"AGENT_LSP_COHORT_RSS_LIMIT_MB", "AGENT_LSP_RESOURCE_COHORT_HARD_LIMIT_MB"}
	default:
		t.Fatalf("unknown diagnostic resource limit variant %q", variant)
	}
	for _, name := range names {
		old, present := os.LookupEnv(name)
		_ = os.Unsetenv(name)
		t.Cleanup(func() {
			if present {
				_ = os.Setenv(name, old)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
}

func TestWindowsARM64ProcessARM64NativeCatalogTransportEnvSummaryIsRedacted(t *testing.T) {
	wirePath := filepath.Join(t.TempDir(), "transport-env.wire.log")
	t.Setenv("SUPER_DOLPHIN_HOME", `C:\private\secret-root`)
	t.Setenv("PATH", `C:\private\secret-bin;C:\Windows\System32`)
	t.Setenv("MCP_LSP_IDLE_TIMEOUT", "17m0s")
	nativeCatalog15x36WriteTransportEnvSummary(wirePath, "baseline")
	data, err := os.ReadFile(wirePath)
	if err != nil {
		t.Fatalf("read transport environment summary: %v", err)
	}
	text := string(data)
	for _, forbidden := range []string{`C:\private\secret-root`, `C:\private\secret-bin`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("transport summary leaked environment value %q", forbidden)
		}
	}
	for _, name := range []string{"SUPER_DOLPHIN_HOME", "PROJECT_ROOT", "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "APPDATA", "LOCALAPPDATA", "TEMP", "TMP", "PATH", "SUPER_DOLPHIN_MSVC_RUNTIME_DIR", "MCP_LSP_IDLE_TIMEOUT"} {
		if !strings.Contains(text, "transport_env."+name+".") {
			t.Fatalf("transport summary omitted %s", name)
		}
	}
}

func TestWindowsARM64ProcessARM64NativeCatalogClangdWarmInitPrecheckE2E(t *testing.T) {
	const gate = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_CLANGD_WARM_INIT_PRECHECK"
	const warmRootEnv = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_WARM_ROOT"
	const prepOnlyEnv = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_WARM_PREP_ONLY"
	const singleDiagnosticEnv = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_CLANGD_SINGLE_DIAGNOSTIC"
	const formalContextCDiagnosticEnv = "FORMAL_CONTEXT_C_ONLY_DIAGNOSTIC"
	const formalContextCDiagnosticVariantEnv = "FORMAL_CONTEXT_C_ONLY_DIAGNOSTIC_VARIANT"
	if os.Getenv(gate) != "1" {
		t.Skipf("set %s=1 for the bounded warm catalog clangd init precheck", gate)
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("warm catalog clangd precheck requires windows/arm64, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	prepOnly := os.Getenv(prepOnlyEnv) == "1"
	singleDiagnostic := os.Getenv(singleDiagnosticEnv) == "1" || os.Getenv(formalContextCDiagnosticEnv) == "1"
	diagnosticVariant := strings.TrimSpace(os.Getenv(formalContextCDiagnosticVariantEnv))
	budget := nativeCatalog15x36PrecheckTimeout
	if prepOnly || singleDiagnostic {
		budget = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	repoRoot := realNodeRepoRoot(t)
	productRoot := strings.TrimSpace(os.Getenv(warmRootEnv))
	warmCacheReused := productRoot != ""
	var err error
	if !warmCacheReused {
		productRoot, err = os.MkdirTemp("", "sd-node-production-windows-native-catalog-warm-precheck-")
		if err != nil {
			t.Fatalf("create warm catalog product root: %v", err)
		}
		t.Cleanup(func() {
			if cleanupErr := removeRealWindowsProductRoot(productRoot); cleanupErr != nil {
				t.Errorf("remove warm catalog product root: %v", cleanupErr)
			}
		})
	} else if info, statErr := os.Stat(productRoot); os.IsNotExist(statErr) {
		if err := os.MkdirAll(productRoot, 0o700); err != nil {
			t.Fatalf("create configured warm catalog root: %v", err)
		}
	} else if statErr != nil || !info.IsDir() {
		t.Fatalf("configured warm catalog root is unavailable: %v", statErr)
	}
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict warm catalog product root: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", repoRoot)
	t.Setenv("APPDATA", "")
	if diagnosticVariant != "without_idle_env" {
		t.Setenv("MCP_LSP_IDLE_TIMEOUT", nativeCatalog15x36ManagerIdle.String())
	}
	if _, err := installer.DetectWindowsHostPlatform(); err != nil {
		t.Fatalf("formal-equivalent Windows host detection: %v", err)
	}
	if err := nativeCatalog15x36ValidateCatalog(); err != nil {
		t.Fatalf("formal-equivalent catalog validation: %v", err)
	}
	provider := setupInstaller()
	for _, languageID := range nativeCatalog15x36LanguageIDs {
		if _, err := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), languageID); err != nil {
			t.Fatalf("warm catalog install %s: %v", languageID, err)
		}
	}
	receiptDir := nativeCatalog15x36EvidenceDirectory(t, repoRoot)
	receiptPath := filepath.Join(receiptDir, "windows-arm64-process-arm64-native-catalog-clangd-warm-init-precheck.receipt")
	if singleDiagnostic {
		binary := buildRealMcpLSPBinary(t, repoRoot)
		fixtureRoot := t.TempDir()
		astFile := filepath.Join(fixtureRoot, "ast_fixture.js")
		writeRealFixture(t, astFile, "function nativeCatalogAst(name) { return name; }\nnativeCatalogAst(\"world\");\n")
		var fixture realMCPFixture
		if diagnosticVariant == "fixture_early" {
			fixture = writeRealMCPLanguageFixture(t, fixtureRoot, nativeCatalog15x36ServerCases()[0])
		}
		wirePath := filepath.Join(receiptDir, "windows-arm64-process-arm64-native-catalog-clangd-single-diagnostic.wire.log")
		_ = nativeCatalog15x36WriteWire(wirePath, "phase=started;status=NON_PASS_diag;installed_languages=15")
		nativeCatalog15x36LogResourceSnapshot(t, wirePath, "single_diag_before_mcp", os.Getpid(), productRoot, 0)
		if os.Getenv(nativeCatalog15x36TransportEnvBisectionEnv) == "1" {
			transportVariant := strings.TrimSpace(os.Getenv(nativeCatalog15x36TransportEnvVariantEnv))
			nativeCatalog15x36ApplyTransportEnvVariant(t, productRoot, repoRoot, transportVariant)
			nativeCatalog15x36WriteTransportEnvSummary(wirePath, transportVariant)
		}
		if limitVariant := strings.TrimSpace(os.Getenv("MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_LIMIT_VARIANT")); limitVariant != "" {
			nativeCatalog15x36ApplyLimitVariant(t, limitVariant)
			_ = nativeCatalog15x36WriteWire(wirePath, "resource_limit_variant="+limitVariant+";resource_limit_policy=diagnostic_only")
		}
		clientCtx, clientCancel := context.WithTimeout(ctx, 3*time.Hour)
		defer clientCancel()
		client := startRealMcpLSPBinary(t, clientCtx, binary, fixtureRoot, repoRoot, "", "", productRoot)
		pid := client.cmd.Process.Pid
		start, startErr := windowsGoplsProcessStartIdentity(pid)
		nativeCatalog15x36LogResourceSnapshot(t, wirePath, "single_diag_before_clangd", pid, productRoot, 0)
		tracked := map[realMCPProcessKey]realMCPProcessIdentity{{PID: pid, StartToken: start}: {PID: pid, StartToken: start, Name: "mcp-lsp", Language: "c"}}
		_ = trackRealMCPProcessTree(t, pid, "single_diag_before_initialize", tracked)
		if diagnosticVariant != "fixture_early" {
			fixture = writeRealMCPLanguageFixture(t, fixtureRoot, nativeCatalog15x36ServerCases()[0])
		}
		status := "runtime_failure"
		childObserved := false
		initialize := client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}})
		if initialize.Error == nil && nativeCatalog15x36Notify(client, "notifications/initialized", map[string]any{}) == nil {
			_ = trackRealMCPProcessTree(t, pid, "single_diag_after_initialize", tracked)
			childObserved = childObserved || len(tracked) > 1
			if diagnosticVariant != "without_tools_list" {
				requireRealMCPToolFamilies(t, callMcpLSPBinaryRaw(t, client, "tools/list", map[string]any{}))
			}
			response := client.callTool(t, "file", realMCPWindowsToolArguments("c", fixtureRoot, "file", "open_file", map[string]any{"action": "open_file", "file_path": fixture.targetFile}))
			_ = trackRealMCPProcessTree(t, pid, "single_diag_after_open_file", tracked)
			childObserved = childObserved || len(tracked) > 1
			definition := client.callTool(t, "file", realMCPWindowsToolArguments("c", fixtureRoot, "file", "definition", map[string]any{"action": "definition", "file_path": fixture.targetFile, "position": fixture.semanticPosition}))
			_ = trackRealMCPProcessTree(t, pid, "single_diag_after_definition", tracked)
			childObserved = childObserved || len(tracked) > 1
			hover := client.callTool(t, "file", realMCPWindowsToolArguments("c", fixtureRoot, "file", "hover", map[string]any{"action": "hover", "file_path": fixture.targetFile, "position": fixture.semanticPosition}))
			_ = trackRealMCPProcessTree(t, pid, "single_diag_after_hover", tracked)
			childObserved = childObserved || len(tracked) > 1
			if response.Error == nil && strings.TrimSpace(response.Result.ContentText()) != "" && definition.Error == nil && strings.TrimSpace(definition.Result.ContentText()) != "" && hover.Error == nil && strings.TrimSpace(hover.Result.ContentText()) != "" && childObserved {
				status = "semantic_success"
			} else if !childObserved {
				status = "NON_PASS_diag_no_clangd_identity"
			}
		}
		nativeCatalog15x36LogResourceSnapshot(t, wirePath, "single_diag_after_clangd", pid, productRoot, 0)
		_ = trackRealMCPProcessTree(t, pid, "single_diag_after_clangd", tracked)
		exitSent := node17x36CloseWithExitProof(t, client)
		requireRealMCPProcessIdentitiesGone(t, tracked)
		_ = nativeCatalog15x36WriteWire(wirePath, fmt.Sprintf("phase=closed;status=%s;shutdown=unknown;exit=%t;zero_residual=verified", status, exitSent))
		lines := []string{"status=NON_PASS_diag", "installed_languages=15", "formal_lifecycle=not_run", fmt.Sprintf("single_c_status=%s", status), fmt.Sprintf("mcp_pid=%d", pid), fmt.Sprintf("mcp_start_captured=%t", startErr == nil), fmt.Sprintf("fixture_target_units=%d", len([]rune(fixture.targetFile))), fmt.Sprintf("fixture_target_sha256=%x", sha256.Sum256([]byte(fixture.targetFile))), fmt.Sprintf("product_root_units=%d", len([]rune(productRoot))), fmt.Sprintf("product_root_sha256=%x", sha256.Sum256([]byte(productRoot))), fmt.Sprintf("tracked_identities=%d", len(tracked)), fmt.Sprintf("clangd_identity_observed=%t", childObserved), fmt.Sprintf("exit_sent=%t", exitSent), "zero_residual=verified", "absolute_path_markers=0"}
		if status != "semantic_success" {
			if sidecarPath, sidecarSHA, sidecarErr := nativeCatalog15x36PersistFailureSidecar(receiptDir, wirePath); sidecarErr != nil {
				t.Errorf("persist single diagnostic failure sidecar: %v", sidecarErr)
			} else {
				lines = append(lines, "failure_sidecar_relative="+filepath.ToSlash(filepath.Base(sidecarPath)), "failure_sidecar_sha256="+sidecarSHA)
			}
		}
		if err := node17x36WriteReceipt(receiptPath, lines); err != nil {
			t.Fatalf("write single clangd diagnostic receipt: %v", err)
		}
		t.Logf("single clangd diagnostic receipt=%s status=NON_PASS_diag", receiptPath)
		return
	}
	if prepOnly {
		lines := []string{"status=NON_PASS_prep", "prep_complete=true", "installed_languages=15", "formal_lifecycle=not_run", "semantic_actions=not_run", "absolute_path_markers=0"}
		if err := node17x36WriteReceipt(receiptPath, lines); err != nil {
			t.Fatalf("write warm catalog preparation receipt: %v", err)
		}
		t.Logf("warm catalog preparation receipt=%s status=warm_prep_pass", receiptPath)
		return
	}
	binary := buildRealMcpLSPBinary(t, repoRoot)
	fixtureRoot := t.TempDir()
	fixture := writeRealMCPLanguageFixture(t, fixtureRoot, nativeCatalog15x36ServerCases()[0])
	lines := []string{"status=NON_PASS_precheck", "installed_languages=15", fmt.Sprintf("warm_cache_reused=%t", warmCacheReused), "formal_lifecycle=not_run", "absolute_path_markers=0"}
	for trial := 1; trial <= 3; trial++ {
		if ctx.Err() != nil {
			lines = append(lines, fmt.Sprintf("trial.%d.status=deadline_exceeded", trial))
			break
		}
		clientCtx, clientCancel := context.WithTimeout(ctx, 75*time.Second)
		client := startRealMcpLSPBinary(t, clientCtx, binary, fixtureRoot, repoRoot, "", "", productRoot)
		pid := client.cmd.Process.Pid
		start, startErr := windowsGoplsProcessStartIdentity(pid)
		trialStatus := "runtime_failure"
		initialize := client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}})
		if initialize.Error == nil && nativeCatalog15x36Notify(client, "notifications/initialized", map[string]any{}) == nil {
			response := client.callTool(t, "file", realMCPWindowsToolArguments("c", fixtureRoot, "file", "open_file", map[string]any{
				"action":    "open_file",
				"file_path": fixture.targetFile,
			}))
			if response.Error == nil && strings.TrimSpace(response.Result.ContentText()) != "" {
				trialStatus = "semantic_success"
			}
		}
		tracked := map[realMCPProcessKey]realMCPProcessIdentity{{PID: pid, StartToken: start}: {PID: pid, StartToken: start, Name: "mcp-lsp", Language: "mcp-lsp"}}
		exitSent := node17x36CloseWithExitProof(t, client)
		requireRealMCPProcessIdentitiesGone(t, tracked)
		clientCancel()
		lines = append(lines, fmt.Sprintf("trial.%d.status=%s;pid=%d;start_captured=%t;exit_sent=%t", trial, trialStatus, pid, startErr == nil, exitSent))
	}
	if err := node17x36WriteReceipt(receiptPath, lines); err != nil {
		t.Fatalf("write warm catalog precheck receipt: %v", err)
	}
	t.Logf("warm catalog clangd init precheck receipt=%s status=NON_PASS_precheck", receiptPath)
}

// TestWindowsARM64ProcessARM64NativeCatalog15x36SoakE2E 通过生产 setupInstaller 和
// EnsureInstalledDetailed 冷安装 15 个 catalog 语言，使用一个 product root、一个真实
// mcp-lsp stdio 进程完成 540 个 action、共享 15 分钟 idle、post-idle、shutdown/exit
// 及 PID+start 零残留证明。正式测试默认关闭，且不会把 capability_unsupported 计入成功。
// TestWindowsARM64ProcessARM64NativeCatalogLocalPayloadPrewarmE2E only materializes
// verified local payloads into a new product cache; it is not a download or lifecycle proof.
func TestWindowsARM64ProcessARM64NativeCatalogLocalPayloadPrewarmE2E(t *testing.T) {
	if os.Getenv(nativeCatalog15x36PrewarmEnv) != "1" {
		t.Skipf("set %s=1 for non-formal local payload prewarm", nativeCatalog15x36PrewarmEnv)
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("local payload prewarm requires windows/arm64, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil || host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("local payload prewarm requires ARM64/ARM64: %v native=%q process=%q", err, host.NativeArch, host.ProcessArch)
	}
	sources := strings.Split(strings.TrimSpace(os.Getenv(nativeCatalog15x36PrewarmSourcesEnv)), ";")
	target := strings.TrimSpace(os.Getenv(nativeCatalog15x36PrewarmTargetEnv))
	evidence := strings.TrimSpace(os.Getenv(nativeCatalog15x36PrewarmEvidenceEnv))
	if len(sources) == 0 || strings.TrimSpace(sources[0]) == "" || target == "" || evidence == "" || !filepath.IsAbs(target) || !filepath.IsAbs(evidence) {
		t.Fatalf("local payload prewarm requires absolute source roots, target root and evidence dir")
	}
	for i := range sources {
		sources[i] = filepath.Clean(strings.TrimSpace(sources[i]))
		if !filepath.IsAbs(sources[i]) {
			t.Fatalf("local payload prewarm source root is not absolute")
		}
	}
	target = filepath.Clean(target)
	evidence = filepath.Clean(evidence)
	if err := os.MkdirAll(evidence, 0o700); err != nil {
		t.Fatalf("create local prewarm evidence dir: %v", err)
	}
	receiptPath := filepath.Join(evidence, "windows-arm64-process-arm64-native-catalog-local-payload-prewarm.receipt")
	wirePath := filepath.Join(evidence, "windows-arm64-process-arm64-native-catalog-local-payload-prewarm.wire.log")
	status, reason := "NON_PASS", "not_started"
	defer func() {
		lines := []string{"proof_kind=setup_non_formal_local_payload_prewarm", "automatic_install=false", "formal_lifecycle=not_run", "native_arch=arm64", "process_arch=arm64", "status=" + status, "reason=" + reason, fmt.Sprintf("source_roots=%d", len(sources)), fmt.Sprintf("source_roots_digest=%x", sha256.Sum256([]byte(strings.Join(sources, "\x00")))), fmt.Sprintf("target_root_digest=%x", sha256.Sum256([]byte(target))), "absolute_path_markers=0", "http_requests=0"}
		_ = node17x36WriteReceipt(receiptPath, lines)
		_ = nativeCatalog15x36WriteWire(wirePath, fmt.Sprintf("phase=closed;status=%s;proof_kind=setup_non_formal_local_payload_prewarm;formal_lifecycle=not_run", status))
	}()
	if _, err := os.Stat(target); err == nil {
		if entries, readErr := os.ReadDir(target); readErr != nil || len(entries) != 0 {
			reason = "target_root_not_empty"
			t.Fatalf("local payload prewarm target root must be new and empty")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		reason = "target_root_stat_failed"
		t.Fatalf("inspect local prewarm target root: %v", err)
	}
	if err := nativeCatalog15x36PrewarmLockedPayloads(t, host, sources, target); err != nil {
		reason = "payload_inventory_or_materialization_failed"
		t.Fatalf("local payload prewarm: %v", err)
	}
	status = "PASS"
	reason = "all_payloads_materialized_and_cache_verified"
}

// nativeCatalog15x36PrewarmLockedPayloads copies only verified payload slots, then lets production materialize ready trees.
func nativeCatalog15x36PrewarmLockedPayloads(t *testing.T, host installer.WindowsHostPlatform, sources []string, target string) error {
	t.Helper()
	type payload struct {
		product installer.WindowsLSPProduct
		asset   installer.WindowsLockedAsset
		source  string
	}
	seen := make(map[string]struct{})
	payloads := make([]payload, 0, len(nativeCatalog15x36ServerCases()))
	for _, server := range nativeCatalog15x36ServerCases() {
		entry, err := installer.WindowsLSPCatalogEntryForLanguage(server.languageID)
		if err != nil {
			return err
		}
		asset, err := installer.WindowsLSPAssetForPlatform(entry.Product, host)
		if err != nil {
			return err
		}
		if asset.Architecture != installer.WindowsHostArchARM64 || asset.SHA256 == "" || asset.Version == "" {
			return fmt.Errorf("%s locked asset is not exact ARM64/version/SHA", server.languageID)
		}
		name, err := nativeCatalog15x36PayloadSlotName(asset.Format)
		if err != nil {
			return err
		}
		key := string(entry.Product) + "\x00" + asset.Version + "\x00" + asset.Architecture + "\x00" + asset.SHA256
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		var found string
		for _, source := range sources {
			candidate := filepath.Join(source, "cache", "lsp-assets", string(entry.Product), asset.Version, asset.Architecture, asset.SHA256, name)
			if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
				found = candidate
				break
			}
		}
		if found == "" {
			return fmt.Errorf("missing locked payload product=%s version=%s arch=%s", entry.Product, asset.Version, asset.Architecture)
		}
		actual, err := hashFileSHA256(found)
		if err != nil || !strings.EqualFold(actual, asset.SHA256) {
			return fmt.Errorf("payload digest mismatch product=%s expected=%s actual=%s", entry.Product, asset.SHA256, actual)
		}
		payloads = append(payloads, payload{product: entry.Product, asset: asset, source: found})
	}
	if len(payloads) == 0 {
		return errors.New("no locked payloads found")
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	if err := securefs.RestrictPrivateOwnerOnly(target, 0o700); err != nil {
		return err
	}
	if err := nativeCatalog15x36RejectReparsePath(target); err != nil {
		return err
	}
	for _, item := range payloads {
		name, err := nativeCatalog15x36PayloadSlotName(item.asset.Format)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, "cache", "lsp-assets", string(item.product), item.asset.Version, item.asset.Architecture, item.asset.SHA256, name)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		if err := copyFileOwnerOnly(item.source, destination); err != nil {
			return err
		}
	}
	return nil
}

// TestWindowsARM64ProcessARM64NativeCatalogMixedPrepareE2E 只准备非正式正确性缓存：
// 已验证 payload 从本地复制，缺失资产交给生产安装器下载；它绝不启动 MCP 或生命周期。
func TestWindowsARM64ProcessARM64NativeCatalogMixedPrepareE2E(t *testing.T) {
	importReady := os.Getenv(nativeCatalog15x36ImportReadyEnv) == "1"
	if os.Getenv(nativeCatalog15x36MixedPrepareEnv) != "1" && !importReady {
		t.Skipf("set %s=1 or %s=1 for non-formal catalog preparation", nativeCatalog15x36MixedPrepareEnv, nativeCatalog15x36ImportReadyEnv)
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("mixed prepare requires windows/arm64, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil || host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("mixed prepare requires ARM64/ARM64: %v native=%q process=%q", err, host.NativeArch, host.ProcessArch)
	}
	target := filepath.Clean(strings.TrimSpace(os.Getenv(nativeCatalog15x36MixedTargetEnv)))
	evidence := filepath.Clean(strings.TrimSpace(os.Getenv(nativeCatalog15x36MixedEvidenceEnv)))
	sourceText := strings.TrimSpace(os.Getenv(nativeCatalog15x36MixedSourcesEnv))
	if target == "." || evidence == "." || !filepath.IsAbs(target) || !filepath.IsAbs(evidence) || sourceText == "" {
		t.Fatal("mixed prepare requires absolute target/evidence and source roots")
	}
	sources := strings.Split(sourceText, ";")
	for i := range sources {
		sources[i] = filepath.Clean(strings.TrimSpace(sources[i]))
		if !filepath.IsAbs(sources[i]) {
			t.Fatalf("mixed source root %q is not absolute", sources[i])
		}
	}
	if info, statErr := os.Stat(target); statErr == nil {
		if !info.IsDir() {
			t.Fatal("mixed target root is not a directory")
		}
		entries, readErr := os.ReadDir(target)
		if readErr != nil || len(entries) != 0 {
			t.Fatal("mixed target root must be new and empty")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("inspect mixed target root: %v", statErr)
	}
	if err := os.MkdirAll(evidence, 0o700); err != nil {
		t.Fatalf("create mixed evidence: %v", err)
	}
	receiptPath := filepath.Join(evidence, "windows-arm64-process-arm64-native-catalog-mixed-prepare.receipt")
	wirePath := filepath.Join(evidence, "windows-arm64-process-arm64-native-catalog-mixed-prepare.wire.log")
	status, reason := "NON_PASS", "not_started"
	localByLanguage := map[string]string{}
	installLines := []string{}
	var observer *node17x36HTTPObserver
	defer func() {
		proofKind := "setup_non_formal_mixed_prepare"
		if importReady {
			proofKind = "setup_non_formal_import_ready"
		}
		lines := []string{
			"proof_kind=" + proofKind, "automatic_install=false", "formal_lifecycle=not_run",
			"native_arch=arm64", "process_arch=arm64", "status=" + status, "reason=" + reason,
			fmt.Sprintf("source_roots=%d", len(sources)),
			fmt.Sprintf("source_roots_digest=%x", sha256.Sum256([]byte(strings.Join(sources, "\x00")))),
			fmt.Sprintf("target_root_digest=%x", sha256.Sum256([]byte(target))),
			fmt.Sprintf("local_locked_payloads=%d", len(localByLanguage)),
			fmt.Sprintf("official_download_languages=%d", len(nativeCatalog15x36ServerCases())-len(localByLanguage)),
			"absolute_path_markers=0",
		}
		if observer != nil {
			counts := observer.Snapshot()
			lines = append(lines, fmt.Sprintf("http_requests=%d", counts.Requests), fmt.Sprintf("http_attempts=%d", counts.Attempts), fmt.Sprintf("http_responses=%d", counts.Responses), fmt.Sprintf("http_redirects=%d", counts.RedirectResponses), fmt.Sprintf("http_transport_errors=%d", counts.TransportErrors))
		} else {
			lines = append(lines, "http_requests=0", "http_attempts=0", "http_responses=0", "http_redirects=0", "http_transport_errors=0")
		}
		for _, server := range nativeCatalog15x36ServerCases() {
			mode := localByLanguage[server.languageID]
			if mode == "" {
				mode = "official_download"
			}
			lines = append(lines, "asset_source."+server.languageID+"="+mode)
		}
		lines = append(lines, installLines...)
		_ = node17x36WriteReceipt(receiptPath, lines)
		_ = nativeCatalog15x36WriteWire(wirePath, fmt.Sprintf("phase=closed;status=%s;proof_kind=%s;formal_lifecycle=not_run", status, proofKind))
	}()
	if err := nativeCatalog15x36CopyAvailablePayloads(host, sources, target, localByLanguage); err != nil {
		reason = "local_payload_copy_failed"
		t.Fatalf("copy available locked payloads: %v", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(target, 0o700); err != nil {
		reason = "target_acl_failed"
		t.Fatalf("restrict mixed target root: %v", err)
	}
	if err := nativeCatalog15x36RejectReparsePath(target); err != nil {
		reason = "target_reparse_failed"
		t.Fatalf("mixed target reparse check: %v", err)
	}
	oldTransport := httpDefaultTransportForNativeCatalog()
	if oldTransport == nil {
		t.Fatal("mixed prepare requires HTTP transport")
	}
	var noNetwork *nativeCatalog15x36NoNetworkTransport
	if importReady {
		noNetwork = &nativeCatalog15x36NoNetworkTransport{base: oldTransport}
		setHTTPDefaultTransportForNativeCatalog(noNetwork)
	} else {
		observer = &node17x36HTTPObserver{base: oldTransport}
		setHTTPDefaultTransportForNativeCatalog(observer)
	}
	defer setHTTPDefaultTransportForNativeCatalog(oldTransport)
	t.Setenv("SUPER_DOLPHIN_HOME", target)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", realNodeRepoRoot(t))
	t.Setenv("APPDATA", "")
	provider := setupInstaller()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	defer cancel()
	for _, server := range nativeCatalog15x36ServerCases() {
		entry, entryErr := installer.WindowsLSPCatalogEntryForLanguage(server.languageID)
		if entryErr != nil {
			reason = "catalog_entry_failed"
			t.Fatal(entryErr)
		}
		asset, assetErr := installer.WindowsLSPAssetForPlatform(entry.Product, host)
		if assetErr != nil {
			reason = "asset_selection_failed"
			t.Fatal(assetErr)
		}
		result, installErr := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), server.languageID)
		if installErr != nil {
			reason = "official_install_failed"
			t.Fatalf("EnsureInstalledDetailed(%s): %v", server.languageID, installErr)
		}
		if result.Status == installer.InstallStatusInstalledFallback || strings.TrimSpace(result.Path) == "" {
			reason = "forbidden_fallback_or_empty_path"
			t.Fatalf("invalid production result for %s: status=%s path_empty=%t", server.languageID, result.Status, strings.TrimSpace(result.Path) == "")
		}
		if err := nativeCatalog15x36VerifyCachedAsset(target, entry.Product, asset); err != nil {
			reason = "ready_asset_verification_failed"
			t.Fatalf("verify %s: %v", server.languageID, err)
		}
		installLines = append(installLines, fmt.Sprintf("install.%s.status=%s;product=%s;version=%s;sha256=%s;architecture=%s", server.languageID, result.Status, entry.Product, asset.Version, asset.SHA256, asset.Architecture))
	}
	if importReady {
		if noNetwork.Requests.Load() != 0 {
			reason = "import_ready_attempted_network"
			t.Fatalf("import-ready attempted network requests=%d", noNetwork.Requests.Load())
		}
	} else {
		counts := observer.Snapshot()
		if counts.Requests <= 0 || counts.Attempts != counts.Requests || counts.Responses != counts.Requests || counts.TransportErrors != 0 || counts.FailedResponses != 0 {
			reason = "http_install_observation_failed"
			t.Fatalf("mixed HTTP ledger invalid: requests=%d attempts=%d responses=%d errors=%d failed=%d", counts.Requests, counts.Attempts, counts.Responses, counts.TransportErrors, counts.FailedResponses)
		}
	}
	status, reason = "PASS", "all_assets_ready_local_or_official"
}

// nativeCatalog15x36CopyAvailablePayloads 只复制已有且逐字节校验通过的 payload；缺失项由生产安装器处理。
func nativeCatalog15x36CopyAvailablePayloads(host installer.WindowsHostPlatform, sources []string, target string, modes map[string]string) error {
	seen := map[string]bool{}
	// clangd 等 native 工具的生产安装先检查 VCLibs；导入时也必须带上同一锁定槽。
	vclibsAsset, err := installer.WindowsVCLibsDesktopAssetForPlatform(host)
	if err != nil {
		return err
	}
	vclibsName, err := nativeCatalog15x36PayloadSlotName(vclibsAsset.Format)
	if err != nil {
		return err
	}
	for _, source := range sources {
		candidate := filepath.Join(source, "cache", "lsp-assets", "windows-vclibs-desktop-app-local", vclibsAsset.Version, vclibsAsset.Architecture, vclibsAsset.SHA256, vclibsName)
		if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
			digest, hashErr := hashFileSHA256(candidate)
			if hashErr != nil {
				return hashErr
			}
			if !strings.EqualFold(digest, vclibsAsset.SHA256) {
				return errors.New("local VCLibs payload digest mismatch")
			}
			destination := filepath.Join(target, "cache", "lsp-assets", "windows-vclibs-desktop-app-local", vclibsAsset.Version, vclibsAsset.Architecture, vclibsAsset.SHA256, vclibsName)
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				return err
			}
			if err := copyFileOwnerOnly(candidate, destination); err != nil {
				return err
			}
			break
		}
	}
	for _, server := range nativeCatalog15x36ServerCases() {
		entry, err := installer.WindowsLSPCatalogEntryForLanguage(server.languageID)
		if err != nil {
			return err
		}
		asset, err := installer.WindowsLSPAssetForPlatform(entry.Product, host)
		if err != nil {
			return err
		}
		name, err := nativeCatalog15x36PayloadSlotName(asset.Format)
		if err != nil {
			return err
		}
		key := string(entry.Product) + "\x00" + asset.Version + "\x00" + asset.Architecture + "\x00" + asset.SHA256
		if seen[key] {
			modes[server.languageID] = "local_locked_payload"
			continue
		}
		seen[key] = true
		for _, source := range sources {
			candidate := filepath.Join(source, "cache", "lsp-assets", string(entry.Product), asset.Version, asset.Architecture, asset.SHA256, name)
			if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
				digest, hashErr := hashFileSHA256(candidate)
				if hashErr != nil {
					return hashErr
				}
				if !strings.EqualFold(digest, asset.SHA256) {
					return fmt.Errorf("local payload digest mismatch for %s", server.languageID)
				}
				destination := filepath.Join(target, "cache", "lsp-assets", string(entry.Product), asset.Version, asset.Architecture, asset.SHA256, name)
				if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
					return err
				}
				if err := copyFileOwnerOnly(candidate, destination); err != nil {
					return err
				}
				modes[server.languageID] = "local_locked_payload"
				break
			}
		}
	}
	return nil
}

func nativeCatalog15x36PayloadSlotName(format installer.WindowsLockedAssetFormat) (string, error) {
	switch format {
	case installer.WindowsLockedAssetFormatRaw:
		return "payload.raw", nil
	case installer.WindowsLockedAssetFormatZip:
		return "payload.zip", nil
	case installer.WindowsLockedAssetFormatTarGz:
		return "payload.tar.gz", nil
	case installer.WindowsLockedAssetFormatTarXz:
		return "payload.tar.xz", nil
	default:
		return "", fmt.Errorf("unsupported locked payload format %q", format)
	}
}

func hashFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// nativeCatalog15x36PersistFailureSidecar 将失败时的 wire 事实压缩为低敏副本；
// 不复制日志正文、命令行、环境值或路径，便于在测试进程退出后按 SHA 复核。
func nativeCatalog15x36PersistFailureSidecar(evidenceDir, wirePath string) (string, string, error) {
	data, err := os.ReadFile(wirePath)
	if err != nil {
		return "", "", err
	}
	sha := sha256.Sum256(data)
	lineCount := 0
	if len(data) > 0 {
		lineCount = 1 + bytes.Count(data, []byte{'\n'})
	}
	sidecar := filepath.Join(evidenceDir, "windows-arm64-process-arm64-native-catalog-failure.sidecar")
	body := []byte(fmt.Sprintf("source_wire_basename=%s\nsource_wire_bytes=%d\nsource_wire_sha256=%x\nsource_wire_lines=%d\nabsolute_path_markers=0\n", filepath.Base(wirePath), len(data), sha, lineCount))
	if err := os.WriteFile(sidecar, body, 0o600); err != nil {
		return "", "", err
	}
	sidecarSHA, err := hashFileSHA256(sidecar)
	if err != nil {
		return "", "", err
	}
	return sidecar, sidecarSHA, nil
}

func TestWindowsARM64ProcessARM64NativeCatalogFailureSidecarReceiptContract(t *testing.T) {
	evidenceDir := t.TempDir()
	wirePath := filepath.Join(evidenceDir, "native.failure.wire.log")
	if err := os.WriteFile(wirePath, []byte("retry_decision=accepted;attempt=1;exact_win32_122=true\nDidOpen_recovery_state=cleanup_complete;retry_budget_used=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sidecarPath, sidecarSHA, err := nativeCatalog15x36PersistFailureSidecar(evidenceDir, wirePath)
	if err != nil {
		t.Fatalf("persist failure sidecar: %v", err)
	}
	sidecar, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read failure sidecar: %v", err)
	}
	if !strings.Contains(string(sidecar), "source_wire_bytes=") || !strings.Contains(string(sidecar), "source_wire_sha256=") || !strings.Contains(string(sidecar), "source_wire_lines=3") {
		t.Fatalf("sidecar lacks bounded wire facts: %q", sidecar)
	}
	receiptPath := filepath.Join(evidenceDir, "native.failure.receipt")
	receipt := []string{
		"status=NON_PASS",
		"retry_decision=accepted;exact_win32_122=true",
		"did_open_recovery_state=cleanup_complete",
		"failure_sidecar_relative=" + filepath.Base(sidecarPath),
		"failure_sidecar_sha256=" + sidecarSHA,
		"absolute_path_markers=0",
	}
	if err := node17x36WriteReceipt(receiptPath, receipt); err != nil {
		t.Fatalf("write failure receipt: %v", err)
	}
	receiptBytes, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatalf("read failure receipt: %v", err)
	}
	if !strings.Contains(string(receiptBytes), "failure_sidecar_sha256="+sidecarSHA) {
		t.Fatalf("receipt does not bind sidecar SHA: %q", receiptBytes)
	}
}

func copyFileOwnerOnly(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func TestWindowsARM64ProcessARM64NativeCatalog15x36SoakE2E(t *testing.T) {
	downloadOnly := os.Getenv(nativeCatalog15x36DownloadOnlyEnv) == "1"
	if os.Getenv(nativeCatalog15x36FormalEnv) != "1" && !downloadOnly {
		t.Skipf("set %s=1 to run the Windows ARM64/process ARM64 native catalog 15x36 proof", nativeCatalog15x36FormalEnv)
	}
	if os.Getenv(nativeCatalog15x36PrecheckEnv) == "1" && downloadOnly {
		t.Fatalf("native catalog formal env cannot be combined with precheck; precheck is NON_PASS only")
	}
	if downloadOnly && strings.TrimSpace(os.Getenv(nativeCatalog15x36DownloadRootEnv)) == "" {
		t.Fatalf("download/install-only mode requires %s", nativeCatalog15x36DownloadRootEnv)
	}
	if testing.Short() {
		t.Skip("native catalog formal lifecycle proof is disabled by -short")
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("native catalog formal proof requires windows/arm64, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if err := nativeCatalog15x36ValidateCatalog(); err != nil {
		t.Fatal(err)
	}
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	if host.OS != installer.WindowsHostOSWindows || host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("native catalog proof requires NativeArch=ProcessArch=arm64, got os=%q native=%q process=%q build=%d", host.OS, host.NativeArch, host.ProcessArch, host.WindowsBuild)
	}

	startedAt := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), nativeCatalog15x36FormalTimeout)
	defer cancel()
	repoRoot := realNodeRepoRoot(t)
	servers := nativeCatalog15x36ServerCases()
	cacheOnlyRoot := strings.TrimSpace(os.Getenv(nativeCatalog15x36CacheOnlyRootEnv))
	cacheOnly := cacheOnlyRoot != ""
	downloadRoot := strings.TrimSpace(os.Getenv(nativeCatalog15x36DownloadRootEnv))
	evidenceDir := nativeCatalog15x36EvidenceDirectory(t, repoRoot)
	receiptPath := filepath.Join(evidenceDir, "windows-arm64-process-arm64-native-catalog-15x36-soak.receipt")
	wirePath := filepath.Join(evidenceDir, "windows-arm64-process-arm64-native-catalog-15x36.wire.log")
	if err := nativeCatalog15x36WriteWire(wirePath, "phase=started;status=started;action_total=0;absolute_path_markers=0"); err != nil {
		t.Fatalf("create native catalog wire log: %v", err)
	}
	receiptBase := []string{
		"test=windows-arm64-process-arm64-native-catalog-15x36",
		"formal=true",
		"status=started",
		"native_arch=arm64",
		"process_arch=arm64",
		fmt.Sprintf("windows_version=%s", host.WindowsVersion),
		fmt.Sprintf("windows_build=%d", host.WindowsBuild),
		fmt.Sprintf("expected_languages=%d", len(servers)),
		fmt.Sprintf("expected_actions=%d", nativeCatalog15x36ExpectedActions),
		fmt.Sprintf("manager_idle=%s", nativeCatalog15x36ManagerIdle),
		fmt.Sprintf("formal_idle=%s", nativeCatalog15x36FormalIdle),
		"precheck=not_used",
		fmt.Sprintf("install_mode=%s", func() string {
			if downloadOnly {
				return "download_install"
			}
			if cacheOnly {
				return "cache_only"
			}
			return "cold_install"
		}()),
		"absolute_path_markers=0",
		"acl_win32_5_1314=typed_authorization_required_only;acl_changes=none",
	}
	if err := node17x36WriteReceipt(receiptPath, receiptBase); err != nil {
		t.Fatalf("write native catalog initial receipt: %v", err)
	}
	results := make(map[string]installer.InstallResult, len(servers))
	assets := make(map[string]installer.WindowsLockedAsset, len(servers))
	installLines := make([]string, 0, len(servers))
	statusCounts := make(map[installer.InstallStatus]int)
	var client *mcpLSPBinaryClient
	var tracked map[realMCPProcessKey]realMCPProcessIdentity
	var mcpPID int
	var mcpStart string
	var finalMatrix realMCPMatrixSummary
	var actionLedger []string
	var matrix realMCPMatrixSummary
	var actionsByLanguage map[string][]realMCPActionSpec
	finalized := false
	// 安装阶段也必须留下终态收据；started 不能在 EnsureInstalledDetailed 首错后残留。
	defer func() {
		if finalized {
			return
		}
		lines := append([]string{}, receiptBase...)
		lines = append(lines, "status=NON_PASS", "action_total=0", "runtime_failure=not_proven", "null_result=not_proven", "shutdown_response=false", "exit_sent=false", "zero_residual=not_proven", "failure_phase=provision_or_start")
		for statusName, count := range statusCounts {
			lines = append(lines, fmt.Sprintf("install_status_count.%s=%d", statusName, count))
		}
		lines = append(lines, actionLedger...)
		lines = append(lines, installLines...)
		if sidecarPath, sidecarSHA, sidecarErr := nativeCatalog15x36PersistFailureSidecar(evidenceDir, wirePath); sidecarErr != nil {
			t.Errorf("persist native catalog failure sidecar: %v", sidecarErr)
		} else {
			lines = append(lines,
				"failure_sidecar_relative="+filepath.ToSlash(filepath.Base(sidecarPath)),
				"failure_sidecar_sha256="+sidecarSHA,
			)
		}
		if err := node17x36WriteReceipt(receiptPath, lines); err != nil {
			t.Errorf("write native catalog early failure receipt: %v", err)
		}
		_ = nativeCatalog15x36WriteWire(wirePath, "phase=closed;status=NON_PASS;action_total=0;zero_residual=not_proven")
		finalized = true
	}()

	var productRoot string
	if downloadOnly {
		if !filepath.IsAbs(downloadRoot) {
			t.Fatalf("download/install-only product root must be absolute: %q", downloadRoot)
		}
		productRoot = filepath.Clean(downloadRoot)
		info, statErr := os.Stat(productRoot)
		if statErr != nil || !info.IsDir() {
			t.Fatalf("download/install-only product root is not an existing directory: %v", statErr)
		}
		if err := nativeCatalog15x36RejectReparsePath(productRoot); err != nil {
			t.Fatalf("download/install-only product root reparse check: %v", err)
		}
	} else if cacheOnly {
		if !filepath.IsAbs(cacheOnlyRoot) {
			t.Fatalf("cache-only product root must be absolute: %q", cacheOnlyRoot)
		}
		productRoot = filepath.Clean(cacheOnlyRoot)
		info, statErr := os.Stat(productRoot)
		if statErr != nil || !info.IsDir() {
			t.Fatalf("cache-only product root is not an existing directory: %v", statErr)
		}
		if err := nativeCatalog15x36RejectReparsePath(productRoot); err != nil {
			t.Fatalf("cache-only product root reparse check: %v", err)
		}
	} else {
		productRoot, err = os.MkdirTemp("", "sd-node-production-windows-native-catalog-15x36-")
		if err != nil {
			t.Fatalf("create native catalog product root: %v", err)
		}
		t.Cleanup(func() {
			if err := removeRealWindowsProductRoot(productRoot); err != nil {
				t.Errorf("remove native catalog product root: %v", err)
			}
		})
		if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
			t.Fatalf("restrict native catalog product root: %v", err)
		}
	}
	if downloadOnly {
		if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
			t.Fatalf("restrict download/install-only product root: %v", err)
		}
	}
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", repoRoot)
	t.Setenv("APPDATA", "")
	t.Setenv("MCP_LSP_IDLE_TIMEOUT", nativeCatalog15x36ManagerIdle.String())

	cacheBefore, err := node17x36CacheEntryCount(productRoot)
	if err != nil {
		t.Fatalf("inspect native catalog empty cache: %v", err)
	}
	if downloadOnly && cacheBefore != 0 {
		t.Fatalf("download/install-only product root must be empty: entries=%d", cacheBefore)
	}
	if cacheOnly && cacheBefore == 0 {
		t.Fatalf("cache-only product root has no cache entries")
	}
	if !cacheOnly && cacheBefore != 0 {
		t.Fatalf("native catalog product cache was not empty: entries=%d", cacheBefore)
	}
	previousTransport := httpDefaultTransportForNativeCatalog()
	if previousTransport == nil {
		t.Fatal("native catalog HTTP observation requires a default transport")
	}
	var httpObserver *node17x36HTTPObserver
	var cacheOnlyHTTP *nativeCatalog15x36NoNetworkTransport
	if cacheOnly {
		cacheOnlyHTTP = &nativeCatalog15x36NoNetworkTransport{base: previousTransport}
		setHTTPDefaultTransportForNativeCatalog(cacheOnlyHTTP)
	} else {
		httpObserver = &node17x36HTTPObserver{base: previousTransport}
		setHTTPDefaultTransportForNativeCatalog(httpObserver)
	}
	httpRestored := false
	defer func() {
		if !httpRestored {
			setHTTPDefaultTransportForNativeCatalog(previousTransport)
		}
	}()

	provider := setupInstaller()
	nativeCatalog15x36LogResourceSnapshot(t, wirePath, "install_before", os.Getpid(), productRoot, 0)
	installCtx, installCancel := context.WithTimeout(ctx, nativeCatalog15x36FormalTimeout)
	defer installCancel()
	for _, server := range servers {
		entry, entryErr := installer.WindowsLSPCatalogEntryForLanguage(server.languageID)
		if entryErr != nil {
			t.Fatalf("resolve production catalog entry for %s: %v", server.languageID, entryErr)
		}
		if expected := nativeCatalog15x36ExpectedProducts[server.languageID]; entry.Product != expected {
			t.Fatalf("production catalog mapping for %s=%q, want %q", server.languageID, entry.Product, expected)
		}
		asset, assetErr := installer.WindowsLSPAssetForPlatform(entry.Product, host)
		if assetErr != nil {
			t.Fatalf("select native catalog asset for %s: %v", server.languageID, assetErr)
		}
		if asset.Architecture != installer.WindowsHostArchARM64 || asset.SHA256 == "" || asset.BinaryPath == "" {
			t.Fatalf("native catalog asset for %s is not locked ARM64: architecture=%q sha_empty=%t binary_empty=%t", server.languageID, asset.Architecture, asset.SHA256 == "", asset.BinaryPath == "")
		}
		if cacheOnly {
			if err := nativeCatalog15x36VerifyCachedAsset(productRoot, entry.Product, asset); err != nil {
				t.Fatalf("cache-only ready asset for %s is missing or invalid: %v", server.languageID, err)
			}
		}
		result, installErr := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(installCtx), server.languageID)
		if installErr != nil {
			t.Fatalf("production EnsureInstalledDetailed(%s): %v", server.languageID, installErr)
		}
		if result.Status == installer.InstallStatusInstalledFallback {
			t.Fatalf("production catalog %s used forbidden PATH fallback: status=%s", server.languageID, result.Status)
		}
		if strings.TrimSpace(result.Path) == "" {
			t.Fatalf("production catalog %s returned empty installed path", server.languageID)
		}
		if _, pathErr := installer.WindowsShortProcessPathWithinRoot(productRoot, result.Path); pathErr != nil {
			t.Fatalf("production catalog %s escaped product root: %v", server.languageID, pathErr)
		}
		resolved, resolveErr := installer.ResolveWindowsLSPAssetPath(productRoot, entry.Product)
		if resolveErr != nil {
			t.Fatalf("readonly resolver for %s: %v", server.languageID, resolveErr)
		}
		if filepath.Clean(resolved) != filepath.Clean(result.Path) {
			t.Fatalf("production catalog %s result path does not equal readonly resolver path", server.languageID)
		}
		machine, machineErr := nativeCatalog15x36PEMachine(result.Path)
		if machineErr != nil {
			t.Fatalf("inspect PE for %s: %v", server.languageID, machineErr)
		}
		architecture, normalizeErr := installer.NormalizeWindowsImageFileMachine(machine)
		if normalizeErr != nil || architecture != installer.WindowsHostArchARM64 {
			t.Fatalf("production catalog %s PE machine=0x%04x architecture=%q want ARM64: %v", server.languageID, machine, architecture, normalizeErr)
		}
		results[server.languageID] = result
		assets[server.languageID] = asset
		statusCounts[result.Status]++
		relative, relErr := filepath.Rel(productRoot, result.Path)
		if relErr != nil || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatalf("production catalog %s path containment evidence failed", server.languageID)
		}
		installLines = append(installLines, fmt.Sprintf("install.%s.status=%s;product=%s;relative_path=%s;architecture=%s;version=%s;sha256=%s", server.languageID, result.Status, entry.Product, filepath.ToSlash(relative), asset.Architecture, asset.Version, asset.SHA256))
		activeResponses := int64(0)
		if httpObserver != nil {
			activeResponses = httpObserver.Snapshot().ActiveResponses
		}
		nativeCatalog15x36LogResourceSnapshot(t, wirePath, "install_after_"+server.languageID, os.Getpid(), productRoot, activeResponses)
	}
	setHTTPDefaultTransportForNativeCatalog(previousTransport)
	httpRestored = true
	installHTTP := node17x36HTTPCounts{}
	if httpObserver != nil {
		installHTTP = httpObserver.Snapshot()
	}
	if cacheOnly {
		if cacheOnlyHTTP.Requests.Load() != 0 {
			t.Fatalf("cache-only installation attempted HTTP requests=%d", cacheOnlyHTTP.Requests.Load())
		}
	} else if installHTTP.Requests <= 0 || installHTTP.Attempts != installHTTP.Requests || installHTTP.Responses != installHTTP.Requests || installHTTP.TransportErrors != 0 || installHTTP.FailedResponses != 0 || installHTTP.SuccessfulResponses <= 0 {
		t.Fatalf("native catalog HTTP install observation failed: requests=%d attempts=%d responses=%d transport_errors=%d redirects=%d successes=%d failed=%d", installHTTP.Requests, installHTTP.Attempts, installHTTP.Responses, installHTTP.TransportErrors, installHTTP.RedirectResponses, installHTTP.SuccessfulResponses, installHTTP.FailedResponses)
	}
	cacheAfter, err := node17x36CacheEntryCount(productRoot)
	if err != nil {
		t.Fatalf("inspect native catalog cache after installation: %v", err)
	}
	if err := nativeCatalog15x36WriteWire(wirePath, fmt.Sprintf("phase=installed;status=pass;ensure_installed_calls=%d;cache_before=%d;cache_after=%d;http_requests=%d;http_responses=%d", len(results), cacheBefore, cacheAfter, installHTTP.Requests, installHTTP.Responses)); err != nil {
		t.Fatalf("write native catalog install wire: %v", err)
	}
	if cacheOnly && os.Getenv(nativeCatalog15x36CacheOnlyPrecheckEnv) == "1" {
		setupLines := append([]string{}, receiptBase...)
		setupLines = append(setupLines,
			"status=SETUP_NON_FORMAL",
			fmt.Sprintf("cache_before=%d", cacheBefore),
			fmt.Sprintf("cache_after=%d", cacheAfter),
			"http_requests=0",
			"http_attempts=0",
			"http_responses=0",
			"formal_lifecycle=not_run",
			"action_total=0",
			"zero_residual=not_applicable")
		if err := node17x36WriteReceipt(receiptPath, setupLines); err != nil {
			t.Fatalf("write cache-only setup receipt: %v", err)
		}
		if err := nativeCatalog15x36WriteWire(wirePath, "phase=cache_only_precheck;status=SETUP_NON_FORMAL;formal_lifecycle=not_run;http_requests=0"); err != nil {
			t.Fatalf("write cache-only setup wire: %v", err)
		}
		finalized = true
		t.Logf("native catalog cache-only precheck complete: receipt=%s", receiptPath)
		return
	}
	if downloadOnly {
		setupLines := append([]string{}, receiptBase...)
		setupLines = append(setupLines,
			"status=PASS",
			"proof_kind=download_install",
			"automatic_install=true",
			"empty_before=true",
			fmt.Sprintf("cache_after=%d", cacheAfter),
			fmt.Sprintf("http_requests=%d", installHTTP.Requests),
			fmt.Sprintf("http_attempts=%d", installHTTP.Attempts),
			fmt.Sprintf("http_responses=%d", installHTTP.Responses),
			"formal_lifecycle=not_run",
			"action_total=0",
			fmt.Sprintf("product_root_digest=%x", sha256.Sum256([]byte(productRoot))),
			"zero_residual=not_applicable")
		if err := node17x36WriteReceipt(receiptPath, setupLines); err != nil {
			t.Fatalf("write download/install-only receipt: %v", err)
		}
		if err := nativeCatalog15x36WriteWire(wirePath, fmt.Sprintf("phase=download_install_complete;status=PASS;assets=%d;http_requests=%d;formal_lifecycle=not_run", len(results), installHTTP.Requests)); err != nil {
			t.Fatalf("write download/install-only wire: %v", err)
		}
		finalized = true
		t.Logf("native catalog download/install-only complete: receipt=%s", receiptPath)
		return
	}

	binaryPath := buildRealMcpLSPBinary(t, repoRoot)
	fixtureRoot := t.TempDir()
	registerRealMCPTempRootCleanup(t, fixtureRoot)
	astFile := filepath.Join(fixtureRoot, "ast_fixture.js")
	writeRealFixture(t, astFile, "function nativeCatalogAst(name) { return name; }\nnativeCatalogAst(\"world\");\n")
	client = startRealMcpLSPBinary(t, ctx, binaryPath, fixtureRoot, repoRoot, "", "", productRoot)
	mcpPID = client.cmd.Process.Pid
	mcpStart, err = windowsGoplsProcessStartIdentity(mcpPID)
	if err != nil {
		t.Fatalf("capture MCP PID+start: %v", err)
	}
	nativeCatalog15x36LogResourceSnapshot(t, wirePath, "mcp_started_before_clangd", mcpPID, productRoot, 0)
	tracked = map[realMCPProcessKey]realMCPProcessIdentity{
		{PID: mcpPID, StartToken: mcpStart}: {PID: mcpPID, StartToken: mcpStart, Name: "mcp-lsp", Language: "mcp-lsp"},
	}
	shutdownSent := false
	exitSent := false
	zeroResidual := false
	postIdleCount := 0
	completed := false
	defer func() {
		if client != nil && client.cmd != nil {
			_ = trackRealMCPProcessTree(t, mcpPID, "final-before-close", tracked)
		}
		if client != nil && client.cmd != nil && !shutdownSent {
			shutdownSent = nativeCatalog15x36TryShutdown(client)
		}
		if client != nil && client.cmd != nil {
			exitSent = node17x36CloseWithExitProof(t, client)
		}
		if len(tracked) > 0 {
			zeroResidual = nativeCatalog15x36ProcessIdentitiesGone(tracked)
		}
		status := "NON_PASS"
		if completed && shutdownSent && exitSent && zeroResidual && !t.Failed() {
			status = "PASS"
		}
		lines := append([]string{}, receiptBase...)
		lines = append(lines,
			"status="+status,
			fmt.Sprintf("action_total=%d", finalMatrix.total),
			fmt.Sprintf("success_including_legal_empty=%d", finalMatrix.succeeded),
			fmt.Sprintf("legal_empty=%d", finalMatrix.legalEmpty),
			fmt.Sprintf("capability_unsupported=%d", finalMatrix.capabilityUnsupported),
			fmt.Sprintf("runtime_failure=%s", func() string {
				if finalMatrix.total == nativeCatalog15x36ExpectedActions && !t.Failed() {
					return "0"
				}
				return "not_proven"
			}()),
			fmt.Sprintf("null_result=%s", func() string {
				if finalMatrix.total == nativeCatalog15x36ExpectedActions && !t.Failed() {
					return "0"
				}
				return "not_proven"
			}()),
			fmt.Sprintf("post_idle_non_empty_actions=%d", postIdleCount),
			fmt.Sprintf("shutdown_response=%t", shutdownSent),
			fmt.Sprintf("exit_sent=%t", exitSent),
			fmt.Sprintf("zero_residual=%t", zeroResidual),
			fmt.Sprintf("mcp_pid=%d;mcp_start=%s", mcpPID, mcpStart),
			fmt.Sprintf("wire_relative=%s", filepath.ToSlash(filepath.Base(wirePath))),
			fmt.Sprintf("finished_at=%s", time.Now().UTC().Format(time.RFC3339Nano)),
			fmt.Sprintf("elapsed=%s", time.Since(startedAt).Round(time.Millisecond)),
		)
		for _, server := range servers {
			if asset, ok := assets[server.languageID]; ok {
				lines = append(lines, fmt.Sprintf("asset.%s.product=%s;architecture=%s;version=%s;sha256=%s;binary=%s", server.languageID, nativeCatalog15x36ExpectedProducts[server.languageID], asset.Architecture, asset.Version, asset.SHA256, filepath.ToSlash(asset.BinaryPath)))
			}
		}
		for statusName, count := range statusCounts {
			lines = append(lines, fmt.Sprintf("install_status_count.%s=%d", statusName, count))
		}
		lines = append(lines, installLines...)
		lines = append(lines, node17x36IdentityReceiptLines(tracked)...)
		if err := node17x36WriteReceipt(receiptPath, lines); err != nil {
			t.Errorf("write native catalog final receipt: %v", err)
		}
		_ = nativeCatalog15x36WriteWire(wirePath, fmt.Sprintf("phase=closed;status=%s;shutdown=%t;exit=%t;zero_residual=%t;%s", status, shutdownSent, exitSent, zeroResidual, nativeCatalog15x36ClosedActionAccounting(finalMatrix.total)))
		if status != "PASS" {
			if sidecarPath, sidecarSHA, sidecarErr := nativeCatalog15x36PersistFailureSidecar(evidenceDir, wirePath); sidecarErr != nil {
				t.Errorf("persist native catalog failure sidecar: %v", sidecarErr)
			} else {
				lines = append(lines,
					"failure_sidecar_relative="+filepath.ToSlash(filepath.Base(sidecarPath)),
					"failure_sidecar_sha256="+sidecarSHA,
				)
				if err := node17x36WriteReceipt(receiptPath, lines); err != nil {
					t.Errorf("update native catalog failure sidecar receipt: %v", err)
				}
			}
		}
		finalized = true
	}()

	initialize := client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}})
	if initialize.JSONRPC != "2.0" || initialize.Error != nil {
		t.Fatalf("initialize response was not a valid JSON-RPC result")
	}
	if err := nativeCatalog15x36Notify(client, "notifications/initialized", map[string]any{}); err != nil {
		t.Fatalf("send initialized notification: %v", err)
	}
	requireRealMCPToolFamilies(t, callMcpLSPBinaryRaw(t, client, "tools/list", map[string]any{}))
	if err := nativeCatalog15x36WriteWire(wirePath, "phase=initialized;status=pass;tools=list_verified"); err != nil {
		t.Fatalf("write native catalog initialized wire: %v", err)
	}

	matrix, _, actionsByLanguage, actionLedger = nativeCatalog15x36RunActionMatrix(t, client, mcpPID, fixtureRoot, productRoot, astFile, servers, tracked, wirePath, &finalMatrix)
	finalMatrix = matrix
	if matrix.total != nativeCatalog15x36ExpectedActions || matrix.succeeded+matrix.capabilityUnsupported != matrix.total {
		t.Fatalf("native catalog matrix accounting failed: total=%d success=%d legal_empty=%d capability_unsupported=%d", matrix.total, matrix.succeeded, matrix.legalEmpty, matrix.capabilityUnsupported)
	}
	if len(tracked) <= 1 {
		t.Fatalf("native catalog process tree captured no language-server descendant: tracked=%d", len(tracked))
	}
	node17x36RequireLanguageIdentities(t, tracked, servers)
	preIdleRefreshStarted := time.Now()
	preIdleRefreshCount := 0
	for _, server := range servers {
		action, ok := nativeCatalog15x36PostIdleHover(actionsByLanguage[server.languageID])
		if !ok {
			t.Fatalf("%s has no required pre-idle refresh hover contract", server.languageID)
		}
		response := client.callTool(t, action.tool, realMCPWindowsToolArguments(server.languageID, fixtureRoot, action.tool, action.name, action.args))
		status := requireRealMCPActionResult(t, response, true, "", false, realMCPActionCapabilityKey(action.tool, action.name), realMCPActionProtocolOptional(action.tool, action.name), server.languageID+" pre-idle refresh")
		if status != realMCPActionSucceeded {
			t.Fatalf("%s pre-idle refresh was not a non-empty semantic success: %s", server.languageID, status)
		}
		preIdleRefreshCount++
		if !trackRealMCPProcessTree(t, mcpPID, "native-catalog-pre-idle-refresh-"+server.languageID, tracked) {
			t.Fatalf("%s pre-idle refresh process tree capture failed", server.languageID)
		}
	}
	preIdleRefreshElapsed := time.Since(preIdleRefreshStarted)
	if preIdleRefreshCount != len(servers) {
		t.Fatalf("native catalog pre-idle refresh count=%d, want %d", preIdleRefreshCount, len(servers))
	}
	if preIdleRefreshElapsed >= 2*time.Minute {
		t.Fatalf("native catalog pre-idle refresh exceeded manager idle buffer: elapsed=%s", preIdleRefreshElapsed)
	}
	baselineReport, err := nativeCatalog15x36ActiveProcessBaseline(tracked, mcpPID, len(servers))
	if err != nil {
		t.Fatalf("native catalog establish active process baseline: %v", err)
	}
	if err := nativeCatalog15x36WriteWire(wirePath, fmt.Sprintf("phase=pre_idle_refresh;status=pass;count=%d;elapsed=%s", preIdleRefreshCount, preIdleRefreshElapsed)); err != nil {
		t.Fatalf("write native catalog pre-idle refresh wire: %v", err)
	}
	if err := nativeCatalog15x36WriteWire(wirePath, fmt.Sprintf("phase=actions_complete;status=pass;action_total=%d;success=%d;legal_empty=%d;capability_unsupported=%d;pre_idle_refresh=%d;stable_server_count=%d;ignored_os_helper_count=%d", matrix.total, matrix.succeeded, matrix.legalEmpty, matrix.capabilityUnsupported, preIdleRefreshCount, baselineReport.stableServerCount, baselineReport.ignoredOSHelperCount)); err != nil {
		t.Fatalf("write native catalog action wire: %v", err)
	}

	idleStarted := time.Now()
	idleDeadline := idleStarted.Add(nativeCatalog15x36FormalIdle)
	heartbeats := 0
	for {
		remaining := time.Until(idleDeadline)
		if remaining <= 0 {
			break
		}
		if remaining > time.Minute {
			remaining = time.Minute
		}
		timer := time.NewTimer(remaining)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			t.Fatalf("native catalog idle stopped before %s: %v", nativeCatalog15x36FormalIdle, ctx.Err())
		case <-timer.C:
		}
		if !trackRealMCPProcessTree(t, mcpPID, "native-catalog-idle-heartbeat", tracked) {
			t.Fatalf("native catalog idle process tree capture failed")
		}
		nativeCatalog15x36AssertActiveProcessBaseline(t, baselineReport.keys, tracked, mcpPID, "native-catalog-idle-heartbeat")
		heartbeats++
		if err := nativeCatalog15x36WriteWire(wirePath, fmt.Sprintf("phase=idle;status=heartbeat;elapsed=%s;identities=%d", time.Since(idleStarted).Round(time.Second), len(tracked))); err != nil {
			t.Fatalf("write native catalog idle wire: %v", err)
		}
	}
	idleElapsed := time.Since(idleStarted)
	if idleElapsed < nativeCatalog15x36FormalIdle || nativeCatalog15x36ManagerIdle < nativeCatalog15x36FormalIdle+10*time.Minute {
		t.Fatalf("native catalog lifecycle window invalid: idle=%s manager=%s heartbeats=%d", idleElapsed, nativeCatalog15x36ManagerIdle, heartbeats)
	}

	for _, server := range servers {
		action, ok := nativeCatalog15x36PostIdleHover(actionsByLanguage[server.languageID])
		if !ok {
			t.Fatalf("%s has no required post-idle hover contract", server.languageID)
		}
		response := client.callTool(t, action.tool, realMCPWindowsToolArguments(server.languageID, fixtureRoot, action.tool, action.name, action.args))
		status := requireRealMCPActionResult(t, response, true, "", false, realMCPActionCapabilityKey(action.tool, action.name), realMCPActionProtocolOptional(action.tool, action.name), server.languageID+" post-idle hover")
		if status != realMCPActionSucceeded {
			t.Fatalf("%s post-idle hover was not a non-empty semantic success: %s", server.languageID, status)
		}
		postIdleCount++
		if !trackRealMCPProcessTree(t, mcpPID, "native-catalog-post-idle-"+server.languageID, tracked) {
			t.Fatalf("%s post-idle process tree capture failed", server.languageID)
		}
		nativeCatalog15x36AssertActiveProcessBaseline(t, baselineReport.keys, tracked, mcpPID, "native-catalog-post-idle-"+server.languageID)
	}
	if postIdleCount != len(servers) {
		t.Fatalf("native catalog post-idle hover count=%d, want %d", postIdleCount, len(servers))
	}
	shutdown := client.call(t, "shutdown", map[string]any{})
	if shutdown.JSONRPC != "2.0" || shutdown.Error != nil {
		t.Fatalf("native catalog shutdown did not return a valid JSON-RPC response")
	}
	shutdownSent = true
	completed = true
	for _, line := range actionLedger {
		if err := nativeCatalog15x36WriteWire(wirePath, "ledger="+line); err != nil {
			t.Fatalf("write native catalog action ledger: %v", err)
		}
	}
	if err := nativeCatalog15x36WriteWire(wirePath, fmt.Sprintf("phase=shutdown;status=pass;post_idle=%d;idle_elapsed=%s", postIdleCount, idleElapsed)); err != nil {
		t.Fatalf("write native catalog shutdown wire: %v", err)
	}
}

type nativeCatalog15x36ActiveBaselineReport struct {
	keys                 map[realMCPProcessKey]struct{}
	stableServerCount    int
	ignoredOSHelperCount int
}

// nativeCatalog15x36ActiveProcessBaseline 只把 actions_complete 时仍存活且
// PID+start-token 精确匹配的 MCP 与 product-managed LSP server 纳入 Native
// 稳定基线；tracked 保留完整历史树，供轮换和最终零残留证据使用。
func nativeCatalog15x36ActiveProcessBaseline(tracked map[realMCPProcessKey]realMCPProcessIdentity, mcpPID, requiredServerCount int) (nativeCatalog15x36ActiveBaselineReport, error) {
	return nativeCatalog15x36ActiveProcessBaselineWithProbe(tracked, mcpPID, requiredServerCount, func(key realMCPProcessKey) (bool, string, error) {
		alive, err := processAliveForE2E(key.PID)
		if err != nil || !alive {
			return alive, "", err
		}
		start, err := windowsGoplsProcessStartIdentity(key.PID)
		return true, start, err
	})
}

// nativeCatalog15x36ActiveProcessBaselineWithProbe 为不依赖真实进程的回归测试
// 提供观测 seam；正式路径仍必须使用 Windows processAlive/start-token 查询。
func nativeCatalog15x36ActiveProcessBaselineWithProbe(tracked map[realMCPProcessKey]realMCPProcessIdentity, mcpPID, requiredServerCount int, probe func(realMCPProcessKey) (bool, string, error)) (nativeCatalog15x36ActiveBaselineReport, error) {
	if probe == nil {
		return nativeCatalog15x36ActiveBaselineReport{}, errors.New("native catalog active process probe is nil")
	}
	active := make(map[realMCPProcessKey]struct{}, len(tracked))
	mcpActive := false
	descendantActive := false
	stableServerCount := 0
	ignoredOSHelperCount := 0
	for key := range tracked {
		alive, currentStart, err := probe(key)
		if err != nil {
			return nativeCatalog15x36ActiveBaselineReport{}, fmt.Errorf("probe native catalog PID=%d start=%s: %w", key.PID, key.StartToken, err)
		}
		if !alive || currentStart != key.StartToken {
			continue
		}
		identity := tracked[key]
		if key.PID != mcpPID && !nativeCatalog15x36IsDirectManagedServerIdentity(identity, mcpPID) {
			ignoredOSHelperCount++
			continue
		}
		active[key] = struct{}{}
		if key.PID == mcpPID {
			mcpActive = true
		} else {
			descendantActive = true
			stableServerCount++
		}
	}
	if !mcpActive {
		return nativeCatalog15x36ActiveBaselineReport{}, fmt.Errorf("native catalog active baseline lost MCP PID=%d", mcpPID)
	}
	if !descendantActive {
		return nativeCatalog15x36ActiveBaselineReport{}, errors.New("native catalog active baseline has no live language-server descendant")
	}
	if stableServerCount < requiredServerCount {
		return nativeCatalog15x36ActiveBaselineReport{}, fmt.Errorf("native catalog active baseline stable server count=%d, want at least %d", stableServerCount, requiredServerCount)
	}
	return nativeCatalog15x36ActiveBaselineReport{keys: active, stableServerCount: stableServerCount, ignoredOSHelperCount: ignoredOSHelperCount}, nil
}

// nativeCatalog15x36IsDirectManagedServerIdentity 将所有 MCP 直接子进程纳入
// stable 集合，不依赖可能被 Windows 8.3 短名改写的 executable basename；server
// 的 conhost、terraform companion 等孙进程自然排除，但仍保留在 tracked 历史树中。
func nativeCatalog15x36IsDirectManagedServerIdentity(identity realMCPProcessIdentity, mcpPID int) bool {
	return identity.ParentPID == mcpPID
}

// nativeCatalog15x36AssertActiveProcessBaseline 严格比较当前活动身份集合。
// 已退出的历史身份不进入集合，也不要求为其保留 process handle；但基线中的
// 当前 MCP/语言服务器一旦退出、被复用或新增，idle/post-idle 必须失败。
func nativeCatalog15x36AssertActiveProcessBaseline(t *testing.T, baseline map[realMCPProcessKey]struct{}, tracked map[realMCPProcessKey]realMCPProcessIdentity, mcpPID int, phase string) {
	t.Helper()
	current, err := nativeCatalog15x36ActiveManagedProcessSet(tracked, mcpPID)
	if err != nil {
		t.Fatalf("native catalog %s active process observation: %v", phase, err)
	}
	missing := make([]string, 0)
	added := make([]string, 0)
	for key := range baseline {
		if _, ok := current[key]; !ok {
			identity := tracked[key]
			missing = append(missing, fmt.Sprintf("pid=%d;start=%s;name=%s", key.PID, key.StartToken, identity.Name))
		}
	}
	for key := range current {
		if _, ok := baseline[key]; !ok {
			identity := tracked[key]
			added = append(added, fmt.Sprintf("pid=%d;start=%s;name=%s", key.PID, key.StartToken, identity.Name))
		}
	}
	if len(missing) != 0 || len(added) != 0 {
		sort.Strings(missing)
		sort.Strings(added)
		t.Fatalf("native catalog %s active process baseline changed: missing=%s added=%s", phase, strings.Join(missing, ","), strings.Join(added, ","))
	}
}

func nativeCatalog15x36ActiveManagedProcessSet(tracked map[realMCPProcessKey]realMCPProcessIdentity, mcpPID int) (map[realMCPProcessKey]struct{}, error) {
	return nativeCatalog15x36ActiveManagedProcessSetWithProbe(tracked, mcpPID, func(key realMCPProcessKey) (bool, string, error) {
		alive, err := processAliveForE2E(key.PID)
		if err != nil || !alive {
			return alive, "", err
		}
		start, err := windowsGoplsProcessStartIdentity(key.PID)
		return true, start, err
	})
}

func nativeCatalog15x36ActiveManagedProcessSetWithProbe(tracked map[realMCPProcessKey]realMCPProcessIdentity, mcpPID int, probe func(realMCPProcessKey) (bool, string, error)) (map[realMCPProcessKey]struct{}, error) {
	if probe == nil {
		return nil, errors.New("native catalog active managed process probe is nil")
	}
	active := make(map[realMCPProcessKey]struct{}, len(tracked))
	for key, identity := range tracked {
		alive, currentStart, err := probe(key)
		if err != nil {
			return nil, fmt.Errorf("probe native catalog PID=%d start=%s: %w", key.PID, key.StartToken, err)
		}
		if alive && currentStart == key.StartToken && (key.PID == mcpPID || nativeCatalog15x36IsDirectManagedServerIdentity(identity, mcpPID)) {
			active[key] = struct{}{}
		}
	}
	return active, nil
}

func TestWindowsARM64ProcessARM64NativeCatalogActiveBaseline(t *testing.T) {
	tracked := map[realMCPProcessKey]realMCPProcessIdentity{
		{PID: 10, StartToken: "mcp"}:    {PID: 10, StartToken: "mcp", Language: "mcp-lsp"},
		{PID: 20, StartToken: "con"}:    {PID: 20, ParentPID: 30, StartToken: "con", Name: "conhost.exe", Language: "cpp"},
		{PID: 30, StartToken: "clang"}:  {PID: 30, ParentPID: 10, StartToken: "clang", Name: "rust-analyzer.exe", Language: "rust"},
		{PID: 40, StartToken: "tf"}:     {PID: 40, ParentPID: 10, StartToken: "tf", Name: "TERRAF~1.EXE", Language: "terraform"},
		{PID: 41, StartToken: "helper"}: {PID: 41, ParentPID: 30, StartToken: "helper", Name: "terraform.exe", Language: "rust"},
	}
	state := map[realMCPProcessKey]struct{}{
		{PID: 10, StartToken: "mcp"}:    {},
		{PID: 20, StartToken: "con"}:    {},
		{PID: 30, StartToken: "clang"}:  {},
		{PID: 40, StartToken: "tf"}:     {},
		{PID: 41, StartToken: "helper"}: {},
	}
	probe := func(key realMCPProcessKey) (bool, string, error) {
		if _, ok := state[key]; !ok {
			return false, "", nil
		}
		return true, key.StartToken, nil
	}
	baselineReport, err := nativeCatalog15x36ActiveProcessBaselineWithProbe(tracked, 10, 1, probe)
	if err != nil {
		t.Fatalf("active baseline: %v", err)
	}
	if _, ok := baselineReport.keys[realMCPProcessKey{PID: 20, StartToken: "con"}]; ok {
		t.Fatal("conhost entered active baseline")
	}
	if _, ok := baselineReport.keys[realMCPProcessKey{PID: 40, StartToken: "tf"}]; !ok {
		t.Fatal("direct 8.3 terraform-ls server was excluded from active baseline")
	}
	if baselineReport.stableServerCount != 2 || baselineReport.ignoredOSHelperCount != 2 {
		t.Fatalf("stable/ignored counts=%d/%d, want 2/2", baselineReport.stableServerCount, baselineReport.ignoredOSHelperCount)
	}
	current, err := nativeCatalog15x36ActiveManagedProcessSetWithProbe(tracked, 10, probe)
	if err != nil || len(current) != len(baselineReport.keys) {
		t.Fatalf("active set did not remain stable: current=%v baseline=%v err=%v", current, baselineReport.keys, err)
	}
	tracked[realMCPProcessKey{PID: 50, StartToken: "new"}] = realMCPProcessIdentity{PID: 50, ParentPID: 10, StartToken: "new", Name: "dart.exe", Language: "dart"}
	state[realMCPProcessKey{PID: 50, StartToken: "new"}] = struct{}{}
	current, err = nativeCatalog15x36ActiveManagedProcessSetWithProbe(tracked, 10, probe)
	if err != nil {
		t.Fatalf("active set after rotation: %v", err)
	}
	if len(current) == len(baselineReport.keys) {
		t.Fatal("new active identity was not observable as a baseline change")
	}
}

func TestWindowsARM64ProcessARM64NativeCatalogPreIdleRefreshContracts(t *testing.T) {
	servers := nativeCatalog15x36ServerCases()
	if len(servers) != len(nativeCatalog15x36LanguageIDs) {
		t.Fatalf("server closure=%d, want %d", len(servers), len(nativeCatalog15x36LanguageIDs))
	}
	for _, server := range servers {
		actions := nativeCatalog15x36ActionSpecs(server, realMCPFixture{}, "main.js")
		if action, ok := nativeCatalog15x36PostIdleHover(actions); !ok || !action.requireResult || action.allowCapabilityUnsupported || (action.tool != "inspect" && action.tool != "file") {
			t.Fatalf("%s has no non-empty pre-idle refresh contract", server.languageID)
		}
	}
}

func nativeCatalog15x36ValidateCatalog() error {
	if err := installer.ValidateWindowsLSPCatalog(); err != nil {
		return fmt.Errorf("validate production Windows LSP catalog: %w", err)
	}
	if len(nativeCatalog15x36ExpectedProducts) != len(nativeCatalog15x36LanguageIDs) {
		return fmt.Errorf("native catalog expected product map=%d, want %d", len(nativeCatalog15x36ExpectedProducts), len(nativeCatalog15x36LanguageIDs))
	}
	seen := make(map[string]struct{}, len(nativeCatalog15x36LanguageIDs))
	platform := installer.WindowsHostPlatform{OS: installer.WindowsHostOSWindows, NativeArch: installer.WindowsHostArchARM64, ProcessArch: installer.WindowsHostArchX64, WindowsVersion: "10.0", WindowsBuild: 19041}
	for _, language := range nativeCatalog15x36LanguageIDs {
		if _, duplicate := seen[language]; duplicate {
			return fmt.Errorf("native catalog language %q is duplicated", language)
		}
		seen[language] = struct{}{}
		expected, ok := nativeCatalog15x36ExpectedProducts[language]
		if !ok {
			return fmt.Errorf("native catalog language %q has no expected production product", language)
		}
		entry, err := installer.WindowsLSPCatalogEntryForLanguage(language)
		if err != nil {
			return fmt.Errorf("production catalog language %q: %w", language, err)
		}
		if entry.Product != expected {
			return fmt.Errorf("production catalog language %q maps to %q, want %q", language, entry.Product, expected)
		}
		asset, err := installer.WindowsLSPAssetForPlatform(entry.Product, platform)
		if err != nil {
			return fmt.Errorf("production catalog ARM64 asset %q: %w", language, err)
		}
		if asset.Architecture != installer.WindowsHostArchARM64 || len(asset.SHA256) != 64 || !strings.HasPrefix(strings.ToLower(asset.URL), "https://") || filepath.IsAbs(filepath.FromSlash(asset.BinaryPath)) {
			return fmt.Errorf("production catalog ARM64 asset %q is not locked: architecture=%q sha_len=%d url_https=%t binary_path=%q", language, asset.Architecture, len(asset.SHA256), strings.HasPrefix(strings.ToLower(asset.URL), "https://"), asset.BinaryPath)
		}
	}
	if len(seen) != len(nativeCatalog15x36LanguageIDs) {
		return fmt.Errorf("native catalog language closure=%d, want %d", len(seen), len(nativeCatalog15x36LanguageIDs))
	}
	for _, processArch := range []string{installer.WindowsHostArchARM64, installer.WindowsHostArchX64, installer.WindowsHostArchX86} {
		platform.ProcessArch = processArch
		for product := range nativeCatalog15x36ExpectedProducts {
			entry, err := installer.WindowsLSPCatalogEntryForLanguage(product)
			if err != nil {
				return fmt.Errorf("recheck catalog product language %q: %w", product, err)
			}
			asset, err := installer.WindowsLSPAssetForPlatform(entry.Product, platform)
			if err != nil || asset.Architecture != installer.WindowsHostArchARM64 {
				return fmt.Errorf("ProcessArch changed NativeArch selection for %q: asset=%q err=%v", product, asset.Architecture, err)
			}
		}
	}
	return nil
}

func nativeCatalog15x36ServerCases() []realNodeServerCase {
	return []realNodeServerCase{
		{name: "c", languageID: "c", packageName: "clangd", fileName: "main.c", content: "int native_value = 1;\nint native_function(int value) { return value + native_value; }\nint main(void) { return native_function(1); }\n", line: 2, character: 4},
		{name: "cpp", languageID: "cpp", packageName: "clangd", fileName: "main.cpp", content: "int native_value = 1;\nint native_function(int value) { return value + native_value; }\nint main() { return native_function(1); }\n", line: 2, character: 4},
		{name: "objective-c", languageID: "objective-c", packageName: "clangd", fileName: "main.m", content: "int native_value = 1;\nint native_function(int value) { return value + native_value; }\nint main(void) { return native_function(1); }\n", line: 2, character: 4},
		{name: "objective-cpp", languageID: "objective-cpp", packageName: "clangd", fileName: "main.mm", content: "int native_value = 1;\nint native_function(int value) { return value + native_value; }\nint main() { return native_function(1); }\n", line: 2, character: 4},
		{name: "mq4", languageID: "mq4", packageName: "clangd", fileName: "main.mq4", content: "#property strict\nint native_function(int value) { return value + 1; }\nint OnInit() { return native_function(1); }\n", line: 2, character: 4},
		{name: "mq5", languageID: "mq5", packageName: "clangd", fileName: "main.mq5", content: "#property strict\nint native_function(int value) { return value + 1; }\nint OnInit() { return native_function(1); }\n", line: 2, character: 4},
		{name: "mqh", languageID: "mqh", packageName: "clangd", fileName: "common.mqh", content: "#property strict\nint native_function(int value) { return value + 1; }\n", line: 2, character: 4},
		{name: "mql", languageID: "mql", packageName: "clangd", fileName: "main.mql", content: "#property strict\nint native_function(int value) { return value + 1; }\nint OnInit() { return native_function(1); }\n", line: 2, character: 4},
		{name: "mql4", languageID: "mql4", packageName: "clangd", fileName: "main.mql4", content: "#property strict\nint native_function(int value) { return value + 1; }\nint OnInit() { return native_function(1); }\n", line: 2, character: 4},
		{name: "mql5", languageID: "mql5", packageName: "clangd", fileName: "main.mql5", content: "#property strict\nint native_function(int value) { return value + 1; }\nint OnInit() { return native_function(1); }\n", line: 2, character: 4},
		{name: "proto", languageID: "proto", packageName: "buf", fileName: "service.proto", content: "syntax = \"proto3\";\nmessage NativeMessage { string value = 1; }\nservice NativeService { rpc Get(NativeMessage) returns (NativeMessage); }\n", line: 2, character: 8},
		{name: "kotlin", languageID: "kotlin", packageName: "kotlin-lsp", fileName: "Main.kt", content: "class NativeGreeter {\n    fun greet(value: String): String = value\n}\nfun main() { NativeGreeter().greet(\"world\") }\n", line: 2, character: 8},
		{name: "dart", languageID: "dart", packageName: "dart", fileName: "main.dart", content: "class NativeGreeter {\n  String greet(String value) => value;\n}\nvoid main() { NativeGreeter().greet('world'); }\n", line: 2, character: 9},
		{name: "terraform", languageID: "terraform", packageName: "terraform-ls", fileName: "main.tf", content: "variable \"native_value\" {\n  type = string\n}\noutput \"native_output\" { value = var.native_value }\n", line: 4, character: 33},
		{name: "rust", languageID: "rust", packageName: "rust-analyzer", fileName: "main.rs", content: "fn native_function(value: i32) -> i32 {\n    value + 1\n}\nfn main() { native_function(1); }\n", line: 1, character: 3},
	}
}

// nativeCatalog15x36PrepareClangdFixture 为 MQL 别名提供 clangd 的真实编译任务。
// MQL 文件不是 clangd 原生扩展名；-x c++ 让 clangd 在产品真实启动路径中建立
// compile task。缺少该 bootstrap 时，file/open_file 必须报 runtime failure，不能
// 被测试错误归类为合法空结果或 capability_unsupported。
func nativeCatalog15x36PrepareClangdFixture(t *testing.T, root string, server realNodeServerCase) {
	t.Helper()
	switch server.languageID {
	case "mq4", "mq5", "mqh", "mql", "mql4", "mql5":
		if err := os.WriteFile(filepath.Join(root, "compile_flags.txt"), []byte("-x\nc++\n"), 0o600); err != nil {
			t.Fatalf("write MQL clangd compile flags: %v", err)
		}
	}
}

// nativeCatalog15x36ActionSpecs 复用公共 36-action 清单；native fixture 的 hover
// 位置是稳定真实符号，因此 hover 必须非空，不能因 generic server 名称未收录而降为合法空。
func nativeCatalog15x36ActionSpecs(server realNodeServerCase, fixture realMCPFixture, astFile string) []realMCPActionSpec {
	actions := realMCPActionSpecs(server, fixture, astFile)
	for i := range actions {
		if actions[i].tool == "inspect" && actions[i].name == "hover" {
			actions[i].requireResult = server.languageID != "proto"
			actions[i].emptyResultReason = ""
			if server.languageID == "proto" {
				actions[i].emptyResultReason = nativeCatalog15x36ProtoHoverEmptyReason
			}
			actions[i].allowCapabilityUnsupported = false
			actions[i].contractSet = true
		}
	}
	return actions
}

// nativeCatalog15x36ProtoHoverIsStrictLegalEmpty 只接受 buf 的已知纯文本空结果；
// 任意 ERROR、JSON、结构化字段或不同 HINT 都必须继续失败，不能泛化为空结果。
func nativeCatalog15x36ProtoHoverIsStrictLegalEmpty(response mcpLSPBinaryResponse) bool {
	if response.Result.IsError || len(strings.TrimSpace(string(response.Result.StructuredContent))) != 0 {
		return false
	}
	doc, err := lineprotocol.Parse(response.Result.ContentText())
	if err != nil || doc.Error != nil || doc.Header.Total != 0 || doc.Header.Showing != 0 || doc.Header.Truncated || doc.Header.Unit != "hover" || len(doc.Records) != 1 {
		return false
	}
	record := doc.Records[0]
	return record.Kind == "HINT" && record.Value == "no hover info available" && len(record.Fields) == 0
}

func TestNativeCatalog15x36ProtoHoverStrictLegalEmpty(t *testing.T) {
	valid := mcpLSPBinaryResponse{}
	if err := json.Unmarshal([]byte(`{"result":{"content":[{"type":"text","text":"OK total=0 showing=0 truncated=0 unit=hover\nHINT\tno hover info available"}],"isError":false}}`), &valid); err != nil {
		t.Fatal(err)
	}
	if !nativeCatalog15x36ProtoHoverIsStrictLegalEmpty(valid) {
		t.Fatal("exact proto hover HINT was not accepted")
	}
	for _, raw := range []string{
		`{"result":{"content":[{"type":"text","text":"OK total=0 showing=0 truncated=0 unit=hover\nHINT\tother"}],"isError":false}}`,
		`{"result":{"content":[{"type":"text","text":"ERROR code=lsp_timeout retryable=0\nMESSAGE\ttimeout"}],"isError":false}}`,
		`{"result":{"content":[{"type":"text","text":"OK total=0 showing=0 truncated=0 unit=hover\nHINT\tno hover info available"}],"structuredContent":{"total":0},"isError":false}}`,
	} {
		var response mcpLSPBinaryResponse
		if err := json.Unmarshal([]byte(raw), &response); err != nil {
			t.Fatal(err)
		}
		if nativeCatalog15x36ProtoHoverIsStrictLegalEmpty(response) {
			t.Fatalf("invalid proto hover response accepted: %s", raw)
		}
	}
}

func nativeCatalog15x36PostIdleHover(actions []realMCPActionSpec) (realMCPActionSpec, bool) {
	for _, action := range actions {
		if action.tool == "inspect" && action.name == "hover" && action.requireResult && !action.allowCapabilityUnsupported {
			return action, true
		}
	}
	// proto/buf 的 hover 合同允许严格白名单空结果；post-idle 仍必须验证真实非空语义，
	// 因此改用同一 fixture 的必需 open_file，而不是把 legal_empty 冒充 semantic success。
	for _, action := range actions {
		if action.tool == "file" && action.name == "open_file" && action.requireResult && !action.allowCapabilityUnsupported {
			return action, true
		}
	}
	return realMCPActionSpec{}, false
}

func nativeCatalog15x36RunActionMatrix(t *testing.T, client *mcpLSPBinaryClient, mcpPID int, fixtureRoot, productRoot, astFile string, servers []realNodeServerCase, tracked map[realMCPProcessKey]realMCPProcessIdentity, wirePath string, progress *realMCPMatrixSummary) (realMCPMatrixSummary, map[string]realMCPFixture, map[string][]realMCPActionSpec, []string) {
	t.Helper()
	var matrix realMCPMatrixSummary
	fixtures := make(map[string]realMCPFixture, len(servers))
	actionsByLanguage := make(map[string][]realMCPActionSpec, len(servers))
	ledger := make([]string, 0, len(servers))
	for _, server := range servers {
		fixture := writeRealMCPLanguageFixture(t, fixtureRoot, server)
		nativeCatalog15x36PrepareClangdFixture(t, fixtureRoot, server)
		actions := nativeCatalog15x36ActionSpecs(server, fixture, astFile)
		if err := validateRealMCPActionClosure(actions); err != nil {
			t.Fatalf("%s native action closure: %v", server.languageID, err)
		}
		fixtures[server.languageID] = fixture
		actionsByLanguage[server.languageID] = actions
		var languageSummary realMCPMatrixSummary
		for index, action := range actions {
			ordinal := index + 1
			started := time.Now()
			if server.languageID == "c" && ordinal == 1 {
				nativeCatalog15x36LogResourceSnapshot(t, wirePath, "before_first_clangd_action", mcpPID, productRoot, 0)
			}
			if err := nativeCatalog15x36WriteWire(wirePath, fmt.Sprintf("phase=action_start;language=%s;ordinal=%d/%d;tool=%s;name=%s;require_non_empty=%t;allow_capability_unsupported=%t", server.languageID, ordinal, realMCPExpectedActionCount, action.tool, action.name, action.requireResult, action.allowCapabilityUnsupported)); err != nil {
				t.Fatalf("write native action start wire: %v", err)
			}
			if action.tool == "patch_edit" {
				path, _ := action.args["file_path"].(string)
				if path == "" {
					path = realMCPPositionPath(fmt.Sprint(action.args["pos"]))
				}
				if path == "" {
					t.Fatalf("%s patch action %s has no target path", server.languageID, action.name)
				}
				opened := client.callTool(t, "file", realMCPWindowsToolArguments(server.languageID, fixtureRoot, "file", "open_file", map[string]any{"action": "open_file", "file_path": path}))
				requireRealMCPActionResult(t, opened, true, "", false, "", false, server.languageID+" patch target")
			}
			response := client.callTool(t, action.tool, realMCPWindowsToolArguments(server.languageID, fixtureRoot, action.tool, action.name, action.args))
			if server.languageID == "proto" && action.tool == "inspect" && action.name == "hover" && !nativeCatalog15x36ProtoHoverIsStrictLegalEmpty(response) && strings.Contains(response.Result.ContentText(), "OK total=0") {
				t.Fatalf("proto hover empty response violated strict legal-empty contract: %q", response.Result.ContentText())
			}
			status := requireRealMCPActionResult(t, response, action.requireResult, action.emptyResultReason, action.allowCapabilityUnsupported, realMCPActionCapabilityKey(action.tool, action.name), realMCPActionProtocolOptional(action.tool, action.name), server.languageID+" "+action.tool+"/"+action.name)
			if action.tool == "patch_edit" && action.name == "replace_range" && status != realMCPActionUnsupported {
				assertRealFileContains(t, fixture.replaceFile, "REAL_MCP_REPLACED", server.languageID+" replace_range")
			}
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
				t.Fatalf("%s/%s returned unclassified action status %q", action.tool, action.name, status)
			}
			if err := nativeCatalog15x36WriteWire(wirePath, fmt.Sprintf("phase=action_done;language=%s;ordinal=%d/%d;tool=%s;name=%s;duration=%s;status=%s", server.languageID, ordinal, realMCPExpectedActionCount, action.tool, action.name, time.Since(started).Round(time.Millisecond), status)); err != nil {
				t.Fatalf("write native action completion wire: %v", err)
			}
			if progress != nil {
				nativeCatalog15x36RecordAction(progress, status)
			}
		}
		if languageSummary.total != realMCPExpectedActionCount || languageSummary.succeeded+languageSummary.capabilityUnsupported != languageSummary.total {
			t.Fatalf("%s action accounting total=%d success=%d legal_empty=%d capability_unsupported=%d", server.languageID, languageSummary.total, languageSummary.succeeded, languageSummary.legalEmpty, languageSummary.capabilityUnsupported)
		}
		matrix.total += languageSummary.total
		matrix.succeeded += languageSummary.succeeded
		matrix.legalEmpty += languageSummary.legalEmpty
		matrix.capabilityUnsupported += languageSummary.capabilityUnsupported
		matrix.unsupportedActions = append(matrix.unsupportedActions, languageSummary.unsupportedActions...)
		ledger = append(ledger, fmt.Sprintf("language.%s.total=%d;success_including_legal_empty=%d;legal_empty=%d;semantic_success=%d;capability_unsupported=%d;runtime_failure=0;null_result=0;unsupported_actions=%s", server.languageID, languageSummary.total, languageSummary.succeeded, languageSummary.legalEmpty, languageSummary.succeeded-languageSummary.legalEmpty, languageSummary.capabilityUnsupported, strings.Join(languageSummary.unsupportedActions, ",")))
		if !trackRealMCPProcessTree(t, mcpPID, server.languageID, tracked) {
			t.Fatalf("%s process tree capture failed", server.languageID)
		}
	}
	return matrix, fixtures, actionsByLanguage, ledger
}

// nativeCatalog15x36RecordAction 在 action_done 已持久化后更新增量账本，保证失败收据不丢失已完成动作。
func nativeCatalog15x36RecordAction(progress *realMCPMatrixSummary, status realMCPActionStatus) {
	if progress == nil {
		return
	}
	progress.total++
	switch status {
	case realMCPActionSucceeded:
		progress.succeeded++
	case realMCPActionLegalEmpty:
		progress.succeeded++
		progress.legalEmpty++
	case realMCPActionUnsupported:
		progress.capabilityUnsupported++
	}
}

func TestNativeCatalog15x36RecordActionCountsCompletedActions(t *testing.T) {
	var progress realMCPMatrixSummary
	for _, status := range []realMCPActionStatus{
		realMCPActionSucceeded,
		realMCPActionLegalEmpty,
		realMCPActionUnsupported,
	} {
		nativeCatalog15x36RecordAction(&progress, status)
	}
	if progress.total != 3 || progress.succeeded != 2 || progress.legalEmpty != 1 || progress.capabilityUnsupported != 1 {
		t.Fatalf("incremental action accounting=%+v", progress)
	}
}

func TestNativeCatalog15x36ClosedWireSeparatesCompletedAndExpectedActions(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		completed int
	}{
		{name: "before_actions", completed: 0},
		{name: "partial_actions", completed: 17},
		{name: "all_actions", completed: nativeCatalog15x36ExpectedActions},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			line := nativeCatalog15x36ClosedActionAccounting(testCase.completed)
			want := fmt.Sprintf("action_total=%d", testCase.completed)
			if !strings.Contains(line, want) || !strings.Contains(line, fmt.Sprintf("expected_actions=%d", nativeCatalog15x36ExpectedActions)) {
				t.Fatalf("closed action accounting line=%q", line)
			}
		})
	}
}

func nativeCatalog15x36ClosedActionAccounting(completed int) string {
	return fmt.Sprintf("action_total=%d;expected_actions=%d", completed, nativeCatalog15x36ExpectedActions)
}

func nativeCatalog15x36EvidenceDirectory(t *testing.T, repoRoot string) string {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv(nativeCatalog15x36EvidenceEnv))
	if dir == "" {
		dir = filepath.Join(repoRoot, ".build-cache", "lsp-test-results", "windows-arm64-process-arm64-native-catalog-15x36")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create native catalog evidence directory: %v", err)
	}
	return dir
}

// nativeCatalog15x36NoNetworkTransport 将 cache-only 正确性入口变成 fail-fast：任何下载尝试都直接失败。
type nativeCatalog15x36NoNetworkTransport struct {
	base     http.RoundTripper
	Requests atomic.Int64
}

func (t *nativeCatalog15x36NoNetworkTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	if t == nil || t.base == nil {
		return nil, errors.New("native catalog cache-only transport is unavailable")
	}
	t.Requests.Add(1)
	return nil, errors.New("native catalog cache-only mode forbids network access")
}

func nativeCatalog15x36RejectReparsePath(root string) error {
	current := filepath.Clean(root)
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("cache-only product root contains symlink: %s", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

// nativeCatalog15x36VerifyCachedAsset 在任何 cache-only 安装调用前确认 ready marker、锁定路径和 ARM64 PE。
func nativeCatalog15x36VerifyCachedAsset(productRoot string, product installer.WindowsLSPProduct, asset installer.WindowsLockedAsset) error {
	resolved, err := installer.ResolveWindowsLSPAssetPath(productRoot, product)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(resolved); err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("resolved binary is not a regular non-symlink file: %v", err)
	}
	if err := nativeCatalog15x36RejectReparsePath(resolved); err != nil {
		return err
	}
	machine, err := nativeCatalog15x36PEMachine(resolved)
	if err != nil {
		return err
	}
	architecture, err := installer.NormalizeWindowsImageFileMachine(machine)
	if err != nil || architecture != installer.WindowsHostArchARM64 {
		return fmt.Errorf("PE architecture=%q machine=0x%04x, want arm64: %v", architecture, machine, err)
	}
	if asset.Architecture != installer.WindowsHostArchARM64 {
		return fmt.Errorf("locked asset architecture=%q, want arm64", asset.Architecture)
	}
	normalized := filepath.ToSlash(filepath.Clean(resolved))
	if !strings.Contains(normalized, "/"+asset.SHA256+"/") || !strings.Contains(normalized, "/"+asset.Version+"/") {
		return fmt.Errorf("resolved path does not carry locked version/hash")
	}
	return nil
}

// nativeCatalog15x36TryShutdown 尝试在失败清理路径发送一次合法 shutdown；它不调用 testing.Fatal，避免遮蔽原始失败。
func nativeCatalog15x36TryShutdown(client *mcpLSPBinaryClient) bool {
	if client == nil || client.cmd == nil || client.stdin == nil || client.stdout == nil {
		return false
	}
	request, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": "shutdown", "params": map[string]any{}})
	if err != nil {
		return false
	}
	if _, err := client.stdin.Write(append(request, '\n')); err != nil {
		return false
	}
	responseCh := make(chan bool, 1)
	go func() {
		line, readErr := client.stdout.ReadBytes('\n')
		if readErr != nil {
			responseCh <- false
			return
		}
		var response mcpLSPBinaryResponse
		if json.Unmarshal(line, &response) != nil || response.JSONRPC != "2.0" || response.Error != nil {
			responseCh <- false
			return
		}
		responseCh <- true
	}()
	select {
	case ok := <-responseCh:
		return ok
	case <-time.After(5 * time.Second):
		return false
	}
}

// nativeCatalog15x36ProcessIdentitiesGone 独立计算精确 PID/start 残留，不受 testing.T 既有失败状态影响。
func nativeCatalog15x36ProcessIdentitiesGone(tracked map[realMCPProcessKey]realMCPProcessIdentity) bool {
	defer closeRealMCPProcessHandles(tracked)
	if runtime.GOOS != "windows" {
		return true
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		remaining := 0
		for key := range tracked {
			alive, err := processAliveForE2E(key.PID)
			if err != nil || !alive {
				continue
			}
			current, err := windowsGoplsProcessStartIdentity(key.PID)
			if err == nil && current != key.StartToken {
				continue
			}
			remaining++
		}
		if remaining == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func nativeCatalog15x36WriteWire(path, line string) error {
	if strings.TrimSpace(line) == "" {
		return errors.New("native catalog wire line is empty")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.ReplaceAll(strings.ReplaceAll(line, "\r", "_"), "\n", "_") + "\n")
	return err
}

func nativeCatalog15x36Notify(client *mcpLSPBinaryClient, method string, params map[string]any) error {
	if client == nil || client.cmd == nil || client.stdin == nil {
		return errors.New("native catalog MCP client is not live")
	}
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return fmt.Errorf("marshal %s notification: %w", method, err)
	}
	_, err = client.stdin.Write(append(payload, '\n'))
	return err
}

func nativeCatalog15x36PEMachine(path string) (uint16, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var mz [2]byte
	if _, err := io.ReadFull(f, mz[:]); err != nil {
		return 0, err
	}
	if string(mz[:]) != "MZ" {
		return 0, errors.New("file is not a PE image")
	}
	if _, err := f.Seek(0x3c, io.SeekStart); err != nil {
		return 0, err
	}
	var offset uint32
	if err := binary.Read(f, binary.LittleEndian, &offset); err != nil {
		return 0, err
	}
	if _, err := f.Seek(int64(offset), io.SeekStart); err != nil {
		return 0, err
	}
	var signature [4]byte
	if _, err := io.ReadFull(f, signature[:]); err != nil {
		return 0, err
	}
	if string(signature[:]) != "PE\x00\x00" {
		return 0, errors.New("invalid PE signature")
	}
	var machine uint16
	if err := binary.Read(f, binary.LittleEndian, &machine); err != nil {
		return 0, err
	}
	return machine, nil
}

// 这些小适配只让本文件观察 http.DefaultTransport；生产安装路径本身仍由 setupInstaller 控制。
// 使用函数而不是直接引入额外平台状态，避免把 catalog E2E 的观察逻辑泄漏到生产装配。
func httpDefaultTransportForNativeCatalog() http.RoundTripper {
	return http.DefaultTransport
}

func setHTTPDefaultTransportForNativeCatalog(value http.RoundTripper) {
	http.DefaultTransport = value
}
