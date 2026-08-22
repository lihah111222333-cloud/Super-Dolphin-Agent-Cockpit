//go:build windows && arm64 && e2e

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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
	markdownWindowsARM64ProcessARM64Soak15mEnv         = "SUPER_DOLPHIN_RUN_MARKDOWN_WINDOWS_ARM64_PROCESS_ARM64_SOAK_15M"
	markdownWindowsARM64ProcessARM64Soak15mIdle        = 15 * time.Minute
	markdownWindowsARM64ProcessARM64Soak15mEvidenceDir = ".build-cache/codex-markdown-windows-proof"
	markdownWindowsARM64ProcessARM64Soak15mReceiptName = "windows-arm64-process-arm64-markdown-soak-15m-receipt.log"
	markdownWindowsARM64ProcessARM64Soak15mWireName    = "windows-arm64-process-arm64-markdown-soak-15m-wire.jsonl"
)

// TestWindowsARM64ProcessARM64MarkdownSoak15mE2E 通过生产 installer 和真实 stdio
// 证明 Windows ARM64/process-arm64 Markdown cohort 在 15 分钟空闲后仍可服务并可收敛退出。
func TestWindowsARM64ProcessARM64MarkdownSoak15mE2E(t *testing.T) {
	if os.Getenv(markdownWindowsARM64ProcessARM64Soak15mEnv) != "1" {
		t.Skipf("set %s=1 to enable the 15-minute production Windows ARM64 Markdown soak", markdownWindowsARM64ProcessARM64Soak15mEnv)
	}
	if testing.Short() {
		t.Skip("the 15-minute production Markdown soak is disabled by -short")
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("this soak requires a Windows ARM64 test process, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	if host.OS != installer.WindowsHostOSWindows || host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("soak requires Windows native ARM64/process ARM64, got os=%q native=%q process=%q build=%d", host.OS, host.NativeArch, host.ProcessArch, host.WindowsBuild)
	}

	repoRoot := realNodeRepoRoot(t)
	evidenceDir := filepath.Join(repoRoot, filepath.FromSlash(markdownWindowsARM64ProcessARM64Soak15mEvidenceDir))
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatalf("create Markdown soak evidence directory: %v", err)
	}
	receiptPath := filepath.Join(evidenceDir, markdownWindowsARM64ProcessARM64Soak15mReceiptName)
	wirePath := filepath.Join(evidenceDir, markdownWindowsARM64ProcessARM64Soak15mWireName)
	if err := os.Remove(wirePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove stale Markdown soak wire receipt %s: %v", wirePath, err)
	}
	receipt := []string{
		"test=windows-arm64-process-arm64-markdown-soak-15m",
		"status=started",
		fmt.Sprintf("started_at=%s", startedAt.Format(time.RFC3339Nano)),
		fmt.Sprintf("host_os=%s", host.OS),
		fmt.Sprintf("host_native_arch=%s", host.NativeArch),
		fmt.Sprintf("host_process_arch=%s", host.ProcessArch),
		fmt.Sprintf("host_windows_version=%s", host.WindowsVersion),
		fmt.Sprintf("host_windows_build=%d", host.WindowsBuild),
		"process_arch_is_diagnostic_only=true",
		"acl_win32_5_1314=typed_securefs_authorization_required_preserved",
		fmt.Sprintf("wire_path=%s", wirePath),
	}
	// 即使测试中途因 ACL、网络或协议错误停止，也保留当前可复核收据。
	t.Cleanup(func() {
		receipt = append(receipt, fmt.Sprintf("finished_at=%s", time.Now().Format(time.RFC3339Nano)))
		receipt = append(receipt, fmt.Sprintf("elapsed=%s", time.Since(startedAt).Round(time.Millisecond)))
		if err := os.WriteFile(receiptPath, []byte(strings.Join(receipt, "\n")+"\n"), 0o600); err != nil {
			t.Logf("write Markdown soak receipt %s: %v", receiptPath, err)
		}
	})

	// 从测试进程环境中剥离系统 Node/npm；生产 child 只接受 productRoot 中的 locked cohort。
	t.Setenv("PATH", realNodePathWithoutNodeNPM(os.Getenv("PATH")))
	t.Setenv("NODE_PATH", "")
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "")
	t.Setenv("SUPER_DOLPHIN_WINDOWS_NODE_PATH", "")
	t.Setenv("SUPER_DOLPHIN_MSVC_RUNTIME_DIR", "")
	for _, commandName := range []string{"node", "node.exe", "npm", "npm.cmd"} {
		if resolved, lookErr := exec.LookPath(commandName); lookErr == nil {
			t.Fatalf("system PATH still resolves forbidden %s at %q", commandName, resolved)
		}
	}

	productRoot, err := os.MkdirTemp("", "sd-node-production-windows-arm64-process-arm64-markdown-soak-15m-")
	if err != nil {
		t.Fatalf("create private production product root: %v", err)
	}
	t.Cleanup(func() {
		if err := removeRealWindowsProductRoot(productRoot); err != nil {
			t.Errorf("remove private production product root %s: %v", productRoot, err)
		}
	})
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict private production product root: %v", err)
	}
	assertDirectoryEmpty(t, productRoot)
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	receipt = append(receipt, fmt.Sprintf("product_root=%s", productRoot))

	provider := setupInstaller()
	installed, err := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), "markdown")
	if err != nil {
		receipt = append(receipt, "provision_error="+markdownSoakErrorKind(err))
		t.Fatalf("production EnsureInstalledDetailed(markdown) from empty product root: %v", err)
	}
	if installed.Status == installer.InstallStatusInstalledFallback {
		t.Fatalf("production Markdown install used forbidden fallback status: %#v", installed)
	}
	if _, err := installer.WindowsShortProcessPathWithinRoot(productRoot, installed.Path); err != nil {
		t.Fatalf("production Markdown binary escaped product root: %v", err)
	}

	nodeRuntime, err := installer.NewWindowsNodeRuntime(productRoot, nil)
	if err != nil {
		t.Fatalf("construct production Windows Node runtime: %v", err)
	}
	expectedPaths, err := nodeRuntime.ExpectedPaths()
	if err != nil {
		t.Fatalf("resolve production Windows Node paths: %v", err)
	}
	wantMarkdownBinary, err := nodeRuntime.BinaryPath(ctx, "vscode-markdown-language-server")
	if err != nil {
		t.Fatalf("resolve production Markdown binary path: %v", err)
	}
	if filepath.Clean(installed.Path) != filepath.Clean(wantMarkdownBinary) {
		t.Fatalf("production Markdown binary=%q, want locked cohort path=%q", installed.Path, wantMarkdownBinary)
	}
	var exactPackages []string
	for _, spec := range runtimeNPMInstallerSpecsForPlatform("windows") {
		if !slicesContainsString(spec.languages, "markdown") {
			continue
		}
		exactPackages, err = runtimeNPMExactPackages(spec.args)
		if err != nil {
			t.Fatalf("parse exact Markdown npm pins: %v", err)
		}
		break
	}
	if len(exactPackages) == 0 {
		t.Fatal("production Markdown exact npm pins are missing")
	}
	if err := nodeRuntime.ValidateExactPackages(ctx, exactPackages); err != nil {
		t.Fatalf("validate exact production Markdown npm cohort: %v", err)
	}
	for _, specification := range exactPackages {
		packageName, packageVersion, parseErr := productionExactPackageNameAndVersion(specification)
		if parseErr != nil {
			t.Fatalf("parse exact Markdown package %q: %v", specification, parseErr)
		}
		verifyRealNodePackageVersion(t, expectedPaths.Prefix, packageName, packageVersion)
		receipt = append(receipt, fmt.Sprintf("npm_package=%s version=%s", packageName, packageVersion))
	}

	nodeAsset, err := installer.WindowsNodeRuntimeAssetForPlatform(host)
	if err != nil {
		t.Fatalf("select locked Node asset: %v", err)
	}
	vclibsAsset, err := installer.WindowsVCLibsDesktopAssetForPlatform(host)
	if err != nil {
		t.Fatalf("select locked VCLibs asset: %v", err)
	}
	verifyMarkdownSoakLockedNode(t, productRoot, expectedPaths.NodePath, nodeAsset)
	vclibsRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalPath(productRoot)
	if err != nil {
		t.Fatalf("resolve locked VCLibs ready root: %v", err)
	}
	vclibsProcessRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalProcessPath(productRoot)
	if err != nil {
		t.Fatalf("resolve locked VCLibs process root: %v", err)
	}
	if same, sameErr := sameRealNodeFile(vclibsRoot, vclibsProcessRoot); sameErr != nil || !same {
		if sameErr != nil {
			t.Fatalf("compare VCLibs ready/process identity: %v", sameErr)
		}
		t.Fatalf("VCLibs process root %q does not identify ready root %q", vclibsProcessRoot, vclibsRoot)
	}
	vclibsPayload := filepath.Join(filepath.Dir(vclibsRoot), "payload.zip")
	gotVCLibsSHA, err := sha256File(vclibsPayload)
	if err != nil {
		t.Fatalf("hash locked VCLibs payload: %v", err)
	}
	if !strings.EqualFold(gotVCLibsSHA, vclibsAsset.SHA256) {
		t.Fatalf("locked VCLibs payload SHA256=%s, want=%s", gotVCLibsSHA, vclibsAsset.SHA256)
	}
	t.Setenv("SUPER_DOLPHIN_WINDOWS_NODE_PATH", expectedPaths.NodePath)
	t.Setenv("SUPER_DOLPHIN_MSVC_RUNTIME_DIR", vclibsProcessRoot)
	receipt = append(receipt,
		fmt.Sprintf("installed_status=%s", installed.Status),
		fmt.Sprintf("markdown_server=%s", installed.Path),
		fmt.Sprintf("node_version=%s", nodeAsset.Version),
		fmt.Sprintf("node_url=%s", nodeAsset.URL),
		fmt.Sprintf("node_sha256=%s", nodeAsset.SHA256),
		fmt.Sprintf("vclibs_version=%s", vclibsAsset.Version),
		fmt.Sprintf("vclibs_url=%s", vclibsAsset.URL),
		fmt.Sprintf("vclibs_sha256=%s", vclibsAsset.SHA256),
		fmt.Sprintf("vclibs_payload_sha256=%s", gotVCLibsSHA),
	)

	fixtureRoot := filepath.Join(t.TempDir(), "windows-arm64-process-arm64-markdown-soak-15m-fixture")
	if err := os.MkdirAll(fixtureRoot, 0o700); err != nil {
		t.Fatalf("create Markdown soak fixture root: %v", err)
	}
	servers := realNodeServerCasesForLanguage("markdown")
	requireRealNodeServerCaseIdentities(t, servers)
	if len(servers) != 1 {
		t.Fatalf("Markdown server cases=%d, want exactly one", len(servers))
	}
	server := servers[0]
	fixture := writeRealMCPLanguageFixture(t, fixtureRoot, server)
	astFile := filepath.Join(fixtureRoot, "ast_fixture.js")
	writeRealFixture(t, astFile, "function realMCPAstFixture(name) { return name; }\nrealMCPAstFixture(\"world\");\n")
	binary := buildRealMcpLSPBinary(t, repoRoot)
	t.Setenv(runtimeMarkdownWireLogEnv, wirePath)
	receipt = append(receipt, fmt.Sprintf("binary=%s", binary))

	client := startRealMcpLSPBinary(t, ctx, binary, fixtureRoot, repoRoot, "", "", productRoot)
	mcpPID := client.cmd.Process.Pid
	startToken, err := windowsGoplsProcessStartIdentity(mcpPID)
	if err != nil {
		client.close(t)
		t.Fatalf("capture mcp-lsp root PID %d start identity: %v", mcpPID, err)
	}
	tracked := map[realMCPProcessKey]realMCPProcessIdentity{
		{PID: mcpPID, StartToken: startToken}: {PID: mcpPID, StartToken: startToken, Name: "mcp-lsp", Language: "markdown"},
	}
	shutdownSent := false
	clientClosed := false
	defer func() {
		if !clientClosed {
			if client == nil || client.cmd == nil {
				return
			}
			if !shutdownSent {
				_ = writeMCPShutdownWithoutFatal(client)
			}
			if !trackRealMCPProcessTree(t, mcpPID, "final-before-close", tracked) {
				receipt = append(receipt, "process_tree_final=error")
			}
			client.close(t)
			clientClosed = true
		}
		logRealMCPProcessIdentities(t, tracked)
		if len(tracked) <= 1 {
			t.Errorf("production Markdown process tree captured no descendant; exact zero-residual proof is incomplete")
			receipt = append(receipt, "process_tree_descendants=missing")
			return
		}
		requireRealMCPProcessIdentitiesGone(t, tracked)
		receipt = append(receipt, "zero_residual=true")
	}()

	client.call(t, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "super-dolphin-windows-arm64-process-arm64-markdown-soak-15m", "version": "1"},
	})
	requireRealMCPToolFamilies(t, callMcpLSPBinaryRaw(t, client, "tools/list", map[string]any{}))
	receipt = append(receipt, fmt.Sprintf("pid=%d", mcpPID), fmt.Sprintf("start_token=%s", startToken), "initialize=ok", "tools_list=seven_public_families")

	actions := realMCPActionSpecs(server, fixture, astFile)
	if err := validateRealMCPActionClosure(actions); err != nil {
		t.Fatalf("Markdown action closure: %v", err)
	}
	var summary realMCPMatrixSummary
	for _, action := range actions {
		response := client.callTool(t, action.tool, realMCPWindowsToolArguments(server.languageID, fixtureRoot, action.tool, action.name, action.args))
		status := requireRealMCPActionResult(t, response, action.requireResult, action.emptyResultReason, action.allowCapabilityUnsupported, realMCPActionCapabilityKey(action.tool, action.name), realMCPActionProtocolOptional(action.tool, action.name), "markdown "+action.tool+" "+action.name)
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
			t.Fatalf("Markdown action %s/%s returned unclassified status %q", action.tool, action.name, status)
		}
		t.Logf("Markdown soak action tool=%s action=%s status=%s structured=%s", action.tool, action.name, status, response.Result.StructuredContent)
		receipt = append(receipt, fmt.Sprintf("action=%s/%s status=%s", action.tool, action.name, status))
	}
	if summary.total != realMCPExpectedActionCount || summary.succeeded+summary.capabilityUnsupported != summary.total {
		t.Fatalf("Markdown 36-action ledger is incomplete: total=%d success=%d legal_empty=%d unsupported=%d actions=%v", summary.total, summary.succeeded, summary.legalEmpty, summary.capabilityUnsupported, summary.unsupportedActions)
	}
	if !trackRealMCPProcessTree(t, mcpPID, "before-soak", tracked) {
		t.Fatalf("capture process tree before Markdown soak idle failed")
	}
	receipt = append(receipt, fmt.Sprintf("action_total=%d", summary.total), fmt.Sprintf("action_success=%d", summary.succeeded), fmt.Sprintf("action_legal_empty=%d", summary.legalEmpty), fmt.Sprintf("action_capability_unsupported=%d", summary.capabilityUnsupported))

	if err := markdownSoakAssertIdentity(mcpPID, startToken); err != nil {
		t.Fatalf("process identity before idle: %v", err)
	}
	idleStarted := time.Now()
	t.Logf("Markdown soak idle_begin=%s required=%s pid=%d start=%s", idleStarted.Format(time.RFC3339Nano), markdownWindowsARM64ProcessARM64Soak15mIdle, mcpPID, startToken)
	receipt = append(receipt, fmt.Sprintf("idle_begin=%s", idleStarted.Format(time.RFC3339Nano)))
	for {
		elapsed := time.Since(idleStarted)
		if elapsed >= markdownWindowsARM64ProcessARM64Soak15mIdle {
			break
		}
		wait := time.Minute
		if remaining := markdownWindowsARM64ProcessARM64Soak15mIdle - elapsed; remaining < wait {
			wait = remaining
		}
		time.Sleep(wait)
		if err := markdownSoakAssertIdentity(mcpPID, startToken); err != nil {
			t.Fatalf("process identity during idle after %s: %v", time.Since(idleStarted).Round(time.Second), err)
		}
		t.Logf("Markdown soak idle heartbeat elapsed=%s pid=%d start=%s", time.Since(idleStarted).Round(time.Second), mcpPID, startToken)
	}
	idleEnded := time.Now()
	if idleEnded.Sub(idleStarted) < markdownWindowsARM64ProcessARM64Soak15mIdle {
		t.Fatalf("idle duration=%s, want at least %s", idleEnded.Sub(idleStarted), markdownWindowsARM64ProcessARM64Soak15mIdle)
	}
	if err := markdownSoakAssertIdentity(mcpPID, startToken); err != nil {
		t.Fatalf("process identity at idle boundary: %v", err)
	}
	trackRealMCPProcessTree(t, mcpPID, "post-soak-boundary", tracked)
	t.Logf("Markdown soak idle_end=%s duration=%s pid=%d start=%s", idleEnded.Format(time.RFC3339Nano), idleEnded.Sub(idleStarted).Round(time.Millisecond), mcpPID, startToken)
	receipt = append(receipt, fmt.Sprintf("idle_end=%s", idleEnded.Format(time.RFC3339Nano)), fmt.Sprintf("idle_duration=%s", idleEnded.Sub(idleStarted).Round(time.Millisecond)), "post_idle_identity=same_pid_and_start_token")

	watcher, err := markdownSoakSelectWatcher(wirePath, fixtureRoot)
	if err != nil {
		receipt = append(receipt,
			"status=failed",
			"watcher_status=server_did_not_request_markdown_fs_watcher_create",
			fmt.Sprintf("watcher_root_cause=%s", strings.ReplaceAll(err.Error(), "\n", "\\n")),
			fmt.Sprintf("server_watcher_create_count=%d", markdownSoakWireMethodCountsFromFile(wirePath, runtimeMarkdownWatcherCreateMethod)),
		)
		t.Fatalf("select real Markdown watcher from wire log: %v", err)
	}
	receipt = append(receipt, fmt.Sprintf("watcher_id=%d", watcher.ID), fmt.Sprintf("watcher_uri=%s", watcher.URI), fmt.Sprintf("watcher_target=%s", watcher.Target))
	counts, err := markdownSoakDriveWatcher(t, wirePath, watcher)
	if err != nil {
		t.Fatalf("drive real Markdown watcher create/change/delete/recreate at soak boundary: %v", err)
	}
	receipt = append(receipt,
		fmt.Sprintf("boundary_onchange_create=%d", counts["create"]),
		fmt.Sprintf("boundary_onchange_change=%d", counts["change"]),
		fmt.Sprintf("boundary_onchange_delete=%d", counts["delete"]),
		"boundary_watcher_create_change_delete_recreate=true",
	)
	if !trackRealMCPProcessTree(t, mcpPID, "post-watcher-boundary", tracked) {
		t.Fatalf("capture process tree after watcher boundary failed")
	}
	if err := markdownSoakAssertIdentity(mcpPID, startToken); err != nil {
		t.Fatalf("process identity after watcher boundary: %v", err)
	}

	postSemantic := client.callTool(t, "structure", realMCPWindowsToolArguments(server.languageID, fixtureRoot, "structure", "document_symbol", map[string]any{
		"action": "document_symbol", "file_path": fixture.targetFile, "max_results": 20,
	}))
	requireRealMCPActionResult(t, postSemantic, true, "", false, realMCPActionCapabilityKey("structure", "document_symbol"), false, "markdown post-soak document_symbol")
	if err := markdownSoakAssertIdentity(mcpPID, startToken); err != nil {
		t.Fatalf("process identity after post-soak semantic request: %v", err)
	}
	receipt = append(receipt, "post_soak_semantic=document_symbol:success", "post_boundary_identity=same_pid_and_start_token")

	if !trackRealMCPProcessTree(t, mcpPID, "before-shutdown", tracked) {
		t.Fatalf("capture process tree before Markdown shutdown failed")
	}
	shutdown := client.call(t, "shutdown", map[string]any{})
	shutdownSent = true
	if shutdown.Error != nil || shutdown.Result.IsError {
		t.Fatalf("Markdown shutdown response invalid: %#v", shutdown)
	}
	receipt = append(receipt, "shutdown=response_ok", "exit=sent_by_client_close")
	client.close(t)
	clientClosed = true

	records, err := markdownSoakReadWire(wirePath)
	if err != nil {
		t.Fatalf("read final Markdown wire receipt: %v", err)
	}
	serverCreate, serverDelete := markdownSoakWireMethodCounts(records, runtimeMarkdownWatcherCreateMethod), markdownSoakWireMethodCounts(records, runtimeMarkdownWatcherDeleteMethod)
	onChange := markdownSoakOnChangeCounts(records, watcher.ID)
	if serverCreate == 0 || serverDelete == 0 {
		t.Fatalf("Markdown watcher protocol lifecycle missing create/delete: create=%d delete=%d wire=%s", serverCreate, serverDelete, wirePath)
	}
	for _, kind := range []string{"create", "change", "delete"} {
		if onChange[kind] < 1 {
			t.Fatalf("Markdown watcher onChange missing kind=%s counts=%v wire=%s", kind, onChange, wirePath)
		}
	}
	if onChange["create"] < 2 {
		t.Fatalf("Markdown watcher onChange did not prove recreate (create count=%d): wire=%s", onChange["create"], wirePath)
	}
	receipt = append(receipt, fmt.Sprintf("protocol_watcher_create=%d", serverCreate), fmt.Sprintf("protocol_watcher_delete=%d", serverDelete), fmt.Sprintf("final_onchange_counts=%v", onChange), "shutdown_exit_zero_residual_pending=true")
	receipt = append(receipt, "status=pass")
	t.Logf("Markdown soak completed action_total=%d success=%d legal_empty=%d capability_unsupported=%d idle=%s watcher_create=%d watcher_delete=%d onchange=%v receipt=%s wire=%s elapsed=%s", summary.total, summary.succeeded, summary.legalEmpty, summary.capabilityUnsupported, idleEnded.Sub(idleStarted).Round(time.Millisecond), serverCreate, serverDelete, onChange, receiptPath, wirePath, time.Since(startedAt).Round(time.Millisecond))
}

