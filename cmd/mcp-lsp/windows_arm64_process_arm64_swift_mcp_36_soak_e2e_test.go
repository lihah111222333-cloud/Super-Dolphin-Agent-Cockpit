//go:build windows && arm64 && e2e

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"golang.org/x/sys/windows"
)

const (
	swiftMCP36ProductRootEnv      = "MCP_LSP_SWIFT_MCP_PRODUCT_ROOT"
	swiftMCP36EvidenceDirEnv      = "MCP_LSP_SWIFT_MCP_EVIDENCE_DIR"
	swiftMCP36PrecheckEnv         = "MCP_LSP_SWIFT_MCP_PRECHECK"
	swiftMCP36Idle                = 15 * time.Minute
	swiftMCP36ManagerIdle         = 17 * time.Minute
	swiftMCP36PrecheckIdle        = 30 * time.Second
	swiftMCP36ProductionMin       = 15 * time.Minute
	swiftMCPCallTimeout           = 2 * time.Minute
	swiftMCPCompletionCallTimeout = 4 * time.Minute
	swiftMCPCancelGrace           = 2 * time.Second
)

// TestWindowsARM64ProcessARM64SwiftMCP36SoakE2E 通过生产 mcp-lsp 二进制、已校验
// Swift 6.3.3 ARM64 cohort 和真实 stdio MCP，逐项记录七个公开工具族的 36 个 action。
// capability_unsupported、method-not-found、空结果和运行时错误都单独记账，绝不计入成功。
func TestWindowsARM64ProcessARM64SwiftMCP36SoakE2E(t *testing.T) {
	if os.Getenv("MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E") != "1" {
		t.Skip("set MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E=1 to enable the Swift MCP 36-action E2E")
	}
	if os.Getenv("MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E_PRODUCTS") != "swift" {
		t.Skip("set MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E_PRODUCTS=swift to select the Swift MCP 36-action E2E")
	}
	precheck := os.Getenv(swiftMCP36PrecheckEnv) == "1"
	topologyProbe := os.Getenv("MCP_LSP_SWIFT_MCP_TOPOLOGY_PROBE") == "1"
	completionProbe := os.Getenv("MCP_LSP_SWIFT_MCP_COMPLETION_PROBE") == "1"
	replacementBarrierProbe := os.Getenv("MCP_LSP_SWIFT_MCP_REPLACEMENT_BARRIER_PROBE") == "1"
	if testing.Short() && !precheck {
		t.Skip("the formal Swift 15-minute lifecycle proof is disabled by -short; set the explicit Swift PRECHECK env for a bounded precheck")
	}
	if swiftMCP36Idle < swiftMCP36ProductionMin || swiftMCP36ManagerIdle <= swiftMCP36Idle {
		t.Fatalf("invalid Swift lifecycle windows: production_min=%s proof=%s manager=%s", swiftMCP36ProductionMin, swiftMCP36Idle, swiftMCP36ManagerIdle)
	}
	proofIdle := swiftMCP36Idle
	if precheck {
		proofIdle = swiftMCP36PrecheckIdle
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("Swift MCP E2E requires Windows ARM64 test process, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	if host.OS != installer.WindowsHostOSWindows || host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("Swift MCP E2E requires Windows native ARM64/process ARM64, got os=%q native=%q process=%q build=%d", host.OS, host.NativeArch, host.ProcessArch, host.WindowsBuild)
	}

	repoRoot := realNodeRepoRoot(t)
	productRoot := strings.TrimSpace(os.Getenv(swiftMCP36ProductRootEnv))
	if productRoot == "" {
		t.Fatalf("%s is required: pass the already verified Swift product root; no network provisioning is performed by this E2E", swiftMCP36ProductRootEnv)
	}
	productRoot, err = filepath.Abs(productRoot)
	if err != nil {
		t.Fatalf("resolve Swift MCP product root: %v", err)
	}
	if info, statErr := os.Stat(productRoot); statErr != nil || !info.IsDir() {
		t.Fatalf("Swift MCP product root is not a directory: %s (%v)", secureSwiftMCPPath(productRoot), statErr)
	}
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict Swift MCP product root ACL (authorization_required is preserved): %v", err)
	}

	evidenceDir := strings.TrimSpace(os.Getenv(swiftMCP36EvidenceDirEnv))
	if evidenceDir == "" {
		evidenceDir = filepath.Join(repoRoot, ".build-cache", "swift-sourcekit-lsp-proof-20260815", "e2e-mcp-swift-36")
	}
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatalf("create Swift MCP evidence directory: %v", err)
	}
	receiptPath := filepath.Join(evidenceDir, "windows-arm64-process-arm64-swift-mcp-36-soak-receipt.log")
	wirePath := filepath.Join(evidenceDir, "windows-arm64-process-arm64-swift-mcp-36-wire.jsonl")
	_ = os.Remove(wirePath)
	_ = os.Remove(receiptPath)
	receipt := []string{
		"test=windows-arm64-process-arm64-swift-mcp-36-soak",
		"phase=" + map[bool]string{true: "PRECHECK_ONLY", false: "FORMAL"}[precheck],
		"status=started",
		fmt.Sprintf("started_at=%s", startedAt.Format(time.RFC3339Nano)),
		fmt.Sprintf("host_os=%s", host.OS),
		fmt.Sprintf("host_native_arch=%s", host.NativeArch),
		fmt.Sprintf("host_process_arch=%s", host.ProcessArch),
		fmt.Sprintf("host_windows_version=%s", host.WindowsVersion),
		fmt.Sprintf("host_windows_build=%d", host.WindowsBuild),
		"process_arch_is_diagnostic_only=true",
		"acl_win32_5_1314=typed_securefs_authorization_required_preserved",
		"network_provisioning=not_requested_cache_only",
		fmt.Sprintf("product_root_digest=%s", swiftMCPPathDigest(productRoot)),
		"product_root_path_policy=private_cohort_path_not_recorded",
		"receipt_paths_redacted=true",
		"wire_payloads=hash_and_shape_only",
		"wire_path=e2e-mcp-swift-36/windows-arm64-process-arm64-swift-mcp-36-wire.jsonl",
		fmt.Sprintf("manager_idle_timeout=%s", swiftMCP36ManagerIdle),
		fmt.Sprintf("proof_idle_duration=%s", proofIdle),
		fmt.Sprintf("production_idle_minimum=%s", swiftMCP36ProductionMin),
		"idle_timeout_headroom=process_identity_sampling_only",
	}
	if replacementBarrierProbe {
		receipt = append(receipt, "mode=windows_arm64_process_arm64_swift_patch_replacement_barrier", "lifecycle=bounded_no_idle", "barrier_proof_requires_old_shutdown_exit_wait_before_new_start=true")
	}
	var wireFile *os.File
	var wire *json.Encoder
	defer func() {
		if wireFile != nil {
			_ = wireFile.Close()
		}
		receipt = append(receipt,
			fmt.Sprintf("finished_at=%s", time.Now().Format(time.RFC3339Nano)),
			fmt.Sprintf("elapsed=%s", time.Since(startedAt).Round(time.Millisecond)),
		)
		if writeErr := os.WriteFile(receiptPath, []byte(strings.Join(receipt, "\n")+"\n"), 0o600); writeErr != nil {
			t.Logf("write Swift MCP receipt %s: %v", secureSwiftMCPPath(receiptPath), writeErr)
		}
	}()
	wireFile, err = os.OpenFile(wirePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("open Swift MCP wire evidence: %v", err)
	}
	wire = json.NewEncoder(wireFile)

	cacheRoot := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir)
	resolved, err := installer.ResolveWindowsRuntimeDependency(installer.WindowsRuntimeDependencyProductSwiftSourceKitLS, cacheRoot)
	if err != nil {
		receipt = append(receipt, "resolver_cache_hit=false", "root_cause=swift_resolver_cache_invalid")
		t.Fatalf("resolve verified Swift cohort cache: %v", err)
	}
	if !swiftMCPPathWithin(productRoot, resolved.RootPath) || !swiftMCPPathWithin(resolved.RootPath, resolved.ServerPath) {
		t.Fatalf("resolved Swift cohort escaped product root: root=%s server=%s", secureSwiftMCPPath(resolved.RootPath), secureSwiftMCPPath(resolved.ServerPath))
	}
	vclibsRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalPath(productRoot)
	if err != nil {
		receipt = append(receipt, "vclibs_ready=false", "root_cause=vclibs_resolver_cache_invalid")
		t.Fatalf("resolve verified app-local VCLibs cohort: %v", err)
	}
	receipt = append(receipt,
		"resolver_cache_hit=true",
		"resolver_cache_http_requests=0",
		fmt.Sprintf("swift_cohort_root_relative=%s", swiftMCPRelativePath(productRoot, resolved.RootPath)),
		fmt.Sprintf("swift_server_relative=%s", swiftMCPRelativePath(productRoot, resolved.ServerPath)),
		fmt.Sprintf("vclibs_ready_relative=%s", swiftMCPRelativePath(productRoot, vclibsRoot)),
		"swift_provision_receipt_identity=verified_before_mcp_start",
	)
	// mcp-lsp 的 tools/list 会先用父进程 PATH 判断是否存在语义服务器；把
	// 已校验 cohort 的 bin 置于测试父 PATH 首位，随后真正的 Swift 子进程仍由
	// runtimeServerWindowsSwiftEnvironment 过滤为 owned runtime/toolchain/VCLibs/System32。
	ownedToolchainBin := installer.WindowsSwiftSourceKitLSPToolchainBin(resolved.RootPath)
	t.Setenv("PATH", ownedToolchainBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	sharedLSPCache := filepath.Join(evidenceDir, "shared-lsp-cache")
	if err := os.MkdirAll(sharedLSPCache, 0o700); err != nil {
		t.Fatalf("create isolated Swift MCP shared LSP cache: %v", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(sharedLSPCache, 0o700); err != nil {
		t.Fatalf("restrict isolated Swift MCP shared LSP cache ACL: %v", err)
	}
	t.Setenv("AGENT_LSP_SHARED_CACHE_DIR", sharedLSPCache)
	receipt = append(receipt, "mcp_parent_path=owned_swift_toolchain_bin_first", "child_path_policy=runtime_allowlist_enforced")
	receipt = append(receipt, "shared_lsp_cache=isolated_evidence_cache")

	fixtureParentRoot := t.TempDir()
	server := swiftMCP36FixtureServer()
	fixture := writeRealMCPBinSourceFixture(t, fixtureParentRoot, server)
	workspaceRoot := fixture.workDir
	// AST action 也必须读取仓库快照的副本；任何 patch_edit 都只能命中 workspace 副本。
	astFile := filepath.Join(workspaceRoot, ".mcp-ast", "ast_fixture.js")
	copyRealMCPBinSourceFile(t, fixture.sourceRoot, "javascript/module-examples/top-level-await/main.js", astFile)
	actions := realMCPActionSpecs(server, fixture, astFile)
	if err := validateRealMCPActionClosure(actions); err != nil {
		t.Fatalf("Swift MCP action closure: %v", err)
	}
	if replacementBarrierProbe {
		probeActions := make([]realMCPActionSpec, 0, 5)
		for _, action := range actions {
			switch action.name {
			case "open_file", "replace_range", "rename", "format", "completion":
				probeActions = append(probeActions, action)
			}
		}
		actions = probeActions
	} else if completionProbe {
		probeActions := make([]realMCPActionSpec, 0, 3)
		for _, action := range actions {
			if action.name == "open_file" {
				probeActions = append(probeActions, action)
			}
		}
		for _, action := range actions {
			if action.name == "completion" {
				probeActions = append(probeActions, action, action)
				break
			}
		}
		actions = probeActions
	}
	receipt = append(receipt,
		fmt.Sprintf("fixture_root_digest=%s", swiftMCPPathDigest(workspaceRoot)),
		"fixture=bin/LSP/test/swift",
		"fixture_workspace=isolated_temp_copy",
		fmt.Sprintf("action_total=%d", len(actions)),
		"action_contract=unsupported_empty_runtime_failure_are_not_success",
	)

	t.Setenv("MCP_LSP_IDLE_TIMEOUT", swiftMCP36ManagerIdle.String())
	binary := buildRealMcpLSPBinary(t, repoRoot)
	receipt = append(receipt, "binary=temporary_bundled_windows_arm64_mcp_lsp", "binary_path_policy=not_recorded")
	client := startRealMcpLSPBinary(t, ctx, binary, workspaceRoot, repoRoot, "", "", productRoot)
	mcpPID := client.cmd.Process.Pid
	startToken, err := windowsGoplsProcessStartIdentity(mcpPID)
	if err != nil {
		client.close(t)
		t.Fatalf("capture MCP PID %d start identity: %v", mcpPID, err)
	}
	receipt = append(receipt,
		fmt.Sprintf("mcp_pid=%d", mcpPID),
		fmt.Sprintf("mcp_start_token_digest=%s", swiftMCPTokenDigest(startToken)),
		"mcp_start_token_recorded_exactly=true",
	)

	tracked := map[realMCPProcessKey]realMCPProcessIdentity{
		{PID: mcpPID, StartToken: startToken}: {PID: mcpPID, StartToken: startToken, Name: "mcp-lsp", Language: "swift"},
	}
	clientClosed := false
	shutdownSent := false
	defer func() {
		if client == nil || client.cmd == nil || clientClosed {
			return
		}
		if !shutdownSent {
			if _, _, shutdownErr := swiftMCPCall(t, client, wire, "shutdown", map[string]any{}, "lifecycle/shutdown-recovery"); shutdownErr != nil {
				receipt = append(receipt, "shutdown_recovery=write_or_read_error")
			}
		}
		_ = trackRealMCPProcessTree(t, mcpPID, "swift-final-before-close", tracked)
		client.close(t)
		clientClosed = true
	}()

	var sequence int64
	initialize, initializeRaw, err := swiftMCPCall(t, client, wire, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "super-dolphin-windows-arm64-process-arm64-swift-mcp-36", "version": "1"},
	}, "lifecycle/initialize")
	if err != nil || initialize.Error != nil {
		receipt = append(receipt, "initialize=failed", "status=initialize_failure", "root_cause="+swiftMCPRedact(errString(err, initialize.Error)))
		t.Fatalf("Swift MCP initialize failed: %v", err)
	}
	_ = initializeRaw
	if err := swiftMCPNotify(t, client, wire, "notifications/initialized", map[string]any{}, &sequence); err != nil {
		receipt = append(receipt, "initialized=write_error")
		t.Fatalf("Swift MCP initialized notification failed: %v", err)
	}
	toolsList, toolsListRaw, err := swiftMCPCall(t, client, wire, "tools/list", map[string]any{}, "lifecycle/tools-list")
	if err != nil {
		t.Fatalf("Swift MCP tools/list failed: %v", err)
	}
	requireRealMCPToolFamilies(t, toolsListRaw)
	receipt = append(receipt, "initialize=success", "initialized=sent", "tools_list=seven_public_families_exact")
	_ = toolsList

	var summary swiftMCPActionSummary
	var sourceKit realMCPProcessIdentity
	sourceKitObserved := false
	for _, action := range actions {
		if (topologyProbe || completionProbe) && action.name != "open_file" && action.name != "format" && action.name != "completion" {
			continue
		}
		if replacementBarrierProbe && action.name != "open_file" && action.name != "replace_range" && action.name != "rename" && action.name != "format" && action.name != "completion" {
			continue
		}
		key := action.tool + "/" + action.name
		if replacementBarrierProbe {
			if !trackRealMCPProcessTree(t, mcpPID, "swift-before-"+key, tracked) {
				t.Fatalf("capture Swift process tree before action %s failed", key)
			}
			swiftMCPAppendSourceKitObservations(&receipt, tracked, mcpPID, "before/"+key)
			if count := swiftMCPSourceKitCount(tracked, mcpPID); count > 1 {
				receipt = append(receipt, fmt.Sprintf("sourcekit_concurrent_count phase=before action=%s count=%d", key, count))
				t.Fatalf("Swift replacement barrier observed %d owned SourceKit processes before %s", count, key)
			}
		}
		if action.name == "completion" {
			_ = trackRealMCPProcessTree(t, mcpPID, "swift-before-completion", tracked)
			if before, ok := swiftMCPFindSourceKitProcess(tracked); ok {
				cpu, cpuErr := swiftMCPProcessCPUTime(before.PID)
				if cpuErr != nil {
					receipt = append(receipt, "sourcekit_completion_before_cpu=unavailable")
				} else {
					receipt = append(receipt, fmt.Sprintf("sourcekit_completion_before pid=%d start_token_digest=%s cpu_ns=%d", before.PID, swiftMCPTokenDigest(before.StartToken), cpu.Nanoseconds()))
				}
			}
		}
		var response mcpLSPBinaryResponse
		var callErr error
		actionWorkspaceRoot := swiftMCP36ActionWorkspaceRoot(fixture, action, workspaceRoot)
		requestArgs := realMCPWindowsToolArguments(server.languageID, actionWorkspaceRoot, action.tool, action.name, action.args)
		response, _, callErr = swiftMCPCall(t, client, wire, "tools/call", map[string]any{
			"name": action.tool, "arguments": requestArgs, "_cwd": actionWorkspaceRoot, "_workspaceRoots": []string{actionWorkspaceRoot},
		}, key)
		status := swiftMCPClassifyAction(action.name, response, callErr)
		detail := swiftMCPResponseDetail(response, callErr)
		summary.total++
		summary.add(key, status)
		receipt = append(receipt, fmt.Sprintf("action=%s status=%s code=%s non_empty=%t detail=%s", key, status, swiftMCPResponseCode(response), swiftMCPResponseNonEmpty(response), swiftMCPRedact(detail)))
		t.Logf("Swift MCP action=%s status=%s code=%s non_empty=%t", key, status, swiftMCPResponseCode(response), swiftMCPResponseNonEmpty(response))
		if !trackRealMCPProcessTree(t, mcpPID, "swift-action-"+key, tracked) {
			t.Fatalf("capture Swift process tree after action %s failed", key)
		}
		swiftMCPAppendSourceKitObservations(&receipt, tracked, mcpPID, key)
		if replacementBarrierProbe {
			if count := swiftMCPSourceKitCount(tracked, mcpPID); count > 1 {
				receipt = append(receipt, fmt.Sprintf("sourcekit_concurrent_count phase=after action=%s count=%d", key, count))
				t.Fatalf("Swift replacement barrier observed %d owned SourceKit processes after %s", count, key)
			}
		}
		if action.name == "completion" {
			if after, ok := swiftMCPFindSourceKitProcess(tracked); ok {
				cpu, cpuErr := swiftMCPProcessCPUTime(after.PID)
				if cpuErr != nil {
					receipt = append(receipt, "sourcekit_completion_after_cpu=unavailable")
				} else {
					receipt = append(receipt, fmt.Sprintf("sourcekit_completion_after pid=%d start_token_digest=%s cpu_ns=%d", after.PID, swiftMCPTokenDigest(after.StartToken), cpu.Nanoseconds()))
				}
			} else {
				receipt = append(receipt, "sourcekit_completion_after=missing_or_rotated")
			}
		}
		if current, ok := swiftMCPFindSourceKitProcess(tracked); ok {
			if !sourceKitObserved {
				sourceKit = current
				sourceKitObserved = true
				receipt = append(receipt, fmt.Sprintf("sourcekit_sample action=%s pid=%d start_token_digest=%s", key, sourceKit.PID, swiftMCPTokenDigest(sourceKit.StartToken)))
			} else if current.PID != sourceKit.PID || current.StartToken != sourceKit.StartToken {
				if err := swiftMCPValidateSourceKitRotation(tracked, sourceKit, current, mcpPID); err != nil {
					t.Fatalf("Swift SourceKit PID/start identity changed after action %s: %v", key, err)
				}
				receipt = append(receipt, fmt.Sprintf("sourcekit_rotation action=%s old_pid=%d new_pid=%d command_sha256=%s", key, sourceKit.PID, current.PID, current.CommandSHA256))
				sourceKit = current
			}
			receipt = append(receipt, fmt.Sprintf("sourcekit_action_sample action=%s pid=%d start_token_digest=%s", key, current.PID, swiftMCPTokenDigest(current.StartToken)))
		} else if sourceKitObserved {
			t.Fatalf("Swift SourceKit process disappeared after action %s", key)
		}
	}
	probeOnly := topologyProbe || completionProbe || replacementBarrierProbe
	if !probeOnly && (summary.total != realMCPExpectedActionCount || summary.classified() != realMCPExpectedActionCount) {
		t.Fatalf("Swift MCP action ledger is not exactly classified: total=%d classified=%d want=%d", summary.total, summary.classified(), realMCPExpectedActionCount)
	}
	receipt = append(receipt,
		fmt.Sprintf("action_success=%d", summary.success),
		fmt.Sprintf("action_capability_unsupported=%d", summary.unsupported),
		fmt.Sprintf("action_legal_empty=%d", summary.legalEmpty),
		fmt.Sprintf("action_runtime_failure=%d", summary.runtimeFailures),
		fmt.Sprintf("action_supported_only=%t", summary.success == summary.total),
		fmt.Sprintf("action_classified_total=%d", summary.classified()),
		"action_unsupported_not_counted_as_pass=true",
		"action_empty_not_counted_as_pass=true",
	)
	if summary.runtimeFailures != 0 {
		receipt = append(receipt, "status=runtime_failure")
		t.Fatalf("Swift MCP 36-action matrix has runtime_failure=%d; runtime failures are not PASS", summary.runtimeFailures)
	}
	if probeOnly {
		if !trackRealMCPProcessTree(t, mcpPID, "swift-topology-before-shutdown", tracked) {
			t.Fatalf("capture Swift topology process tree before shutdown failed")
		}
		swiftMCPAppendTopologyReceipt(&receipt, tracked)
		shutdown, _, shutdownErr := swiftMCPCall(t, client, wire, "shutdown", map[string]any{}, "topology/shutdown")
		if shutdownErr != nil || shutdown.Error != nil || shutdown.Result.IsError {
			t.Fatalf("Swift topology probe shutdown failed: %v", shutdownErr)
		}
		shutdownSent = true
		if exitErr := swiftMCPExit(t, client, wire, &sequence); exitErr != nil {
			t.Fatalf("Swift topology probe exit failed: %v", exitErr)
		}
		clientClosed = true
		requireRealMCPProcessIdentitiesGone(t, tracked)
		probeActions := "open_file,format"
		if completionProbe {
			probeActions = "open_file,completion(cold),completion(warm)"
		} else if replacementBarrierProbe {
			probeActions = "open_file,replace_range,rename,format,completion"
		}
		receipt = append(receipt, "status=NON_PASS_probe_only", "probe_actions="+probeActions, "zero_residual=verified_pid_plus_start_token")
		return
	}
	if !trackRealMCPProcessTree(t, mcpPID, "swift-before-soak", tracked) {
		receipt = append(receipt, "process_tree_before_soak=error")
	}
	currentSourceKit, ok := swiftMCPFindSourceKitProcess(tracked)
	if !ok {
		receipt = append(receipt, "sourcekit_lsp_identity=missing", "status=sourcekit_identity_blocker")
		t.Fatalf("Swift MCP action phase did not expose a live sourcekit-lsp.exe descendant; host PID alone cannot prove language-server lifecycle")
	}
	if sourceKitObserved && (currentSourceKit.PID != sourceKit.PID || currentSourceKit.StartToken != sourceKit.StartToken) {
		if err := swiftMCPValidateSourceKitRotation(tracked, sourceKit, currentSourceKit, mcpPID); err != nil {
			t.Fatalf("Swift SourceKit PID/start identity changed before idle boundary: %v", err)
		}
		receipt = append(receipt, fmt.Sprintf("sourcekit_rotation boundary=before_idle old_pid=%d new_pid=%d command_sha256=%s", sourceKit.PID, currentSourceKit.PID, currentSourceKit.CommandSHA256))
	}
	sourceKit = currentSourceKit
	sourceKitObserved = true
	receipt = append(receipt,
		fmt.Sprintf("sourcekit_pid=%d", sourceKit.PID),
		fmt.Sprintf("sourcekit_start_token_digest=%s", swiftMCPTokenDigest(sourceKit.StartToken)),
		"sourcekit_start_token_recorded_exactly=true",
	)
	if err := swiftMCPAssertIdentity(sourceKit.PID, sourceKit.StartToken); err != nil {
		t.Fatalf("Swift sourcekit-lsp identity before idle boundary: %v", err)
	}

	if err := swiftMCPAssertIdentity(mcpPID, startToken); err != nil {
		receipt = append(receipt, "idle_identity_start=same_pid_start_failed", "status=idle_identity_failure")
		t.Fatalf("Swift MCP identity before idle boundary: %v", err)
	}
	idleStarted := time.Now()
	receipt = append(receipt, fmt.Sprintf("idle_begin=%s", idleStarted.Format(time.RFC3339Nano)), fmt.Sprintf("idle_required=%s", proofIdle))
	heartbeats := 0
	for {
		elapsed := time.Since(idleStarted)
		if elapsed >= proofIdle {
			break
		}
		wait := time.Minute
		if remaining := proofIdle - elapsed; remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		<-timer.C
		if err := swiftMCPAssertIdentity(mcpPID, startToken); err != nil {
			receipt = append(receipt, fmt.Sprintf("idle_identity_failure_elapsed=%s", time.Since(idleStarted).Round(time.Millisecond)), "status=idle_identity_failure")
			t.Fatalf("Swift MCP identity changed/died during idle after %s: %v", time.Since(idleStarted).Round(time.Second), err)
		}
		if err := swiftMCPAssertIdentity(sourceKit.PID, sourceKit.StartToken); err != nil {
			receipt = append(receipt, fmt.Sprintf("sourcekit_idle_identity_failure_elapsed=%s", time.Since(idleStarted).Round(time.Millisecond)), "status=sourcekit_identity_failure")
			t.Fatalf("Swift sourcekit-lsp identity changed/died during idle after %s: %v", time.Since(idleStarted).Round(time.Second), err)
		}
		heartbeats++
		receipt = append(receipt, fmt.Sprintf("sourcekit_idle_sample elapsed=%s pid=%d start_token_digest=%s", time.Since(idleStarted).Round(time.Millisecond), sourceKit.PID, swiftMCPTokenDigest(sourceKit.StartToken)))
		t.Logf("Swift MCP 36-action soak heartbeat elapsed=%s pid=%d start_token_digest=%s", time.Since(idleStarted).Round(time.Second), mcpPID, swiftMCPTokenDigest(startToken))
	}
	idleDuration := time.Since(idleStarted)
	receipt = append(receipt, fmt.Sprintf("idle_end=%s", time.Now().Format(time.RFC3339Nano)), fmt.Sprintf("idle_duration=%s", idleDuration.Round(time.Millisecond)), fmt.Sprintf("idle_heartbeats=%d", heartbeats), "idle_identity=same_pid_and_start_token")
	if idleDuration < proofIdle {
		t.Fatalf("Swift MCP idle duration=%s, want at least %s", idleDuration, proofIdle)
	}
	postIdle, _, postIdleErr := swiftMCPCall(t, client, wire, "tools/call", map[string]any{
		"name": "structure", "arguments": realMCPWindowsToolArguments(server.languageID, workspaceRoot, "structure", "document_symbol", map[string]any{"action": "document_symbol", "file_path": fixture.targetFile, "max_results": 20}),
		"_cwd": workspaceRoot, "_workspaceRoots": []string{workspaceRoot},
	}, "boundary/post_idle/structure/document_symbol")
	postIdleStatus := swiftMCPClassifyAction("document_symbol", postIdle, postIdleErr)
	postIdleNonEmpty := swiftMCPResponseNonEmpty(postIdle)
	postIdleSemanticOK := postIdleStatus == swiftMCPActionSuccess && postIdleNonEmpty
	receipt = append(receipt, fmt.Sprintf("post_idle_action=structure/document_symbol status=%s code=%s non_empty=%t", postIdleStatus, swiftMCPResponseCode(postIdle), postIdleNonEmpty), "post_idle_identity=same_pid_and_start_token")
	if !postIdleSemanticOK {
		t.Errorf("Swift post-idle semantic action structure/document_symbol status=%s non_empty=%t; want success with non-empty result", postIdleStatus, postIdleNonEmpty)
	}
	if err := swiftMCPAssertIdentity(mcpPID, startToken); err != nil {
		t.Fatalf("Swift MCP identity after post-idle semantic action: %v", err)
	}
	if err := swiftMCPAssertIdentity(sourceKit.PID, sourceKit.StartToken); err != nil {
		t.Fatalf("Swift sourcekit-lsp identity after post-idle semantic action: %v", err)
	}
	receipt = append(receipt, "sourcekit_post_idle_identity=same_pid_and_start_token")

	if !trackRealMCPProcessTree(t, mcpPID, "swift-before-shutdown", tracked) {
		receipt = append(receipt, "process_tree_before_shutdown=error")
	}
	shutdown, _, shutdownErr := swiftMCPCall(t, client, wire, "shutdown", map[string]any{}, "lifecycle/shutdown")
	if shutdownErr != nil || shutdown.Error != nil || shutdown.Result.IsError {
		receipt = append(receipt, "shutdown=response_failure", "shutdown_detail="+swiftMCPRedact(errString(shutdownErr, shutdown.Error)))
		t.Errorf("Swift MCP shutdown failed: %v", shutdownErr)
	} else {
		shutdownSent = true
		receipt = append(receipt, "shutdown=response_ok")
	}
	exitErr := swiftMCPExit(t, client, wire, &sequence)
	if exitErr != nil {
		receipt = append(receipt, "exit=process_wait_failure", "exit_detail="+swiftMCPRedact(exitErr.Error()))
		t.Errorf("Swift MCP exit failed: %v", exitErr)
	} else {
		receipt = append(receipt, "exit=sent_and_process_wait_zero")
		clientClosed = true
	}
	if exitErr != nil {
		return
	}

	if len(tracked) <= 1 {
		receipt = append(receipt, "process_tree_descendants=missing", "zero_residual=false")
		t.Errorf("Swift MCP process tree captured no descendant; PID-only zero-residual proof is incomplete")
	} else {
		receipt = append(receipt, fmt.Sprintf("process_tree_identities=%d", len(tracked)))
		requireRealMCPProcessIdentitiesGone(t, tracked)
		receipt = append(receipt, "zero_residual=verified_pid_plus_start_token")
	}
	if !postIdleSemanticOK {
		receipt = append(receipt, "status=post_idle_semantic_failure")
		return
	}
	if precheck {
		receipt = append(receipt, "status=NON_PASS_precheck_complete_not_formal_15m")
		t.Logf("Swift MCP bounded precheck complete: success=%d unsupported=%d legal_empty=%d runtime_failure=%d; receipt=%s wire=%s", summary.success, summary.unsupported, summary.legalEmpty, summary.runtimeFailures, secureSwiftMCPPath(receiptPath), secureSwiftMCPPath(wirePath))
		return
	}
	if summary.success != summary.total {
		receipt = append(receipt, "status=matrix_complete_not_full_support")
		t.Logf("Swift MCP 36-action matrix complete but not full support: success=%d unsupported=%d legal_empty=%d runtime_failure=%d; receipt=%s wire=%s", summary.success, summary.unsupported, summary.legalEmpty, summary.runtimeFailures, secureSwiftMCPPath(receiptPath), secureSwiftMCPPath(wirePath))
		return
	}
	receipt = append(receipt, "status=full_action_support_and_15m_soak")
	t.Logf("Swift MCP 36-action matrix and 15-minute soak completed: receipt=%s wire=%s", secureSwiftMCPPath(receiptPath), secureSwiftMCPPath(wirePath))
}

