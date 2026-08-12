package runtimeenv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestPackagedResourcesDir(t *testing.T) {
	got, err := packagedResourcesDirForOS("darwin", "/Applications/Super Dolphin.app/Contents/MacOS/agent-terminal")
	if err != nil {
		t.Fatalf("packagedResourcesDirForOS(darwin): %v", err)
	}
	want := filepath.FromSlash("/Applications/Super Dolphin.app/Contents/Resources")
	if got != want {
		t.Fatalf("packagedResourcesDir() = %q, want %q", got, want)
	}
}

func TestPackagedRuntimeFromExecutableDetectsMacOSAppMainBinary(t *testing.T) {
	if runtimeGOOS() != "darwin" {
		t.Skip("macOS app executable auto-detection is platform-specific")
	}
	app := filepath.Join(t.TempDir(), "Super Dolphin.app")
	resources := filepath.Join(app, "Contents", "Resources")
	writePackagedRuntimeFixture(t, resources, runtimeGOOS()+"-"+runtimeGOARCH())

	got, ok := PackagedRuntimeFromExecutable(filepath.Join(app, "Contents", "MacOS", "agent-terminal"), "/Users/alice")
	if !ok {
		t.Fatal("PackagedRuntimeFromExecutable ok = false, want true")
	}
	if got.ResourcesDir != resources {
		t.Fatalf("ResourcesDir = %q", got.ResourcesDir)
	}
	if got.BinDir != filepath.Join(resources, "bin") {
		t.Fatalf("BinDir = %q", got.BinDir)
	}
	if got.MigrationsDir != filepath.Join(resources, "internal", "platform", "db", "sqlite", "migrations") {
		t.Fatalf("MigrationsDir = %q", got.MigrationsDir)
	}
	if _, ok := reflect.TypeFor[PackagedRuntime]().FieldByName("PostgresRoot"); ok {
		t.Fatalf("PackagedRuntime exposes PostgresRoot after SQLite switch: %#v", got)
	}
	if got.AppDataDir != "/Users/alice/Library/Application Support/Super Dolphin" {
		t.Fatalf("AppDataDir = %q", got.AppDataDir)
	}
}

func TestPackagedRuntimeFromResourcesUsesSQLiteMigrationsDir(t *testing.T) {
	resources := t.TempDir()

	got := packagedRuntimeFromResourcesForOS("linux", resources, "/home/alice")
	want := filepath.Join(resources, "internal", "platform", "db", "sqlite", "migrations")
	if got.MigrationsDir != want {
		t.Fatalf("MigrationsDir = %q, want %q", got.MigrationsDir, want)
	}
	if got.MigrationsDir == filepath.Join(resources, "migrations") {
		t.Fatalf("MigrationsDir = %q, must not use legacy top-level migrations", got.MigrationsDir)
	}
}

func TestPackagedRuntimeFromExecutableRejectsMacOSResourcePeerBinary(t *testing.T) {
	_, ok := PackagedRuntimeFromExecutable("/Applications/Super Dolphin.app/Contents/Resources/bin/mcp-orch", "/Users/alice")
	if ok {
		t.Fatal("PackagedRuntimeFromExecutable ok = true, want false for sidecar peer binary")
	}
}

func TestPackagedRuntimeFromExecutableRejectsDevBinary(t *testing.T) {
	_, ok := PackagedRuntimeFromExecutable("/Users/alice/src/Super-Dolphin/bin/agent-terminal", "/Users/alice")
	if ok {
		t.Fatal("PackagedRuntimeFromExecutable ok = true, want false")
	}
}