type markdownSoakWireRecord struct {
	Direction string          `json:"direction"`
	Method    string          `json:"method"`
	Payload   json.RawMessage `json:"payload"`
	Error     string          `json:"error,omitempty"`
}

type markdownSoakWatcher struct {
	ID     int
	URI    string
	Target string
}

func slicesContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func verifyMarkdownSoakLockedNode(t *testing.T, productRoot, nodePath string, asset installer.WindowsLockedAsset) {
	t.Helper()
	payload := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir, "node-runtime", asset.Version, asset.Architecture, strings.ToLower(asset.SHA256), "payload.zip")
	digest, err := sha256File(payload)
	if err != nil {
		t.Fatalf("hash locked Node payload %s: %v", payload, err)
	}
	if !strings.EqualFold(digest, asset.SHA256) {
		t.Fatalf("locked Node payload SHA256=%s, want=%s", digest, asset.SHA256)
	}
	contents, err := os.ReadFile(nodePath)
	if err != nil {
		t.Fatalf("read locked Node executable: %v", err)
	}
	digestSum := sha256.Sum256(contents)
	t.Logf("locked Node verified version=%s url=%s sha256=%s payload_sha256=%s executable_sha256=%s path=%s", asset.Version, asset.URL, asset.SHA256, digest, hex.EncodeToString(digestSum[:]), nodePath)
	image, err := pe.NewFile(bytes.NewReader(contents))
	if err != nil {
		t.Fatalf("parse locked Node PE %s: %v", nodePath, err)
	}
	defer image.Close()
	if image.FileHeader.Machine != installer.WindowsImageFileMachineARM64 {
		t.Fatalf("locked Node PE machine=0x%04x, want ARM64 0x%04x", image.FileHeader.Machine, installer.WindowsImageFileMachineARM64)
	}
}

