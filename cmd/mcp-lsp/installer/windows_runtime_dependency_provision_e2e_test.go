//go:build windows && e2e

package installer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

type windowsRuntimeDependencyE2ECase struct {
	product WindowsRuntimeDependencyProduct
	version string
}

type windowsRuntimeDependencyE2EVerification struct {
	VersionArgCount     int    `json:"version_arg_count,omitempty"`
	VersionArgsSHA256   string `json:"version_args_sha256,omitempty"`
	VersionOutputBytes  int    `json:"version_output_bytes,omitempty"`
	VersionOutputSHA256 string `json:"version_output_sha256,omitempty"`
	VersionDuration     string `json:"version_duration,omitempty"`
	LSPCommandArgCount  int    `json:"lsp_command_arg_count,omitempty"`
	LSPCommandSHA256    string `json:"lsp_command_sha256,omitempty"`
	// LSPWorkspaceRootSHA256 记录真实隔离工作区路径摘要，不把本机路径写入公开收据。
	LSPWorkspaceRootSHA256 string `json:"lsp_workspace_root_sha256,omitempty"`
	// InitializeRootURISHA256 证明实际发送的 Windows file URI，同时避免暴露绝对路径。
	InitializeRootURISHA256             string `json:"initialize_root_uri_sha256,omitempty"`
	LSPPID                              int    `json:"lsp_pid,omitempty"`
	LSPExitCode                         int    `json:"lsp_exit_code"`
	LSPDuration                         string `json:"lsp_duration,omitempty"`
	LSPPIDAlive                         bool   `json:"lsp_pid_alive"`
	LSPProcessStartTokenSHA256          string `json:"lsp_process_start_token_sha256,omitempty"`
	LSPProcessStartTokenPresent         bool   `json:"lsp_process_start_token_present"`
	LSPStderrBytes                      int    `json:"lsp_stderr_bytes,omitempty"`
	LSPStderrSHA256                     string `json:"lsp_stderr_sha256,omitempty"`
	LSPWireLogPath                      string `json:"lsp_wire_log_path,omitempty"`
	LSPStderrLogPath                    string `json:"lsp_stderr_log_path,omitempty"`
	SemanticMethod                      string `json:"semantic_method,omitempty"`
	SemanticResponseID                  int    `json:"semantic_response_id,omitempty"`
	SemanticResponseBytes               int    `json:"semantic_response_bytes,omitempty"`
	SemanticResponseSHA256              string `json:"semantic_response_sha256,omitempty"`
	SemanticNonEmpty                    bool   `json:"semantic_non_empty"`
	SwiftInstallerSHA256                string `json:"swift_installer_sha256,omitempty"`
	SwiftInstallerPEMachine             string `json:"swift_installer_pe_machine,omitempty"`
	SwiftAttachedCABSize                int64  `json:"swift_attached_cab_size,omitempty"`
	SwiftAttachedCABSHA512              string `json:"swift_attached_cab_sha512,omitempty"`
	SwiftPayloadCount                   int    `json:"swift_payload_count,omitempty"`
	SwiftCompilerPEMachine              string `json:"swift_compiler_pe_machine,omitempty"`
	SwiftSourceKitPEMachine             string `json:"swift_sourcekit_pe_machine,omitempty"`
	SwiftRuntimeMSMFileCount            int    `json:"swift_runtime_msm_file_count,omitempty"`
	SwiftRuntimeMSMManifestSHA256       string `json:"swift_runtime_msm_manifest_sha256,omitempty"`
	SwiftRuntimeMSMSourceManifestSHA256 string `json:"swift_runtime_msm_source_manifest_sha256,omitempty"`
	SwiftRuntimeMSMCABSize              int64  `json:"swift_runtime_msm_cab_size,omitempty"`
	SwiftRuntimeMSMCABSHA256            string `json:"swift_runtime_msm_cab_sha256,omitempty"`
	SwiftRuntimeVCLibsFileCount         int    `json:"swift_runtime_vclibs_file_count,omitempty"`
	SwiftRuntimeVCLibsManifestSHA256    string `json:"swift_runtime_vclibs_manifest_sha256,omitempty"`
	SwiftRejectedRuntimeDLL             string `json:"swift_rejected_runtime_dll,omitempty"`
	SwiftRejectedRuntimeFileID          string `json:"swift_rejected_runtime_file_id,omitempty"`
	SwiftRejectedRuntimeComponent       string `json:"swift_rejected_runtime_component,omitempty"`
	SwiftRejectedRuntimeCABMember       string `json:"swift_rejected_runtime_cab_member,omitempty"`
	SwiftRejectedRuntimeSHA256          string `json:"swift_rejected_runtime_sha256,omitempty"`
	SwiftRejectedRuntimePEMachine       string `json:"swift_rejected_runtime_pe_machine,omitempty"`
	SwiftRejectedRuntimePresent         bool   `json:"swift_rejected_runtime_present"`
	SwiftTypecheckArgCount              int    `json:"swift_typecheck_arg_count,omitempty"`
	SwiftTypecheckArgsSHA256            string `json:"swift_typecheck_args_sha256,omitempty"`
	SwiftTypecheckBytes                 int    `json:"swift_typecheck_output_bytes,omitempty"`
	SwiftTypecheckSHA256                string `json:"swift_typecheck_output_sha256,omitempty"`
}

type windowsRuntimeDependencyE2EReceipt struct {
	Product              string `json:"product"`
	Architecture         string `json:"architecture,omitempty"`
	HostOS               string `json:"host_os"`
	WindowsVersion       string `json:"windows_version"`
	WindowsBuild         uint32 `json:"windows_build"`
	NativeArch           string `json:"native_arch"`
	ProcessArch          string `json:"process_arch"`
	CacheRoot            string `json:"cache_root"`
	ReceiptPath          string `json:"receipt_path"`
	StartedAt            string `json:"started_at"`
	FinishedAt           string `json:"finished_at"`
	ProvisionDuration    string `json:"provision_duration"`
	CacheHit             bool   `json:"cache_hit"`
	CacheHitHTTPRequests int64  `json:"cache_hit_http_requests"`
	ResolverCacheHit     bool   `json:"resolver_cache_hit"`
	Cohort               string `json:"cohort,omitempty"`
	RootPath             string `json:"root_path,omitempty"`
	WorkingDirectory     string `json:"working_directory,omitempty"`
	// WorkspaceRoot 记录收据所对应的真实 Windows LSP 工作区，便于复核语义和生命周期证据。
	WorkspaceRoot        string                                  `json:"workspace_root,omitempty"`
	ExecutablePath       string                                  `json:"executable_path,omitempty"`
	ServerPath           string                                  `json:"server_path,omitempty"`
	LaunchArgCount       int                                     `json:"launch_arg_count,omitempty"`
	LaunchArgsSHA256     string                                  `json:"launch_args_sha256,omitempty"`
	LaunchEnvKeyCount    int                                     `json:"launch_env_key_count,omitempty"`
	LaunchEnvKeysSHA256  string                                  `json:"launch_env_keys_sha256,omitempty"`
	Verification         windowsRuntimeDependencyE2EVerification `json:"verification"`
	StagingClean         bool                                    `json:"staging_clean"`
	SwiftMSIExecLogPaths []string                                `json:"swift_msiexec_log_paths,omitempty"`
	Error                string                                  `json:"error,omitempty"`
	PathEvidencePolicy   string                                  `json:"path_evidence_policy,omitempty"`
}

