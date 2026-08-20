//go:build windows && arm64 && e2e

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	windowsARM64ProcessARM64Ruby36FormalEnv   = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_RUBY_36_E2E"
	windowsARM64ProcessARM64Ruby36PrecheckEnv = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_RUBY_36_PRECHECK"
	windowsARM64ProcessARM64Ruby36EvidenceEnv = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_RUBY_36_EVIDENCE_DIR"
	windowsARM64ProcessARM64Ruby36FormalIdle  = 15 * time.Minute
	windowsARM64ProcessARM64Ruby36ManagerIdle = 17 * time.Minute
	windowsARM64ProcessARM64Ruby36Timeout     = 45 * time.Minute
	windowsARM64ProcessARM64Ruby36Actions     = realMCPExpectedActionCount
	windowsARM64ProcessARM64RubyLSPShortEnv   = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_RUBY_LSP_SHORT_STDIO"
)

// windowsARM64ProcessARM64Ruby36ServerCase 是自然 Ruby fixture；生产 catalog
// 使用固定 RubyInstaller ARM64 与 Ruby LSP 0.26.10，禁止其他架构或服务器替代。
func windowsARM64ProcessARM64Ruby36ServerCase() realNodeServerCase {
	return realNodeServerCase{
		name:                 "ruby",
		languageID:           "ruby",
		packageName:          "ruby-lsp",
		fileName:             "lib/rake/application.rb",
		line:                 20,
		character:            8,
		sourceDir:            "ruby",
		sourceFile:           "lib/rake/application.rb",
		sourceSecondaryFile:  "lib/rake.rb",
		sourceIdentifier:     "Application",
		sourceWorkspaceQuery: "Application",
		sourceLine:           20,
		sourceCharacter:      8,
	}
}

// windowsARM64ProcessARM64Ruby36ActionSpecs 复用公共七族 36-action 合同，并把
// Ruby 的真实 greet 符号固定为非空 hover；legal_empty 与 capability_unsupported
// 仍由运行时响应和能力快照分别裁决，不能由 fixture 预先伪造成功。
func windowsARM64ProcessARM64Ruby36ActionSpecs(server realNodeServerCase, fixture realMCPFixture, astFile string) []realMCPActionSpec {
	actions := realMCPActionSpecs(server, fixture, astFile)
	for i := range actions {
		if actions[i].tool == "inspect" && actions[i].name == "hover" {
			actions[i].requireResult = true
			actions[i].emptyResultReason = ""
			actions[i].allowCapabilityUnsupported = false
			actions[i].contractSet = true
		}
	}
	return actions
}

// TestWindowsARM64ProcessARM64Ruby36CatalogContract 是无网守卫：只接受固定
// RubyInstaller ARM64 与 Ruby LSP gems，拒绝 x64/x86 fallback。
func TestWindowsARM64ProcessARM64Ruby36CatalogContract(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("Ruby ARM64 catalog contract requires windows/arm64, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if err := installer.ValidateWindowsRuntimeDependencyCatalog(); err != nil {
		t.Fatalf("validate Windows runtime dependency catalog: %v", err)
	}
	entry, err := installer.WindowsRuntimeDependencyCatalogEntryForLanguage("ruby")
	if err != nil {
		t.Fatalf("resolve production Ruby catalog entry: %v", err)
	}
	if entry.Product != installer.WindowsRuntimeDependencyProductRubyLSP {
		t.Fatalf("Ruby catalog product=%q, want %q", entry.Product, installer.WindowsRuntimeDependencyProductRubyLSP)
	}
	assets, planErr := installer.WindowsRuntimeDependencyAssetsForArchitecture(entry.Product, installer.WindowsHostArchARM64)
	if planErr != nil {
		t.Fatalf("Ruby ARM64 plan must be installable after the fixed closure is locked, got %v", planErr)
	}
	if len(assets) != 3 {
		t.Fatalf("Ruby ARM64 locked asset count=%d, want runtime plus two fixed gems", len(assets))
	}
	var rubyAsset, serverAsset *installer.WindowsRuntimeDependencyAsset
	for i := range assets {
		asset := &assets[i]
		if asset.Architecture != installer.WindowsHostArchARM64 {
			t.Fatalf("Ruby ARM64 asset %q has architecture=%q; cross-architecture fallback is forbidden", asset.Component, asset.Architecture)
		}
		switch asset.Component {
		case "ruby":
			rubyAsset = asset
		case "ruby-lsp":
			serverAsset = asset
		}
	}
	if rubyAsset == nil || !rubyAsset.Native || !strings.HasSuffix(strings.ToLower(rubyAsset.URL), "rubyinstaller-4.0.5-1-arm.7z") || rubyAsset.Checksum != "c7c6bcd0b070bf7c2e0c03e70fb9754d022b8a216ebc4befab880874c6180b51" {
		t.Fatalf("Ruby ARM64 runtime asset is not the locked official ARM64 archive: %#v", rubyAsset)
	}
	if serverAsset == nil || serverAsset.Native || serverAsset.Checksum != "e67284af94423531f6b9a583350596421b5a6a4dd93083f1c2ba03da7c23bbed" {
		t.Fatalf("Ruby LSP server asset is not the locked gem payload: %#v", serverAsset)
	}
	if _, err := installer.WindowsRuntimeDependencyPlanForArchitecture(entry.Product, installer.WindowsHostArchARM64); err != nil {
		t.Fatalf("Ruby ARM64 plan unexpectedly failed after closure lock: %v", err)
	}

	server := windowsARM64ProcessARM64Ruby36ServerCase()
	if server.sourceDir != "ruby" || server.sourceFile != "lib/rake/application.rb" || server.sourceSecondaryFile != "lib/rake.rb" || server.sourceIdentifier != "Application" || server.sourceWorkspaceQuery != "Application" || server.sourceLine != 20 || server.sourceCharacter != 8 {
		t.Fatalf("Ruby semantic fixture source mapping is not locked to bin/LSP/test/ruby: %#v", server)
	}
	fixtureRoot := t.TempDir()
	fixture := writeRealMCPLanguageFixture(t, fixtureRoot, server)
	requireRealMCPFixturePositions(t, fixture, server)
	if !realMCPPathWithinRoot(fixtureRoot, fixture.workDir) || !realMCPPathWithinRoot(fixture.workDir, fixture.targetFile) || !realMCPPathWithinRoot(fixture.workDir, fixture.secondaryFile) {
		t.Fatalf("Ruby fixture escaped isolated workspace: root=%q work_dir=%q target=%q secondary=%q", fixtureRoot, fixture.workDir, fixture.targetFile, fixture.secondaryFile)
	}
	if filepath.Clean(fixture.sourcePath) == filepath.Clean(fixture.targetFile) || filepath.Clean(fixture.sourceSecondaryPath) == filepath.Clean(fixture.secondaryFile) {
		t.Fatalf("Ruby fixture aliases checked-in source: source=%q target=%q secondary_source=%q secondary=%q", fixture.sourcePath, fixture.targetFile, fixture.sourceSecondaryPath, fixture.secondaryFile)
	}
	astFile := filepath.Join(fixture.workDir, ".mcp-ast", "ast_fixture.js")
	copyRealMCPBinSourceFile(t, filepath.Join(realNodeRepoRoot(t), "bin", "LSP", "test"), "javascript/module-examples/top-level-await/main.js", astFile)
	if !realMCPPathWithinRoot(fixture.workDir, astFile) {
		t.Fatalf("Ruby ast_search fixture escaped isolated workspace: %q", astFile)
	}
	actions := windowsARM64ProcessARM64Ruby36ActionSpecs(server, fixture, astFile)
	if err := validateRealMCPActionClosure(actions); err != nil {
		t.Fatalf("Ruby 36-action closure: %v", err)
	}
	if len(actions) != windowsARM64ProcessARM64Ruby36Actions {
		t.Fatalf("Ruby action count=%d, want %d", len(actions), windowsARM64ProcessARM64Ruby36Actions)
	}
	familyCounts := map[string]int{}
	for _, action := range actions {
		familyCounts[action.tool]++
		if action.tool == "inspect" && action.name == "hover" && (!action.requireResult || action.allowCapabilityUnsupported) {
			t.Fatalf("Ruby hover contract must require non-empty semantic success: %#v", action)
		}
	}
	wantFamilies := map[string]int{"file": 8, "inspect": 5, "xref": 7, "grep": 6, "structure": 5, "patch_edit": 4, "completion": 1}
	if len(familyCounts) != len(wantFamilies) {
		t.Fatalf("Ruby public tool family count=%d, want %d: %#v", len(familyCounts), len(wantFamilies), familyCounts)
	}
	for family, want := range wantFamilies {
		if familyCounts[family] != want {
			t.Fatalf("Ruby %s action count=%d, want %d", family, familyCounts[family], want)
		}
	}
	for _, action := range actions {
		for _, key := range []string{"file_path", "pos"} {
			value, ok := action.args[key].(string)
			if !ok || strings.TrimSpace(value) == "" {
				continue
			}
			path := value
			if key == "pos" {
				path = realMCPPositionPath(value)
			}
			if path != "" && !realMCPPathWithinRoot(fixture.workDir, path) {
				t.Fatalf("Ruby %s/%s path escaped isolated workspace: %s=%q", action.tool, action.name, key, path)
			}
		}
	}
}

