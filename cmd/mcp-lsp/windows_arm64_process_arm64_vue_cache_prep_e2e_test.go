//go:build windows && arm64 && e2e

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

const vueWindowsARM64ProcessARM64SoakPrepEnv = "MCP_LSP_REAL_NODE_VUE_WINDOWS_ARM64_PROCESS_ARM64_SOAK_PREP"

// TestWindowsARM64ProcessARM64VueCachePrepE2E 只做正式 soak 前的生产 cache-only 准备。
// check-only 上下文禁止 InstallAction，因此本阶段不会下载或修改 cohort；所有 Node、
// Vue、TypeScript 和 VCLibs 身份都从同一 product-owned root 只读复验并独立留证。
func TestWindowsARM64ProcessARM64VueCachePrepE2E(t *testing.T) {
	if os.Getenv(vueWindowsARM64ProcessARM64SoakPrepEnv) != "1" {
		t.Skipf("set %s=1 to run the cache-only Vue preparation proof", vueWindowsARM64ProcessARM64SoakPrepEnv)
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("Vue preparation proof requires Windows ARM64, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	if host.OS != installer.WindowsHostOSWindows || host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("Vue preparation proof requires native/process ARM64, got os=%q native=%q process=%q", host.OS, host.NativeArch, host.ProcessArch)
	}

	productRoot := strings.TrimSpace(os.Getenv(vueWindowsARM64ProcessARM64SoakProductRootEnv))
	if productRoot == "" {
		t.Fatalf("%s is required", vueWindowsARM64ProcessARM64SoakProductRootEnv)
	}
	productRoot, err = filepath.Abs(productRoot)
	if err != nil {
		t.Fatalf("resolve Vue product root: %v", err)
	}
	if info, statErr := os.Stat(productRoot); statErr != nil || !info.IsDir() {
		t.Fatalf("Vue product root is not a directory: %v", statErr)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "")
	t.Setenv("NODE_PATH", "")
	t.Setenv("SUPER_DOLPHIN_WINDOWS_NODE_PATH", "")
	t.Setenv("SUPER_DOLPHIN_MSVC_RUNTIME_DIR", "")
	t.Setenv("PATH", realNodePathWithoutNodeNPM(os.Getenv("PATH")))

	evidenceDir := strings.TrimSpace(os.Getenv(vueWindowsARM64ProcessARM64SoakEvidenceEnv))
	if evidenceDir == "" {
		evidenceDir = filepath.Join(realNodeRepoRoot(t), ".build-cache", "codex-vue-windows-arm64-process-arm64-soak-15m")
	}
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatalf("create Vue preparation evidence directory: %v", err)
	}
	// 正式 soak 只引用这一固定名称；prep 与 soak 仍通过不同测试阶段留证。
	receiptPath := filepath.Join(evidenceDir, "vue-windows-arm64-process-arm64-soak-prep-receipt.log")
	receipt := []string{
		"test=windows-arm64-process-arm64-vue-cache-prep",
		"status=NON_PASS_started_cache_only",
		"host_os=windows",
		fmt.Sprintf("host_native_arch=%s", host.NativeArch),
		fmt.Sprintf("host_process_arch=%s", host.ProcessArch),
		fmt.Sprintf("host_windows_build=%d", host.WindowsBuild),
		"production_resolver=EnsureInstalledDetailed",
		"install_context=check_only",
		"install_action=not_called",
		"network_download_attempts=0",
		"product_root_path_policy=not_recorded",
		fmt.Sprintf("product_root_digest=%s", vueLifecyclePathDigest(productRoot)),
		"acl_win32_5_1314=typed_securefs_authorization_required_preserved",
		"absolute_path_markers=0",
	}
	receiptWritten := false
	writeReceipt := func(status string) {
		receipt = append(receipt, "status="+status)
		receipt = append(receipt, "finished_at="+time.Now().Format(time.RFC3339Nano))
		if writeErr := os.WriteFile(receiptPath, []byte(strings.Join(receipt, "\n")+"\n"), 0o600); writeErr != nil {
			t.Errorf("write Vue preparation receipt: %v", writeErr)
		}
		receiptWritten = true
	}
	t.Cleanup(func() {
		if !receiptWritten {
			writeReceipt("NON_PASS_failed_cache_only")
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	provider := setupInstaller()
	result, err := provider.EnsureInstalledDetailed(installer.WithToolCallInstallCheckOnly(ctx), "vue")
	if err != nil {
		t.Fatalf("production Vue cache-only resolver: %v", err)
	}
	if result.Status != installer.InstallStatusPathFound {
		t.Fatalf("Vue cache-only resolver status=%q, want %q", result.Status, installer.InstallStatusPathFound)
	}
	if _, err := installer.WindowsShortProcessPathWithinRoot(productRoot, result.Path); err != nil {
		t.Fatalf("Vue resolver escaped product root: %v", err)
	}
	receipt = append(receipt, fmt.Sprintf("vue_server_relative=%s", vueLifecycleRelativePath(productRoot, result.Path)))

	nodeRuntime, err := installer.NewWindowsNodeRuntime(productRoot, nil)
	if err != nil {
		t.Fatalf("construct cached Windows Node runtime: %v", err)
	}
	expectedPaths, err := nodeRuntime.ExpectedPaths()
	if err != nil {
		t.Fatalf("resolve cached Node paths: %v", err)
	}
	nodeAsset, err := installer.WindowsNodeRuntimeAssetForPlatform(host)
	if err != nil {
		t.Fatalf("select locked Node asset: %v", err)
	}
	nodePayload := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir, "node-runtime", nodeAsset.Version, nodeAsset.Architecture, strings.ToLower(nodeAsset.SHA256), "payload.zip")
	if got, hashErr := vueLifecycleSHA256File(nodePayload); hashErr != nil || !strings.EqualFold(got, nodeAsset.SHA256) {
		t.Fatalf("cached Node payload SHA256=%s err=%v want=%s", got, hashErr, nodeAsset.SHA256)
	}
	receipt = append(receipt,
		"node_version="+nodeAsset.Version,
		"node_url="+nodeAsset.URL,
		"node_sha256="+nodeAsset.SHA256,
		"node_payload_sha256="+nodeAsset.SHA256,
		"node_relative="+vueLifecycleRelativePath(productRoot, expectedPaths.NodePath),
	)

	packages := []string(nil)
	for _, spec := range runtimeNPMInstallerSpecsForPlatform("windows") {
		if !strings.Contains(strings.Join(spec.languages, ","), "vue") {
			continue
		}
		packages, err = runtimeNPMExactPackages(spec.args)
		if err != nil {
			t.Fatalf("parse locked Vue npm packages: %v", err)
		}
		break
	}
	if len(packages) == 0 {
		t.Fatal("locked Vue npm package list is empty")
	}
	if err := nodeRuntime.ValidateExactPackages(ctx, packages); err != nil {
		t.Fatalf("validate cached Vue npm cohort: %v", err)
	}
	for _, specification := range packages {
		name, version, parseErr := productionExactPackageNameAndVersion(specification)
		if parseErr != nil {
			t.Fatalf("parse Vue package %q: %v", specification, parseErr)
		}
		verifyRealNodePackageVersion(t, expectedPaths.Prefix, name, version)
		receipt = append(receipt, fmt.Sprintf("npm_package=%s version=%s", name, version))
	}
	bridgeSpec, err := runtimeServerWindowsVueTSBridgeSpec(result.Path)
	if err != nil {
		t.Fatalf("resolve cached Vue TypeScript companion: %v", err)
	}
	receipt = append(receipt,
		"typescript_relative="+vueLifecycleRelativePath(productRoot, bridgeSpec.typescriptBinary),
		"vue_plugin_relative="+vueLifecycleRelativePath(productRoot, bridgeSpec.vuePluginLocation),
	)

	vclibsAsset, err := installer.WindowsVCLibsDesktopAssetForPlatform(host)
	if err != nil {
		t.Fatalf("select locked VCLibs asset: %v", err)
	}
	vclibsRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalPath(productRoot)
	if err != nil {
		t.Fatalf("resolve cached VCLibs root: %v", err)
	}
	vclibsProcessRoot, err := installer.ResolveWindowsVCLibsDesktopAppLocalProcessPath(productRoot)
	if err != nil {
		t.Fatalf("resolve cached VCLibs process root: %v", err)
	}
	if same, sameErr := sameRealNodeFile(vclibsRoot, vclibsProcessRoot); sameErr != nil || !same {
		t.Fatalf("cached VCLibs identity mismatch: same=%t err=%v", same, sameErr)
	}
	vclibsPayload := filepath.Join(filepath.Dir(vclibsRoot), "payload.zip")
	if got, hashErr := vueLifecycleSHA256File(vclibsPayload); hashErr != nil || !strings.EqualFold(got, vclibsAsset.SHA256) {
		t.Fatalf("cached VCLibs payload SHA256=%s err=%v want=%s", got, hashErr, vclibsAsset.SHA256)
	}
	receipt = append(receipt,
		"vclibs_version="+vclibsAsset.Version,
		"vclibs_url="+vclibsAsset.URL,
		"vclibs_sha256="+vclibsAsset.SHA256,
		"vclibs_payload_sha256="+vclibsAsset.SHA256,
		"vclibs_relative="+vueLifecycleRelativePath(productRoot, vclibsRoot),
		"vclibs_process_same_file_identity=true",
		"vclibs_process_path_policy=8.3_same_file_identity",
		"vclibs_process_path_digest="+vueLifecyclePathDigest(vclibsProcessRoot),
		"cache_validation=read_only_success",
	)
	writeReceipt("NON_PASS_cache_only_ready")
	t.Logf("Vue cache-only preparation ready; receipt=%s", receiptPath)
}