func markdownSoakAssertIdentity(pid int, wantStart string) error {
	alive, err := processAliveForE2E(pid)
	if err != nil {
		return fmt.Errorf("inspect PID %d: %w", pid, err)
	}
	if !alive {
		return fmt.Errorf("PID %d is not alive", pid)
	}
	gotStart, err := windowsGoplsProcessStartIdentity(pid)
	if err != nil {
		return fmt.Errorf("read PID %d start identity: %w", pid, err)
	}
	if gotStart != wantStart {
		return fmt.Errorf("PID %d start identity changed from %s to %s", pid, wantStart, gotStart)
	}
	return nil
}

func markdownSoakReadWire(path string) ([]markdownSoakWireRecord, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var records []markdownSoakWireRecord
	for lineNumber, line := range strings.Split(string(contents), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record markdownSoakWireRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return nil, fmt.Errorf("decode wire line %d: %w", lineNumber+1, err)
		}
		records = append(records, record)
	}
	return records, nil
}

func markdownSoakWireMethodCounts(records []markdownSoakWireRecord, method string) int {
	count := 0
	for _, record := range records {
		if record.Direction == "server_request" && record.Method == method {
			count++
		}
	}
	return count
}

func markdownSoakWireMethodCountsFromFile(path, method string) int {
	records, err := markdownSoakReadWire(path)
	if err != nil {
		return 0
	}
	return markdownSoakWireMethodCounts(records, method)
}