// TestWindowsARM64ProcessARM64Ruby36ProductionRegistrationContract 验证正式 Ruby
// 注册选择 Ruby LSP product 与绝对 Ruby runtime 路径；联网安装由正式入口另行证明。
func TestWindowsARM64ProcessARM64Ruby36ProductionRegistrationContract(t *testing.T) {
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("Ruby production registration requires windows/arm64, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	entry, err := installer.WindowsRuntimeDependencyCatalogEntryForLanguage("ruby")
	if err != nil {
		t.Fatalf("resolve production Ruby registration: %v", err)
	}
	if entry.Product != installer.WindowsRuntimeDependencyProductRubyLSP {
		t.Fatalf("production Ruby registration product=%q, want %q", entry.Product, installer.WindowsRuntimeDependencyProductRubyLSP)
	}
	if _, err := installer.WindowsRuntimeDependencyPlanForArchitecture(entry.Product, installer.WindowsHostArchARM64); err != nil {
		t.Fatalf("production Ruby ARM64 registration is not installable: %v", err)
	}
}

// TestWindowsARM64ProcessARM64RubyLSPAutoInstallShortStdioE2E 只验证一次生产
// EnsureInstalledDetailed、固定 Ruby LSP stdio initialize/非空 documentSymbol、
// shutdown/exit 与 PID+start 零残留；它不是 36-action 或 15 分钟生命周期证明。
func TestWindowsARM64ProcessARM64RubyLSPAutoInstallShortStdioE2E(t *testing.T) {
	if os.Getenv(windowsARM64ProcessARM64RubyLSPShortEnv) != "1" {
		t.Skipf("set %s=1 to run the bounded Ruby LSP auto-install stdio proof", windowsARM64ProcessARM64RubyLSPShortEnv)
	}
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	if host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("Ruby LSP short proof requires NativeArch=ProcessArch=arm64, got native=%q process=%q", host.NativeArch, host.ProcessArch)
	}
	productRoot := windowsRubyProductionProductRoot(t)
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict Ruby product root: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("APPDATA", "")
	t.Setenv("LOCALAPPDATA", "")
	previousTransport := http.DefaultTransport
	httpObserver := &node17x36HTTPObserver{base: previousTransport}
	http.DefaultTransport = httpObserver
	defer func() { http.DefaultTransport = previousTransport }()
	provider, err := setupInstallerWithError()
	if err != nil {
		t.Fatalf("construct production Ruby installer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	installResult, err := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), "ruby")
	if err != nil {
		t.Fatalf("production EnsureInstalledDetailed(ruby): %v", err)
	}
	cacheRoot := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir)
	resolved, err := installer.ResolveWindowsRuntimeDependency(installer.WindowsRuntimeDependencyProductRubyLSP, cacheRoot)
	if err != nil {
		t.Fatalf("resolve ready Ruby LSP cohort: %v", err)
	}
	if !strings.EqualFold(filepath.Clean(installResult.Path), filepath.Clean(resolved.ExecutablePath)) {
		t.Fatalf("production launch path=%q differs from ready Ruby executable=%q", filepath.Base(installResult.Path), filepath.Base(resolved.ExecutablePath))
	}
	processArgs, err := installer.WindowsRubyLSPProcessLaunchArguments(resolved.RootPath)
	if err != nil {
		t.Fatalf("resolve short Ruby LSP launch arguments: %v", err)
	}
	processEnv, err := installer.WindowsRubyLSPProcessEnvironment(resolved.RootPath)
	if err != nil {
		t.Fatalf("resolve short Ruby LSP environment: %v", err)
	}
	if len(processArgs) == 0 || len(processEnv) == 0 {
		t.Fatalf("ready Ruby LSP result did not carry explicit args/env")
	}
	processExecutable, err := installer.WindowsShortProcessPathWithinRoot(resolved.RootPath, resolved.ExecutablePath)
	if err != nil {
		t.Fatalf("resolve short Ruby LSP executable: %v", err)
	}
	installHTTP := httpObserver.Snapshot()
	if installHTTP.Requests == 0 || installHTTP.TransportErrors != 0 || installHTTP.FailedResponses != 0 {
		t.Fatalf("Ruby install HTTP proof failed: requests=%d transport_errors=%d failed_responses=%d", installHTTP.Requests, installHTTP.TransportErrors, installHTTP.FailedResponses)
	}

	fixtureRoot := t.TempDir()
	fixture := filepath.Join(fixtureRoot, "main.rb")
	content := "class RubyGreeter\n  def greet(name)\n    \"hello #{name}\"\n  end\nend\n"
	if err := os.WriteFile(fixture, []byte(content), 0o600); err != nil {
		t.Fatalf("write Ruby stdio fixture: %v", err)
	}
	// 真实启动工作目录就是 hostile workspace；任何 RubyGems 向上扫描 Gemfile
	// 或 gem.deps.rb 都会立刻抛出唯一哨兵，握手成功才证明私有环境隔离生效。
	for name, content := range map[string]string{
		"Gemfile":     "raise \"HOSTILE_GEMFILE_EXECUTED\"\n",
		"gem.deps.rb": "raise \"HOSTILE_GEMDEPS_EXECUTED\"\n",
	} {
		if err := os.WriteFile(filepath.Join(fixtureRoot, name), []byte(content), 0o600); err != nil {
			t.Fatalf("write hostile Ruby workspace %s: %v", name, err)
		}
	}
	t.Cleanup(func() {
		for _, name := range []string{"Gemfile", "gem.deps.rb"} {
			_ = os.Remove(filepath.Join(fixtureRoot, name))
		}
	})
	cmd := exec.CommandContext(ctx, processExecutable, processArgs...)
	cmd.Dir = fixtureRoot
	cmd.Env = windowsARM64ProcessARM64RubyMergeEnvironment(os.Environ(), processEnv)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("Ruby stdio stdin: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Ruby stdio stdout: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("Ruby stdio stderr: %v", err)
	}
	client := &realLSPClient{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: &realNodeBuffer{}, documents: map[string]string{realFileURI(fixture): content}}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start product Ruby LSP: %v", err)
	}
	go func() { _, _ = io.Copy(client.stderr, stderr) }()
	pid := cmd.Process.Pid
	startToken, err := windowsGoplsProcessStartIdentity(pid)
	if err != nil {
		t.Fatalf("capture Ruby LSP PID+start: %v", err)
	}
	shutdownOK := false
	exitSent := false
	closed := false
	defer func() {
		if !closed {
			if !exitSent {
				_ = client.notify("exit", nil)
			}
			client.close(t)
			closed = true
		}
		if processExists(pid) {
			t.Errorf("Ruby LSP PID %d remains after stdio close", pid)
		}
	}()
	initialize, err := client.request(ctx, "initialize", realInitializeParams(realFileURI(fixtureRoot)))
	if err != nil || !realJSONNonEmpty(initialize) {
		t.Fatalf("Ruby LSP initialize failed: %v; response=%s; stderr=%s", err, initialize, client.stderr.String())
	}
	if err := client.notify("initialized", map[string]any{}); err != nil {
		t.Fatalf("Ruby LSP initialized notification: %v", err)
	}
	if err := client.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{"uri": realFileURI(fixture), "languageId": "ruby", "version": 1, "text": content}}); err != nil {
		t.Fatalf("Ruby LSP didOpen notification: %v", err)
	}
	symbols, err := client.request(ctx, "textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": realFileURI(fixture)}})
	if err != nil || !realJSONNonEmpty(symbols) {
		t.Fatalf("Ruby LSP documentSymbol was not non-empty: %v; response=%s; stderr=%s", err, symbols, client.stderr.String())
	}
	shutdown, err := client.request(ctx, "shutdown", map[string]any{})
	if err != nil || !bytes.Equal(bytes.TrimSpace(shutdown), []byte("null")) {
		t.Fatalf("Ruby LSP shutdown response=%s err=%v, want JSON null", shutdown, err)
	}
	shutdownOK = true
	if err := client.notify("exit", nil); err != nil {
		t.Fatalf("Ruby LSP exit notification: %v", err)
	}
	exitSent = true
	client.close(t)
	closed = true
	if processExists(pid) {
		t.Fatalf("Ruby LSP PID %d remains after shutdown/exit", pid)
	}
	t.Logf("NON_PASS TARGETED_DIAGNOSTIC/NON_LIFECYCLE Ruby LSP short stdio proof product=%s pid=%d start=%s shutdown=%t exit=%t zero_residual=true http_requests=%d", resolved.Product, pid, startToken, shutdownOK, exitSent, installHTTP.Requests)
}

