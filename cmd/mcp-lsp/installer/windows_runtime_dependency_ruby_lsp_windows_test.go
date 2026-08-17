//go:build windows

package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bodgit/sevenzip"
)

func TestWindowsRubyLSPPrivateInstallEnvironmentLocksRubyGems(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cohort")
	env := windowsRubyLSPPrivateEnvironment(root)
	want := map[string]string{
		"GEM_HOME":           filepath.Join(root, "gems"),
		"BUNDLE_GEMFILE":     filepath.Join(root, ".ruby-lsp-private", "Gemfile"),
		"RUBYGEMS_GEMDEPS":   "",
		"BUNDLE_USER_CONFIG": filepath.Join(root, ".ruby-lsp-private", ".bundle", "config"),
		"BUNDLE_APP_CONFIG":  filepath.Join(root, ".ruby-lsp-private", ".bundle"),
		"BUNDLE_PATH":        filepath.Join(root, "gems"),
		"GEMRC":              filepath.Join(root, ".ruby-lsp-private", "gemrc"),
		"RUBYOPT":            "",
		"RUBYLIB":            "",
		"HOME":               filepath.Join(root, ".ruby-lsp-private", "home"),
		"USERPROFILE":        filepath.Join(root, ".ruby-lsp-private", "home"),
		"APPDATA":            filepath.Join(root, ".ruby-lsp-private", "appdata"),
		"LOCALAPPDATA":       filepath.Join(root, ".ruby-lsp-private", "localappdata"),
	}
	for key, value := range want {
		if got := windowsRubyLSPEnvironmentValue(env, key); got != value {
			t.Fatalf("%s=%q, want %q", key, got, value)
		}
	}
	gemPath := windowsRubyLSPEnvironmentValue(env, "GEM_PATH")
	if !strings.Contains(gemPath, filepath.Join(root, "gems")) || !strings.Contains(gemPath, filepath.Join(root, "rubyinstaller-4.0.5-1-arm", "lib", "ruby", "gems", "4.0.0")) {
		t.Fatalf("GEM_PATH=%q does not include private and RubyInstaller default gem roots", gemPath)
	}
	if strings.Contains(gemPath, "USERPROFILE") {
		t.Fatalf("GEM_PATH=%q leaks ambient user configuration", gemPath)
	}
}

func TestWindowsRubyLSPCommandEnvironmentRemovesGemDeps(t *testing.T) {
	t.Setenv("RUBYGEMS_GEMDEPS", "-")
	got := runtimeDependencyCommandEnvironment(windowsRubyLSPPrivateEnvironment(filepath.Join(t.TempDir(), "cohort")))
	for _, item := range got {
		name, _, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(name, "RUBYGEMS_GEMDEPS") {
			t.Fatalf("child environment still contains RUBYGEMS_GEMDEPS=%q", item)
		}
	}
	for _, key := range []string{"GEMRC", "BUNDLE_USER_CONFIG", "BUNDLE_APP_CONFIG", "BUNDLE_PATH"} {
		if value := windowsRubyLSPEnvironmentValue(got, key); value == "" {
			t.Fatalf("child %s was not pinned to the private Ruby cohort", key)
		}
	}
	for _, item := range got {
		name, _, ok := strings.Cut(item, "=")
		if ok && (strings.EqualFold(name, "RUBYOPT") || strings.EqualFold(name, "RUBYLIB")) {
			t.Fatalf("child environment still contains %s", name)
		}
	}
}

func TestWindowsRubyLSPLaunchArgumentsAreProductPrivate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cohort")
	args, err := windowsRubyLSPLaunchArguments(root)
	if err != nil {
		t.Fatalf("windowsRubyLSPLaunchArguments() error = %v", err)
	}
	if len(args) != 5 {
		t.Fatalf("launch args length=%d, want 5: %#v", len(args), args)
	}
	for _, arg := range args {
		if arg == "-I" {
			continue
		}
		if !strings.HasPrefix(filepath.Clean(arg), filepath.Clean(root)) {
			t.Fatalf("launch argument escaped product root: %q", arg)
		}
	}
	if !strings.HasSuffix(filepath.ToSlash(args[4]), "/ruby-lsp-0.26.10/exe/ruby-lsp") {
		t.Fatalf("launch script=%q, want locked Ruby LSP 0.26.10", args[4])
	}
}