func TestLoadVideoEnvRejectsMalformedLine(t *testing.T) {
	home := t.TempDir()
	t.Setenv(superDolphinHomeEnv, home)
	t.Setenv("MALFORMED_VIDEO_ENV_LINE", "")
	path := filepath.Join(home, "video.env")
	if err := os.WriteFile(path, []byte("MALFORMED_VIDEO_ENV_LINE\n"), 0o600); err != nil {
		t.Fatalf("write video.env: %v", err)
	}

	err := LoadVideoEnv()
	if err == nil {
		t.Fatal("LoadVideoEnv() error = nil, want malformed line error")
	}
	if !strings.Contains(err.Error(), "video.env:1") {
		t.Fatalf("LoadVideoEnv() error = %v, want video.env line number", err)
	}
	if got := os.Getenv("MALFORMED_VIDEO_ENV_LINE"); got != "" {
		t.Fatalf("MALFORMED_VIDEO_ENV_LINE = %q, want unset", got)
	}
}

func TestPackagedResourcesDirDetectsWindowsBinExecutable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Super Dolphin")
	makeDirs(t, root)
	writeRuntimeManifestFixture(t, root, "windows-amd64")

	got, err := packagedResourcesDirForOS("windows", filepath.Join(root, "bin", "agent-terminal.exe"))
	if err != nil {
		t.Fatalf("packagedResourcesDirForOS(windows): %v", err)
	}
	if got != root {
		t.Fatalf("packagedResourcesDirForOS(windows) = %q, want %q", got, root)
	}
}

func TestPackagedResourcesDirRejectsResourcesBinPeer(t *testing.T) {
	got, err := packagedResourcesDirForOS("darwin", "/Applications/Super Dolphin.app/Contents/Resources/bin/mcp-orch")
	if err != nil {
		t.Fatalf("packagedResourcesDirForOS(darwin): %v", err)
	}
	if got != "" {
		t.Fatalf("packagedResourcesDirForOS(darwin) = %q, want empty for sidecar peer binary", got)
	}
}

func TestPackagedAppDataDirUsesWindowsRoamingAppData(t *testing.T) {
	t.Setenv("APPDATA", "")

	got := packagedAppDataDirForOS("windows", `C:\Users\alice`)
	want := filepath.Join(`C:\Users\alice`, "AppData", "Roaming", "Super Dolphin")
	if got != want {
		t.Fatalf("packagedAppDataDirForOS(windows) = %q, want %q", got, want)
	}
}

func TestPackagedAppDataDirPrefersWindowsAppDataEnv(t *testing.T) {
	t.Setenv("APPDATA", `D:\Users\alice\Roaming`)

	got := packagedAppDataDirForOS("windows", `C:\Users\alice`)
	want := filepath.Join(`D:\Users\alice\Roaming`, "Super Dolphin")
	if got != want {
		t.Fatalf("packagedAppDataDirForOS(windows) = %q, want %q", got, want)
	}
}

func TestPackagedPathEntriesForWindowsOmitUnixSystemDirs(t *testing.T) {
	resources := filepath.Join(t.TempDir(), "Super Dolphin")
	rt := packagedRuntimeFromResourcesForOS("windows", resources, `C:\Users\alice`)
	t.Setenv("SystemRoot", `C:\Windows`)

	got := packagedPathEntriesForOS("windows", rt)
	for _, want := range []string{
		filepath.Join(resources, "bin"),
		filepath.Join(resources, "lsp", "bin"),
		filepath.Join(resources, "lsp", "node"),
		filepath.Join(resources, "lsp", "node_modules", ".bin"),
		filepath.Join(`C:\Windows`, "System32"),
		filepath.Join(`C:\Windows`),
	} {
		if !slices.Contains(got, want) {
			t.Fatalf("packagedPathEntriesForOS(windows) = %#v, missing %q", got, want)
		}
	}
	for _, forbidden := range []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("packagedPathEntriesForOS(windows) = %#v, must not include %q", got, forbidden)
		}
	}
}

