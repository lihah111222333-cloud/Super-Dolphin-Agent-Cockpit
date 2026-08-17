//go:build windows

package installer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

func TestSwiftWindowsLaunchSelectsOfficialPlatformSDK(t *testing.T) {
	root := `C:\cache\runtime-dependencies\swift-sourcekit-lsp\arm64\swift-toolchain-6.3.3`
	sdk := filepath.Join(root, swiftWindowsFlatSDKPath)
	launchArgs := swiftWindowsSourceKitLSPLaunchArgs(root)
	if len(launchArgs) != 4 {
		t.Fatalf("Swift SourceKit-LSP launch args = %d, want 4: %v", len(launchArgs), launchArgs)
	}
	for _, want := range []string{"-Xswiftc", "-sdk", sdk} {
		found := false
		for _, arg := range launchArgs {
			if arg == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Swift SourceKit-LSP launch args missing %q: %v", want, launchArgs)
		}
	}
	env := swiftWindowsRuntimeEnvironment(root)
	if len(env) != 1 || !strings.HasPrefix(env[0], "SDKROOT=") || !strings.HasSuffix(env[0], filepath.FromSlash(swiftWindowsFlatSDKPath)) {
		t.Fatalf("Swift runtime environment = %v", env)
	}
}

func TestSwiftWindowsTypecheckArgsSelectOfficialSDK(t *testing.T) {
	root := `C:\cache\runtime-dependencies\swift-sourcekit-lsp\arm64\swift-toolchain-6.3.3`
	source := filepath.Join(root, "workspace", "probe.swift")
	args := swiftWindowsTypecheckArgs(root, source)
	if len(args) != 6 || args[len(args)-2] != "-typecheck" || args[len(args)-1] != source {
		t.Fatalf("Swift typecheck args = %v", args)
	}
}

