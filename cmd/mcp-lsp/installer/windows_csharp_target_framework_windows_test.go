//go:build windows

package installer

import (
	"archive/zip"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateWindowsCSharpTargetFrameworkReferencePacksReadsTargetFramework(t *testing.T) {
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "Projects", "Yahtzee")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	project := `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`
	if err := os.WriteFile(filepath.Join(projectDir, "Yahtzee.csproj"), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(workspace, "cohort", "packs", "Microsoft.NETCore.App.Ref", "8.0.30", "ref", "net8.0")
	if err := os.MkdirAll(pack, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "System.Runtime.dll"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWindowsCSharpTargetFrameworkReferencePacks(filepath.Join(workspace, "cohort"), workspace); err != nil {
		t.Fatalf("ValidateWindowsCSharpTargetFrameworkReferencePacks() = %v", err)
	}
}

func TestWindowsCSharpTargetFrameworkPartsParsesNet8(t *testing.T) {
	base, major, minor, supported, err := windowsCSharpTargetFrameworkParts("net8.0")
	if err != nil || !supported || base != "net8.0" || major != 8 || minor != 0 {
		t.Fatalf("windowsCSharpTargetFrameworkParts(net8.0) = %q, %d, %d, %t, %v", base, major, minor, supported, err)
	}
}

func TestValidateWindowsCSharpTargetFrameworkReferencePacksRejectsEmptyRoots(t *testing.T) {
	if err := ValidateWindowsCSharpTargetFrameworkReferencePacks("", t.TempDir()); err == nil || !strings.Contains(err.Error(), "cohort root") {
		t.Fatalf("empty cohort root error = %v", err)
	}
	if err := ValidateWindowsCSharpTargetFrameworkReferencePacks(t.TempDir(), " "); err == nil || !strings.Contains(err.Error(), "workspace root") {
		t.Fatalf("empty workspace root error = %v", err)
	}
}

func TestValidateWindowsCSharpTargetFrameworkReferencePacksRejectsMismatchedPack(t *testing.T) {
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "Projects", "Yahtzee")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "Yahtzee.csproj"), []byte(`<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`), 0o600); err != nil {
		t.Fatal(err)
	}
	wrong := filepath.Join(workspace, "cohort", "packs", "Microsoft.NETCore.App.Ref", "10.0.11", "ref", "net10.0")
	if err := os.MkdirAll(wrong, 0o700); err != nil {
		t.Fatal(err)
	}
	err := ValidateWindowsCSharpTargetFrameworkReferencePacks(filepath.Join(workspace, "cohort"), workspace)
	if err == nil || !strings.Contains(err.Error(), "net8.0") || !strings.Contains(err.Error(), "Microsoft.NETCore.App.Ref") {
		t.Fatalf("mismatched reference pack error = %v", err)
	}
}