func TestRequireExecutableFileForWindowsAcceptsDotExeWithoutUnixExecBit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex.exe")
	if err := os.WriteFile(path, []byte("windows binary fixture"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	if err := requireExecutableFileForOS("windows", path); err != nil {
		t.Fatalf("requireExecutableFileForOS(windows) error = %v, want nil", err)
	}
}

func TestApplyPackagedEnvPrependsBundledTools(t *testing.T) {
	resources := t.TempDir()
	binDir := filepath.Join(resources, "bin")
	lspBinDir := filepath.Join(resources, "lsp", "bin")
	lspNodePathDir := packagedNodePathEntryForTest(resources)
	lspNodeModulesBinDir := filepath.Join(resources, "lsp", "node_modules", ".bin")
	gitCore := filepath.Join(resources, "libexec", "git-core")
	templates := filepath.Join(resources, "share", "git-core", "templates")
	makeDirs(t, binDir, lspBinDir, lspNodePathDir, lspNodeModulesBinDir, gitCore, templates)
	writeBundledSidecars(t, binDir)
	t.Setenv("PATH", strings.Join([]string{"/opt/homebrew/bin", "/usr/bin"}, string(os.PathListSeparator)))
	t.Setenv(peerBinDirEnv, "/old/bin")

	if err := applyPackagedEnv(resources, "/Users/alice"); err != nil {
		t.Fatalf("applyPackagedEnv() error = %v", err)
	}

	path := os.Getenv("PATH")
	assertPathHasPrefix(t, path, binDir, lspBinDir, lspNodePathDir, lspNodeModulesBinDir)
	assertPathListExcludes(t, path,
		"/opt/homebrew/bin",
		"/usr/local/bin",
		filepath.Join("/Users/alice", ".local", "bin"),
		filepath.Join("/Users/alice", ".npm-global", "bin"),
		filepath.Join("/Users/alice", "bin"),
	)
	assertEnvEquals(t, peerBinDirEnv, binDir)
	assertEnvEquals(t, "SUPER_DOLPHIN_LSP_BUNDLE_DIR", filepath.Join(resources, "lsp"))
	assertEnvEquals(t, "SUPER_DOLPHIN_LSP_MANIFEST", filepath.Join(resources, "lsp", "lsp-manifest.json"))
	assertEnvEquals(t, "GIT_EXEC_PATH", gitCore)
	assertEnvEquals(t, "GIT_TEMPLATE_DIR", templates)
}

func TestApplySidecarRuntimeContractPackagedRebuildsBundledToolPath(t *testing.T) {
	resources := t.TempDir()
	binDir := filepath.Join(resources, "bin")
	lspBinDir := filepath.Join(resources, "lsp", "bin")
	lspNodePathDir := packagedNodePathEntryForTest(resources)
	lspNodeModulesBinDir := filepath.Join(resources, "lsp", "node_modules", ".bin")
	makeDirs(t, binDir, lspBinDir, lspNodePathDir, lspNodeModulesBinDir)
	t.Setenv("PATH", strings.Join([]string{"/bin", "/opt/homebrew/bin"}, string(os.PathListSeparator)))
	t.Setenv(peerBinDirEnv, "")
	t.Setenv(lspBundleDirEnv, "")
	t.Setenv(lspManifestEnv, "")

	if err := applySidecarRuntimeContract(SidecarRuntimeContract{Mode: "packaged", ResourcesDir: resources}); err != nil {
		t.Fatalf("applySidecarRuntimeContract() error = %v", err)
	}

	path := os.Getenv("PATH")
	assertPathHasPrefix(t, path, lspBinDir, lspNodePathDir, lspNodeModulesBinDir, binDir)
	assertPathListExcludes(t, path, "/opt/homebrew/bin")
	assertEnvEquals(t, peerBinDirEnv, binDir)
	assertEnvEquals(t, lspBundleDirEnv, filepath.Join(resources, "lsp"))
	assertEnvEquals(t, lspManifestEnv, filepath.Join(resources, "lsp", "lsp-manifest.json"))
}

func TestApplyPackagedEnvSetsControlPlaneDefaults(t *testing.T) {
	resources := t.TempDir()
	writeBundledSidecars(t, filepath.Join(resources, "bin"))
	t.Setenv(controlRPCAddrEnv, "")
	t.Setenv(sessionTokenEnv, "")
	t.Setenv("SUPER_DOLPHIN_HTTP_ADDR", "")

	if err := applyPackagedEnv(resources, "/Users/alice"); err != nil {
		t.Fatalf("applyPackagedEnv() error = %v", err)
	}

	if got := os.Getenv(controlRPCAddrEnv); got != "127.0.0.1:0" {
		t.Fatalf("%s = %q, want packaged ephemeral bind", controlRPCAddrEnv, got)
	}
	if got := os.Getenv(sessionTokenEnv); !strings.HasPrefix(got, "sd-") || len(got) <= len("sd-") {
		t.Fatalf("%s = %q, want generated token", sessionTokenEnv, got)
	}
	if got := os.Getenv("SUPER_DOLPHIN_HTTP_ADDR"); got != "127.0.0.1:0" {
		t.Fatalf("SUPER_DOLPHIN_HTTP_ADDR = %q, want packaged ephemeral bind", got)
	}
}

func TestApplyPackagedEnvPreservesExplicitControlAddrAndSessionToken(t *testing.T) {
	resources := t.TempDir()
	writeBundledSidecars(t, filepath.Join(resources, "bin"))
	t.Setenv(controlRPCAddrEnv, "127.0.0.1:19090")
	t.Setenv(httpAddrEnv, "127.0.0.1:14511")
	t.Setenv(sessionTokenEnv, "existing-token")

	if err := applyPackagedEnv(resources, "/Users/alice"); err != nil {
		t.Fatalf("applyPackagedEnv() error = %v", err)
	}

	if got := os.Getenv(controlRPCAddrEnv); got != "127.0.0.1:19090" {
		t.Fatalf("%s = %q, want explicit address preserved", controlRPCAddrEnv, got)
	}
	if got := os.Getenv(sessionTokenEnv); got != "existing-token" {
		t.Fatalf("%s = %q, want explicit token preserved", sessionTokenEnv, got)
	}
	if got := os.Getenv(httpAddrEnv); got != "127.0.0.1:14511" {
		t.Fatalf("%s = %q, want explicit address preserved", httpAddrEnv, got)
	}
}

func TestApplyPackagedEnvSetsModelRegistryWhenBundled(t *testing.T) {
	resources := t.TempDir()
	writeBundledSidecars(t, filepath.Join(resources, "bin"))
	modelRegistry := filepath.Join(resources, "models.yaml")
	if err := os.WriteFile(modelRegistry, []byte("providers: []\n"), 0o600); err != nil {
		t.Fatalf("write models.yaml: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_MODEL_REGISTRY", "")

	if err := applyPackagedEnv(resources, ""); err != nil {
		t.Fatalf("applyPackagedEnv() error = %v", err)
	}

	if got := os.Getenv("SUPER_DOLPHIN_MODEL_REGISTRY"); got != modelRegistry {
		t.Fatalf("SUPER_DOLPHIN_MODEL_REGISTRY = %q, want %q", got, modelRegistry)
	}
}

func TestApplyPackagedEnvRequiresBundledCodex(t *testing.T) {
	resources := t.TempDir()
	binDir := filepath.Join(resources, "bin")
	writeBundledSidecars(t, binDir)
	t.Setenv("SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX", "")
	t.Setenv(codexHomeEnv, "")
	t.Setenv(packagedCodexEnv, "")

	if err := applyPackagedEnv(resources, "/Users/alice"); err != nil {
		t.Fatalf("applyPackagedEnv() error = %v", err)
	}

	if got := os.Getenv("SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX"); got != "1" {
		t.Fatalf("SUPER_DOLPHIN_REQUIRE_BUNDLED_CODEX = %q, want packaged runtime to require bundled Codex", got)
	}
	if got, want := os.Getenv(codexHomeEnv), filepath.Join(packagedAppDataDirForOS(runtimeGOOS(), "/Users/alice"), "providers", "codex"); got != want {
		t.Fatalf("%s = %q, want %q", codexHomeEnv, got, want)
	}
	if got := os.Getenv(packagedCodexEnv); got != "1" {
		t.Fatalf("%s = %q, want packaged runtime default Codex identity enabled", packagedCodexEnv, got)
	}
}

func TestApplyPackagedEnvOverridesInheritedPackagedPaths(t *testing.T) {
	resources := t.TempDir()
	binDir := filepath.Join(resources, "bin")
	writeBundledSidecars(t, binDir)
	t.Setenv(projectRootEnv, "/old/project")
	t.Setenv(peerBinDirEnv, "/old/bin")
	t.Setenv(requireCodexEnv, "0")

	if err := applyPackagedEnv(resources, "/Users/alice"); err != nil {
		t.Fatalf("applyPackagedEnv() error = %v", err)
	}

	if got := os.Getenv(projectRootEnv); got != resources {
		t.Fatalf("%s = %q, want packaged resources %q", projectRootEnv, got, resources)
	}
	if got := os.Getenv(peerBinDirEnv); got != binDir {
		t.Fatalf("%s = %q, want packaged bin %q", peerBinDirEnv, got, binDir)
	}
	if got := os.Getenv(requireCodexEnv); got != "1" {
		t.Fatalf("%s = %q, want packaged runtime override", requireCodexEnv, got)
	}
}

func TestApplyPackagedEnvFailsWhenBundledSidecarMissing(t *testing.T) {
	resources := t.TempDir()
	binDir := filepath.Join(resources, "bin")
	writeExecutable(t, binDir, "mcp-orch")
	writeExecutable(t, binDir, "mcp-ida")

	err := applyPackagedEnv(resources, "/Users/alice")
	if err == nil {
		t.Fatal("applyPackagedEnv() error = nil, want missing sidecar failure")
	}
	for _, want := range []string{"missing bundled sidecar", filepath.Join(binDir, "mcp-lsp")} {
		want = strings.ReplaceAll(want, filepath.Join(binDir, "mcp-lsp"), filepath.Join(binDir, executableNameForOS(runtimeGOOS(), "mcp-lsp")))
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("applyPackagedEnv() error = %v, want substring %q", err, want)
		}
	}
}

func TestApplyPackagedEnvFailsWhenBundledLSPManifestMissing(t *testing.T) {
	resources := t.TempDir()
	binDir := filepath.Join(resources, "bin")
	writeOnlyBundledSidecars(t, binDir)

	err := applyPackagedEnv(resources, "/Users/alice")
	if err == nil {
		t.Fatal("applyPackagedEnv() error = nil, want missing bundled LSP manifest failure")
	}
	for _, want := range []string{"missing bundled LSP manifest", filepath.Join(resources, "lsp", "lsp-manifest.json")} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("applyPackagedEnv() error = %v, want substring %q", err, want)
		}
	}
}