func markdownSoakOnChangeCounts(records []markdownSoakWireRecord, watcherID int) map[string]int {
	counts := map[string]int{"create": 0, "change": 0, "delete": 0}
	for _, record := range records {
		if record.Direction != "client_request" || record.Method != runtimeMarkdownWatcherOnChangeMethod {
			continue
		}
		var payload struct {
			ID   int    `json:"id"`
			Kind string `json:"kind"`
		}
		if json.Unmarshal(record.Payload, &payload) == nil && payload.ID == watcherID {
			counts[payload.Kind]++
		}
	}
	return counts
}

func markdownSoakSelectWatcher(wirePath, fixtureRoot string) (markdownSoakWatcher, error) {
	records, err := markdownSoakReadWire(wirePath)
	if err != nil {
		return markdownSoakWatcher{}, err
	}
	for _, record := range records {
		if record.Direction != "server_request" || record.Method != runtimeMarkdownWatcherCreateMethod {
			continue
		}
		var payload struct {
			ID      int    `json:"id"`
			URI     string `json:"uri"`
			Options struct {
				IgnoreCreate bool `json:"ignoreCreate"`
				IgnoreChange bool `json:"ignoreChange"`
				IgnoreDelete bool `json:"ignoreDelete"`
			} `json:"options"`
		}
		if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.ID < 0 || strings.TrimSpace(payload.URI) == "" || payload.Options.IgnoreCreate || payload.Options.IgnoreChange || payload.Options.IgnoreDelete {
			continue
		}
		target, err := markdownSoakPathFromURI(payload.URI)
		if err != nil || !markdownSoakWithin(fixtureRoot, target) {
			continue
		}
		if _, err := os.Stat(target); err != nil {
			continue
		}
		return markdownSoakWatcher{ID: payload.ID, URI: payload.URI, Target: target}, nil
	}
	return markdownSoakWatcher{}, fmt.Errorf("no non-ignored watcher/create request rooted in fixture %s; wire=%s", fixtureRoot, wirePath)
}