func TestValidateWindowsCSharpTargetFrameworkReferencePacksReadsTargetFrameworks(t *testing.T) {
	workspace := t.TempDir()
	projectDir := filepath.Join(workspace, "Projects", "Yahtzee")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "Yahtzee.csproj"), []byte(`<Project><PropertyGroup><TargetFrameworks>net8.0;net10.0</TargetFrameworks></PropertyGroup></Project>`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"8.0.30", "10.0.11"} {
		major := strings.Split(version, ".")[0]
		base := "net" + major + ".0"
		pack := filepath.Join(workspace, "cohort", "packs", "Microsoft.NETCore.App.Ref", version, "ref", base)
		if err := os.MkdirAll(pack, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateWindowsCSharpTargetFrameworkReferencePacks(filepath.Join(workspace, "cohort"), workspace); err != nil {
		t.Fatalf("TargetFrameworks validation = %v", err)
	}
}

func TestMaterializeWindowsDotnetNet8SDKMergesTargetingPack(t *testing.T) {
	originalResolver := runtimeDependencySystemDotnetSDKRootResolver
	runtimeDependencySystemDotnetSDKRootResolver = func(string, string) (string, error) { return "", nil }
	t.Cleanup(func() { runtimeDependencySystemDotnetSDKRootResolver = originalResolver })
	archivePath := filepath.Join(t.TempDir(), "dotnet-sdk-net8.zip")
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archiveFile)
	for _, name := range []string{
		"dotnet.exe",
		"sdk/8.0.424/MSBuild.dll",
		"packs/Microsoft.NETCore.App.Ref/8.0.30/ref/net8.0/System.Runtime.dll",
		"packs/Microsoft.AspNetCore.App.Ref/8.0.30/ref/net8.0/Microsoft.AspNetCore.App.Ref.dll",
		"packs/Microsoft.WindowsDesktop.App.Ref/8.0.30/ref/net8.0/Microsoft.WindowsDesktop.App.Ref.dll",
		"shared/Microsoft.NETCore.App/8.0.30/System.Private.CoreLib.dll",
		"sdk-manifests/8.0.100/WorkloadManifest.json",
	} {
		entry, createErr := writer.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := io.WriteString(entry, "fixture"); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archiveFile.Close(); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha512.Sum512(payload)
	asset := runtimeDependencyDotnetAsset(WindowsHostArchARM64, "dotnet-sdk-net8", "8.0.424", "https://example.invalid/dotnet-sdk-net8.zip", WindowsRuntimeDependencyChecksumSHA512, hex.EncodeToString(hash[:]), WindowsRuntimeDependencyAssetFormatZIP, "dotnet.exe", "dotnet.exe", true)
	stage := filepath.Join(t.TempDir(), "stage")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := materializeWindowsRuntimeDependencyAsset(t.Context(), stage, asset, func(_ context.Context, _ WindowsRuntimeDependencyAsset, destination string) error {
		input, openErr := os.Open(archivePath)
		if openErr != nil {
			return openErr
		}
		defer input.Close()
		output, createErr := os.Create(destination)
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
		t.Fatalf("materializeWindowsRuntimeDependencyAsset() = %v", err)
	}
	if got == "" {
		t.Fatal("materializeWindowsRuntimeDependencyAsset() returned an empty payload")
	}
	for _, relative := range []string{
		"sdk/8.0.424/MSBuild.dll",
		"packs/Microsoft.NETCore.App.Ref/8.0.30/ref/net8.0/System.Runtime.dll",
		"shared/Microsoft.NETCore.App/8.0.30/System.Private.CoreLib.dll",
	} {
		if _, err := os.Stat(filepath.Join(stage, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("merged .NET 8 path %s: %v", relative, err)
		}
	}
	if _, err := os.Stat(filepath.Join(stage, ".runtime-assets", "dotnet-sdk-net8", "expanded")); !os.IsNotExist(err) {
		t.Fatalf("temporary .NET 8 extraction tree still exists, err=%v", err)
	}
}

func TestMaterializeWindowsDotnetNet8SDKReusesVerifiedSystemSDK(t *testing.T) {
	systemRoot := filepath.Join(t.TempDir(), "dotnet")
	for _, relative := range []string{
		"sdk/8.0.424",
		"packs/Microsoft.NETCore.App.Ref/8.0.30/ref/net8.0",
		"packs/Microsoft.AspNetCore.App.Ref/8.0.30/ref/net8.0",
		"packs/Microsoft.WindowsDesktop.App.Ref/8.0.30/ref/net8.0",
		"shared/Microsoft.NETCore.App/8.0.30",
		"sdk-manifests/8.0.100",
	} {
		if err := os.MkdirAll(filepath.Join(systemRoot, filepath.FromSlash(relative)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(systemRoot, "sdk", "8.0.424", "MSBuild.dll"), []byte("system-sdk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(systemRoot, "dotnet.exe"), []byte("system-host"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		"packs/Microsoft.NETCore.App.Ref/8.0.30/ref/net8.0/System.Runtime.dll",
		"packs/Microsoft.AspNetCore.App.Ref/8.0.30/ref/net8.0/Microsoft.AspNetCore.App.Ref.dll",
		"packs/Microsoft.WindowsDesktop.App.Ref/8.0.30/ref/net8.0/Microsoft.WindowsDesktop.App.Ref.dll",
		"shared/Microsoft.NETCore.App/8.0.30/System.Private.CoreLib.dll",
	} {
		if err := os.WriteFile(filepath.Join(systemRoot, filepath.FromSlash(relative)), []byte("system-sdk"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stage := filepath.Join(t.TempDir(), "stage")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "dotnet.exe"), []byte("net10-host"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalResolver := runtimeDependencySystemDotnetSDKRootResolver
	runtimeDependencySystemDotnetSDKRootResolver = func(architecture, version string) (string, error) {
		if architecture != WindowsHostArchARM64 || version != "8.0.424" {
			t.Fatalf("system SDK resolver args = %q/%q", architecture, version)
		}
		return systemRoot, nil
	}
	t.Cleanup(func() { runtimeDependencySystemDotnetSDKRootResolver = originalResolver })
	asset := runtimeDependencyDotnetAsset(
		WindowsHostArchARM64,
		"dotnet-sdk-net8",
		"8.0.424",
		"https://example.invalid/dotnet-sdk-net8.zip",
		WindowsRuntimeDependencyChecksumSHA512,
		strings.Repeat("a", 128),
		WindowsRuntimeDependencyAssetFormatZIP,
		"dotnet.exe",
		"dotnet.exe",
		true,
	)
	fetchCalled := false
	if _, err := materializeWindowsRuntimeDependencyAsset(t.Context(), stage, asset, func(context.Context, WindowsRuntimeDependencyAsset, string) error {
		fetchCalled = true
		return os.ErrNotExist
	}); err != nil {
		t.Fatalf("materializeWindowsRuntimeDependencyAsset() = %v", err)
	}
	if fetchCalled {
		t.Fatal("system SDK reuse unexpectedly fetched the official SDK8 archive")
	}
	for _, relative := range []string{
		"sdk/8.0.424/MSBuild.dll",
		"packs/Microsoft.NETCore.App.Ref/8.0.30/ref/net8.0",
		"shared/Microsoft.NETCore.App/8.0.30",
	} {
		if _, err := os.Stat(filepath.Join(stage, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("reused .NET 8 path %s: %v", relative, err)
		}
	}
}

func TestValidateWindowsCSharpTargetFrameworkReferencePacksSkipsGeneratedTrees(t *testing.T) {
	workspace := t.TempDir()
	// A normal source container named bin is a valid workspace subtree.
	projectDir := filepath.Join(workspace, "bin", "Source", "Project")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "Project.csproj"), []byte(`<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, generated := range []string{".build-cache/noise", ".super-dolphin/noise", ".worktrees/noise", ".workspace/noise", "node_modules/noise", "src/Project/bin"} {
		path := filepath.Join(workspace, filepath.FromSlash(generated), "Ignored.csproj")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`<Project/>`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pack := filepath.Join(workspace, "cohort", "packs", "Microsoft.NETCore.App.Ref", "8.0.30", "ref", "net8.0")
	if err := os.MkdirAll(pack, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWindowsCSharpTargetFrameworkReferencePacks(filepath.Join(workspace, "cohort"), workspace); err != nil {
		t.Fatalf("generated trees must not affect C# project discovery: %v", err)
	}
}

func TestMaterializeWindowsCSharpProcessRootPublishesShortDigestTree(t *testing.T) {
	productRoot := t.TempDir()
	cohortRoot := filepath.Join(productRoot, "cache", "LSP-assets", "runtime-dependencies", "dotnet-csharp-ls", "arm64", strings.Repeat("c", 64))
	server := filepath.Join(cohortRoot, "tools", "csharp-ls.exe")
	for _, path := range []string{
		filepath.Dir(server),
		filepath.Join(cohortRoot, "sdk", "10.0.400", "Sdks"),
		filepath.Join(cohortRoot, "packs", "Microsoft.NETCore.App.Ref", "8.0.30", "ref", "net8.0"),
		filepath.Join(cohortRoot, "shared", "Microsoft.NETCore.App", "8.0.30"),
	} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, contents := range map[string]string{
		server:                                  filepath.Base(server),
		filepath.Join(cohortRoot, "dotnet.exe"): filepath.Base(cohortRoot),
		filepath.Join(cohortRoot, "sdk", "10.0.400", "Sdks", "Microsoft.NET.Sdk.dll"):                                    "sdk",
		filepath.Join(cohortRoot, "packs", "Microsoft.NETCore.App.Ref", "8.0.30", "ref", "net8.0", "System.Runtime.dll"): "reference-pack",
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	targetRoot, err := MaterializeWindowsCSharpProcessRoot(productRoot, cohortRoot)
	if err != nil {
		t.Fatalf("MaterializeWindowsCSharpProcessRoot() = %v", err)
	}
	if strings.EqualFold(filepath.Clean(targetRoot), filepath.Clean(cohortRoot)) || !strings.HasPrefix(filepath.Base(targetRoot), "cs-") {
		t.Fatalf("target root = %q, want digest-isolated short root distinct from %q", targetRoot, cohortRoot)
	}
	for _, relative := range []string{
		"dotnet.exe",
		"sdk/10.0.400/Sdks/Microsoft.NET.Sdk.dll",
		"packs/Microsoft.NETCore.App.Ref/8.0.30/ref/net8.0/System.Runtime.dll",
	} {
		if _, err := os.Stat(filepath.Join(targetRoot, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("materialized C# file %s: %v", relative, err)
		}
	}
	targetAgain, err := MaterializeWindowsCSharpProcessRoot(productRoot, cohortRoot)
	if err != nil || targetAgain != targetRoot {
		t.Fatalf("materialized C# root reuse = (%q, %v), want %q", targetAgain, err, targetRoot)
	}
	sourcePath := filepath.Join(cohortRoot, filepath.FromSlash("sdk/10.0.400/Sdks/Microsoft.NET.Sdk.dll"))
	sourceBefore, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	sourceHashBefore, err := windowsCSharpProcessFileSHA256(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	processPath := filepath.Join(targetRoot, filepath.FromSlash("sdk/10.0.400/Sdks/Microsoft.NET.Sdk.dll"))
	if err := os.WriteFile(processPath, []byte("process-only-tamper"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceAfter, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	sourceHashAfter, err := windowsCSharpProcessFileSHA256(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(sourceAfter) != string(sourceBefore) || sourceHashAfter != sourceHashBefore {
		t.Fatalf("canonical source changed after process-root mutation: before=%q after=%q", sourceBefore, sourceAfter)
	}
	if _, err := MaterializeWindowsCSharpProcessRoot(productRoot, cohortRoot); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tampered physical C# root was accepted: %v", err)
	}
}

func TestValidateWindowsCSharpProcessTargetPathAcceptsRealDeepSDKPathWithinMAXPATH(t *testing.T) {
	deepSDKRelative := filepath.FromSlash("sdk/8.0.424/DotnetTools/dotnet-watch/8.0.424-servicing.26373.23/tools/net8.0/any/BuildHost-netcore/Microsoft.CodeAnalysis.Workspaces.MSBuild.BuildHost.runtimeconfig.json")
	productRoot := `C:\product\` + strings.Repeat("p", 52)
	finalPath := filepath.Join(productRoot, "cs-0123456789abcdef", deepSDKRelative)
	units := len([]rune(finalPath))
	if units <= 240 || units > windowsCSharpProcessPathMaxUnits {
		t.Fatalf("deep SDK regression fixture path_units=%d, want 241..%d", units, windowsCSharpProcessPathMaxUnits)
	}
	if err := validateWindowsCSharpProcessTargetPath(finalPath, deepSDKRelative); err != nil {
		t.Fatalf("real deep SDK path within MAX_PATH was rejected: %v", err)
	}
	overlong := filepath.Join(productRoot+strings.Repeat("x", windowsCSharpProcessPathMaxUnits), "cs-0123456789abcdef", deepSDKRelative)
	if err := validateWindowsCSharpProcessTargetPath(overlong, deepSDKRelative); err == nil || !strings.Contains(err.Error(), "relative=") {
		t.Fatalf("overlong C# process path error = %v, want redacted relative-path evidence", err)
	}
}