// TestWindowsRuntimeDependencyProvisionNativeNetworkE2E 自动检测 Windows 原生/进程架构，
// 从空缓存或命中缓存验证版本、LSP 生命周期、退出码、PID 残留和暂存目录；失败即测试失败。
func TestWindowsRuntimeDependencyProvisionNativeNetworkE2E(t *testing.T) {
	if os.Getenv("MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E") != "1" {
		t.Skip("set MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E=1 to run the real native Windows network E2E")
	}
	platform, err := DetectWindowsHostPlatform()
	if err != nil {
		t.Fatal(err)
	}
	testCases, err := windowsRuntimeDependencyE2EProducts(os.Getenv("MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E_PRODUCTS"))
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := strings.TrimSpace(os.Getenv("MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E_CACHE_ROOT"))
	emptyCache := cacheRoot == ""
	if emptyCache {
		cacheRoot = windowsRuntimeDependencyE2EShortCacheRoot(t)
	}
	for _, testCase := range testCases {
		testCase := testCase
		caseName, nameErr := windowsE2EPlatformCaseName(platform, string(testCase.product))
		if nameErr != nil {
			t.Fatal(nameErr)
		}
		t.Run(caseName, func(t *testing.T) {
			started := time.Now()
			receiptPath, receiptErr := windowsRuntimeDependencyE2EReceiptPath(testCase.product, platform)
			if receiptErr != nil {
				t.Fatal(receiptErr)
			}
			receipt := windowsRuntimeDependencyE2EReceipt{
				Product: string(testCase.product), CacheRoot: cacheRoot, ReceiptPath: receiptPath,
				StartedAt: started.UTC().Format(time.RFC3339Nano), HostOS: platform.OS,
				WindowsVersion: platform.WindowsVersion, WindowsBuild: platform.WindowsBuild,
				NativeArch: platform.NativeArch, ProcessArch: platform.ProcessArch,
			}
			var phase atomic.Value
			phase.Store("provision")
			stopHeartbeat := windowsRuntimeDependencyE2EHeartbeat(t, started, cacheRoot, &phase, testCase.product, platform)
			defer stopHeartbeat()
			defer func() {
				receipt.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
				if windowsRuntimeDependencyStagingError(cacheRoot) == nil {
					receipt.StagingClean = true
				}
				if writeErr := writeWindowsRuntimeDependencyE2EReceipt(receiptPath, redactWindowsRuntimeDependencyE2EReceipt(receipt)); writeErr != nil {
					t.Errorf("write E2E receipt %q: %v", receiptPath, writeErr)
				}
			}()

			provisionOptions := WindowsRuntimeDependencyProvisionOptions{
				CacheRoot: cacheRoot, InstallTimeout: 45 * time.Minute,
			}
			if localSwiftAsset := strings.TrimSpace(os.Getenv("MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E_SWIFT_ASSET")); localSwiftAsset != "" && testCase.product == WindowsRuntimeDependencyProductSwiftSourceKitLS {
				provisionOptions.FetchAsset = windowsRuntimeDependencyE2ELocalSwiftAssetFetcher(localSwiftAsset)
			}
			var swiftMSIExecLogPaths []string
			if testCase.product == WindowsRuntimeDependencyProductSwiftSourceKitLS {
				if logDir := strings.TrimSpace(os.Getenv("MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E_LOG_DIR")); logDir != "" {
					provisionOptions.RunCommand = windowsRuntimeDependencyE2ESwiftMSICommandRunner(logDir, &swiftMSIExecLogPaths)
				}
			}
			result, provisionErr := ProvisionWindowsRuntimeDependencyWithOptions(context.Background(), testCase.product, provisionOptions)
			phase.Store("version-typecheck-lsp")
			receipt.ProvisionDuration = time.Since(started).Round(time.Millisecond).String()
			receipt.SwiftMSIExecLogPaths = append([]string(nil), swiftMSIExecLogPaths...)
			receipt.CacheHit = result.CacheHit
			receipt.Architecture = result.Architecture
			receipt.Cohort = result.Cohort
			receipt.RootPath = result.RootPath
			receipt.WorkingDirectory = result.WorkingDirectory
			receipt.ExecutablePath = result.ExecutablePath
			receipt.ServerPath = result.ServerPath
			launchResult := result
			if result.Product == WindowsRuntimeDependencyProductJDKJDTLS {
				workspaceRoot, launchArgs, prepareErr := prepareWindowsRuntimeDependencyJDTLSWorkspace(result)
				if prepareErr != nil {
					receipt.Error = fmt.Sprintf("prepare JDTLS workspace: %v", prepareErr)
					t.Fatal(prepareErr)
				}
				receipt.WorkspaceRoot = workspaceRoot
				launchResult.Args = launchArgs
			} else if result.Product == WindowsRuntimeDependencyProductRubySolargraph {
				workspaceRoot, prepareErr := prepareWindowsRuntimeDependencyRubyWorkspace(result)
				if prepareErr != nil {
					receipt.Error = fmt.Sprintf("prepare Ruby workspace: %v", prepareErr)
					t.Fatal(prepareErr)
				}
				receipt.WorkspaceRoot = workspaceRoot
			} else if result.Product == WindowsRuntimeDependencyProductSwiftSourceKitLS {
				workspaceRoot, prepareErr := prepareWindowsRuntimeDependencySwiftWorkspace(t.TempDir())
				if prepareErr != nil {
					receipt.Error = fmt.Sprintf("prepare Swift workspace: %v", prepareErr)
					t.Fatal(prepareErr)
				}
				receipt.WorkspaceRoot = workspaceRoot
			}
			receipt.LaunchArgCount = len(launchResult.Args)
			receipt.LaunchArgsSHA256 = windowsRuntimeDependencyE2EStringListSHA256(launchResult.Args)
			envKeys := windowsRuntimeDependencyE2EEnvKeys(result.Env)
			receipt.LaunchEnvKeyCount = len(envKeys)
			receipt.LaunchEnvKeysSHA256 = windowsRuntimeDependencyE2EStringListSHA256(envKeys)
			if provisionErr != nil {
				receipt.Error = fmt.Sprintf("provision: %v", provisionErr)
				if len(swiftMSIExecLogPaths) > 0 {
					t.Logf("Swift MSI verbose logs: %s", strings.Join(swiftMSIExecLogPaths, ", "))
				}
				t.Fatalf("provision %q after %s: %v", testCase.product, receipt.ProvisionDuration, provisionErr)
			}
			if result.Platform.NativeArch != platform.NativeArch || result.Platform.ProcessArch != platform.ProcessArch || result.Architecture != platform.NativeArch || !filepath.IsAbs(result.ServerPath) || windowsRuntimeDependencyHasPATHEnv(result.Env) {
				receipt.Error = "unsafe launch contract"
				t.Fatalf("%q returned an unsafe launch contract: %+v", testCase.product, result)
			}
			if testCase.product == WindowsRuntimeDependencyProductSwiftSourceKitLS && emptyCache && result.CacheHit {
				receipt.Error = "Swift provision unexpectedly hit the fresh empty cache"
				t.Fatal(receipt.Error)
			}
			verification, verifyErr := verifyWindowsRuntimeDependencyVersionAndLSP(context.Background(), launchResult, testCase.version, receipt.WorkspaceRoot)
			receipt.Verification = verification
			if verifyErr != nil {
				receipt.Error = fmt.Sprintf("verify: %v", verifyErr)
				t.Fatalf("verify %q after %s: %v", testCase.product, time.Since(started).Round(time.Millisecond), verifyErr)
			}
			if stagingErr := windowsRuntimeDependencyStagingError(cacheRoot); stagingErr != nil {
				receipt.Error = stagingErr.Error()
				t.Fatal(stagingErr)
			}
			phase.Store("resolver-cache-hit")
			checked, checkErr := ResolveWindowsRuntimeDependencyForPlatform(context.Background(), testCase.product, cacheRoot, result.Platform)
			if checkErr != nil || !checked.CacheHit {
				receipt.Error = fmt.Sprintf("post-LSP cache validation: %v", checkErr)
				t.Fatalf("post-LSP cache validation for %q: result=%+v err=%v", testCase.product, checked, checkErr)
			}
			receipt.ResolverCacheHit = checked.CacheHit
			if checked.RootPath != result.RootPath || checked.ServerPath != result.ServerPath || checked.Architecture != result.Architecture {
				receipt.Error = "resolver cache-hit changed the published identity"
				t.Fatalf("resolver cache-hit changed %q identity: first=%+v checked=%+v", testCase.product, result, checked)
			}
			if testCase.product == WindowsRuntimeDependencyProductSwiftSourceKitLS {
				phase.Store("cache-hit-no-network")
				cacheHitGate := &windowsRuntimeDependencyE2ENetworkGate{}
				cacheHitResult, cacheHitErr := ProvisionWindowsRuntimeDependencyWithOptions(context.Background(), testCase.product, WindowsRuntimeDependencyProvisionOptions{
					CacheRoot: cacheRoot, HTTPClient: &http.Client{Transport: cacheHitGate}, InstallTimeout: 2 * time.Minute,
				})
				receipt.CacheHitHTTPRequests = cacheHitGate.requests.Load()
				if cacheHitErr != nil || !cacheHitResult.CacheHit {
					receipt.Error = fmt.Sprintf("Swift cache-hit provision: %v", cacheHitErr)
					t.Fatalf("Swift cache-hit provision: result=%+v err=%v", cacheHitResult, cacheHitErr)
				}
				if receipt.CacheHitHTTPRequests != 0 {
					receipt.Error = "Swift cache-hit provision performed HTTP requests"
					t.Fatalf("Swift cache-hit provision performed %d HTTP requests", receipt.CacheHitHTTPRequests)
				}
				if cacheHitResult.RootPath != result.RootPath || cacheHitResult.ServerPath != result.ServerPath || cacheHitResult.Architecture != result.Architecture {
					receipt.Error = "Swift cache-hit provision changed the published identity"
					t.Fatalf("Swift cache-hit changed identity: first=%+v second=%+v", result, cacheHitResult)
				}
				if stagingErr := windowsRuntimeDependencyStagingError(cacheRoot); stagingErr != nil {
					receipt.Error = stagingErr.Error()
					t.Fatal(stagingErr)
				}
			}
			receipt.StagingClean = true
			phase.Store("complete")
			fileCount, totalBytes, treeExists := windowsRuntimeDependencyE2EVisibleTreeStats(cacheRoot)
			t.Logf("%q E2E phase=complete elapsed=%s cache_base=%q tree_exists=%t files=%d bytes=%d pid=%d start_identity_present=%t alive=%t", testCase.product, time.Since(started).Round(time.Millisecond), filepath.Base(cacheRoot), treeExists, fileCount, totalBytes, verification.LSPPID, verification.LSPProcessStartTokenPresent, verification.LSPPIDAlive)
			t.Logf("%q Windows %s/process-%s provision/version/LSP lifecycle: provision=%s version=%s lsp=%s pid=%d exit=%d alive=%t receipt=%s", testCase.product, platform.NativeArch, platform.ProcessArch, receipt.ProvisionDuration, verification.VersionDuration, verification.LSPDuration, verification.LSPPID, verification.LSPExitCode, verification.LSPPIDAlive, receiptPath)
		})
	}
}

func windowsRuntimeDependencyE2ELocalSwiftAssetFetcher(source string) WindowsRuntimeDependencyAssetFetcher {
	return func(ctx context.Context, _ WindowsRuntimeDependencyAsset, destination string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		input, err := os.Open(source)
		if err != nil {
			return fmt.Errorf("open pinned local Swift asset: %w", err)
		}
		defer input.Close()
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return fmt.Errorf("create local Swift asset destination: %w", err)
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return fmt.Errorf("copy pinned local Swift asset: %w", copyErr)
		}
		return closeErr
	}
}

// windowsRuntimeDependencyE2EHeartbeat 记录产品中立的 Windows runtime-dependency 阶段、平台和树规模，避免把 SQLS/其他产品误标为 Swift。
func windowsRuntimeDependencyE2EHeartbeat(t *testing.T, started time.Time, cacheRoot string, phase *atomic.Value, product WindowsRuntimeDependencyProduct, platform WindowsHostPlatform) func() {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				phaseName, _ := phase.Load().(string)
				fileCount, totalBytes, exists := windowsRuntimeDependencyE2EVisibleTreeStats(cacheRoot)
				t.Logf("Windows runtime-dependency E2E heartbeat product=%s platform=windows-native-%s-process-%s phase=%s elapsed=%s cache_base=%q tree_exists=%t files=%d bytes=%d pid=0 start_identity_present=false", product, platform.NativeArch, platform.ProcessArch, phaseName, time.Since(started).Round(time.Second), filepath.Base(cacheRoot), exists, fileCount, totalBytes)
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func windowsRuntimeDependencyE2EVisibleTreeStats(root string) (int, int64, bool) {
	root = filepath.Clean(root)
	if root == "." || strings.TrimSpace(root) == "" {
		return 0, 0, false
	}
	if _, err := os.Stat(root); err != nil {
		return 0, 0, false
	}
	count := 0
	var totalBytes int64
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type().IsRegular() {
			info, infoErr := entry.Info()
			if infoErr == nil {
				count++
				totalBytes += info.Size()
			}
		}
		return nil
	})
	return count, totalBytes, true
}