func markdownSoakDriveWatcher(t *testing.T, wirePath string, watcher markdownSoakWatcher) (map[string]int, error) {
	t.Helper()
	info, err := os.Stat(watcher.Target)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{"create": 0, "change": 0, "delete": 0}
	var target string
	if info.IsDir() {
		target = filepath.Join(watcher.Target, "windows-arm64-process-arm64-markdown-soak-15m-child.md")
		if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	} else {
		target = watcher.Target
	}
	write := func(content string, kind string) error {
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			return err
		}
		if err := markdownSoakWaitForKind(wirePath, watcher.ID, kind, counts[kind]); err != nil {
			return err
		}
		records, err := markdownSoakReadWire(wirePath)
		if err != nil {
			return err
		}
		counts = markdownSoakOnChangeCounts(records, watcher.ID)
		return nil
	}
	if err := write("windows-arm64-process-arm64-markdown-soak-15m-create\n", "create"); err != nil {
		return nil, err
	}
	if err := write("windows-arm64-process-arm64-markdown-soak-15m-change\n", "change"); err != nil {
		return nil, err
	}
	if err := os.Remove(target); err != nil {
		return nil, err
	}
	if err := markdownSoakWaitForKind(wirePath, watcher.ID, "delete", counts["delete"]); err != nil {
		return nil, err
	}
	records, err := markdownSoakReadWire(wirePath)
	if err != nil {
		return nil, err
	}
	counts = markdownSoakOnChangeCounts(records, watcher.ID)
	if err := write("windows-arm64-process-arm64-markdown-soak-15m-recreate\n", "create"); err != nil {
		return nil, err
	}
	if err := write("windows-arm64-process-arm64-markdown-soak-15m-final-change\n", "change"); err != nil {
		return nil, err
	}
	return counts, nil
}