func windowsARM64ProcessARM64RubyMergeEnvironment(base, overrides []string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, item := range append(append([]string(nil), base...), overrides...) {
		key, _, ok := strings.Cut(item, "=")
		if ok && key != "" {
			upperKey := strings.ToUpper(key)
			_, value, _ := strings.Cut(item, "=")
			if value == "" && (upperKey == "RUBYGEMS_GEMDEPS" || upperKey == "RUBYOPT" || upperKey == "RUBYLIB") {
				delete(values, upperKey)
				continue
			}
			values[upperKey] = item
		}
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		result = append(result, item)
	}
	return result
}

// TestWindowsARM64ProcessARM64Ruby36PrecheckE2E 只写 NON_PASS 证据，避免预检被误报为
// 安装、真实语义或 15 分钟生命周期证明；当前 catalog gap 会在正式入口 fail-fast。
func TestWindowsARM64ProcessARM64Ruby36PrecheckE2E(t *testing.T) {
	if os.Getenv(windowsARM64ProcessARM64Ruby36PrecheckEnv) != "1" {
		t.Skipf("set %s=1 to write the bounded Ruby NON_PASS precheck receipt", windowsARM64ProcessARM64Ruby36PrecheckEnv)
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("Ruby precheck requires windows/arm64, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	receipt := windowsARM64ProcessARM64Ruby36ReceiptPath(t, "windows-arm64-process-arm64-ruby-36-precheck.receipt")
	if err := node17x36WriteReceipt(receipt, []string{
		"test=windows-arm64-process-arm64-ruby-36",
		"status=NON_PASS",
		"precheck=true",
		"native_arch=arm64",
		"process_arch=arm64",
		"action_total=0",
		"runtime_failure=not_run",
		"semantic_success=not_run",
		"manager_idle=not_run",
		"formal_idle=not_run",
		"shutdown_response=false",
		"exit_sent=false",
		"zero_residual=not_proven",
		"absolute_path_markers=0",
		"reason=bounded_precheck_is_not_a_formal_proof",
	}); err != nil {
		t.Fatalf("write Ruby precheck receipt: %v", err)
	}
	t.Logf("Ruby precheck receipt=%s status=NON_PASS", filepath.Base(receipt))
}

// TestWindowsARM64ProcessARM64Ruby36SoakE2E 是唯一正式入口：先要求生产 catalog
// 对原生 ARM64 可安装；当前官方证据缺口只写脱敏 BLOCKED 收据，并在联网、安装器、MCP
// 或语义动作之前停止。原生 Ruby LSP 闭包锁定后，同一路径才执行一次生产安装、36 个
// action、15 分钟 idle 与完整生命周期证明。
func TestWindowsARM64ProcessARM64Ruby36SoakE2E(t *testing.T) {
	if os.Getenv(windowsARM64ProcessARM64Ruby36FormalEnv) != "1" {
		t.Skipf("set %s=1 to run the Windows ARM64/process ARM64 Ruby 36-action proof", windowsARM64ProcessARM64Ruby36FormalEnv)
	}
	if os.Getenv(windowsARM64ProcessARM64Ruby36PrecheckEnv) == "1" {
		t.Fatalf("Ruby formal proof cannot be combined with precheck; precheck is NON_PASS only")
	}
	if testing.Short() {
		t.Skip("Ruby formal lifecycle proof is disabled by -short")
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("Ruby formal proof requires windows/arm64, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform for Ruby: %v", err)
	}
	if host.OS != installer.WindowsHostOSWindows || host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("Ruby proof requires NativeArch=ProcessArch=arm64, got os=%q native=%q process=%q", host.OS, host.NativeArch, host.ProcessArch)
	}
	entry, planErr := installer.WindowsRuntimeDependencyPlanForArchitecture(installer.WindowsRuntimeDependencyProductRubyLSP, host.NativeArch)
	if planErr != nil {
		windowsARM64ProcessARM64Ruby36WriteBlockedReceipt(t, host, entry, planErr)
		t.Fatalf("Ruby ARM64 production install is blocked before network: %v", planErr)
	}

	startedAt := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), windowsARM64ProcessARM64Ruby36Timeout)
	defer cancel()
	repoRoot := realNodeRepoRoot(t)
	evidenceDir := windowsARM64ProcessARM64Ruby36EvidenceDirectory(t, repoRoot)
	receiptPath := filepath.Join(evidenceDir, "windows-arm64-process-arm64-ruby-36-soak.receipt")
	wirePath := filepath.Join(evidenceDir, "windows-arm64-process-arm64-ruby-36.wire.log")
	if err := windowsARM64ProcessARM64Ruby36WriteWire(wirePath, "phase=started;status=started;action_total=0;absolute_path_markers=0"); err != nil {
		t.Fatalf("create Ruby wire log: %v", err)
	}
	receiptBase := []string{
		"test=windows-arm64-process-arm64-ruby-36",
		"formal=true",
		"status=started",
		"native_arch=arm64",
		"process_arch=arm64",
		fmt.Sprintf("windows_version=%s", host.WindowsVersion),
		fmt.Sprintf("windows_build=%d", host.WindowsBuild),
		fmt.Sprintf("expected_actions=%d", windowsARM64ProcessARM64Ruby36Actions),
		fmt.Sprintf("manager_idle=%s", windowsARM64ProcessARM64Ruby36ManagerIdle),
		fmt.Sprintf("formal_idle=%s", windowsARM64ProcessARM64Ruby36FormalIdle),
		"acl_win32_5_1314=typed_authorization_required_only;acl_changes=none",
		"absolute_path_markers=0",
	}
	if err := node17x36WriteReceipt(receiptPath, receiptBase); err != nil {
		t.Fatalf("write Ruby initial receipt: %v", err)
	}

	productRoot := windowsRubyProductionProductRoot(t)
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict Ruby product root; Win32 5/1314 must remain authorization_required: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", repoRoot)
	t.Setenv("APPDATA", "")
	t.Setenv("MCP_LSP_IDLE_TIMEOUT", windowsARM64ProcessARM64Ruby36ManagerIdle.String())

	previousTransport := http.DefaultTransport
	httpObserver := &node17x36HTTPObserver{base: previousTransport}
	http.DefaultTransport = httpObserver
	defer func() { http.DefaultTransport = previousTransport }()
	provider, err := setupInstallerWithError()
	if err != nil {
		t.Fatalf("construct production Ruby installer: %v", err)
	}
	installCtx, installCancel := context.WithTimeout(ctx, windowsARM64ProcessARM64Ruby36Timeout)
	defer installCancel()
	result, err := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(installCtx), "ruby")
	if err != nil {
		t.Fatalf("production EnsureInstalledDetailed(ruby): %v", err)
	}
	if strings.TrimSpace(result.Path) == "" || !realMCPPathWithinRoot(productRoot, result.Path) {
		t.Fatalf("production Ruby server path is empty or escaped product root")
	}
	installHTTP := httpObserver.Snapshot()
	if installHTTP.Requests <= 0 || installHTTP.Attempts != installHTTP.Requests || installHTTP.Responses != installHTTP.Requests || installHTTP.TransportErrors != 0 || installHTTP.FailedResponses != 0 || installHTTP.SuccessfulResponses <= 0 {
		t.Fatalf("Ruby install HTTP proof failed: requests=%d attempts=%d responses=%d transport_errors=%d successes=%d failed=%d", installHTTP.Requests, installHTTP.Attempts, installHTTP.Responses, installHTTP.TransportErrors, installHTTP.SuccessfulResponses, installHTTP.FailedResponses)
	}
	if err := windowsARM64ProcessARM64Ruby36WriteWire(wirePath, fmt.Sprintf("phase=installed;status=pass;http_requests=%d;http_responses=%d", installHTTP.Requests, installHTTP.Responses)); err != nil {
		t.Fatalf("write Ruby install wire: %v", err)
	}

	server := windowsARM64ProcessARM64Ruby36ServerCase()
	fixtureRoot := t.TempDir()
	registerRealMCPTempRootCleanup(t, fixtureRoot)
	fixture := writeRealMCPLanguageFixture(t, fixtureRoot, server)
	astFile := filepath.Join(fixture.workDir, ".mcp-ast", "ast_fixture.js")
	copyRealMCPBinSourceFile(t, filepath.Join(repoRoot, "bin", "LSP", "test"), "javascript/module-examples/top-level-await/main.js", astFile)
	binaryPath := buildRealMcpLSPBinary(t, repoRoot)
	client := startRealMcpLSPBinary(t, ctx, binaryPath, fixture.workDir, repoRoot, "", "", productRoot)
	mcpPID := client.cmd.Process.Pid
	mcpStart, err := windowsGoplsProcessStartIdentity(mcpPID)
	if err != nil {
		t.Fatalf("capture Ruby MCP PID+start: %v", err)
	}
	tracked := map[realMCPProcessKey]realMCPProcessIdentity{{PID: mcpPID, StartToken: mcpStart}: {PID: mcpPID, StartToken: mcpStart, Name: "mcp-lsp", Language: "mcp-lsp"}}
	var matrix realMCPMatrixSummary
	var actions []realMCPActionSpec
	var actionLedger []string
	shutdownResponse := false
	exitSent := false
	zeroResidual := false
	postIdle := 0
	completed := false
	defer func() {
		if client != nil && client.cmd != nil {
			_ = trackRealMCPProcessTree(t, mcpPID, "ruby-final-before-close", tracked)
		}
		if client != nil && client.cmd != nil {
			if shutdownResponse {
				exitSent = node17x36CloseWithExitProof(t, client)
			} else {
				client.close(t)
			}
		}
		if len(tracked) > 0 {
			requireRealMCPProcessIdentitiesGone(t, tracked)
			zeroResidual = !t.Failed()
		}
		status := "NON_PASS"
		if completed && matrix.total == windowsARM64ProcessARM64Ruby36Actions && matrix.succeeded+matrix.capabilityUnsupported == matrix.total && shutdownResponse && exitSent && zeroResidual && !t.Failed() {
			status = "PASS"
		}
		lines := append([]string{}, receiptBase...)
		lines = append(lines,
			"status="+status,
			fmt.Sprintf("action_total=%d", matrix.total),
			fmt.Sprintf("success_including_legal_empty=%d", matrix.succeeded),
			fmt.Sprintf("legal_empty=%d", matrix.legalEmpty),
			fmt.Sprintf("capability_unsupported=%d", matrix.capabilityUnsupported),
			fmt.Sprintf("runtime_failure=%s", rubyWindowsARM64ProcessARM64RuntimeFailureValue(matrix.total, t.Failed())),
			fmt.Sprintf("null_result=%s", rubyWindowsARM64ProcessARM64RuntimeFailureValue(matrix.total, t.Failed())),
			fmt.Sprintf("post_idle_non_empty_actions=%d", postIdle),
			fmt.Sprintf("shutdown_response=%t", shutdownResponse),
			fmt.Sprintf("exit_sent=%t", exitSent),
			fmt.Sprintf("zero_residual=%t", zeroResidual),
			fmt.Sprintf("mcp_pid=%d;mcp_start=%s", mcpPID, mcpStart),
			fmt.Sprintf("http_requests=%d;http_responses=%d;http_failed=%d", installHTTP.Requests, installHTTP.Responses, installHTTP.FailedResponses),
			fmt.Sprintf("elapsed=%s", time.Since(startedAt).Round(time.Millisecond)),
			"absolute_path_markers=0",
		)
		lines = append(lines, actionLedger...)
		lines = append(lines, node17x36IdentityReceiptLines(tracked)...)
		if err := node17x36WriteReceipt(receiptPath, lines); err != nil {
			t.Errorf("write Ruby final receipt: %v", err)
		}
		_ = windowsARM64ProcessARM64Ruby36WriteWire(wirePath, fmt.Sprintf("phase=closed;status=%s;shutdown=%t;exit=%t;zero_residual=%t;action_total=%d", status, shutdownResponse, exitSent, zeroResidual, matrix.total))
	}()

	initialize := client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}})
	if initialize.JSONRPC != "2.0" || initialize.Error != nil {
		t.Fatalf("Ruby initialize response was not valid JSON-RPC")
	}
	if err := windowsARM64ProcessARM64Ruby36Notify(client, "notifications/initialized", map[string]any{}); err != nil {
		t.Fatalf("send Ruby initialized notification: %v", err)
	}
	requireRealMCPToolFamilies(t, callMcpLSPBinaryRaw(t, client, "tools/list", map[string]any{}))
	fixture, actions, matrix, actionLedger = windowsARM64ProcessARM64Ruby36RunActionMatrix(t, client, mcpPID, fixtureRoot, astFile, server, tracked, wirePath)
	if matrix.total != windowsARM64ProcessARM64Ruby36Actions || matrix.succeeded+matrix.capabilityUnsupported != matrix.total {
		t.Fatalf("Ruby action accounting failed: total=%d success=%d legal_empty=%d capability_unsupported=%d", matrix.total, matrix.succeeded, matrix.legalEmpty, matrix.capabilityUnsupported)
	}
	if len(tracked) <= 1 {
		t.Fatalf("Ruby process tree captured no real server descendant: tracked=%d", len(tracked))
	}
	node17x36RequireLanguageIdentities(t, tracked, []realNodeServerCase{server})
	baselineKeys := node17x36ProcessKeys(tracked)
	baseline := make(map[realMCPProcessKey]realMCPProcessIdentity, len(tracked))
	for key, identity := range tracked {
		baseline[key] = identity
	}

	idleStarted := time.Now()
	idleDeadline := idleStarted.Add(windowsARM64ProcessARM64Ruby36FormalIdle)
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
			t.Fatalf("Ruby idle stopped before %s: %v", windowsARM64ProcessARM64Ruby36FormalIdle, ctx.Err())
		case <-timer.C:
		}
		if !trackRealMCPProcessTree(t, mcpPID, "ruby-idle-heartbeat", tracked) {
			t.Fatalf("Ruby idle process tree capture failed")
		}
		node17x36AssertProcessKeysUnchanged(t, baselineKeys, tracked, "ruby-idle-heartbeat")
		heartbeats++
		if err := windowsARM64ProcessARM64Ruby36WriteWire(wirePath, fmt.Sprintf("phase=idle;status=heartbeat;elapsed=%s;identities=%d", time.Since(idleStarted).Round(time.Second), len(tracked))); err != nil {
			t.Fatalf("write Ruby idle wire: %v", err)
		}
	}
	if time.Since(idleStarted) < windowsARM64ProcessARM64Ruby36FormalIdle || windowsARM64ProcessARM64Ruby36ManagerIdle <= windowsARM64ProcessARM64Ruby36FormalIdle || heartbeats < 1 {
		t.Fatalf("Ruby lifecycle window invalid: idle=%s manager=%s heartbeats=%d", time.Since(idleStarted), windowsARM64ProcessARM64Ruby36ManagerIdle, heartbeats)
	}
	postAction, ok := windowsARM64ProcessARM64Ruby36PostIdleHover(actions)
	if !ok {
		t.Fatalf("Ruby action matrix has no required post-idle hover")
	}
	postIdlePath := realMCPPositionPath(fmt.Sprint(postAction.args["pos"]))
	if postIdlePath == "" {
		t.Fatalf("Ruby post-idle hover position has no fixture path: %#v", postAction.args["pos"])
	}
	// manager idle 可能重建 Ruby child；先用同一 fixture 的公开 open_file
	// 重新 hydration 文档，再验证 hover 非空，不能把 child 重建后的空状态当语义结果。
	postIdleOpen := client.callTool(t, "file", realMCPWindowsToolArguments(server.languageID, fixture.workDir, "file", "open_file", map[string]any{
		"action":    "open_file",
		"file_path": postIdlePath,
	}))
	requireRealMCPActionResult(t, postIdleOpen, true, "", false, "", false, "ruby post-idle fixture hydration")
	response := client.callTool(t, postAction.tool, realMCPWindowsToolArguments(server.languageID, fixture.workDir, postAction.tool, postAction.name, postAction.args))
	if status := requireRealMCPActionResult(t, response, true, "", false, realMCPActionCapabilityKey(postAction.tool, postAction.name), realMCPActionProtocolOptional(postAction.tool, postAction.name), "ruby post-idle hover"); status != realMCPActionSucceeded {
		t.Fatalf("Ruby post-idle hover was not a non-empty semantic success: %s", status)
	}
	postIdle = 1
	if !trackRealMCPProcessTree(t, mcpPID, "ruby-post-idle", tracked) {
		t.Fatalf("Ruby post-idle process tree capture failed")
	}
	rubyWindowsARM64ProcessARM64AssertPostIdleIdentityPolicy(t, baseline, mcpPID)
	shutdown := client.call(t, "shutdown", map[string]any{})
	if shutdown.JSONRPC != "2.0" || shutdown.Error != nil {
		t.Fatalf("Ruby shutdown response was not valid JSON-RPC")
	}
	shutdownResponse = true
	completed = true
}