// swiftMCP36FixtureServer 锁定 Swift 36-action 使用的仓库快照和真实语义锚点。
func swiftMCP36FixtureServer() realNodeServerCase {
	return realNodeServerCase{
		name:                 "swift",
		languageID:           "swift",
		fileName:             "Greeting.swift",
		sourceDir:            "swift",
		sourceFile:           "lsp-package/Sources/LSPFixture/Greeting.swift",
		sourceSecondaryFile:  "lsp-package/Sources/LSPFixture/MathTools.swift",
		sourceIdentifier:     "Greeting",
		sourceWorkspaceQuery: "Greeting",
		sourceLine:           1,
		sourceCharacter:      7,
	}
}

// TestWindowsARM64ProcessARM64SwiftActionFixturesSharePackageRoot prevents the
// action matrix from opening Swift files outside the Package.swift workspace.
func TestWindowsARM64ProcessARM64SwiftActionFixturesSharePackageRoot(t *testing.T) {
	server := swiftMCP36FixtureServer()
	fixture := writeRealMCPBinSourceFixture(t, t.TempDir(), server)
	packageRoot := filepath.Join(fixture.workDir, "lsp-package")
	if _, err := os.Stat(filepath.Join(packageRoot, "Package.swift")); err != nil {
		t.Fatalf("Swift fixture package root is missing Package.swift: %v", err)
	}
	paths := map[string]string{
		"target":      fixture.targetFile,
		"secondary":   fixture.secondaryFile,
		"replace":     fixture.replaceFile,
		"rename":      fixture.renameFile,
		"code_action": fixture.codeActionFile,
		"format":      fixture.formatFile,
		"completion":  fixture.completionFile,
	}
	for name, path := range paths {
		if !swiftMCPPathWithin(packageRoot, path) {
			t.Errorf("Swift %s fixture escaped Package.swift root: %q", name, path)
		}
	}
}