func TestApplyPackagedEnvFailsWhenBundledLSPServerMissing(t *testing.T) {
	resources := t.TempDir()
	binDir := filepath.Join(resources, "bin")
	writeOnlyBundledSidecars(t, binDir)
	writeBundledLSPManifest(t, resources)
	writeExecutable(t, filepath.Join(resources, "lsp", "bin"), "clangd")
	writeExecutable(t, filepath.Join(resources, "lsp", "bin"), "typescript-language-server")
	writeExecutable(t, filepath.Join(resources, "lsp", "bin"), "pyright-langserver")
	writeExecutable(t, filepath.Join(resources, "lsp", "bin"), "vscode-css-language-server")
	writeExecutable(t, filepath.Join(resources, "lsp", "bin"), "rust-analyzer")
	writeExecutable(t, filepath.Join(resources, "lsp", "bin"), "jdtls")

	err := applyPackagedEnv(resources, "/Users/alice")
	if err == nil {
		t.Fatal("applyPackagedEnv() error = nil, want missing bundled LSP server failure")
	}
	for _, want := range []string{"missing bundled LSP server", filepath.Join(resources, "lsp", "bin", executableNameForOS(runtimeGOOS(), "gopls"))} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("applyPackagedEnv() error = %v, want substring %q", err, want)
		}
	}
}