func TestWindowsRubyLSPCatalogUsesNativeARM64Only(t *testing.T) {
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductRubyLSP)
	if err != nil {
		t.Fatalf("Ruby LSP catalog lookup: %v", err)
	}
	if got := entry.StatusByArchitecture[WindowsHostArchARM64]; got != WindowsRuntimeDependencyStatusInstallable {
		t.Fatalf("ARM64 status=%q, want installable", got)
	}
	for _, architecture := range []string{WindowsHostArchX64, WindowsHostArchX86} {
		if got := entry.StatusByArchitecture[architecture]; got != WindowsRuntimeDependencyStatusTypedUnsupported {
			t.Fatalf("%s status=%q, want typed_unsupported", architecture, got)
		}
	}
	if got, err := WindowsRuntimeDependencyCatalogEntryForLanguage("ruby"); err != nil || got.Product != WindowsRuntimeDependencyProductRubyLSP {
		t.Fatalf("Ruby language mapping=(%q,%v), want ruby-lsp", got.Product, err)
	}
}

// TestWindowsRubyLSPLocalProvisionProbe 使用已下载且由目录外环境显式提供的固定资产，
// 复现完整 materialize→gem install→ready 校验链；默认跳过，不产生网络流量。
func TestWindowsRubyLSPLocalProvisionProbe(t *testing.T) {
	assetRoot := strings.TrimSpace(os.Getenv("MCP_LSP_WINDOWS_RUBY_LSP_LOCAL_PROBE_ASSET_ROOT"))
	if assetRoot == "" {
		t.Skip("set MCP_LSP_WINDOWS_RUBY_LSP_LOCAL_PROBE_ASSET_ROOT for the bounded local Ruby provision probe")
	}
	assetRoot = filepath.Clean(assetRoot)
	t.Setenv("RUBYGEMS_GEMDEPS", "-")
	t.Setenv("RUBYOPT", "-rhostile-rubyopt")
	t.Setenv("RUBYLIB", filepath.Join(t.TempDir(), "hostile-rubylib"))
	t.Setenv("BUNDLE_GEMFILE", filepath.Join(t.TempDir(), "hostile-Gemfile"))
	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 26100}
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	probeRoot := strings.TrimSpace(os.Getenv("MCP_LSP_WINDOWS_RUBY_LSP_LOCAL_PROBE_LOG_DIR"))
	if probeRoot == "" {
		probeRoot = filepath.Join(t.TempDir(), "probe")
	}
	probeRoot = filepath.Clean(probeRoot)
	if err := os.MkdirAll(probeRoot, 0o700); err != nil {
		t.Fatalf("create local Ruby probe root: %v", err)
	}
	sources := map[string]string{
		"ruby":                     filepath.Join(assetRoot, "rubyinstaller-4.0.5-1-arm.7z"),
		"ruby-lsp":                 filepath.Join(assetRoot, "ruby-lsp-0.26.10.gem"),
		"language-server-protocol": filepath.Join(assetRoot, "language_server-protocol-3.17.0.0.gem"),
	}
	fetch := func(ctx context.Context, asset WindowsRuntimeDependencyAsset, destination string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		source := sources[asset.Component]
		if source == "" {
			return os.ErrNotExist
		}
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	runner := func(ctx context.Context, executable, workingDir string, args, env []string) error {
		command := exec.CommandContext(ctx, executable, args...)
		command.Dir = workingDir
		command.Env = runtimeDependencyCommandEnvironment(env)
		output, err := command.CombinedOutput()
		logPath := filepath.Join(probeRoot, "ruby-command-output.log")
		_ = os.WriteFile(logPath, output, 0o600)
		digest := sha256.Sum256(output)
		t.Logf("Ruby local command base=%s output_bytes=%d output_sha256=%s", filepath.Base(executable), len(output), hex.EncodeToString(digest[:]))
		if err != nil {
			return newProcessFailureError("runtime-dependency-command", "runtime", err, output, len(args), 0)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	result, err := ProvisionWindowsRuntimeDependencyWithOptions(ctx, WindowsRuntimeDependencyProductRubyLSP, WindowsRuntimeDependencyProvisionOptions{
		CacheRoot:      cacheRoot,
		InstallTimeout: 5 * time.Minute,
		Platform:       &platform,
		FetchAsset:     fetch,
		RunCommand:     runner,
	})
	if err != nil {
		t.Fatalf("local Ruby LSP provision probe failed: %v; raw command output is retained only in the probe temp root", err)
	}
	if result.Status != WindowsRuntimeDependencyStatusInstallable || result.ExecutablePath == "" || len(result.Args) == 0 || len(result.Env) == 0 {
		t.Fatalf("local Ruby LSP provision result = %#v, want ready installable cohort", result)
	}
}

// TestWindowsRubyLSPRuntimeArchiveContainsRubyGemsClosure 检查固定 7z 中的 RubyGems
// 解析器文件是否被纯 Go 解包器完整物化；默认跳过，仅由本地资产诊断门控启用。
func TestWindowsRubyLSPRuntimeArchiveContainsRubyGemsClosure(t *testing.T) {
	assetRoot := strings.TrimSpace(os.Getenv("MCP_LSP_WINDOWS_RUBY_LSP_LOCAL_PROBE_ASSET_ROOT"))
	if assetRoot == "" {
		t.Skip("set MCP_LSP_WINDOWS_RUBY_LSP_LOCAL_PROBE_ASSET_ROOT for the Ruby archive closure probe")
	}
	payload := filepath.Join(filepath.Clean(assetRoot), "rubyinstaller-4.0.5-1-arm.7z")
	reader, err := sevenzip.OpenReader(payload)
	if err != nil {
		t.Fatalf("open Ruby ARM64 archive: %v", err)
	}
	hasSpecificationProvider := false
	entryCount := 0
	for _, entry := range reader.File {
		entryCount++
		if strings.HasSuffix(filepath.ToSlash(entry.Name), "/rubygems/vendor/molinillo/lib/molinillo/delegates/specification_provider.rb") {
			hasSpecificationProvider = true
		}
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close Ruby ARM64 archive: %v", err)
	}
	extractionRoot := strings.TrimSpace(os.Getenv("MCP_LSP_WINDOWS_RUBY_LSP_ARCHIVE_EXTRACT_ROOT"))
	if extractionRoot == "" {
		extractionRoot = t.TempDir()
	}
	extracted := filepath.Join(filepath.Clean(extractionRoot), "rubyinstaller-4.0.5-1-arm")
	if err := extractWindowsRuntimeDependencySevenZipAsset(payload, extracted, runtimeDependencyMaxTreeBytes); err != nil {
		t.Fatalf("extract Ruby ARM64 archive: %v", err)
	}
	relative := filepath.FromSlash("rubyinstaller-4.0.5-1-arm/lib/ruby/4.0.0/rubygems/vendor/molinillo/lib/molinillo/delegates/specification_provider.rb")
	_, statErr := os.Stat(filepath.Join(extracted, relative))
	t.Logf("Ruby archive entry_count=%d archive_has_specification_provider=%t extracted_specification_provider=%t", entryCount, hasSpecificationProvider, statErr == nil)
	if !hasSpecificationProvider || statErr != nil {
		t.Fatalf("Ruby ARM64 archive closure lost rubygems vendor resolver file: archive_present=%t extracted_error=%v", hasSpecificationProvider, statErr)
	}
}