// rubyWindowsARM64ProcessARM64ValidatePostIdleSnapshot 只允许 manager 预期的 Ruby 子进程轮换：
// MCP/cohort owner 必须保持同一 PID+启动身份，旧 Ruby 身份必须消失，新 Ruby 必须由同一 MCP
// 直接托管并保持完全相同的命令摘要，从而证明仍使用同一产品根和 ARM64 server 路径。
func rubyWindowsARM64ProcessARM64ValidatePostIdleSnapshot(baseline map[realMCPProcessKey]realMCPProcessIdentity, snapshot []realMCPProcessIdentity, mcpPID int) error {
	if mcpPID <= 0 {
		return fmt.Errorf("invalid MCP owner PID %d", mcpPID)
	}
	var oldRuby *realMCPProcessIdentity
	var mcpKey *realMCPProcessKey
	for key, identity := range baseline {
		name := strings.ToLower(identity.Name)
		if key.PID == mcpPID {
			copy := key
			mcpKey = &copy
		}
		if strings.Contains(name, "ruby") && key.PID != mcpPID {
			copy := identity
			oldRuby = &copy
		}
	}
	if mcpKey == nil {
		return fmt.Errorf("MCP owner PID %d was absent from baseline", mcpPID)
	}
	if oldRuby == nil {
		return fmt.Errorf("Ruby language child was absent from baseline")
	}
	if oldRuby.CommandSHA256 == "" {
		return fmt.Errorf("baseline Ruby command identity was empty")
	}
	newRuby := false
	for _, identity := range snapshot {
		key := realMCPProcessKey{PID: identity.PID, StartToken: identity.StartToken}
		if strings.Contains(strings.ToLower(identity.Name), "ruby") && identity.ParentPID == mcpPID && identity.CommandSHA256 == oldRuby.CommandSHA256 && key != (realMCPProcessKey{PID: oldRuby.PID, StartToken: oldRuby.StartToken}) {
			newRuby = true
		}
		if key == (realMCPProcessKey{PID: oldRuby.PID, StartToken: oldRuby.StartToken}) {
			return fmt.Errorf("old Ruby PID/start identity remained active: pid=%d start=%s", oldRuby.PID, oldRuby.StartToken)
		}
	}
	if !newRuby {
		return fmt.Errorf("no replacement Ruby child with same product-owned command identity")
	}
	return nil
}