func markdownSoakWaitForKind(wirePath string, watcherID int, kind string, previous int) error {
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		records, err := markdownSoakReadWire(wirePath)
		if err != nil {
			lastErr = err
		} else {
			counts := markdownSoakOnChangeCounts(records, watcherID)
			if counts[kind] > previous {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		return fmt.Errorf("wait for watcher id=%d kind=%s: %w", watcherID, kind, lastErr)
	}
	return fmt.Errorf("wait for watcher id=%d kind=%s timed out after 30s", watcherID, kind)
}

func markdownSoakPathFromURI(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(parsed.Scheme, "file") || parsed.Host != "" {
		return "", fmt.Errorf("watcher URI is not a local file URI: %q", raw)
	}
	pathValue, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil {
		return "", err
	}
	if len(pathValue) >= 3 && pathValue[0] == '/' && pathValue[2] == ':' {
		pathValue = pathValue[1:]
	}
	if strings.TrimSpace(pathValue) == "" {
		return "", errors.New("watcher URI path is empty")
	}
	return filepath.Clean(filepath.FromSlash(pathValue)), nil
}

func markdownSoakWithin(root, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative)
}

func markdownSoakErrorKind(err error) string {
	if err == nil {
		return ""
	}
	var permissionErr *securefs.WindowsPermissionError
	if errors.As(err, &permissionErr) && permissionErr != nil && (permissionErr.Win32Code() == 5 || permissionErr.Win32Code() == 1314) {
		return fmt.Sprintf("authorization_required:win32_%d", permissionErr.Win32Code())
	}
	return err.Error()
}