// swiftMCP36ActionWorkspaceRoot anchors workspace-only actions to the Swift package.
// workspace_symbol-language has no file_path, so the outer fixture root is otherwise a
// valid but different dir_fallback workspace and starts a second SourceKit client.
func swiftMCP36ActionWorkspaceRoot(fixture realMCPFixture, action realMCPActionSpec, defaultRoot string) string {
	if action.tool == "structure" && action.name == "workspace_symbol-language" {
		return filepath.Dir(filepath.Dir(filepath.Dir(fixture.targetFile)))
	}
	return defaultRoot
}

func TestWindowsARM64ProcessARM64SwiftWorkspaceSymbolLanguageRoot(t *testing.T) {
	server := swiftMCP36FixtureServer()
	fixture := writeRealMCPBinSourceFixture(t, t.TempDir(), server)
	action := realMCPActionSpec{tool: "structure", name: "workspace_symbol-language"}
	got := swiftMCP36ActionWorkspaceRoot(fixture, action, fixture.workDir)
	want := filepath.Join(fixture.workDir, "lsp-package")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("Swift workspace_symbol-language work_dir=%q, want package root %q", got, want)
	}
	if !swiftMCPPathWithin(got, fixture.targetFile) {
		t.Fatalf("Swift workspace_symbol-language root %q does not contain target %q", got, fixture.targetFile)
	}
}