func TestApplyPackagedEnvAcceptsStandardLSPBundleWithoutJDTLS(t *testing.T) {
	resources := t.TempDir()
	binDir := filepath.Join(resources, "bin")
	writeOnlyBundledSidecars(t, binDir)
	writeStandardBundledLSPManifest(t, resources)
	for _, name := range []string{
		"gopls",
		"clangd",
		"typescript-language-server",
		"pyright-langserver",
		"vscode-css-language-server",
		"rust-analyzer",
		"bash-language-server",
		"sqruff",
	} {
		writeExecutable(t, filepath.Join(resources, "lsp", "bin"), name)
	}
	if _, err := os.Stat(filepath.Join(resources, "lsp", "bin", "jdtls")); !os.IsNotExist(err) {
		t.Fatalf("standard LSP bundle unexpectedly contains jdtls: %v", err)
	}

	if err := applyPackagedEnv(resources, "/Users/alice"); err != nil {
		t.Fatalf("applyPackagedEnv() error = %v", err)
	}
	bundle, packaged, err := LoadLSPBundleFromEnv()
	if err != nil {
		t.Fatalf("LoadLSPBundleFromEnv() error = %v", err)
	}
	if !packaged {
		t.Fatal("LoadLSPBundleFromEnv() packaged = false, want true")
	}
	for _, languageID := range []string{"go", "gomod", "gosum", "gowork", "c", "cpp", "objective-c", "objective-cpp", "mql", "mql4", "mql5", "mq4", "mq5", "mqh", "javascript", "javascriptreact", "typescript", "typescriptreact", "css", "python", "rust", "shellscript", "sql"} {
		if _, ok := bundle.ServerForLanguage(languageID); !ok {
			t.Fatalf("standard LSP bundle missing language %q; languages=%v", languageID, bundle.SemanticLanguages())
		}
	}
	if _, ok := bundle.ServerForLanguage("java"); ok {
		t.Fatalf("standard LSP bundle registered java without bundled jdtls; languages=%v", bundle.SemanticLanguages())
	}
}