func windowsRuntimeDependencyE2EProducts(raw string) ([]windowsRuntimeDependencyE2ECase, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "dotnet,jdtls"
	}
	all := map[string]windowsRuntimeDependencyE2ECase{
		"go":            {product: WindowsRuntimeDependencyProductGoGopls, version: "0.23.0"},
		"gopls":         {product: WindowsRuntimeDependencyProductGoGopls, version: "0.23.0"},
		"sql":           {product: WindowsRuntimeDependencyProductGoSQLS, version: WindowsGoSQLSVersion},
		"sqls":          {product: WindowsRuntimeDependencyProductGoSQLS, version: WindowsGoSQLSVersion},
		"go-sqls":       {product: WindowsRuntimeDependencyProductGoSQLS, version: WindowsGoSQLSVersion},
		"dotnet":        {product: WindowsRuntimeDependencyProductDotnetCsharpLS, version: "0.26.0"},
		"csharp":        {product: WindowsRuntimeDependencyProductDotnetCsharpLS, version: "0.26.0"},
		"jdtls":         {product: WindowsRuntimeDependencyProductJDKJDTLS},
		"java":          {product: WindowsRuntimeDependencyProductJDKJDTLS},
		"ruby":          {product: WindowsRuntimeDependencyProductRubySolargraph, version: "0.60.2"},
		"solargraph":    {product: WindowsRuntimeDependencyProductRubySolargraph, version: "0.60.2"},
		"swift":         {product: WindowsRuntimeDependencyProductSwiftSourceKitLS, version: "6.3.3"},
		"sourcekit":     {product: WindowsRuntimeDependencyProductSwiftSourceKitLS, version: "6.3.3"},
		"sourcekit-lsp": {product: WindowsRuntimeDependencyProductSwiftSourceKitLS, version: "6.3.3"},
	}
	seen := make(map[WindowsRuntimeDependencyProduct]bool)
	selected := make([]windowsRuntimeDependencyE2ECase, 0, 2)
	for _, token := range strings.Split(raw, ",") {
		key := strings.ToLower(strings.TrimSpace(token))
		if key == "" {
			continue
		}
		testCase, ok := all[key]
		if !ok {
			return nil, fmt.Errorf("unknown MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E_PRODUCTS value %q", token)
		}
		if !seen[testCase.product] {
			selected = append(selected, testCase)
			seen[testCase.product] = true
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E_PRODUCTS selected no products")
	}
	return selected, nil
}

func prepareWindowsRuntimeDependencyJDTLSWorkspace(result WindowsRuntimeDependencyProvisionResult) (string, []string, error) {
	workspaceRoot := runtimeDependencyJDTLSWorkspaceRoot(result.RootPath, result.Architecture, result.Cohort)
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "src"), 0o700); err != nil {
		return "", nil, fmt.Errorf("create JDTLS workspace %q: %w", workspaceRoot, err)
	}
	javaSource := []byte("package e2e;\n\npublic final class Main {\n    private Main() {}\n}\n")
	if err := os.WriteFile(filepath.Join(workspaceRoot, "src", "Main.java"), javaSource, 0o600); err != nil {
		return "", nil, fmt.Errorf("write JDTLS workspace source: %w", err)
	}
	if err := prepareWindowsRuntimeDependencyJDTLSWorkspaceConfiguration(result.RootPath, workspaceRoot); err != nil {
		return "", nil, fmt.Errorf("copy JDTLS mutable configuration: %w", err)
	}
	args, err := WindowsJDTLSLaunchArguments(result.ExecutablePath, workspaceRoot)
	if err != nil {
		return "", nil, err
	}
	if !sameWindowsRuntimeDependencyArgs(args, result.Args) {
		return "", nil, fmt.Errorf("production JDTLS launch args differ from WindowsJDTLSLaunchArguments: result=%#v constructed=%#v", result.Args, args)
	}
	dataPath, ok := windowsRuntimeDependencyArgumentValue(args, "-data")
	if !ok || !filepath.IsAbs(dataPath) || filepath.Dir(dataPath) != filepath.Clean(workspaceRoot) {
		return "", nil, fmt.Errorf("JDTLS -data is not the mutable workspace path: %#v", args)
	}
	if relative, err := filepath.Rel(result.RootPath, dataPath); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("JDTLS -data points inside immutable asset tree: %q", dataPath)
	}
	return workspaceRoot, args, nil
}

func prepareWindowsRuntimeDependencyRubyWorkspace(result WindowsRuntimeDependencyProvisionResult) (string, error) {
	workspaceRoot := runtimeDependencyRubySolargraphWorkspaceRoot(result.RootPath, result.Architecture, result.Cohort)
	if err := ensureDirectoryNoSymlink(workspaceRoot); err != nil {
		return "", fmt.Errorf("create Ruby workspace %q: %w", workspaceRoot, err)
	}
	rubySource := []byte("class Main\n  def greeting\n    \\\"hello\\\"\n  end\nend\n")
	if err := os.WriteFile(filepath.Join(workspaceRoot, "main.rb"), rubySource, 0o600); err != nil {
		return "", fmt.Errorf("write Ruby workspace source: %w", err)
	}
	return workspaceRoot, nil
}

func prepareWindowsRuntimeDependencySwiftWorkspace(parent string) (string, error) {
	if strings.TrimSpace(parent) == "" {
		return "", errors.New("Swift E2E workspace parent is empty")
	}
	workspaceRoot := filepath.Join(parent, "swift-workspace")
	if err := ensureDirectoryNoSymlink(workspaceRoot); err != nil {
		return "", fmt.Errorf("create Swift workspace %q: %w", workspaceRoot, err)
	}
	// SourceKit-LSP needs a real SwiftPM target (or a compilation database) to
	// construct a language service. A loose main.swift only proves initialize;
	// it deterministically returns -32001 for semantic requests.
	const targetName = "WindowsRuntimeDependencyE2E"
	sourceDirectory := filepath.Join(workspaceRoot, "Sources", targetName)
	if err := ensureDirectoryNoSymlink(sourceDirectory); err != nil {
		return "", fmt.Errorf("create Swift workspace source directory %q: %w", sourceDirectory, err)
	}
	packageManifest := []byte("// swift-tools-version: 6.0\nimport PackageDescription\n\nlet package = Package(\n    name: \"WindowsRuntimeDependencyE2E\",\n    targets: [\n        .executableTarget(\n            name: \"WindowsRuntimeDependencyE2E\",\n            path: \"Sources/WindowsRuntimeDependencyE2E\"\n        )\n    ]\n)\n")
	if err := os.WriteFile(filepath.Join(workspaceRoot, "Package.swift"), packageManifest, 0o600); err != nil {
		return "", fmt.Errorf("write Swift workspace Package.swift: %w", err)
	}
	swiftSource := []byte("struct Greeter {\n    let message: String\n}\n\nlet greeter = Greeter(message: \"hello\")\ngreeter.message\n")
	if err := os.WriteFile(filepath.Join(sourceDirectory, "main.swift"), swiftSource, 0o600); err != nil {
		return "", fmt.Errorf("write Swift workspace source: %w", err)
	}
	return workspaceRoot, nil
}

func verifyWindowsRuntimeDependencyVersionAndLSP(parent context.Context, result WindowsRuntimeDependencyProvisionResult, wantVersion, preparedWorkspaceRoot string) (windowsRuntimeDependencyE2EVerification, error) {
	if result.Product == WindowsRuntimeDependencyProductJDKJDTLS {
		workspaceRoot, ok := windowsRuntimeDependencyArgumentValue(result.Args, "-data")
		if !ok || !filepath.IsAbs(workspaceRoot) {
			return windowsRuntimeDependencyE2EVerification{}, errors.New("JDTLS launch args have no absolute -data path")
		}
		workspaceRoot = filepath.Dir(workspaceRoot)
		return verifyWindowsRuntimeDependencyLSPProcess(parent, result.ExecutablePath, result.Args, result.Env, result.WorkingDirectory, workspaceRoot, false)
	}
	if result.Product == WindowsRuntimeDependencyProductSwiftSourceKitLS {
		return verifyWindowsRuntimeDependencySwift(parent, result, wantVersion, preparedWorkspaceRoot)
	}
	workspaceRoot := ""
	if result.Product == WindowsRuntimeDependencyProductRubySolargraph {
		workspaceRoot = runtimeDependencyRubySolargraphWorkspaceRoot(result.RootPath, result.Architecture, result.Cohort)
	}
	versionContext, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	versionArgs := []string{"--version"}
	if result.Product == WindowsRuntimeDependencyProductGoGopls {
		versionArgs = []string{"version"}
	}
	versionStarted := time.Now()
	versionCommand := exec.CommandContext(versionContext, result.ServerPath, versionArgs...)
	versionCommand.Dir = result.WorkingDirectory
	versionCommand.Env = runtimeDependencyMinimalEnvironment(result.Env)
	versionOutput, err := versionCommand.CombinedOutput()
	if err != nil {
		report := windowsRuntimeDependencyE2EVerification{VersionDuration: time.Since(versionStarted).Round(time.Millisecond).String()}
		recordWindowsRuntimeDependencyVersionEvidence(&report, versionArgs, versionOutput)
		return report, fmt.Errorf("run version command: %w (%s)", err, windowsRuntimeDependencyE2EByteEvidence(versionOutput))
	}
	if !strings.Contains(string(versionOutput), wantVersion) {
		report := windowsRuntimeDependencyE2EVerification{VersionDuration: time.Since(versionStarted).Round(time.Millisecond).String()}
		recordWindowsRuntimeDependencyVersionEvidence(&report, versionArgs, versionOutput)
		return report, fmt.Errorf("version output does not contain %q (%s)", wantVersion, windowsRuntimeDependencyE2EByteEvidence(versionOutput))
	}

	versionDuration := time.Since(versionStarted).Round(time.Millisecond).String()
	verification, lspErr := verifyWindowsRuntimeDependencyLSPProcess(parent, result.ServerPath, result.Args, result.Env, result.WorkingDirectory, workspaceRoot, false)
	recordWindowsRuntimeDependencyVersionEvidence(&verification, versionArgs, versionOutput)
	verification.VersionDuration = versionDuration
	return verification, lspErr
}