// swiftMCPCompletionCharacter 使用 greeter. 后的候选位置；环境变量只供有界诊断覆盖。
func swiftMCPCompletionCharacter() int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv("MCP_LSP_SWIFT_COMPLETION_CHARACTER")))
	if err != nil || value < 0 {
		return 23
	}
	return value
}

func TestWindowsARM64ProcessARM64SwiftCompletionFixturePosition(t *testing.T) {
	t.Setenv("MCP_LSP_SWIFT_COMPLETION_CHARACTER", "")
	if got := swiftMCPCompletionCharacter(); got != 23 {
		t.Fatalf("default Swift completion character=%d, want 23 after greeter. trigger", got)
	}
	t.Setenv("MCP_LSP_SWIFT_COMPLETION_CHARACTER", "15")
	if got := swiftMCPCompletionCharacter(); got != 15 {
		t.Fatalf("diagnostic Swift completion character=%d, want 15", got)
	}
}

type swiftMCPActionStatus string

const (
	swiftMCPActionSuccess        swiftMCPActionStatus = "success"
	swiftMCPActionUnsupported    swiftMCPActionStatus = "capability_unsupported"
	swiftMCPActionLegalEmpty     swiftMCPActionStatus = "legal_empty"
	swiftMCPActionRuntimeFailure swiftMCPActionStatus = "runtime_failure"
)