// rubyWindowsARM64ProcessARM64AssertPostIdleIdentityPolicy 读取轮换后的实时树，并确认旧身份已退出。
func rubyWindowsARM64ProcessARM64AssertPostIdleIdentityPolicy(t *testing.T, baseline map[realMCPProcessKey]realMCPProcessIdentity, mcpPID int) {
	t.Helper()
	snapshot, err := realMCPProcessTreeSnapshot(mcpPID)
	if err != nil {
		t.Fatalf("capture Ruby post-idle identity snapshot: %v", err)
	}
	if err := rubyWindowsARM64ProcessARM64ValidatePostIdleSnapshot(baseline, snapshot, mcpPID); err != nil {
		t.Fatalf("Ruby post-idle identity policy: %v", err)
	}
	var ownerKey realMCPProcessKey
	for key := range baseline {
		if key.PID == mcpPID {
			ownerKey = key
			break
		}
	}
	ownerStart, ownerErr := windowsGoplsProcessStartIdentity(mcpPID)
	if ownerErr != nil || ownerStart != ownerKey.StartToken {
		t.Fatalf("MCP/cohort owner PID/start identity changed after idle: pid=%d expected=%s actual=%s err=%v", mcpPID, ownerKey.StartToken, ownerStart, ownerErr)
	}
	for key, identity := range baseline {
		if !strings.Contains(strings.ToLower(identity.Name), "ruby") || key.PID == mcpPID {
			continue
		}
		alive, aliveErr := processAliveForE2E(key.PID)
		if aliveErr != nil {
			t.Fatalf("inspect retired Ruby PID %d start %s: %v", key.PID, key.StartToken, aliveErr)
		}
		if alive {
			current, startErr := windowsGoplsProcessStartIdentity(key.PID)
			if startErr == nil && current == key.StartToken {
				t.Fatalf("retired Ruby PID/start identity still alive: pid=%d start=%s", key.PID, key.StartToken)
			}
		}
	}
}