func verifyWindowsRuntimeDependencySwift(parent context.Context, result WindowsRuntimeDependencyProvisionResult, wantVersion, workspaceRoot string) (windowsRuntimeDependencyE2EVerification, error) {
	report := windowsRuntimeDependencyE2EVerification{}
	if err := verifyWindowsRuntimeDependencySwiftFacts(result, &report); err != nil {
		return report, err
	}
	if strings.TrimSpace(workspaceRoot) == "" {
		return report, errors.New("Swift E2E workspace root is empty")
	}
	versionContext, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	versionArgs := []string{"--version"}
	versionStarted := time.Now()
	versionCommand := exec.CommandContext(versionContext, result.ExecutablePath, versionArgs...)
	versionCommand.Dir = result.WorkingDirectory
	versionCommand.Env = runtimeDependencyMinimalEnvironment(result.Env)
	versionOutput, err := versionCommand.CombinedOutput()
	recordWindowsRuntimeDependencyVersionEvidence(&report, versionArgs, versionOutput)
	report.VersionDuration = time.Since(versionStarted).Round(time.Millisecond).String()
	if err != nil {
		return report, fmt.Errorf("run Swift compiler version command: %w (%s)", err, windowsRuntimeDependencyE2EByteEvidence(versionOutput))
	}
	if !bytes.Contains(bytes.ToLower(versionOutput), []byte(strings.ToLower(wantVersion))) {
		return report, fmt.Errorf("Swift compiler version output does not contain %q (%s)", wantVersion, windowsRuntimeDependencyE2EByteEvidence(versionOutput))
	}

	sourcePath := filepath.Join(workspaceRoot, "typecheck.swift")
	if err := os.WriteFile(sourcePath, []byte("struct TypecheckProbe { let value: String }\nlet probe = TypecheckProbe(value: \"ok\")\n_ = probe.value\n"), 0o600); err != nil {
		return report, fmt.Errorf("write Swift typecheck source: %w", err)
	}
	defer func() { _ = os.Remove(sourcePath) }()
	// Swift's Windows SDK and toolchain resource directory are selected
	// explicitly from the published cohort. This is the production target
	// configuration; no freestanding or machine-wide SDK workaround is allowed.
	typecheckArgs := swiftWindowsTypecheckArgs(result.RootPath, sourcePath)
	typecheckStarted := time.Now()
	typecheckCommand := exec.CommandContext(versionContext, result.ExecutablePath, typecheckArgs...)
	typecheckCommand.Dir = result.WorkingDirectory
	typecheckCommand.Env = runtimeDependencyMinimalEnvironment(result.Env)
	typecheckOutput, typecheckErr := typecheckCommand.CombinedOutput()
	report.SwiftTypecheckArgCount = len(typecheckArgs)
	report.SwiftTypecheckArgsSHA256 = windowsRuntimeDependencyE2EStringListSHA256(typecheckArgs)
	report.SwiftTypecheckBytes = len(typecheckOutput)
	report.SwiftTypecheckSHA256 = windowsRuntimeDependencyE2EBytesSHA256(typecheckOutput)
	if typecheckErr != nil {
		return report, fmt.Errorf("Swift minimum typecheck failed after %s: %w (%s)", time.Since(typecheckStarted).Round(time.Millisecond), typecheckErr, windowsRuntimeDependencyE2EByteEvidence(typecheckOutput))
	}

	lspReport, lspErr := verifyWindowsRuntimeDependencyLSPProcess(parent, result.ServerPath, result.Args, result.Env, result.WorkingDirectory, workspaceRoot, true)
	lspReport.SwiftInstallerSHA256 = report.SwiftInstallerSHA256
	lspReport.SwiftInstallerPEMachine = report.SwiftInstallerPEMachine
	lspReport.SwiftAttachedCABSize = report.SwiftAttachedCABSize
	lspReport.SwiftAttachedCABSHA512 = report.SwiftAttachedCABSHA512
	lspReport.SwiftPayloadCount = report.SwiftPayloadCount
	lspReport.SwiftCompilerPEMachine = report.SwiftCompilerPEMachine
	lspReport.SwiftSourceKitPEMachine = report.SwiftSourceKitPEMachine
	lspReport.SwiftRuntimeMSMFileCount = report.SwiftRuntimeMSMFileCount
	lspReport.SwiftRuntimeMSMManifestSHA256 = report.SwiftRuntimeMSMManifestSHA256
	lspReport.SwiftRuntimeMSMSourceManifestSHA256 = report.SwiftRuntimeMSMSourceManifestSHA256
	lspReport.SwiftRuntimeMSMCABSize = report.SwiftRuntimeMSMCABSize
	lspReport.SwiftRuntimeMSMCABSHA256 = report.SwiftRuntimeMSMCABSHA256
	lspReport.SwiftRuntimeVCLibsFileCount = report.SwiftRuntimeVCLibsFileCount
	lspReport.SwiftRuntimeVCLibsManifestSHA256 = report.SwiftRuntimeVCLibsManifestSHA256
	lspReport.SwiftRejectedRuntimeDLL = report.SwiftRejectedRuntimeDLL
	lspReport.SwiftRejectedRuntimeFileID = report.SwiftRejectedRuntimeFileID
	lspReport.SwiftRejectedRuntimeComponent = report.SwiftRejectedRuntimeComponent
	lspReport.SwiftRejectedRuntimeCABMember = report.SwiftRejectedRuntimeCABMember
	lspReport.SwiftRejectedRuntimeSHA256 = report.SwiftRejectedRuntimeSHA256
	lspReport.SwiftRejectedRuntimePEMachine = report.SwiftRejectedRuntimePEMachine
	lspReport.SwiftRejectedRuntimePresent = report.SwiftRejectedRuntimePresent
	lspReport.VersionArgCount = report.VersionArgCount
	lspReport.VersionArgsSHA256 = report.VersionArgsSHA256
	lspReport.VersionOutputBytes = report.VersionOutputBytes
	lspReport.VersionOutputSHA256 = report.VersionOutputSHA256
	lspReport.VersionDuration = report.VersionDuration
	lspReport.SwiftTypecheckArgCount = report.SwiftTypecheckArgCount
	lspReport.SwiftTypecheckArgsSHA256 = report.SwiftTypecheckArgsSHA256
	lspReport.SwiftTypecheckBytes = report.SwiftTypecheckBytes
	lspReport.SwiftTypecheckSHA256 = report.SwiftTypecheckSHA256
	return lspReport, lspErr
}

func verifyWindowsRuntimeDependencySwiftFacts(result WindowsRuntimeDependencyProvisionResult, report *windowsRuntimeDependencyE2EVerification) error {
	if report == nil {
		return errors.New("Swift E2E verification report is nil")
	}
	if result.Platform.NativeArch != WindowsHostArchARM64 || result.Architecture != WindowsHostArchARM64 {
		return fmt.Errorf("Swift E2E requires NativeArch=%s, got platform=%s asset=%s", WindowsHostArchARM64, result.Platform.NativeArch, result.Architecture)
	}
	installerPath := filepath.Join(result.RootPath, ".runtime-assets", "swift-toolchain", "swift-toolchain-6.3.3.payload")
	wantCompilerPath := filepath.Join(WindowsSwiftSourceKitLSPToolchainBin(result.RootPath), "swiftc.exe")
	wantServerPath := filepath.Join(WindowsSwiftSourceKitLSPToolchainBin(result.RootPath), "sourcekit-lsp.exe")
	if filepath.Clean(result.ExecutablePath) != filepath.Clean(wantCompilerPath) || filepath.Clean(result.ServerPath) != filepath.Clean(wantServerPath) {
		return fmt.Errorf("Swift launch paths are not the locked toolchain paths: executable=%q/%q server=%q/%q", result.ExecutablePath, wantCompilerPath, result.ServerPath, wantServerPath)
	}
	for _, installed := range []string{
		filepath.Join(result.RootPath, filepath.FromSlash(swiftCompilerPath)),
		filepath.Join(result.RootPath, filepath.FromSlash(swiftSourceKitLSPServerPath)),
	} {
		if _, err := requireRegularWindowsRuntimeDependencyPath(installed); err != nil {
			return fmt.Errorf("Swift official installed toolchain path is missing: %w", err)
		}
	}
	if err := verifySwiftSHA256(installerPath, swiftARM64InstallerSHA256); err != nil {
		return fmt.Errorf("verify official Swift installer SHA-256: %w", err)
	}
	if err := validateSwiftInstallerPE(installerPath, WindowsHostArchARM64); err != nil {
		return fmt.Errorf("verify official Swift installer PE ARM64: %w", err)
	}
	attachedSize, attachedSHA512, err := windowsRuntimeDependencySwiftAttachedCABFacts(installerPath)
	if err != nil {
		return err
	}
	if err := validateSwiftWindowsRuntimeDependencyPayloads(result.RootPath); err != nil {
		return fmt.Errorf("verify official Swift embedded payloads: %w", err)
	}
	msmManifest, err := windowsRuntimeDependencySwiftRuntimeManifest(result.RootPath, swiftWindowsMSMRuntimeNames())
	if err != nil {
		return fmt.Errorf("verify Swift rtl.arm64.msm runtime manifest: %w", err)
	}
	vcManifest, err := windowsRuntimeDependencySwiftRuntimeManifest(result.RootPath, swiftWindowsVCLibsRuntimeNames())
	if err != nil {
		return fmt.Errorf("verify Swift app-local VCLibs runtime manifest: %w", err)
	}
	rejectedPath := filepath.Join(result.RootPath, swiftWindowsRejectedRuntimeDLL)
	if _, err := os.Lstat(rejectedPath); err == nil {
		return fmt.Errorf("rejected Swift x64 runtime helper remains in cohort: %q", swiftWindowsRejectedRuntimeDLL)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect rejected Swift x64 runtime helper: %w", err)
	}
	installerPE, err := windowsRuntimeDependencyPEMachine(installerPath)
	if err != nil {
		return fmt.Errorf("read Swift installer PE machine: %w", err)
	}
	compilerPE, err := windowsRuntimeDependencyPEMachine(result.ExecutablePath)
	if err != nil {
		return fmt.Errorf("read swiftc PE machine: %w", err)
	}
	serverPE, err := windowsRuntimeDependencyPEMachine(result.ServerPath)
	if err != nil {
		return fmt.Errorf("read sourcekit-lsp PE machine: %w", err)
	}
	for name, machine := range map[string]string{"installer": installerPE, "swiftc": compilerPE, "sourcekit-lsp": serverPE} {
		if machine != "0xaa64" {
			return fmt.Errorf("Swift %s PE machine = %s, want 0xaa64 ARM64", name, machine)
		}
	}
	payloadDir := filepath.Join(result.RootPath, ".runtime-assets", "swift-toolchain", "payloads")
	entries, err := os.ReadDir(payloadDir)
	if err != nil {
		return fmt.Errorf("read Swift payload directory: %w", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".swift-attached-") || entry.Name() == ".swift-attached.cab" {
			return fmt.Errorf("Swift attached CAB temporary remains in payload directory: %q", entry.Name())
		}
	}
	report.SwiftInstallerSHA256 = strings.ToLower(swiftARM64InstallerSHA256)
	report.SwiftInstallerPEMachine = installerPE
	report.SwiftAttachedCABSize = attachedSize
	report.SwiftAttachedCABSHA512 = attachedSHA512
	report.SwiftPayloadCount = len(swiftEmbeddedPayloads)
	report.SwiftCompilerPEMachine = compilerPE
	report.SwiftSourceKitPEMachine = serverPE
	report.SwiftRuntimeMSMFileCount = len(swiftARM64RuntimeMSMFiles)
	report.SwiftRuntimeMSMManifestSHA256 = msmManifest
	report.SwiftRuntimeMSMSourceManifestSHA256 = windowsRuntimeDependencySwiftMSMSourceManifestSHA256()
	report.SwiftRuntimeMSMCABSize = swiftARM64RuntimeCABSize
	report.SwiftRuntimeMSMCABSHA256 = strings.ToLower(swiftARM64RuntimeCABSHA256)
	report.SwiftRuntimeVCLibsFileCount = len(swiftWindowsVCLibsRuntimeNames())
	report.SwiftRuntimeVCLibsManifestSHA256 = vcManifest
	report.SwiftRejectedRuntimeDLL = swiftWindowsRejectedRuntimeDLL
	report.SwiftRejectedRuntimeFileID = swiftWindowsRejectedRuntimeFileID
	report.SwiftRejectedRuntimeComponent = swiftWindowsRejectedRuntimeComponent
	report.SwiftRejectedRuntimeCABMember = swiftWindowsRejectedRuntimeCABMember
	report.SwiftRejectedRuntimeSHA256 = strings.ToLower(swiftWindowsRejectedRuntimeSHA256)
	report.SwiftRejectedRuntimePEMachine = "0x8664"
	report.SwiftRejectedRuntimePresent = false
	return nil
}