type swiftMCPActionSummary struct {
	total           int
	success         int
	unsupported     int
	legalEmpty      int
	runtimeFailures int
}

func (s *swiftMCPActionSummary) add(_ string, status swiftMCPActionStatus) {
	switch status {
	case swiftMCPActionSuccess:
		s.success++
	case swiftMCPActionUnsupported:
		s.unsupported++
	case swiftMCPActionLegalEmpty:
		s.legalEmpty++
	default:
		s.runtimeFailures++
	}
}

func (s swiftMCPActionSummary) classified() int {
	return s.success + s.unsupported + s.legalEmpty + s.runtimeFailures
}

type swiftMCPWireRecord struct {
	Direction       string `json:"direction"`
	Method          string `json:"method"`
	Action          string `json:"action,omitempty"`
	RequestBytes    int    `json:"request_bytes,omitempty"`
	RequestSHA256   string `json:"request_sha256,omitempty"`
	RequestIDHash   string `json:"request_id_hash,omitempty"`
	ElapsedMS       int64  `json:"elapsed_ms,omitempty"`
	TimeoutTier     string `json:"timeout_tier,omitempty"`
	CancelGraceMS   int64  `json:"cancel_grace_ms,omitempty"`
	TimeoutObserved bool   `json:"timeout_observed,omitempty"`
	ResponseBytes   int    `json:"response_bytes,omitempty"`
	ResponseSHA256  string `json:"response_sha256,omitempty"`
	ResultBytes     int    `json:"result_bytes,omitempty"`
	ContentBytes    int    `json:"content_bytes,omitempty"`
	NonEmpty        bool   `json:"non_empty,omitempty"`
	IsError         bool   `json:"is_error,omitempty"`
	ErrorCode       int    `json:"error_code,omitempty"`
	ErrorMessage    string `json:"error_message,omitempty"`
	WriteError      string `json:"write_error,omitempty"`
}