func TestWindowsARM64ProcessARM64Ruby36PostIdleIdentityPolicy(t *testing.T) {
	oldKey := realMCPProcessKey{PID: 20, StartToken: "old-ruby"}
	mcpKey := realMCPProcessKey{PID: 10, StartToken: "mcp"}
	baseline := map[realMCPProcessKey]realMCPProcessIdentity{
		mcpKey: {PID: 10, StartToken: "mcp", Name: "mcp-lsp"},
		oldKey: {PID: 20, StartToken: "old-ruby", ParentPID: 10, Name: "ruby.exe", CommandSHA256: "same-product-command"},
	}
	snapshot := []realMCPProcessIdentity{
		{PID: 10, StartToken: "mcp", Name: "mcp-lsp"},
		{PID: 30, StartToken: "new-ruby", ParentPID: 10, Name: "ruby.exe", CommandSHA256: "same-product-command"},
	}
	if err := rubyWindowsARM64ProcessARM64ValidatePostIdleSnapshot(baseline, snapshot, 10); err != nil {
		t.Fatalf("expected controlled Ruby child rotation to pass: %v", err)
	}
	snapshot[1].CommandSHA256 = "different-product-command"
	if err := rubyWindowsARM64ProcessARM64ValidatePostIdleSnapshot(baseline, snapshot, 10); err == nil {
		t.Fatalf("expected product-owned command identity mismatch to fail")
	}
}