func TestSwiftInstallerPEAndEmbeddedPayloadPins(t *testing.T) {
	installerPath := os.Getenv("SWIFT_SOURCEKIT_LSP_PROOF_INSTALLER")
	if installerPath == "" {
		t.Skip("set SWIFT_SOURCEKIT_LSP_PROOF_INSTALLER to run the pinned official installer extraction proof")
	}
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductSwiftSourceKitLS)
	if err != nil {
		t.Fatal(err)
	}
	asset := entry.AssetsByArchitecture[WindowsHostArchARM64][0]
	stage := t.TempDir()
	payload, err := materializeSwiftWindowsRuntimeDependencyAsset(context.Background(), stage, asset, func(_ context.Context, _ WindowsRuntimeDependencyAsset, destination string) error {
		input, openErr := os.Open(installerPath)
		if openErr != nil {
			return openErr
		}
		defer input.Close()
		output, createErr := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
		if createErr != nil {
			return createErr
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if payload != filepath.Join(stage, ".runtime-assets", "swift-toolchain", "swift-toolchain-6.3.3.payload") {
		t.Fatalf("Swift installer payload path = %q", payload)
	}
	payloadDir := filepath.Join(filepath.Dir(payload), "payloads")
	if err := verifySwiftWindowsPlatformPayloadDirectory(payloadDir); err != nil {
		t.Fatal(err)
	}
	for _, sourceName := range []string{"a0", "a2", "a7", "a9", "a10", "a11", "a13", "a18", "a20", "a21", "a22", "a23", "a24", "a25", "a26", "a27", "a28"} {
		if _, err := requireRegularWindowsRuntimeDependencyPath(filepath.Join(payloadDir, sourceName)); err != nil {
			t.Fatalf("embedded source %s missing: %v", sourceName, err)
		}
	}
}

func TestSwiftWindowsRuntimeMergeModuleProof(t *testing.T) {
	path := os.Getenv("SWIFT_SOURCEKIT_LSP_PROOF_RUNTIME_MSM")
	if path == "" {
		t.Skip("set SWIFT_SOURCEKIT_LSP_PROOF_RUNTIME_MSM to run the pinned ARM64 runtime MSM/CAB/File-table proof")
	}
	if err := validateSwiftWindowsRuntimeMSM(path); err != nil {
		t.Fatal(err)
	}
}

func TestSwiftWindowsRuntimeMergeModuleRejectsSharedVariant(t *testing.T) {
	err := validateSwiftWindowsRuntimeMSM(filepath.Join(t.TempDir(), "rtl.shared.arm64.msm"))
	if err == nil || !strings.Contains(err.Error(), "rtl.shared.arm64.msm") {
		t.Fatalf("rtl.shared.arm64.msm rejection = %v", err)
	}
}

func TestSwiftWindowsRTLMSIFileOriginProbe(t *testing.T) {
	path := os.Getenv("SWIFT_SOURCEKIT_LSP_PROOF_RTL_MSI")
	if path == "" {
		t.Skip("set SWIFT_SOURCEKIT_LSP_PROOF_RTL_MSI to print the official rtl.msi File/Component/Media origin")
	}
	database, err := swiftMSIOpenDatabaseReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer swiftMSIClose(database, "close Swift rtl.msi origin probe")
	allFileRows, err := swiftMSIQuery(database, "SELECT File, Component_, FileName, FileSize, Sequence FROM File", 5, 1, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	fileRows := make([]swiftMSIQueryRow, 0, 1)
	for _, row := range allFileRows {
		_, longName := swiftMSIFileNameParts(row.strings[2])
		if strings.EqualFold(longName, "vcruntime140_1.dll") {
			fileRows = append(fileRows, row)
		}
	}
	for _, row := range fileRows {
		t.Logf("rtl.msi File key=%s component=%s file_name=%s size=%d sequence=%d short_name=%s", row.strings[0], row.strings[1], row.strings[2], row.integers[3], row.integers[4], strings.Split(row.strings[2], "|")[0])
	}
	componentRows, err := swiftMSIQuery(database, "SELECT Component, ComponentId, Directory_, Attributes, Condition, KeyPath FROM Component", 6, 1, 2, 3, 5, 6)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range componentRows {
		for _, fileRow := range fileRows {
			if row.strings[0] == fileRow.strings[1] {
				t.Logf("rtl.msi Component key=%s component_id=%s directory=%s attributes=%d condition=%s key_path=%s", row.strings[0], row.strings[1], row.strings[2], row.integers[3], row.strings[3], row.strings[4])
			}
		}
	}
	mediaRows, err := swiftMSIQuery(database, "SELECT DiskId, LastSequence, DiskPrompt, Cabinet, VolumeLabel, Source FROM Media", 6, 3, 4, 5, 6)
	if err != nil {
		t.Fatal(err)
	}
	for _, fileRow := range fileRows {
		for _, mediaRow := range mediaRows {
			if fileRow.integers[4] <= mediaRow.integers[1] {
				t.Logf("rtl.msi Media disk_id=%d last_sequence=%d disk_prompt=%s cabinet=%s volume=%s source=%s file=%s", mediaRow.integers[0], mediaRow.integers[1], mediaRow.strings[0], mediaRow.strings[1], mediaRow.strings[2], mediaRow.strings[3], fileRow.strings[2])
				break
			}
		}
	}
	streamRows, err := swiftMSIQuery(database, "SELECT Name FROM _Streams", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range streamRows {
		t.Logf("rtl.msi stream=%s", row.strings[0])
	}
}

func TestSwiftWindowsRejectedRuntimeOriginProof(t *testing.T) {
	root := os.Getenv("SWIFT_SOURCEKIT_LSP_PROOF_RTL_MSI_ROOT")
	if root == "" {
		t.Skip("set SWIFT_SOURCEKIT_LSP_PROOF_RTL_MSI_ROOT to verify the locked rtl.msi rejected x64 helper origin")
	}
	if err := validateSwiftWindowsRejectedRuntimeOrigin(root); err != nil {
		t.Fatal(err)
	}
}

// swiftWindowsFastPathFixture 保存一个扁平 Swift cohort，供 check-only 完整性测试使用。
type swiftWindowsFastPathFixture struct {
	entry         WindowsRuntimeDependencyCatalogEntry
	platform      WindowsHostPlatform
	architecture  string
	cohort        string
	cacheRoot     string
	finalRoot     string
	manifestPath  string
	sourceBin     string
	sourceRuntime string
	manifest      []byte
}

// TestSwiftWindowsCheckOnlyFastPathRejectsReadyTamper 证明 Swift 快速复验仍检查 owner-only、manifest、关键二进制和运行时 DLL。
func TestSwiftWindowsCheckOnlyFastPathRejectsReadyTamper(t *testing.T) {
	fixture := newSwiftWindowsFastPathFixture(t)
	if _, err := runtimeDependencySwiftCacheResult(context.Background(), fixture.entry, fixture.platform, fixture.architecture, fixture.cohort, fixture.cacheRoot, fixture.finalRoot); err != nil {
		t.Fatalf("initial Swift check-only fast path = %v", err)
	}

	var manifest runtimeDependencyReadyManifest
	if err := json.Unmarshal(fixture.manifest, &manifest); err != nil {
		t.Fatalf("decode fixture manifest: %v", err)
	}
	manifest.Schema++
	invalidManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("encode tampered manifest: %v", err)
	}
	if err := os.WriteFile(fixture.manifestPath, invalidManifest, 0o600); err != nil {
		t.Fatalf("tamper ready manifest: %v", err)
	}
	assertSwiftWindowsFastPathCacheMiss(t, fixture, "ready manifest schema")
	if err := os.WriteFile(fixture.manifestPath, fixture.manifest, 0o600); err != nil {
		t.Fatalf("restore ready manifest: %v", err)
	}

	sourcekit := filepath.Join(WindowsSwiftSourceKitLSPToolchainBin(fixture.finalRoot), "sourcekit-lsp.exe")
	if err := os.WriteFile(sourcekit, []byte("tampered sourcekit"), 0o600); err != nil {
		t.Fatalf("tamper sourcekit-lsp: %v", err)
	}
	assertSwiftWindowsFastPathCacheMiss(t, fixture, "sourcekit-lsp binary")
	copySwiftWindowsFastPathFile(t, fixture.sourceBin, "sourcekit-lsp.exe", sourcekit)

	runtimeDLL := filepath.Join(swiftWindowsFlatRuntimeRoot(fixture.finalRoot), "swiftCore.dll")
	if err := os.WriteFile(runtimeDLL, []byte("tampered runtime"), 0o600); err != nil {
		t.Fatalf("tamper swiftCore.dll: %v", err)
	}
	assertSwiftWindowsFastPathCacheMiss(t, fixture, "Swift runtime DLL")
	copySwiftWindowsFastPathFile(t, fixture.sourceRuntime, "swiftCore.dll", runtimeDLL)

	if _, err := runtimeDependencySwiftCacheResult(context.Background(), fixture.entry, fixture.platform, fixture.architecture, fixture.cohort, fixture.cacheRoot, fixture.finalRoot); err != nil {
		t.Fatalf("restored Swift check-only fast path = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runtimeDependencySwiftCacheResult(ctx, fixture.entry, fixture.platform, fixture.architecture, fixture.cohort, fixture.cacheRoot, fixture.finalRoot); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Swift check-only fast path = %v, want context.Canceled", err)
	}
}

func newSwiftWindowsFastPathFixture(t *testing.T) swiftWindowsFastPathFixture {
	t.Helper()
	sourceRoot := strings.TrimSpace(os.Getenv("SWIFT_SOURCEKIT_LSP_PROOF_TOOLCHAIN_ROOT"))
	if sourceRoot == "" {
		t.Skip("set SWIFT_SOURCEKIT_LSP_PROOF_TOOLCHAIN_ROOT to run the Swift fast-path integrity proof")
	}
	sourceBin := filepath.Join(sourceRoot, filepath.FromSlash("tc/usr/bin"))
	sourceRuntime := filepath.Join(sourceRoot, filepath.FromSlash("rt"))
	if sourceBinInfo, binErr := os.Stat(sourceBin); binErr == nil && sourceBinInfo.IsDir() {
		runtimeInfo, runtimeErr := os.Stat(sourceRuntime)
		if runtimeErr != nil || !runtimeInfo.IsDir() {
			t.Fatalf("Swift cohort runtime root missing beside tc/usr/bin: %v", runtimeErr)
		}
	} else {
		sourceBin = filepath.Join(sourceRoot, filepath.FromSlash("Toolchains/6.3.3+Asserts/usr/bin"))
		if info, err := os.Stat(sourceBin); err != nil || !info.IsDir() {
			sourceBin = filepath.Join(sourceRoot, filepath.FromSlash("usr/bin"))
		}
		if info, err := os.Stat(sourceBin); err != nil || !info.IsDir() {
			t.Fatalf("official Swift toolchain bin missing: %v", err)
		}
		// 旧官方提取树把 runtime DLL 与工具放在同一 usr/bin；正式 cohort 必须走上面的 tc/rt 分离布局。
		sourceRuntime = sourceBin
	}
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductSwiftSourceKitLS)
	if err != nil {
		t.Fatal(err)
	}
	architecture := WindowsHostArchARM64
	cohort := runtimeDependencyCohort(entry, architecture)
	platform := WindowsHostPlatform{
		OS: WindowsHostOSWindows, NativeArch: architecture, ProcessArch: architecture,
		WindowsVersion: "10.0", WindowsBuild: 26100,
	}
	productRoot := t.TempDir()
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict fixture product root (authorization_required must remain visible): %v", err)
	}
	cacheRoot := filepath.Join(productRoot, "cache", WindowsLSPAssetCacheSubdir)
	finalRoot := runtimeDependencyFinalRoot(cacheRoot, entry.Product, architecture, cohort)
	if err := ensureDirectoryNoSymlink(finalRoot); err != nil {
		t.Fatalf("create Swift fast-path fixture root: %v", err)
	}
	for _, relative := range []string{
		swiftWindowsFlatToolchainPath + "/usr/bin",
		swiftWindowsFlatToolchainPath + "/usr/lib/swift",
		swiftWindowsFlatSDKPath,
		swiftWindowsFlatRuntimePath,
	} {
		if err := ensureDirectoryNoSymlink(filepath.Join(finalRoot, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("create Swift flat fixture directory %q: %v", relative, err)
		}
	}
	critical, err := swiftWindowsCriticalHashItems()
	if err != nil {
		t.Fatalf("build Swift critical fixture set: %v", err)
	}
	for _, item := range critical {
		relative := filepath.ToSlash(item.relative)
		name := filepath.Base(filepath.FromSlash(relative))
		switch {
		case strings.HasPrefix(relative, swiftWindowsFlatToolchainPath+"/usr/bin/"):
			copySwiftWindowsFastPathFile(t, sourceBin, name, filepath.Join(WindowsSwiftSourceKitLSPToolchainBin(finalRoot), name))
		case strings.HasPrefix(relative, swiftWindowsFlatRuntimePath+"/"):
			copySwiftWindowsFastPathFile(t, sourceRuntime, name, filepath.Join(swiftWindowsFlatRuntimeRoot(finalRoot), name))
		default:
			t.Fatalf("unexpected Swift critical fixture path %q", item.relative)
		}
	}
	if err := writeWindowsRuntimeDependencyReady(finalRoot, entry, architecture, cohort); err != nil {
		t.Fatalf("write Swift fast-path fixture manifest: %v", err)
	}
	if err := validateSwiftWindowsFlatLayout(finalRoot); err != nil {
		t.Fatalf("validate Swift fast-path fixture layout: %v", err)
	}
	manifestPath := filepath.Join(finalRoot, runtimeDependencyReadyFile)
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read Swift fast-path fixture manifest: %v", err)
	}
	return swiftWindowsFastPathFixture{
		entry: entry, platform: platform, architecture: architecture, cohort: cohort,
		cacheRoot: cacheRoot, finalRoot: finalRoot,
		manifestPath: manifestPath, sourceBin: sourceBin, sourceRuntime: sourceRuntime, manifest: manifest,
	}
}

func copySwiftWindowsFastPathFile(t *testing.T, sourceDir, name, destination string) {
	t.Helper()
	source := filepath.Join(sourceDir, name)
	input, err := os.Open(source)
	if err != nil {
		t.Fatalf("open official Swift fixture file %q: %v", name, err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("create Swift fixture file %q: %v", name, err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		t.Fatalf("copy official Swift fixture file %q: %v", name, err)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("close Swift fixture file %q: %v", name, err)
	}
}

func assertSwiftWindowsFastPathCacheMiss(t *testing.T, fixture swiftWindowsFastPathFixture, label string) {
	t.Helper()
	_, err := runtimeDependencySwiftCacheResult(context.Background(), fixture.entry, fixture.platform, fixture.architecture, fixture.cohort, fixture.cacheRoot, fixture.finalRoot)
	if !errors.Is(err, ErrWindowsRuntimeDependencyCacheMiss) {
		t.Fatalf("%s error = %v, want typed cache miss", label, err)
	}
}