// swiftMCPAppendTopologyReceipt 只记录进程身份摘要，避免把用户路径或源码写入证据。
func swiftMCPAppendTopologyReceipt(receipt *[]string, tracked map[realMCPProcessKey]realMCPProcessIdentity) {
	for _, identity := range tracked {
		executableSHA, err := swiftMCPExecutableSHA256(identity)
		if err != nil {
			executableSHA = "unavailable"
		}
		*receipt = append(*receipt, fmt.Sprintf("cohort_member pid=%d parent_pid=%d role=%s name=%s start_token_digest=%s command_sha256=%s executable_sha256=%s", identity.PID, identity.ParentPID, identity.Language, identity.Name, swiftMCPTokenDigest(identity.StartToken), identity.CommandSHA256, executableSHA))
	}
}

// swiftMCPAppendSourceKitObservations 仅记录 resolver-owned SourceKit 的低敏拓扑摘要。
// 参数值、路径和源码均不落盘；父链与命令形状用于区分真实 replacement、旧 child 滞留和其他进程树。
func swiftMCPAppendSourceKitObservations(receipt *[]string, tracked map[realMCPProcessKey]realMCPProcessIdentity, mcpPID int, action string) {
	for _, identity := range tracked {
		name := strings.ToLower(filepath.Base(strings.TrimSpace(identity.Name)))
		if name != "sourcekit-lsp.exe" && name != "sourcekit-lsp" && name != "source~1.exe" {
			continue
		}
		parentStartDigest := "unavailable"
		for _, parent := range tracked {
			if parent.PID == identity.ParentPID {
				parentStartDigest = swiftMCPTokenDigest(parent.StartToken)
				break
			}
		}
		direct := identity.ParentPID == mcpPID
		indirect := direct || swiftMCPIdentityDescendsFrom(tracked, identity, mcpPID)
		executableSHA, err := swiftMCPExecutableSHA256(identity)
		if err != nil {
			executableSHA = "unavailable"
		}
		basename, argCount, argShapeDigest := swiftMCPCommandArgShape(identity.CommandLine)
		*receipt = append(*receipt, fmt.Sprintf("sourcekit_observation action=%s pid=%d parent_pid=%d parent_start_token_digest=%s start_token_digest=%s name=%s executable_sha256=%s command_executable_basename=%s command_arg_count=%d command_arg_shape_sha256=%s direct_mcp_descendant=%t indirect_mcp_descendant=%t job_membership=unavailable", action, identity.PID, identity.ParentPID, parentStartDigest, swiftMCPTokenDigest(identity.StartToken), name, executableSHA, basename, argCount, argShapeDigest, direct, indirect))
	}
}

// swiftMCPSourceKitCount 统计当前 exact MCP 后代中的 SourceKit 数量；replacement
// barrier diagnostic 要求旧 owner 消失后才允许新 owner 出现，不能只取第一个进程。
func swiftMCPSourceKitCount(tracked map[realMCPProcessKey]realMCPProcessIdentity, mcpPID int) int {
	count := 0
	for _, identity := range tracked {
		name := strings.ToLower(filepath.Base(strings.TrimSpace(identity.Name)))
		if name != "sourcekit-lsp.exe" && name != "sourcekit-lsp" && name != "source~1.exe" {
			continue
		}
		if identity.ParentPID == mcpPID || swiftMCPIdentityDescendsFrom(tracked, identity, mcpPID) {
			count++
		}
	}
	return count
}

func swiftMCPCommandArgShape(commandLine string) (string, int, string) {
	parts := strings.Fields(strings.TrimSpace(commandLine))
	if len(parts) == 0 {
		return "unavailable", 0, "unavailable"
	}
	basename := filepath.Base(strings.Trim(parts[0], "\""))
	shape := make([]string, 0, len(parts)-1)
	for _, part := range parts[1:] {
		part = strings.Trim(part, "\"")
		if index := strings.IndexByte(part, '='); index >= 0 {
			part = part[:index]
		}
		if !strings.HasPrefix(part, "-") {
			part = "arg"
		}
		shape = append(shape, part)
	}
	digest := sha256.Sum256([]byte(strings.Join(shape, "\x00")))
	return basename, len(shape), hex.EncodeToString(digest[:])
}

func swiftMCPCall(t *testing.T, client *mcpLSPBinaryClient, wire *json.Encoder, method string, params map[string]any, action string) (mcpLSPBinaryResponse, json.RawMessage, error) {
	t.Helper()
	started := time.Now()
	id := time.Now().UnixNano()
	timeoutTier := swiftMCPTimeoutTier(action)
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	rawRequest, err := json.Marshal(request)
	if err != nil {
		return mcpLSPBinaryResponse{}, nil, fmt.Errorf("marshal %s: %w", method, err)
	}
	if err := swiftMCPWriteWire(wire, swiftMCPWireRecord{Direction: "client_to_server", Method: method, Action: action, RequestBytes: len(rawRequest), RequestSHA256: swiftMCPBytesDigest(rawRequest), RequestIDHash: swiftMCPBytesDigest([]byte(fmt.Sprintf("%d", id))), TimeoutTier: timeoutTier}); err != nil {
		return mcpLSPBinaryResponse{}, nil, err
	}
	if _, err := client.stdin.Write(append(rawRequest, '\n')); err != nil {
		return mcpLSPBinaryResponse{}, nil, fmt.Errorf("write %s: %w", method, err)
	}
	readTimeout := swiftMCPCallTimeout
	if strings.Contains(strings.ToLower(action), "completion") {
		readTimeout = swiftMCPCompletionCallTimeout
		if strings.TrimSpace(os.Getenv("MCP_LSP_SWIFT_COMPLETION_CHARACTER")) != "" {
			readTimeout = 90 * time.Second
		}
	}
	line, err := swiftMCPReadLine(client, readTimeout)
	if err != nil {
		_ = swiftMCPWriteWire(wire, swiftMCPWireRecord{Direction: "server_to_client", Method: method, Action: action, ElapsedMS: time.Since(started).Milliseconds(), TimeoutTier: timeoutTier, CancelGraceMS: swiftMCPCancelGrace.Milliseconds(), TimeoutObserved: true, ErrorMessage: swiftMCPRedact(err.Error())})
		return mcpLSPBinaryResponse{}, nil, fmt.Errorf("read %s: %w", method, err)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		return mcpLSPBinaryResponse{}, nil, fmt.Errorf("decode %s response: %w", method, err)
	}
	var response mcpLSPBinaryResponse
	if err := json.Unmarshal(line, &response); err != nil {
		return mcpLSPBinaryResponse{}, nil, fmt.Errorf("decode %s typed response: %w", method, err)
	}
	errorCode, errorMessage := 0, ""
	if envelope.Error != nil {
		errorCode, errorMessage = envelope.Error.Code, swiftMCPRedact(envelope.Error.Message)
	}
	if err := swiftMCPWriteWire(wire, swiftMCPWireRecord{
		Direction: "server_to_client", Method: method, Action: action, ResponseBytes: len(line), ResponseSHA256: swiftMCPBytesDigest(line), ResultBytes: len(bytes.TrimSpace(envelope.Result)), ContentBytes: len(response.Result.ContentText()), NonEmpty: swiftMCPResponseNonEmpty(response), IsError: response.Result.IsError || envelope.Error != nil, ErrorCode: errorCode, ErrorMessage: errorMessage, ElapsedMS: time.Since(started).Milliseconds(), TimeoutTier: timeoutTier,
	}); err != nil {
		return response, envelope.Result, err
	}
	return response, envelope.Result, nil
}