func TestDefaultLSPLanguagesMapsBundledLanguageServers(t *testing.T) {
	for serverID, want := range map[string][]string{
		"bash-language-server": {"shellscript"},
		"clangd":               {"c", "cpp", "objective-c", "objective-cpp", "mql", "mql4", "mql5", "mq4", "mq5", "mqh"},
		"sqruff":               {"sql"},
	} {
		got := defaultLSPLanguages(serverID)
		if !slices.Equal(got, want) {
			t.Fatalf("defaultLSPLanguages(%s) = %v, want %v", serverID, got, want)
		}
	}
}

func TestDefaultLSPLanguagesReturnsIndependentDescriptor(t *testing.T) {
	first := defaultLSPLanguages("gopls")
	first[0] = "changed"
	if got, want := defaultLSPLanguages("gopls"), []string{"go", "gomod", "gosum", "gowork"}; !slices.Equal(got, want) {
		t.Fatalf("defaultLSPLanguages(gopls) after caller mutation = %v, want %v", got, want)
	}
}

func TestLoadLSPBundleResolvesClangdRelativeToEachDeviceBundle(t *testing.T) {
	resolved := make([]string, 0, 2)
	for range 2 {
		bundleDir := t.TempDir()
		executable := executableNameForOS(runtimeGOOS(), "clangd")
		writeExecutable(t, filepath.Join(bundleDir, "bin"), executable)
		manifestPath := filepath.Join(bundleDir, "lsp-manifest.json")
		manifest := fmt.Sprintf(`{"servers":{"clangd":{"path":"bin/%s","languages":["c","cpp","mql4","mql5"]}}}`, executable)
		if err := os.WriteFile(manifestPath, []byte(manifest), 0o644); err != nil {
			t.Fatalf("write clangd bundle manifest: %v", err)
		}
		bundle, err := LoadLSPBundle(bundleDir, manifestPath)
		if err != nil {
			t.Fatalf("LoadLSPBundle: %v", err)
		}
		server, ok := bundle.ServerForLanguage("mql5")
		if !ok {
			t.Fatalf("bundle languages = %v, missing mql5", bundle.SemanticLanguages())
		}
		want := filepath.Join(bundleDir, "bin", executable)
		if server.Path != want {
			t.Fatalf("clangd path = %q, want device-local bundle path %q", server.Path, want)
		}
		resolved = append(resolved, server.Path)
	}
	if resolved[0] == resolved[1] {
		t.Fatalf("distinct device bundle roots resolved the same clangd path: %v", resolved)
	}
}