func windowsRuntimeDependencySwiftAttachedCABFacts(installerPath string) (int64, string, error) {
	input, err := os.Open(installerPath)
	if err != nil {
		return 0, "", fmt.Errorf("open Swift installer for attached CAB evidence: %w", err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return 0, "", fmt.Errorf("stat Swift installer for attached CAB evidence: %w", err)
	}
	offset, size, err := findSwiftAttachedCAB(input, info.Size())
	if err != nil {
		return 0, "", fmt.Errorf("locate official Swift attached CAB: %w", err)
	}
	if _, err := input.Seek(offset, io.SeekStart); err != nil {
		return 0, "", fmt.Errorf("seek official Swift attached CAB: %w", err)
	}
	hasher := sha512.New()
	count, err := io.CopyN(hasher, input, size)
	if err != nil {
		return 0, "", fmt.Errorf("hash official Swift attached CAB: %w", err)
	}
	if count != size {
		return 0, "", fmt.Errorf("official Swift attached CAB size = %d, want %d", count, size)
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(digest, swiftAttachedCABSHA512) {
		return 0, "", fmt.Errorf("official Swift attached CAB SHA-512 = %s, want %s", digest, swiftAttachedCABSHA512)
	}
	return size, digest, nil
}

func windowsRuntimeDependencyPEMachine(path string) (string, error) {
	file, err := pe.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return fmt.Sprintf("0x%04x", uint16(file.FileHeader.Machine)), nil
}

func swiftWindowsMSMRuntimeNames() []string {
	names := make([]string, 0, len(swiftARM64RuntimeMSMFiles))
	for _, row := range swiftARM64RuntimeMSMFiles {
		names = append(names, row.longName)
	}
	return names
}

func swiftWindowsVCLibsRuntimeNames() []string {
	names := make([]string, 0, len(swiftWindowsARM64RuntimeDLLs))
	for _, name := range swiftWindowsARM64RuntimeDLLs {
		if _, isMSMFile := swiftARM64RuntimeMSMFileSHA256[name]; !isMSMFile {
			names = append(names, name)
		}
	}
	return names
}

func windowsRuntimeDependencySwiftRuntimeManifest(root string, names []string) (string, error) {
	entries := make([]string, 0, len(names))
	runtimeRoot := WindowsSwiftSourceKitLSPRuntimeRoot(root)
	for _, name := range names {
		path := filepath.Join(runtimeRoot, name)
		info, err := requireRegularWindowsRuntimeDependencyPath(path)
		if err != nil {
			return "", fmt.Errorf("runtime file %q: %w", name, err)
		}
		input, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("open runtime file %q: %w", name, err)
		}
		hasher := sha256.New()
		_, copyErr := io.Copy(hasher, input)
		closeErr := input.Close()
		if copyErr != nil {
			return "", fmt.Errorf("hash runtime file %q: %w", name, copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close runtime file %q: %w", name, closeErr)
		}
		machine, err := windowsRuntimeDependencyPEMachine(path)
		if err != nil {
			return "", fmt.Errorf("read runtime file %q PE: %w", name, err)
		}
		entries = append(entries, fmt.Sprintf("%s|%d|%s|%s", name, info.Size(), hex.EncodeToString(hasher.Sum(nil)), machine))
	}
	sort.Strings(entries)
	return windowsRuntimeDependencyE2EStringListSHA256(entries), nil
}

func windowsRuntimeDependencySwiftMSMSourceManifestSHA256() string {
	entries := make([]string, 0, len(swiftARM64RuntimeMSMFiles))
	for _, row := range swiftARM64RuntimeMSMFiles {
		entries = append(entries, fmt.Sprintf("%s|%s|%s|%d|%d|%s", row.fileID, row.shortName, row.longName, row.size, row.sequence, swiftARM64RuntimeMSMFileSHA256[row.longName]))
	}
	sort.Strings(entries)
	return windowsRuntimeDependencyE2EStringListSHA256(entries)
}

func verifyWindowsRuntimeDependencyLSPProcess(parent context.Context, executable string, args, env []string, workingDirectory, workspaceRoot string, requireNonEmptySemantic bool) (report windowsRuntimeDependencyE2EVerification, err error) {
	lspContext, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	lspStarted := time.Now()
	command := exec.CommandContext(lspContext, executable, args...)
	command.Dir = workingDirectory
	command.Env = runtimeDependencyMinimalEnvironment(env)
	commandEvidence := append([]string{executable}, args...)
	report.LSPCommandArgCount = len(commandEvidence)
	report.LSPCommandSHA256 = windowsRuntimeDependencyE2EStringListSHA256(commandEvidence)
	report.LSPWorkspaceRootSHA256 = windowsRuntimeDependencyE2EStringSHA256(workspaceRoot)
	initializeRootURI := ""
	if workspaceRoot != "" {
		initializeRootURI = windowsRuntimeDependencyFileURI(workspaceRoot)
		report.InitializeRootURISHA256 = windowsRuntimeDependencyE2EStringSHA256(initializeRootURI)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return report, fmt.Errorf("open LSP stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return report, fmt.Errorf("open LSP stdout: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return report, fmt.Errorf("start exact LSP path: %w", err)
	}
	pid := command.Process.Pid
	report.LSPPID = pid
	wireTrace, wireErr := newWindowsRuntimeDependencyLSPWireTrace(pid)
	if wireErr != nil {
		return report, wireErr
	}
	if wireTrace != nil {
		report.LSPWireLogPath = wireTrace.path
	}
	processStartToken, err := windowsRuntimeDependencyProcessStartToken(pid)
	if err != nil {
		return report, fmt.Errorf("capture LSP process start identity for PID %d: %w", pid, err)
	}
	report.LSPProcessStartTokenSHA256 = windowsRuntimeDependencyE2EStringSHA256(processStartToken)
	report.LSPProcessStartTokenPresent = true
	finished := false
	defer func() {
		report.LSPDuration = time.Since(lspStarted).Round(time.Millisecond).String()
		report.LSPStderrBytes = stderr.Len()
		report.LSPStderrSHA256 = windowsRuntimeDependencyE2EBytesSHA256(stderr.Bytes())
		if wireTrace != nil {
			report.LSPStderrLogPath = wireTrace.stderrPath
			if stderrErr := wireTrace.writeStderr(stderr.Bytes()); stderrErr != nil && err == nil {
				err = stderrErr
			}
			if closeErr := wireTrace.close(); closeErr != nil && err == nil {
				err = fmt.Errorf("close LSP wire log: %w", closeErr)
			}
		}
		if !finished && command.Process != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	reader := bufio.NewReader(stdout)
	initializeParams := map[string]any{"processId": os.Getpid(), "rootUri": initializeRootURI, "capabilities": map[string]any{}}
	if requireNonEmptySemantic {
		initializeParams["capabilities"] = map[string]any{
			"textDocument": map[string]any{
				"hover": map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
			},
		}
	}
	if initializeRootURI != "" {
		initializeParams["workspaceFolders"] = []map[string]string{{"uri": initializeRootURI, "name": "windows-runtime-dependency-e2e"}}
	}
	if err := writeWindowsRuntimeDependencyLSPRequest(stdin, 1, "initialize", initializeParams, wireTrace); err != nil {
		return report, err
	}
	if _, err := readWindowsRuntimeDependencyLSPResponse(reader, 1, wireTrace); err != nil {
		return report, fmt.Errorf("read initialize response: %w (%s)", err, windowsRuntimeDependencyE2EByteEvidence(stderr.Bytes()))
	}
	if err := writeWindowsRuntimeDependencyLSPNotification(stdin, "initialized", map[string]any{}, wireTrace); err != nil {
		return report, fmt.Errorf("write initialized notification: %w", err)
	}
	nextRequestID := 2
	if workspaceRoot != "" {
		documentPath, documentErr := windowsRuntimeDependencySemanticDocumentPath(workspaceRoot)
		if documentErr != nil {
			return report, documentErr
		}
		documentURI := windowsRuntimeDependencyFileURI(documentPath)
		semanticLanguageID, semanticLine, semanticCharacter := windowsRuntimeDependencySemanticPosition(documentPath)
		if requireNonEmptySemantic {
			documentContents, readErr := os.ReadFile(documentPath)
			if readErr != nil {
				return report, fmt.Errorf("read semantic workspace document: %w", readErr)
			}
			if err := writeWindowsRuntimeDependencyLSPNotification(stdin, "textDocument/didOpen", map[string]any{
				"textDocument": map[string]any{"uri": documentURI, "languageId": semanticLanguageID, "version": 1, "text": string(documentContents)},
			}, wireTrace); err != nil {
				return report, fmt.Errorf("write semantic didOpen notification: %w", err)
			}
			// SourceKit-LSP opens the document asynchronously. The upstream SwiftPM
			// test harness uses workspace/synchronize(index: true) before semantic
			// requests so the build graph and language-service assignment are
			// observable here; an empty params object does not wait for the graph.
			if err := writeWindowsRuntimeDependencyLSPRequest(stdin, nextRequestID, "workspace/synchronize", map[string]any{"index": true}, wireTrace); err != nil {
				return report, fmt.Errorf("write workspace synchronize request: %w", err)
			}
			if _, err := readWindowsRuntimeDependencyLSPResponse(reader, nextRequestID, wireTrace); err != nil {
				return report, fmt.Errorf("read workspace synchronize response: %w (%s)", err, windowsRuntimeDependencyE2EByteEvidence(stderr.Bytes()))
			}
			nextRequestID++
		}
		if err := writeWindowsRuntimeDependencyLSPRequest(stdin, nextRequestID, "textDocument/hover", map[string]any{
			"textDocument": map[string]string{"uri": documentURI},
			"position":     map[string]int{"line": semanticLine, "character": semanticCharacter},
		}, wireTrace); err != nil {
			return report, fmt.Errorf("write semantic request: %w", err)
		}
		semanticResponse, err := readWindowsRuntimeDependencyLSPResponse(reader, nextRequestID, wireTrace)
		if err != nil {
			return report, fmt.Errorf("read semantic response: %w (%s)", err, windowsRuntimeDependencyE2EByteEvidence(stderr.Bytes()))
		}
		var envelope struct {
			Error  json.RawMessage `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(semanticResponse, &envelope); err != nil {
			return report, fmt.Errorf("decode semantic response: %w", err)
		}
		if errorPayload := bytes.TrimSpace(envelope.Error); len(errorPayload) != 0 && !bytes.Equal(errorPayload, []byte("null")) {
			return report, fmt.Errorf("semantic request returned error (%s)", windowsRuntimeDependencyE2EByteEvidence(errorPayload))
		}
		if requireNonEmptySemantic {
			if err := requireWindowsRuntimeDependencyNonEmptyHover(envelope.Result); err != nil {
				return report, err
			}
			report.SemanticResponseBytes = len(envelope.Result)
			report.SemanticResponseSHA256 = windowsRuntimeDependencyE2EBytesSHA256(envelope.Result)
			report.SemanticNonEmpty = true
		}
		report.SemanticMethod = "textDocument/hover"
		report.SemanticResponseID = nextRequestID
		nextRequestID++
	}
	if err := writeWindowsRuntimeDependencyLSPRequest(stdin, nextRequestID, "shutdown", nil, wireTrace); err != nil {
		return report, err
	}
	if _, err := readWindowsRuntimeDependencyLSPResponse(reader, nextRequestID, wireTrace); err != nil {
		return report, fmt.Errorf("read shutdown response: %w (%s)", err, windowsRuntimeDependencyE2EByteEvidence(stderr.Bytes()))
	}
	if err := writeWindowsRuntimeDependencyLSPNotification(stdin, "exit", nil, wireTrace); err != nil {
		return report, err
	}
	_ = stdin.Close()
	waitErr := command.Wait()
	finished = true
	if command.ProcessState != nil {
		report.LSPExitCode = command.ProcessState.ExitCode()
	}
	if waitErr != nil {
		return report, fmt.Errorf("wait for LSP exit (pid %d): %w (%s)", pid, waitErr, windowsRuntimeDependencyE2EByteEvidence(stderr.Bytes()))
	}
	if command.ProcessState == nil || !command.ProcessState.Exited() {
		return report, fmt.Errorf("LSP process %d did not reach an exited state", pid)
	}
	alive, err := runtimeDependencyWindowsPIDExists(pid)
	if err != nil {
		return report, err
	}
	report.LSPPIDAlive = alive
	if alive {
		currentStartToken, identityErr := windowsRuntimeDependencyProcessStartToken(pid)
		if identityErr != nil {
			return report, fmt.Errorf("LSP process %d remains but its start identity cannot be read: %w", pid, identityErr)
		}
		if currentStartToken == processStartToken {
			return report, fmt.Errorf("LSP process %d with the same start identity remains after shutdown/exit", pid)
		}
		return report, fmt.Errorf("LSP PID %d was reused by a different start identity after shutdown/exit", pid)
	}
	return report, nil
}

func runtimeDependencyMinimalEnvironment(overrides []string) []string {
	result := append([]string(nil), overrides...)
	for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP"} {
		if value := os.Getenv(key); value != "" {
			result = append(result, key+"="+value)
		}
	}
	return result
}

func sameWindowsRuntimeDependencyArgs(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func windowsRuntimeDependencyFileURI(path string) string {
	// Windows 驱动器路径必须编码为 file:///C:/...；file://C:/... 会被 SourceKit-LSP 当成错误 authority。
	return (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(filepath.Clean(path))}).String()
}

func windowsRuntimeDependencySemanticDocumentURI(workspaceRoot string) (string, error) {
	path, err := windowsRuntimeDependencySemanticDocumentPath(workspaceRoot)
	if err != nil {
		return "", err
	}
	return windowsRuntimeDependencyFileURI(path), nil
}

func windowsRuntimeDependencySemanticDocumentPath(workspaceRoot string) (string, error) {
	candidates := []string{
		filepath.Join(workspaceRoot, "Sources", "WindowsRuntimeDependencyE2E", "main.swift"),
		filepath.Join(workspaceRoot, "main.swift"),
		filepath.Join(workspaceRoot, "main.rb"),
		filepath.Join(workspaceRoot, "src", "Main.java"),
	}
	for _, candidate := range candidates {
		info, err := os.Lstat(candidate)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("inspect semantic workspace document %q: %w", candidate, err)
		}
		if isUnsafeAssetFile(info) || !info.Mode().IsRegular() {
			return "", fmt.Errorf("semantic workspace document is unsafe: %q", candidate)
		}
		return candidate, nil
	}
	return "", fmt.Errorf("workspace %q has no prepared semantic document", workspaceRoot)
}

func windowsRuntimeDependencySemanticPosition(documentPath string) (string, int, int) {
	switch strings.ToLower(filepath.Ext(documentPath)) {
	case ".swift":
		return "swift", 4, 14
	case ".java":
		return "java", 0, 0
	case ".rb":
		return "ruby", 0, 0
	default:
		return "", 0, 0
	}
}

func requireWindowsRuntimeDependencyNonEmptyHover(result json.RawMessage) error {
	trimmed := bytes.TrimSpace(result)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("semantic hover result is empty or null")
	}
	var hover struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := json.Unmarshal(trimmed, &hover); err != nil {
		return fmt.Errorf("decode semantic hover result: %w", err)
	}
	if len(bytes.TrimSpace(hover.Contents)) == 0 || bytes.Equal(bytes.TrimSpace(hover.Contents), []byte("null")) {
		return errors.New("semantic hover result has empty contents")
	}
	var value any
	if err := json.Unmarshal(hover.Contents, &value); err != nil {
		return fmt.Errorf("decode semantic hover contents: %w", err)
	}
	if !windowsRuntimeDependencyJSONValueNonEmpty(value) {
		return errors.New("semantic hover contents are empty")
	}
	return nil
}

func windowsRuntimeDependencyJSONValueNonEmpty(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		if len(typed) == 0 {
			return false
		}
		for _, item := range typed {
			if windowsRuntimeDependencyJSONValueNonEmpty(item) {
				return true
			}
		}
		return false
	case map[string]any:
		if len(typed) == 0 {
			return false
		}
		if content, ok := typed["value"]; ok {
			return windowsRuntimeDependencyJSONValueNonEmpty(content)
		}
		for key, item := range typed {
			if key == "kind" || key == "language" {
				continue
			}
			if windowsRuntimeDependencyJSONValueNonEmpty(item) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func windowsRuntimeDependencyHasPATHEnv(values []string) bool {
	for _, value := range values {
		key, _, ok := strings.Cut(value, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "PATH") {
			return true
		}
	}
	return false
}

type windowsRuntimeDependencyLSPWireTrace struct {
	file       *os.File
	path       string
	stderrPath string
}

func newWindowsRuntimeDependencyLSPWireTrace(pid int) (*windowsRuntimeDependencyLSPWireTrace, error) {
	directory := strings.TrimSpace(os.Getenv("MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E_WIRE_LOG_DIR"))
	if directory == "" {
		return nil, nil
	}
	if pid <= 1 {
		return nil, fmt.Errorf("invalid LSP PID %d for wire log", pid)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create LSP wire log directory %q: %w", directory, err)
	}
	path := filepath.Join(directory, fmt.Sprintf("swift-sourcekit-lsp-%d-wire.jsonl", pid))
	stderrPath := filepath.Join(directory, fmt.Sprintf("swift-sourcekit-lsp-%d-stderr.log", pid))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open LSP wire log %q: %w", path, err)
	}
	return &windowsRuntimeDependencyLSPWireTrace{file: file, path: path, stderrPath: stderrPath}, nil
}

func (trace *windowsRuntimeDependencyLSPWireTrace) writeStderr(stderr []byte) error {
	if trace == nil || trace.stderrPath == "" {
		return nil
	}
	redacted := []byte(windowsRuntimeDependencyE2ERedactAbsoluteWindowsPaths(string(stderr)))
	if err := os.WriteFile(trace.stderrPath, redacted, 0o600); err != nil {
		return fmt.Errorf("write LSP stderr log %q: %w", trace.stderrPath, err)
	}
	return nil
}

func (trace *windowsRuntimeDependencyLSPWireTrace) record(direction string, payload []byte) error {
	if trace == nil || trace.file == nil {
		return nil
	}
	record := struct {
		Direction string `json:"direction"`
		Bytes     int    `json:"bytes"`
		SHA256    string `json:"sha256"`
		Payload   string `json:"payload"`
	}{
		Direction: direction,
		Bytes:     len(payload),
		SHA256:    windowsRuntimeDependencyE2EBytesSHA256(payload),
		Payload:   windowsRuntimeDependencyE2ERedactAbsoluteWindowsPaths(string(payload)),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode LSP wire log record: %w", err)
	}
	if _, err := fmt.Fprintln(trace.file, string(encoded)); err != nil {
		return fmt.Errorf("write LSP wire log record: %w", err)
	}
	return nil
}

func (trace *windowsRuntimeDependencyLSPWireTrace) close() error {
	if trace == nil || trace.file == nil {
		return nil
	}
	err := trace.file.Close()
	trace.file = nil
	return err
}

func writeWindowsRuntimeDependencyLSPRequest(writer io.Writer, id int, method string, params any, trace ...*windowsRuntimeDependencyLSPWireTrace) error {
	payload := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if params == nil {
		delete(payload, "params")
	}
	return writeWindowsRuntimeDependencyLSPPayload(writer, payload, trace...)
}

func writeWindowsRuntimeDependencyLSPNotification(writer io.Writer, method string, params any, trace ...*windowsRuntimeDependencyLSPWireTrace) error {
	payload := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if params == nil {
		delete(payload, "params")
	}
	return writeWindowsRuntimeDependencyLSPPayload(writer, payload, trace...)
}

func writeWindowsRuntimeDependencyLSPPayload(writer io.Writer, payload any, trace ...*windowsRuntimeDependencyLSPWireTrace) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if len(trace) != 0 && trace[0] != nil {
		if err := trace[0].record("send", data); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}

func readWindowsRuntimeDependencyLSPMessage(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Content-Length") {
			contentLength, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
		}
	}
	if contentLength < 0 {
		return nil, errors.New("LSP response has no Content-Length")
	}
	message := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, message); err != nil {
		return nil, err
	}
	return message, nil
}

func readWindowsRuntimeDependencyLSPResponse(reader *bufio.Reader, wantID int, trace ...*windowsRuntimeDependencyLSPWireTrace) ([]byte, error) {
	for {
		message, err := readWindowsRuntimeDependencyLSPMessage(reader)
		if err != nil {
			return nil, err
		}
		if len(trace) != 0 && trace[0] != nil {
			if err := trace[0].record("receive", message); err != nil {
				return nil, err
			}
		}
		var envelope struct {
			ID *int `json:"id"`
		}
		if err := json.Unmarshal(message, &envelope); err != nil {
			return nil, fmt.Errorf("decode LSP response envelope: %w", err)
		}
		if envelope.ID != nil && *envelope.ID == wantID {
			var responseError struct {
				Error json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(message, &responseError); err != nil {
				return nil, fmt.Errorf("decode LSP response error envelope: %w", err)
			}
			if errorPayload := bytes.TrimSpace(responseError.Error); len(errorPayload) != 0 && !bytes.Equal(errorPayload, []byte("null")) {
				return nil, fmt.Errorf("LSP response %d returned error (%s)", wantID, windowsRuntimeDependencyE2EByteEvidence(errorPayload))
			}
			return message, nil
		}
	}
}

func runtimeDependencyWindowsPIDExists(pid int) (bool, error) {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		return false, errors.New("SystemRoot is empty while checking LSP PID")
	}
	tasklist := filepath.Join(systemRoot, "System32", "tasklist.exe")
	output, err := exec.Command(tasklist, "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("query exact LSP PID %d: %w (%s)", pid, err, strings.TrimSpace(string(output)))
	}
	return strings.Contains(string(output), fmt.Sprintf(" %d ", pid)) || strings.HasPrefix(strings.TrimSpace(string(output)), strconv.Itoa(pid)+" "), nil
}

func windowsRuntimeDependencyProcessStartToken(pid int) (string, error) {
	if pid <= 1 {
		return "", fmt.Errorf("invalid Windows process PID %d", pid)
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", fmt.Errorf("open Windows process %d for start identity: %w", pid, err)
	}
	defer windows.CloseHandle(handle)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return "", fmt.Errorf("read Windows process %d start identity: %w", pid, err)
	}
	return strconv.FormatUint(uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime), 10), nil
}

type windowsRuntimeDependencyE2ENetworkGate struct {
	requests atomic.Int64
}

func (gate *windowsRuntimeDependencyE2ENetworkGate) RoundTrip(request *http.Request) (*http.Response, error) {
	if gate == nil {
		return nil, errors.New("Windows runtime dependency E2E network gate is nil")
	}
	gate.requests.Add(1)
	if request == nil || request.URL == nil {
		return nil, errors.New("unexpected network request during cache-hit proof: request URL is nil")
	}
	return nil, fmt.Errorf("unexpected network request during cache-hit proof: %s", request.URL.Redacted())
}

func windowsRuntimeDependencyE2ESwiftMSICommandRunner(logDir string, logPaths *[]string) WindowsRuntimeDependencyCommandRunner {
	return func(ctx context.Context, executable, workingDir string, args, env []string) error {
		commandArgs := append([]string(nil), args...)
		if strings.EqualFold(filepath.Base(executable), "msiexec.exe") {
			if err := os.MkdirAll(logDir, 0o700); err != nil {
				return fmt.Errorf("create Swift MSI verbose log directory %q: %w", logDir, err)
			}
			logPath := filepath.Join(logDir, fmt.Sprintf("swift-msiexec-%d.log", time.Now().UnixNano()))
			commandArgs = append(commandArgs, "/L*v", logPath)
			if logPaths != nil {
				*logPaths = append(*logPaths, logPath)
			}
		}
		return defaultWindowsRuntimeDependencyCommandRunner(ctx, executable, workingDir, commandArgs, env)
	}
}

func windowsRuntimeDependencyE2EShortCacheRoot(t *testing.T) string {
	t.Helper()
	tempRoot := strings.TrimSpace(os.Getenv("TEMP"))
	if tempRoot == "" {
		t.Fatal("TEMP is empty; cannot create the fresh short Windows runtime dependency cache")
	}
	parent, err := os.MkdirTemp(tempRoot, "mcp-rd-")
	if err != nil {
		t.Fatalf("create fresh short Windows runtime dependency cache parent: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(parent); err != nil {
			t.Errorf("remove fresh Windows runtime dependency cache parent %q: %v", parent, err)
		}
	})
	if len(parent) > 180 {
		t.Fatalf("fresh Windows runtime dependency cache parent is not short enough: %q", parent)
	}
	// Keep the cache root itself at the short TEMP child. Adding another
	// "empty-cache" component pushes the official Windows SDK MSI's deepest
	// paths over MAX_PATH even after Windows 8.3 canonicalization.
	return parent
}

// windowsRuntimeDependencyE2EReceiptPath 把原生架构和进程架构写入目录与文件名，
// 防止 ARM64、x64、x86 或 WOW64 证据在多机汇总时相互覆盖。
func windowsRuntimeDependencyE2EReceiptPath(product WindowsRuntimeDependencyProduct, platform WindowsHostPlatform) (string, error) {
	platformToken := "windows-" + platform.NativeArch + "-process-" + platform.ProcessArch
	directory := strings.TrimSpace(os.Getenv("MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E_RECEIPT_DIR"))
	if directory == "" {
		directory = filepath.Join(os.TempDir(), "mcp-lsp-"+platformToken+"-runtime-dependency-e2e-receipts")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create E2E receipt directory %q: %w", directory, err)
	}
	return filepath.Join(directory, platformToken+"-"+string(product)+"-e2e-receipt.json"), nil
}

func redactWindowsRuntimeDependencyE2EReceipt(receipt windowsRuntimeDependencyE2EReceipt) windowsRuntimeDependencyE2EReceipt {
	redacted := receipt
	cacheRoot := filepath.Clean(receipt.CacheRoot)
	rootPath := filepath.Clean(receipt.RootPath)
	workspaceRoot := filepath.Clean(receipt.WorkspaceRoot)
	redacted.CacheRoot = windowsRuntimeDependencyE2EPathDigest(cacheRoot)
	redacted.ReceiptPath = filepath.Base(receipt.ReceiptPath)
	redacted.RootPath = windowsRuntimeDependencyE2ERelativePath(cacheRoot, rootPath)
	redacted.WorkingDirectory = windowsRuntimeDependencyE2ERelativePath(cacheRoot, filepath.Clean(receipt.WorkingDirectory))
	redacted.WorkspaceRoot = windowsRuntimeDependencyE2EPathDigest(workspaceRoot)
	redacted.ExecutablePath = windowsRuntimeDependencyE2ERelativePath(rootPath, filepath.Clean(receipt.ExecutablePath))
	redacted.ServerPath = windowsRuntimeDependencyE2ERelativePath(rootPath, filepath.Clean(receipt.ServerPath))
	// wire/stderr 日志只保留文件名；绝对路径会暴露用户工作区或临时目录。
	redacted.Verification.LSPWireLogPath = filepath.Base(receipt.Verification.LSPWireLogPath)
	redacted.Verification.LSPStderrLogPath = filepath.Base(receipt.Verification.LSPStderrLogPath)
	redacted.SwiftMSIExecLogPaths = make([]string, 0, len(receipt.SwiftMSIExecLogPaths))
	for _, path := range receipt.SwiftMSIExecLogPaths {
		redacted.SwiftMSIExecLogPaths = append(redacted.SwiftMSIExecLogPaths, filepath.Base(path))
	}
	redacted.Error = windowsRuntimeDependencyE2ERedactPaths(receipt.Error, cacheRoot, rootPath, workspaceRoot, receipt.SwiftMSIExecLogPaths)
	redacted.PathEvidencePolicy = "absolute paths omitted; cache/root paths are relative or sha256 digests"
	return redacted
}

func windowsRuntimeDependencyE2EPathDigest(path string) string {
	if path == "." || strings.TrimSpace(path) == "" {
		return ""
	}
	return "sha256:" + windowsRuntimeDependencyE2EStringSHA256(path)
}

func windowsRuntimeDependencyE2ERelativePath(root, path string) string {
	if path == "." || strings.TrimSpace(path) == "" {
		return ""
	}
	if root != "." && strings.TrimSpace(root) != "" {
		if relative, err := filepath.Rel(root, path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			return filepath.ToSlash(relative)
		}
	}
	return windowsRuntimeDependencyE2EPathDigest(path)
}

func windowsRuntimeDependencyE2ERedactPaths(value string, paths ...interface{}) string {
	redacted := value
	for _, item := range paths {
		switch typed := item.(type) {
		case string:
			if typed != "" && typed != "." {
				redacted = strings.ReplaceAll(redacted, typed, "<redacted-path>")
			}
		case []string:
			for _, path := range typed {
				if path != "" {
					redacted = strings.ReplaceAll(redacted, path, "<redacted-path>")
				}
			}
		}
	}
	return windowsRuntimeDependencyE2ERedactAbsoluteWindowsPaths(redacted)
}

var windowsRuntimeDependencyE2EAbsoluteWindowsPathPattern = regexp.MustCompile(`(?i)(?:[a-z]:[\\/]|\\\\)[^"'\r\n,}\]]+`)

// windowsRuntimeDependencyE2ERedactAbsoluteWindowsPaths 统一清理错误和线协议载荷中的
// Windows 绝对路径；摘要仍由原始字节计算，便于复核而不泄露用户目录。
func windowsRuntimeDependencyE2ERedactAbsoluteWindowsPaths(value string) string {
	return windowsRuntimeDependencyE2EAbsoluteWindowsPathPattern.ReplaceAllString(value, "<redacted-path>")
}

func writeWindowsRuntimeDependencyE2EReceipt(path string, receipt windowsRuntimeDependencyE2EReceipt) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

// recordWindowsRuntimeDependencyVersionEvidence 只记录命令参数和输出的大小/摘要，
// 既能关联失败根因，又不会把本机路径或工具输出原文写进收据。
func recordWindowsRuntimeDependencyVersionEvidence(report *windowsRuntimeDependencyE2EVerification, args []string, output []byte) {
	if report == nil {
		return
	}
	report.VersionArgCount = len(args)
	report.VersionArgsSHA256 = windowsRuntimeDependencyE2EStringListSHA256(args)
	report.VersionOutputBytes = len(output)
	report.VersionOutputSHA256 = windowsRuntimeDependencyE2EBytesSHA256(output)
}

// windowsRuntimeDependencyE2EEnvKeys 返回排序后的环境变量键；值可能包含凭据，禁止进入日志或摘要输入。
func windowsRuntimeDependencyE2EEnvKeys(values []string) []string {
	keys := make([]string, 0, len(values))
	for _, value := range values {
		key, _, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, strings.ToUpper(strings.TrimSpace(key)))
	}
	sort.Strings(keys)
	return keys
}

func windowsRuntimeDependencyE2EStringListSHA256(values []string) string {
	hash := sha256.New()
	for _, value := range values {
		_, _ = io.WriteString(hash, strconv.Itoa(len(value)))
		_, _ = hash.Write([]byte{0})
		_, _ = io.WriteString(hash, value)
		_, _ = hash.Write([]byte{0})
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func windowsRuntimeDependencyE2EStringSHA256(value string) string {
	if value == "" {
		return ""
	}
	return windowsRuntimeDependencyE2EBytesSHA256([]byte(value))
}

func windowsRuntimeDependencyE2EBytesSHA256(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("%x", digest[:])
}

func windowsRuntimeDependencyE2EByteEvidence(value []byte) string {
	return fmt.Sprintf("bytes=%d sha256=%s", len(value), windowsRuntimeDependencyE2EBytesSHA256(value))
}

func windowsRuntimeDependencyStagingError(cacheRoot string) error {
	root := filepath.Join(cacheRoot, "runtime-dependencies")
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".staging-") {
			return fmt.Errorf("staging directory remains: %s", path)
		}
		return nil
	})
}

func assertNoWindowsRuntimeDependencyStaging(t *testing.T, cacheRoot string) {
	t.Helper()
	err := windowsRuntimeDependencyStagingError(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
}

// TestWindowsRuntimeDependencyE2EProductsKeepsSwiftOptIn 证明 Swift 官方网络下载只由显式产品过滤开启，默认门禁不联网。
func TestWindowsRuntimeDependencyE2EProductsKeepsSwiftOptIn(t *testing.T) {
	defaultProducts, err := windowsRuntimeDependencyE2EProducts("")
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range defaultProducts {
		if testCase.product == WindowsRuntimeDependencyProductSwiftSourceKitLS {
			t.Fatal("default runtime dependency E2E products unexpectedly include Swift")
		}
	}
	selected, err := windowsRuntimeDependencyE2EProducts("swift,sourcekit-lsp,swift")
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].product != WindowsRuntimeDependencyProductSwiftSourceKitLS || selected[0].version != "6.3.3" {
		t.Fatalf("Swift product filter = %#v, want one Swift 6.3.3 case", selected)
	}
}

// TestWindowsRuntimeDependencyFileURIUsesCanonicalWindowsForm 防止驱动器号被误编码为 URI authority。
func TestWindowsRuntimeDependencyFileURIUsesCanonicalWindowsForm(t *testing.T) {
	uri := windowsRuntimeDependencyFileURI(`C:\runtime\workspace\main.swift`)
	if !strings.HasPrefix(uri, "file:///C:/") {
		t.Fatalf("Windows runtime dependency file URI = %q, want file:///C:/ prefix", uri)
	}
	if strings.HasPrefix(uri, "file://C:/") {
		t.Fatalf("Windows runtime dependency file URI retained malformed authority form: %q", uri)
	}
}

// TestWindowsRuntimeDependencyNonEmptyHoverRejectsEmpty 证明空/unsupported hover 不会被 E2E 当作语义 PASS。
func TestWindowsRuntimeDependencyNonEmptyHoverRejectsEmpty(t *testing.T) {
	tests := []struct {
		name    string
		payload json.RawMessage
		wantErr bool
	}{
		{name: "null", payload: json.RawMessage(`null`), wantErr: true},
		{name: "empty contents", payload: json.RawMessage(`{"contents":[]}`), wantErr: true},
		{name: "empty markdown", payload: json.RawMessage(`{"contents":{"kind":"markdown","value":""}}`), wantErr: true},
		{name: "real hover", payload: json.RawMessage(`{"contents":{"kind":"markdown","value":"Greeter"}}`), wantErr: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			err := requireWindowsRuntimeDependencyNonEmptyHover(testCase.payload)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("requireWindowsRuntimeDependencyNonEmptyHover() error = %v, wantErr=%t", err, testCase.wantErr)
			}
		})
	}
}

// TestWindowsRuntimeDependencyE2EReceiptFilenameExposesPlatformE2E 锁定证据文件名必须同时显示原生架构和进程架构。
func TestWindowsRuntimeDependencyE2EReceiptFilenameExposesPlatformE2E(t *testing.T) {
	t.Setenv("MCP_LSP_WINDOWS_RUNTIME_DEPENDENCY_E2E_RECEIPT_DIR", t.TempDir())
	platform := WindowsHostPlatform{
		OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX64, ProcessArch: WindowsHostArchX86,
		WindowsVersion: "10.0", WindowsBuild: 26100,
	}
	path, err := windowsRuntimeDependencyE2EReceiptPath(WindowsRuntimeDependencyProductJDKJDTLS, platform)
	if err != nil {
		t.Fatal(err)
	}
	want := "windows-x64-process-x86-jdk-jdtls-e2e-receipt.json"
	if filepath.Base(path) != want {
		t.Fatalf("receipt filename = %q, want %q", filepath.Base(path), want)
	}
}

// TestWindowsRuntimeDependencyE2EReceiptRedactsProcessInputsE2E 证明收据只保存参数/环境键摘要，不保存路径、凭据或 stderr 原文。
func TestWindowsRuntimeDependencyE2EReceiptRedactsProcessInputsE2E(t *testing.T) {
	const secret = "contract-secret-must-not-appear"
	args := []string{"--config", `C:\\private\\` + secret}
	env := []string{"TOKEN=" + secret, "SystemRoot=C:\\Windows"}
	receipt := windowsRuntimeDependencyE2EReceipt{
		LaunchArgCount:      len(args),
		LaunchArgsSHA256:    windowsRuntimeDependencyE2EStringListSHA256(args),
		LaunchEnvKeyCount:   len(env),
		LaunchEnvKeysSHA256: windowsRuntimeDependencyE2EStringListSHA256(windowsRuntimeDependencyE2EEnvKeys(env)),
		Verification: windowsRuntimeDependencyE2EVerification{
			LSPStderrBytes:  len(secret),
			LSPStderrSHA256: windowsRuntimeDependencyE2EBytesSHA256([]byte(secret)),
		},
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(secret)) || bytes.Contains(payload, []byte(`C:\\private`)) {
		t.Fatalf("receipt leaked process input: %s", payload)
	}
}

func TestWindowsRuntimeDependencyE2EReceiptRedactsAbsolutePathsE2E(t *testing.T) {
	receipt := windowsRuntimeDependencyE2EReceipt{
		CacheRoot:            `C:\Users\mima0000\AppData\Local\Temp\mcp-rd-proof`,
		ReceiptPath:          `C:\Users\mima0000\Desktop\proof\receipt.json`,
		RootPath:             `C:\Users\mima0000\AppData\Local\Temp\mcp-rd-proof\runtime-dependencies\swift\arm64\cohort`,
		WorkingDirectory:     `C:\Users\mima0000\AppData\Local\Temp\mcp-rd-proof\runtime-dependencies\swift\arm64\cohort`,
		WorkspaceRoot:        `C:\Users\mima0000\AppData\Local\Temp\go-workspace-1`,
		ExecutablePath:       `C:\Users\mima0000\AppData\Local\Temp\mcp-rd-proof\runtime-dependencies\swift\arm64\cohort\tc\usr\bin\swiftc.exe`,
		ServerPath:           `C:\Users\mima0000\AppData\Local\Temp\mcp-rd-proof\runtime-dependencies\swift\arm64\cohort\tc\usr\bin\sourcekit-lsp.exe`,
		SwiftMSIExecLogPaths: []string{`C:\Users\mima0000\Desktop\proof\msiexec.log`},
		Verification: windowsRuntimeDependencyE2EVerification{
			LSPWireLogPath:   `C:\Users\mima0000\Desktop\proof\wire\stdio.jsonl`,
			LSPStderrLogPath: `C:\Users\mima0000\Desktop\proof\wire\stderr.log`,
		},
		Error: `inspect C:\Users\mima0000\AppData\Local\Temp\sd-sw-123\rtl.msi: The system cannot find the file specified.`,
	}
	redacted := redactWindowsRuntimeDependencyE2EReceipt(receipt)
	payload, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte(`C:\Users\mima0000`)) || bytes.Contains(payload, []byte(`mcp-rd-proof`)) || bytes.Contains(payload, []byte(`sd-sw-123`)) {
		t.Fatalf("receipt leaked an absolute path: %s", payload)
	}
	wirePayload := windowsRuntimeDependencyE2ERedactAbsoluteWindowsPaths(`{"uri":"file:///C:/Users/mima0000/AppData/Local/Temp/sd-sw-123/main.swift"}`)
	if strings.Contains(wirePayload, `C:/Users/mima0000`) || strings.Contains(wirePayload, `sd-sw-123`) {
		t.Fatalf("wire evidence leaked an absolute path: %s", wirePayload)
	}
	if redacted.ExecutablePath != `tc/usr/bin/swiftc.exe` || redacted.ServerPath != `tc/usr/bin/sourcekit-lsp.exe` {
		t.Fatalf("receipt did not preserve safe relative executable paths: %+v", redacted)
	}
	if redacted.SwiftMSIExecLogPaths[0] != "msiexec.log" || redacted.Verification.LSPWireLogPath != "stdio.jsonl" || redacted.Verification.LSPStderrLogPath != "stderr.log" || redacted.PathEvidencePolicy == "" {
		t.Fatalf("receipt path policy = %+v", redacted)
	}
}