// swiftMCPTimeoutTier 仅为 Swift E2E 诊断标注当前既有 stdio 超时层，不改变超时策略。
func swiftMCPTimeoutTier(action string) string {
	if strings.Contains(strings.ToLower(action), "completion") {
		return "swift_completion_stdio_120s"
	}
	return "swift_stdio_120s"
}

func swiftMCPReadLine(client *mcpLSPBinaryClient, timeout time.Duration) ([]byte, error) {
	if client == nil || client.stdout == nil {
		return nil, errors.New("MCP stdio reader is nil")
	}
	result := make(chan struct {
		line []byte
		err  error
	}, 1)
	go func() {
		line, err := client.stdout.ReadBytes('\n')
		result <- struct {
			line []byte
			err  error
		}{line: line, err: err}
	}()
	select {
	case value := <-result:
		return value.line, value.err
	case <-time.After(timeout):
		grace := time.NewTimer(swiftMCPCancelGrace)
		<-grace.C
		if client.cmd != nil && client.cmd.Process != nil {
			_ = client.cmd.Process.Kill()
		}
		return nil, fmt.Errorf("stdio response timeout after %s; cancel grace %s", timeout, swiftMCPCancelGrace)
	}
}

func swiftMCPNotify(t *testing.T, client *mcpLSPBinaryClient, wire *json.Encoder, method string, params map[string]any, sequence *int64) error {
	t.Helper()
	request := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	raw, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("marshal notification %s: %w", method, err)
	}
	if err := swiftMCPWriteWire(wire, swiftMCPWireRecord{Direction: "client_to_server", Method: method, RequestBytes: len(raw), RequestSHA256: swiftMCPBytesDigest(raw)}); err != nil {
		return err
	}
	if _, err := client.stdin.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("write notification %s: %w", method, err)
	}
	if sequence != nil {
		*sequence++
	}
	return nil
}

func swiftMCPExit(t *testing.T, client *mcpLSPBinaryClient, wire *json.Encoder, sequence *int64) error {
	t.Helper()
	request := []byte(`{"jsonrpc":"2.0","method":"exit"}`)
	if err := swiftMCPWriteWire(wire, swiftMCPWireRecord{Direction: "client_to_server", Method: "exit", RequestBytes: len(request), RequestSHA256: swiftMCPBytesDigest(request)}); err != nil {
		return err
	}
	if _, err := client.stdin.Write(append(request, '\n')); err != nil {
		return fmt.Errorf("write exit: %w", err)
	}
	if sequence != nil {
		*sequence++
	}
	stdin := client.stdin
	client.stdin = nil
	_ = stdin.Close()
	cmd := client.cmd
	client.cmd = nil
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("wait after exit: %w", err)
		}
		return nil
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return errors.New("process did not exit within 30 seconds")
	}
}

func swiftMCPWriteWire(wire *json.Encoder, record swiftMCPWireRecord) error {
	if wire == nil {
		return errors.New("Swift MCP wire encoder is nil")
	}
	return wire.Encode(record)
}

const swiftMCPTypeDefinitionUnsupportedText = "type definition unsupported by current language server"

func swiftMCPClassifyAction(actionName string, response mcpLSPBinaryResponse, callErr error) swiftMCPActionStatus {
	if callErr != nil {
		return swiftMCPActionRuntimeFailure
	}
	text := response.Result.ContentText()
	combined := strings.ToLower(text)
	if response.Error != nil {
		return swiftMCPActionRuntimeFailure
	}
	if actionName == "type_definition" && strings.Contains(text, swiftMCPTypeDefinitionUnsupportedText) {
		return swiftMCPActionUnsupported
	}
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		return swiftMCPActionRuntimeFailure
	}
	if doc.Error != nil {
		code := strings.ToLower(strings.TrimSpace(doc.Error.Code))
		if code == "capability_unsupported" {
			return swiftMCPActionUnsupported
		}
		return swiftMCPActionRuntimeFailure
	}
	if response.Result.IsError || strings.Contains(combined, "timed out") || strings.Contains(combined, "process exited") || strings.Contains(combined, "lsp_unavailable") {
		return swiftMCPActionRuntimeFailure
	}
	if strings.Contains(combined, "unsupported") || strings.Contains(combined, "method not found") {
		return swiftMCPActionRuntimeFailure
	}
	if !swiftMCPResponseNonEmpty(response) {
		return swiftMCPActionLegalEmpty
	}
	return swiftMCPActionSuccess
}

func swiftMCPResponseNonEmpty(response mcpLSPBinaryResponse) bool {
	doc, err := lineprotocol.Parse(response.Result.ContentText())
	if err != nil {
		return false
	}
	return realMCPActionContentNonEmpty(doc)
}

func swiftMCPResponseCode(response mcpLSPBinaryResponse) string {
	if response.Error != nil {
		return fmt.Sprintf("jsonrpc_%d", response.Error.Code)
	}
	if doc, err := lineprotocol.Parse(response.Result.ContentText()); err == nil && doc.Error != nil {
		return doc.Error.Code
	}
	return "success"
}

func swiftMCPResponseDetail(response mcpLSPBinaryResponse, callErr error) string {
	if callErr != nil {
		return callErr.Error()
	}
	if response.Error != nil {
		return response.Error.Message
	}
	return response.Result.ContentText()
}

func errString(callErr error, responseErr *struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}) string {
	if callErr != nil {
		return callErr.Error()
	}
	if responseErr != nil {
		return responseErr.Message
	}
	return "unknown error"
}

func swiftMCPAssertIdentity(pid int, wantStart string) error {
	alive, err := processAliveForE2E(pid)
	if err != nil {
		return fmt.Errorf("inspect PID %d: %w", pid, err)
	}
	if !alive {
		return fmt.Errorf("PID %d is not alive", pid)
	}
	got, err := windowsGoplsProcessStartIdentity(pid)
	if err != nil {
		return fmt.Errorf("read PID %d start identity: %w", pid, err)
	}
	if got != wantStart {
		return fmt.Errorf("PID %d start identity changed", pid)
	}
	return nil
}

func swiftMCPFindSourceKitProcess(tracked map[realMCPProcessKey]realMCPProcessIdentity) (realMCPProcessIdentity, bool) {
	for _, identity := range tracked {
		name := strings.ToLower(filepath.Base(strings.TrimSpace(identity.Name)))
		if name != "sourcekit-lsp.exe" && name != "sourcekit-lsp" && name != "source~1.exe" {
			continue
		}
		if alive, err := processAliveForE2E(identity.PID); err == nil && alive {
			return identity, true
		}
	}
	return realMCPProcessIdentity{}, false
}