func TestBundledSidecarNamesReturnsIndependentDescriptor(t *testing.T) {
	first := bundledSidecarNames()
	first[0] = "changed"
	if got, want := bundledSidecarNames(), []string{"mcp-orch", "mcp-lsp", "mcp-ida"}; !slices.Equal(got, want) {
		t.Fatalf("bundledSidecarNames() after caller mutation = %v, want %v", got, want)
	}
}

func writeBundledSidecars(t *testing.T, binDir string) {
	t.Helper()
	writeOnlyBundledSidecars(t, binDir)
	resources := filepath.Dir(binDir)
	writeBundledLSPManifest(t, resources)
	writeExecutable(t, filepath.Join(resources, "lsp", "bin"), executableNameForOS(runtimeGOOS(), "gopls"))
	writeExecutable(t, filepath.Join(resources, "lsp", "bin"), executableNameForOS(runtimeGOOS(), "clangd"))
	writeExecutable(t, filepath.Join(resources, "lsp", "bin"), executableNameForOS(runtimeGOOS(), "typescript-language-server"))
	writeExecutable(t, filepath.Join(resources, "lsp", "bin"), executableNameForOS(runtimeGOOS(), "pyright-langserver"))
	writeExecutable(t, filepath.Join(resources, "lsp", "bin"), executableNameForOS(runtimeGOOS(), "vscode-css-language-server"))
	writeExecutable(t, filepath.Join(resources, "lsp", "bin"), executableNameForOS(runtimeGOOS(), "rust-analyzer"))
	writeExecutable(t, filepath.Join(resources, "lsp", "bin"), executableNameForOS(runtimeGOOS(), "jdtls"))
}

func writeOnlyBundledSidecars(t *testing.T, binDir string) {
	t.Helper()
	for _, name := range []string{"mcp-orch", "mcp-lsp", "mcp-ida"} {
		writeExecutable(t, binDir, executableNameForOS(runtimeGOOS(), name))
	}
}