// windowsRubyProductionProductRoot 使用受控前缀创建真实产品根，并在 ACL 设置前注册
// reparse-aware cleanup；不能用 testing.TempDir 的普通 RemoveAll 代替产品清理契约。
func windowsRubyProductionProductRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "sd-ruby-production-windows-arm64-")
	if err != nil {
		t.Fatalf("create Ruby Windows ARM64 product root: %v", err)
	}
	t.Cleanup(func() {
		if err := removeRealWindowsProductRoot(root); err != nil {
			t.Errorf("remove Ruby Windows ARM64 product root: %v", err)
		}
	})
	return root
}

func windowsARM64ProcessARM64Ruby36RunActionMatrix(t *testing.T, client *mcpLSPBinaryClient, mcpPID int, fixtureRoot, astFile string, server realNodeServerCase, tracked map[realMCPProcessKey]realMCPProcessIdentity, wirePath string) (realMCPFixture, []realMCPActionSpec, realMCPMatrixSummary, []string) {
	t.Helper()
	fixture := writeRealMCPLanguageFixture(t, fixtureRoot, server)
	actions := windowsARM64ProcessARM64Ruby36ActionSpecs(server, fixture, astFile)
	if err := validateRealMCPActionClosure(actions); err != nil {
		t.Fatalf("Ruby action closure: %v", err)
	}
	var matrix realMCPMatrixSummary
	ledger := make([]string, 0, 1)
	for index, action := range actions {
		started := time.Now()
		ordinal := index + 1
		if err := windowsARM64ProcessARM64Ruby36WriteWire(wirePath, fmt.Sprintf("phase=action_start;language=ruby;ordinal=%d/%d;tool=%s;name=%s;require_non_empty=%t;allow_capability_unsupported=%t", ordinal, windowsARM64ProcessARM64Ruby36Actions, action.tool, action.name, action.requireResult, action.allowCapabilityUnsupported)); err != nil {
			t.Fatalf("write Ruby action start wire: %v", err)
		}
		if action.tool == "patch_edit" {
			path, _ := action.args["file_path"].(string)
			if path == "" {
				path = realMCPPositionPath(fmt.Sprint(action.args["pos"]))
			}
			if path == "" {
				t.Fatalf("Ruby patch action %s has no target path", action.name)
			}
			opened := client.callTool(t, "file", realMCPWindowsToolArguments(server.languageID, fixture.workDir, "file", "open_file", map[string]any{"action": "open_file", "file_path": path}))
			requireRealMCPActionResult(t, opened, true, "", false, "", false, "ruby patch target")
		}
		response := client.callTool(t, action.tool, realMCPWindowsToolArguments(server.languageID, fixture.workDir, action.tool, action.name, action.args))
		status := requireRealMCPActionResult(t, response, action.requireResult, action.emptyResultReason, action.allowCapabilityUnsupported, realMCPActionCapabilityKey(action.tool, action.name), realMCPActionProtocolOptional(action.tool, action.name), "ruby "+action.tool+"/"+action.name)
		if action.tool == "patch_edit" && action.name == "replace_range" && status != realMCPActionUnsupported {
			assertRealFileContains(t, fixture.replaceFile, "REAL_MCP_REPLACED", "ruby replace_range")
		}
		matrix.total++
		switch status {
		case realMCPActionSucceeded:
			matrix.succeeded++
		case realMCPActionLegalEmpty:
			matrix.succeeded++
			matrix.legalEmpty++
		case realMCPActionUnsupported:
			matrix.capabilityUnsupported++
			matrix.unsupportedActions = append(matrix.unsupportedActions, action.tool+"/"+action.name)
		default:
			t.Fatalf("Ruby %s/%s returned unclassified status %q", action.tool, action.name, status)
		}
		if err := windowsARM64ProcessARM64Ruby36WriteWire(wirePath, fmt.Sprintf("phase=action_done;language=ruby;ordinal=%d/%d;tool=%s;name=%s;duration=%s;status=%s", ordinal, windowsARM64ProcessARM64Ruby36Actions, action.tool, action.name, time.Since(started).Round(time.Millisecond), status)); err != nil {
			t.Fatalf("write Ruby action completion wire: %v", err)
		}
	}
	if matrix.total != windowsARM64ProcessARM64Ruby36Actions || matrix.succeeded+matrix.capabilityUnsupported != matrix.total {
		t.Fatalf("Ruby action accounting total=%d success=%d legal_empty=%d capability_unsupported=%d", matrix.total, matrix.succeeded, matrix.legalEmpty, matrix.capabilityUnsupported)
	}
	ledger = append(ledger, fmt.Sprintf("language.ruby.total=%d;semantic_success=%d;legal_empty=%d;capability_unsupported=%d;runtime_failure=0;null_result=0;unsupported_actions=%s", matrix.total, matrix.succeeded-matrix.legalEmpty, matrix.legalEmpty, matrix.capabilityUnsupported, strings.Join(matrix.unsupportedActions, ",")))
	if !trackRealMCPProcessTree(t, mcpPID, "ruby", tracked) {
		t.Fatalf("Ruby action process tree capture failed")
	}
	return fixture, actions, matrix, ledger
}