// swiftMCPValidateSourceKitRotation 只允许同一 MCP 后代树内的受控轮换：旧 PID
// 必须消失，新旧命令摘要与可执行文件摘要相同，且新 child 的父链仍回到同一 MCP。
func swiftMCPValidateSourceKitRotation(tracked map[realMCPProcessKey]realMCPProcessIdentity, old, next realMCPProcessIdentity, mcpPID int) error {
	if old.PID == next.PID && old.StartToken == next.StartToken {
		return errors.New("SourceKit rotation reused the same PID/start identity")
	}
	if old.CommandSHA256 == "" || next.CommandSHA256 == "" || old.CommandSHA256 != next.CommandSHA256 {
		return errors.New("SourceKit rotation command identity changed")
	}
	oldExecutableSHA, err := swiftMCPExecutableSHA256(old)
	if err != nil {
		return fmt.Errorf("read old SourceKit executable identity: %w", err)
	}
	nextExecutableSHA, err := swiftMCPExecutableSHA256(next)
	if err != nil {
		return fmt.Errorf("read new SourceKit executable identity: %w", err)
	}
	if oldExecutableSHA != nextExecutableSHA {
		return errors.New("SourceKit rotation executable identity changed")
	}
	if alive, aliveErr := processAliveForE2E(old.PID); aliveErr != nil {
		return fmt.Errorf("inspect retired SourceKit PID %d: %w", old.PID, aliveErr)
	} else if alive {
		current, startErr := windowsGoplsProcessStartIdentity(old.PID)
		if startErr == nil && current == old.StartToken {
			return fmt.Errorf("retired SourceKit PID/start identity still alive: pid=%d", old.PID)
		}
	}
	if !swiftMCPIdentityDescendsFrom(tracked, next, mcpPID) {
		return errors.New("new SourceKit child is outside the same MCP parent chain")
	}
	return nil
}

func swiftMCPIdentityDescendsFrom(tracked map[realMCPProcessKey]realMCPProcessIdentity, identity realMCPProcessIdentity, mcpPID int) bool {
	seen := map[int]bool{}
	parent := identity.ParentPID
	for parent != 0 && !seen[parent] {
		if parent == mcpPID {
			return true
		}
		seen[parent] = true
		found := false
		for _, candidate := range tracked {
			if candidate.PID == parent {
				parent = candidate.ParentPID
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return false
}

func swiftMCPExecutableSHA256(identity realMCPProcessIdentity) (string, error) {
	command := strings.TrimSpace(identity.CommandLine)
	if command == "" {
		return "", errors.New("SourceKit command line is empty")
	}
	if command[0] == '"' {
		end := strings.IndexByte(command[1:], '"')
		if end < 0 {
			return "", errors.New("SourceKit command line executable is malformed")
		}
		command = command[1 : end+1]
	} else if index := strings.IndexAny(command, " \t"); index >= 0 {
		command = command[:index]
	}
	payload, err := os.ReadFile(command)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// swiftMCPProcessCPUTime 读取 SourceKit 的累计用户态/内核态时间，仅写入低敏纳秒计数。
func swiftMCPProcessCPUTime(pid int) (time.Duration, error) {
	if pid <= 0 {
		return 0, errors.New("invalid SourceKit PID")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(handle)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return 0, err
	}
	// Win32 FILETIME 以 100ns 为单位；Duration 使用 ns，避免记录原始路径或命令行。
	ticks := (uint64(kernel.HighDateTime)<<32 | uint64(kernel.LowDateTime)) + (uint64(user.HighDateTime)<<32 | uint64(user.LowDateTime))
	return time.Duration(ticks) * 100, nil
}

func TestWindowsARM64ProcessARM64SwiftClassifyTypeDefinitionUnsupportedContract(t *testing.T) {
	response := mcpLSPBinaryResponse{Result: mcpLSPBinaryToolResult{Content: []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: "MESSAGE\ttype definition unsupported by current language server"}}}}
	if got := swiftMCPClassifyAction("type_definition", response, nil); got != swiftMCPActionUnsupported {
		t.Fatalf("exact Swift type_definition unsupported text = %q, want capability_unsupported", got)
	}
	if got := swiftMCPClassifyAction("hover", response, nil); got == swiftMCPActionUnsupported {
		t.Fatal("type_definition unsupported text was downgraded for hover")
	}
	other := response
	other.Result.Content[0].Text = "MESSAGE\ttype definition unsupported by another server"
	if got := swiftMCPClassifyAction("type_definition", other, nil); got == swiftMCPActionUnsupported {
		t.Fatal("near-miss type_definition text was downgraded")
	}
}

func TestWindowsARM64ProcessARM64SwiftSourceKitRotationContract(t *testing.T) {
	dir := t.TempDir()
	oldExe := filepath.Join(dir, "sourcekit-old.exe")
	newExe := filepath.Join(dir, "sourcekit-new.exe")
	payload := []byte("same resolver-owned SourceKit cohort")
	if err := os.WriteFile(oldExe, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newExe, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	old := realMCPProcessIdentity{PID: 200, ParentPID: 100, StartToken: "old", Name: "sourcekit-lsp.exe", CommandLine: oldExe + " --stdio", CommandSHA256: "same-command"}
	next := realMCPProcessIdentity{PID: 300, ParentPID: 100, StartToken: "new", Name: "sourcekit-lsp.exe", CommandLine: newExe + " --stdio", CommandSHA256: "same-command"}
	tracked := map[realMCPProcessKey]realMCPProcessIdentity{
		{PID: 100, StartToken: "mcp"}: {PID: 100, StartToken: "mcp", Name: "mcp-lsp"},
		{PID: 300, StartToken: "new"}: next,
	}
	// 旧 PID 不存在于当前树，表示已退出；新 child 仍挂在同一 MCP owner 下。
	if err := swiftMCPValidateSourceKitRotation(tracked, old, next, 100); err != nil {
		t.Fatalf("controlled SourceKit rotation rejected: %v", err)
	}
	next.CommandSHA256 = "different-command"
	if err := swiftMCPValidateSourceKitRotation(tracked, old, next, 100); err == nil {
		t.Fatal("SourceKit command identity drift was accepted")
	}
}

func TestWindowsARM64ProcessARM64SwiftCommandArgShapeRedactsValues(t *testing.T) {
	basename, count, digest := swiftMCPCommandArgShape(`"C:\private\sourcekit-lsp.exe" --stdio --workspace=C:\private\workspace`)
	if basename != "sourcekit-lsp.exe" || count != 2 || digest == "" || strings.Contains(digest, "private") {
		t.Fatalf("command shape = (%q, %d, %q), want basename/count/digest without values", basename, count, digest)
	}
}

var swiftMCPWindowsPathPattern = regexp.MustCompile(`(?i)[a-z]:[\\/][^"'\r\n]*`)

func swiftMCPRedact(value string) string {
	value = swiftMCPWindowsPathPattern.ReplaceAllString(value, "<private>")
	value = strings.ReplaceAll(value, "C:/Users", "<private>")
	value = strings.ReplaceAll(value, "C:\\Users", "<private>")
	return strings.ReplaceAll(value, "\r\n", "\\n")
}

func secureSwiftMCPPath(value string) string {
	return "<private>"
}

func swiftMCPPathDigest(value string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(value)))
	return hex.EncodeToString(sum[:])
}

func swiftMCPTokenDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func swiftMCPBytesDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func swiftMCPPathWithin(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func swiftMCPRelativePath(root, candidate string) string {
	if relative, err := filepath.Rel(root, candidate); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(relative)
	}
	return "<outside-root>"
}