func writeBundledLSPManifest(t *testing.T, resources string) {
	t.Helper()
	manifest := fmt.Sprintf(`{
  "schema_version": 1,
  "servers": {
    "gopls": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"},
    "clangd": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"},
    "typescript-language-server": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"},
    "pyright": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"},
    "vscode-langservers-extracted": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"},
    "rust-analyzer": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"},
    "jdtls": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"}
  }
}
`, executableNameForOS(runtimeGOOS(), "gopls"), executableNameForOS(runtimeGOOS(), "clangd"), executableNameForOS(runtimeGOOS(), "typescript-language-server"), executableNameForOS(runtimeGOOS(), "pyright-langserver"), executableNameForOS(runtimeGOOS(), "vscode-css-language-server"), executableNameForOS(runtimeGOOS(), "rust-analyzer"), executableNameForOS(runtimeGOOS(), "jdtls"))
	path := filepath.Join(resources, "lsp", "lsp-manifest.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func writeStandardBundledLSPManifest(t *testing.T, resources string) {
	t.Helper()
	manifest := fmt.Sprintf(`{
  "schema_version": 1,
  "servers": {
    "gopls": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"},
    "clangd": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"},
    "typescript-language-server": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"},
    "pyright": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"},
    "vscode-langservers-extracted": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"},
    "rust-analyzer": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"},
    "bash-language-server": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"},
    "sqruff": {"path": "lsp/bin/%s", "version": "test", "sha256": "0000000000000000000000000000000000000000000000000000000000000000"}
  }
}
`, executableNameForOS(runtimeGOOS(), "gopls"), executableNameForOS(runtimeGOOS(), "clangd"), executableNameForOS(runtimeGOOS(), "typescript-language-server"), executableNameForOS(runtimeGOOS(), "pyright-langserver"), executableNameForOS(runtimeGOOS(), "vscode-css-language-server"), executableNameForOS(runtimeGOOS(), "rust-analyzer"), executableNameForOS(runtimeGOOS(), "bash-language-server"), executableNameForOS(runtimeGOOS(), "sqruff"))
	path := filepath.Join(resources, "lsp", "lsp-manifest.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func pathListContains(pathList, want string) bool {
	return slices.Contains(filepath.SplitList(pathList), want)
}

func makeDirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
}

func packagedNodePathEntryForTest(resources string) string {
	if runtimeGOOS() == "windows" {
		return filepath.Join(resources, "lsp", "node")
	}
	return filepath.Join(resources, "lsp", "node", "bin")
}

func assertPathHasPrefix(t *testing.T, path string, entries ...string) {
	t.Helper()
	wantPrefix := strings.Join(entries, string(os.PathListSeparator)) + string(os.PathListSeparator)
	if !strings.HasPrefix(path, wantPrefix) {
		t.Fatalf("PATH = %q, want bundled runtime and LSP bins first: %q", path, wantPrefix)
	}
}

func assertPathListExcludes(t *testing.T, pathList string, entries ...string) {
	t.Helper()
	for _, forbidden := range entries {
		if pathListContains(pathList, forbidden) {
			t.Fatalf("PATH = %q, must not include user package-manager path %q in packaged runtime", pathList, forbidden)
		}
	}
}

func assertEnvEquals(t *testing.T, key, want string) {
	t.Helper()
	if got := os.Getenv(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func writeExecutable(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", dir, err)
	}
	names := []string{name}
	if runtimeGOOS() == "windows" && filepath.Ext(name) == "" {
		names = append(names, name+".exe")
	}
	for _, current := range names {
		if err := os.WriteFile(filepath.Join(dir, current), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", filepath.Join(dir, current), err)
		}
	}
}

func TestConfigurePackagedAppReturnsSetenvError(t *testing.T) {
	resources := t.TempDir()
	writePackagedRuntimeFixture(t, resources, runtimeGOOS()+"-"+runtimeGOARCH())
	deps := runtimeDeps{
		executable: func() (string, error) {
			return filepath.Join(resources, "bin", executableNameForOS(runtimeGOOS(), "agent-terminal")), nil
		},
		userHomeDir: func() (string, error) {
			return "/Users/alice", nil
		},
		setenv: func(key, value string) error {
			if key == projectRootEnv {
				return errors.New("injected setenv failure")
			}
			return nil
		},
	}
	t.Setenv(projectRootEnv, "")
	t.Setenv(packageRootEnv, resources)

	err := configurePackagedApp(deps)
	if err == nil {
		t.Fatal("ConfigurePackagedApp() error = nil, want setenv failure")
	}
	for _, want := range []string{"configure packaged runtime env", projectRootEnv, "injected setenv failure"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ConfigurePackagedApp() error = %v, want substring %q", err, want)
		}
	}
}

func TestConfigurePackagedAppSkipsDevBinaryWithoutUserHome(t *testing.T) {
	t.Setenv(runtimeModeEnv, "")
	t.Setenv(packageRootEnv, "")
	t.Setenv(packagedLauncherEnv, "")

	deps := runtimeDeps{
		executable: func() (string, error) {
			return filepath.Join(t.TempDir(), "bin", "agent-terminal"), nil
		},
		userHomeDir: func() (string, error) {
			t.Fatal("user home must not be required for a dev binary")
			return "", nil
		},
		setenv: os.Setenv,
	}

	if err := configurePackagedApp(deps); err != nil {
		t.Fatalf("ConfigurePackagedApp() error = %v, want nil for dev binary", err)
	}
}