func windowsARM64ProcessARM64Ruby36PostIdleHover(actions []realMCPActionSpec) (realMCPActionSpec, bool) {
	for _, action := range actions {
		if action.tool == "inspect" && action.name == "hover" && action.requireResult && !action.allowCapabilityUnsupported {
			return action, true
		}
	}
	return realMCPActionSpec{}, false
}

func windowsARM64ProcessARM64Ruby36EvidenceDirectory(t *testing.T, repoRoot string) string {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv(windowsARM64ProcessARM64Ruby36EvidenceEnv))
	if dir == "" {
		dir = filepath.Join(repoRoot, ".build-cache", "lsp-test-results", "windows-arm64-process-arm64-ruby-36")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create Ruby evidence directory: %v", err)
	}
	return dir
}

func windowsARM64ProcessARM64Ruby36ReceiptPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(windowsARM64ProcessARM64Ruby36EvidenceDirectory(t, realNodeRepoRoot(t)), name)
}

func windowsARM64ProcessARM64Ruby36WriteWire(path, line string) error {
	if strings.TrimSpace(line) == "" {
		return errors.New("Ruby wire line is empty")
	}
	line = strings.NewReplacer("\r", "_", "\n", "_", "C:\\Users\\", "<user>\\").Replace(line)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

func windowsARM64ProcessARM64Ruby36Notify(client *mcpLSPBinaryClient, method string, params map[string]any) error {
	if client == nil || client.stdin == nil {
		return errors.New("Ruby MCP client is not live")
	}
	payload := fmt.Sprintf("{\"jsonrpc\":\"2.0\",\"method\":%q,\"params\":%s}\n", method, windowsARM64ProcessARM64Ruby36JSON(params))
	_, err := client.stdin.Write([]byte(payload))
	return err
}

func windowsARM64ProcessARM64Ruby36WriteBlockedReceipt(t *testing.T, host installer.WindowsHostPlatform, entry installer.WindowsRuntimeDependencyCatalogEntry, err error) {
	t.Helper()
	receipt := windowsARM64ProcessARM64Ruby36ReceiptPath(t, "windows-arm64-process-arm64-ruby-36-blocked.receipt")
	status := "BLOCKED"
	if !errors.Is(err, installer.ErrWindowsRuntimeDependencyEvidenceGap) && !errors.Is(err, installer.ErrWindowsRuntimeDependencyUnsupported) {
		status = "NON_PASS"
	}
	lines := []string{
		"test=windows-arm64-process-arm64-ruby-36",
		"status=" + status,
		"native_arch=" + host.NativeArch,
		"process_arch=" + host.ProcessArch,
		"product=" + string(entry.Product),
		"action_total=0",
		"runtime_failure=not_run",
		"semantic_success=not_run",
		"manager_idle=not_run",
		"formal_idle=not_run",
		"shutdown_response=false",
		"exit_sent=false",
		"zero_residual=verified_not_started",
		"http_requests=0;http_responses=0",
		"absolute_path_markers=0",
		"reason=Ruby_LSP_ARM64_closure_failed_catalog_or_ready_validation",
	}
	if writeErr := node17x36WriteReceipt(receipt, lines); writeErr != nil {
		t.Errorf("write Ruby blocked receipt: %v", writeErr)
	}
	t.Logf("Ruby blocked receipt=%s status=%s product=%s", filepath.Base(receipt), status, entry.Product)
}

func rubyWindowsARM64ProcessARM64RuntimeFailureValue(actionTotal int, testFailed bool) string {
	if actionTotal == windowsARM64ProcessARM64Ruby36Actions && !testFailed {
		return "0"
	}
	return "not_proven"
}

// windowsARM64ProcessARM64Ruby36JSON 只用于固定 JSON-RPC notification；参数来自测试常量，编码失败必须 panic
// 而不是继续发出不完整的协议消息，避免把失败伪装成生命周期成功。
func windowsARM64ProcessARM64Ruby36JSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
