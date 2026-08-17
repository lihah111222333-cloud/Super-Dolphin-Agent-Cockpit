//go:build linux && amd64

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestLinuxSwiftRubyManifestsArePinnedOfficialAmd64Assets(t *testing.T) {
	manifest := linuxSwiftRubyManifests()
	assertLinuxSwiftRubyPinned(t, "swift", manifest.swift)
	assertLinuxSwiftRubyPinned(t, "portable-ruby", manifest.ruby)
	if len(manifest.swiftDependencies) != 2 || len(manifest.rubyDependencies) != 2 {
		t.Fatalf("managed dependency counts changed: swift=%d ruby=%d", len(manifest.swiftDependencies), len(manifest.rubyDependencies))
	}
	for index, dependency := range append(append([]lspinstaller.NativeArtifactSpec{}, manifest.swiftDependencies...), manifest.rubyDependencies...) {
		assertLinuxSwiftRubyPinned(t, fmt.Sprintf("native dependency %d", index), dependency)
		if dependency.Format != lspinstaller.NativeArtifactFormatDeb {
			t.Fatalf("native dependency %s format = %q, want deb", dependency.Name, dependency.Format)
		}
	}
	assertLinuxSwiftRubyExactManifests(t, manifest)
}

// assertLinuxSwiftRubyPinned 校验原生工件固定到完整 HTTPS 摘要。
func assertLinuxSwiftRubyPinned(t *testing.T, name string, spec lspinstaller.NativeArtifactSpec) {
	t.Helper()
	if spec.Name == "" || spec.Version == "" {
		t.Fatalf("%s manifest identity is incomplete: %#v", name, spec)
	}
	if !strings.HasPrefix(spec.URL, "https://") {
		t.Fatalf("%s manifest URL is incomplete: %#v", name, spec)
	}
	if len(spec.SHA256) != sha256.Size*2 {
		t.Fatalf("%s SHA256 length is invalid: %q", name, spec.SHA256)
	}
	if strings.Trim(spec.SHA256, "0123456789abcdef") != "" {
		t.Fatalf("%s SHA256 is not lowercase: %q", name, spec.SHA256)
	}
	if spec.Format == "" || spec.BinaryPath == "" {
		t.Fatalf("%s manifest layout is incomplete: %#v", name, spec)
	}
	if spec.LauncherName == "" {
		t.Fatalf("%s launcher is incomplete: %#v", name, spec)
	}
}

// assertLinuxSwiftRubyExactManifests 校验上游版本和摘要没有漂移。
func assertLinuxSwiftRubyExactManifests(t *testing.T, manifest linuxSwiftRubyManifest) {
	t.Helper()
	if manifest.swift.URL != "https://download.swift.org/swift-6.3.3-release/ubuntu2404/swift-6.3.3-RELEASE/swift-6.3.3-RELEASE-ubuntu24.04.tar.gz" || manifest.swift.SHA256 != "da8272a5fddccd65b1529ed0e52e04526e2eadd4237d58d6220efeb973c6cd19" {
		t.Fatalf("Swift manifest changed: %#v", manifest.swift)
	}
	if !manifest.swift.AllowSymlinks {
		t.Fatal("Swift manifest must permit validated internal toolchain symlinks")
	}
	if manifest.ruby.URL != "https://ghcr.io/v2/homebrew/core/portable-ruby/blobs/sha256:0980099dc2668dc47bd4c85b704beb76b9406b4a85f77fdda9820d8341b40f87" {
		t.Fatalf("portable Ruby URL changed: %#v", manifest.ruby)
	}
	if manifest.ruby.SHA256 != "0980099dc2668dc47bd4c85b704beb76b9406b4a85f77fdda9820d8341b40f87" {
		t.Fatalf("portable Ruby digest changed: %#v", manifest.ruby)
	}
	if manifest.solargraph.name != "solargraph" || manifest.solargraph.version != "0.60.2" {
		t.Fatalf("Solargraph identity changed: %#v", manifest.solargraph)
	}
	if manifest.solargraph.url != "https://rubygems.org/downloads/solargraph-0.60.2.gem" || manifest.solargraph.sha256 != "35c8fb31fcdbe8ccd0e0e84862a65b8deb319f86210c5966e41e2fc011e52538" {
		t.Fatalf("Solargraph source changed: %#v", manifest.solargraph)
	}
}

func TestLinuxSwiftRubyRegistrationIsManagedOnlyOnCleanPATH(t *testing.T) {
	root := filepath.Join(t.TempDir(), "native-artifacts")
	provider := lspinstaller.NewProvider()
	if err := registerLinuxSwiftRubyInstallers(provider, root, &http.Client{}); err != nil {
		t.Fatalf("registerLinuxSwiftRubyInstallers: %v", err)
	}
	for _, language := range []string{contract.LSPServiceSwift, contract.LSPServiceRuby} {
		assertLinuxSwiftRubyManagedConfig(t, provider, language)
	}
	pathDir := filepath.Join(t.TempDir(), "path")
	if err := os.Mkdir(pathDir, 0o700); err != nil {
		t.Fatalf("create clean PATH: %v", err)
	}
	t.Setenv("PATH", pathDir)
	ctx := lspinstaller.WithToolCallInstallCheckOnly(context.Background())
	for _, language := range []string{contract.LSPServiceSwift, contract.LSPServiceRuby} {
		_, err := provider.EnsureInstalledDetailed(ctx, language)
		var missing *lspinstaller.MissingBinaryError
		if err == nil || !errors.As(err, &missing) {
			t.Fatalf("EnsureInstalledDetailed(%s) = %v, want managed-only MissingBinaryError", language, err)
		}
	}
}

// assertLinuxSwiftRubyManagedConfig 校验语言只允许受管安装。
func assertLinuxSwiftRubyManagedConfig(t *testing.T, provider *lspinstaller.Provider, language string) {
	t.Helper()
	cfg, ok := provider.ConfigForLanguage(language)
	if !ok {
		t.Fatalf("missing %s installer config", language)
	}
	if !cfg.ManagedOnly || cfg.InstallCmd != "" {
		t.Fatalf("%s config is not managed-only: %#v", language, cfg)
	}
	if cfg.ManagedInstall == nil || cfg.ManagedBinaryPath == "" {
		t.Fatalf("%s managed installer is incomplete: %#v", language, cfg)
	}
	if !filepath.IsAbs(cfg.ManagedBinaryPath) {
		t.Fatalf("%s managed path is not absolute: %#v", language, cfg)
	}
}

func TestLinuxSwiftLauncherPinsManagedToolchainAndRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "toolchain", "usr", "bin", "sourcekit-lsp")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatalf("create sourcekit directory: %v", err)
	}
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake sourcekit: %v", err)
	}
	launcher := filepath.Join(root, "launcher", "sourcekit-lsp")
	if err := writeLinuxSwiftLauncher(launcher, target, filepath.Join(root, "toolchain", "usr"), filepath.Join(root, "toolchain", "usr", "lib", "swift", "linux")); err != nil {
		t.Fatalf("writeLinuxSwiftLauncher: %v", err)
	}
	content, err := os.ReadFile(launcher)
	if err != nil {
		t.Fatalf("read Swift launcher: %v", err)
	}
	launcherText := string(content)
	assertLinuxSwiftLauncherContent(t, launcher, launcherText, target)
	link := filepath.Join(root, "launcher", "symlink")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create launcher symlink: %v", err)
	}
	if err := writeLinuxManagedLauncher(link, "#!/bin/sh\n"); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink launcher overwrite error = %v, want regular-file rejection", err)
	}
}

// assertLinuxSwiftLauncherContent 校验启动器内容与执行权限。
func assertLinuxSwiftLauncherContent(t *testing.T, launcher, content, target string) {
	t.Helper()
	for _, fragment := range []string{"SWIFT_EXEC=", "PATH=", "exec '", target} {
		if !strings.Contains(content, fragment) {
			t.Fatalf("Swift launcher misses %q: %s", fragment, content)
		}
	}
	info, err := os.Stat(launcher)
	if err != nil {
		t.Fatalf("stat Swift launcher: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("Swift launcher is not executable: %v", info)
	}
}

func TestLinuxSolargraphGemTLSDownloadAndSecondUse(t *testing.T) {
	payload := []byte("fixed solargraph gem fixture")
	digest := sha256HexLinuxSwiftRuby(payload)
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path != "/solargraph.gem" {
			http.NotFound(w, request)
			return
		}
		_, _ = io.Copy(w, strings.NewReader(string(payload)))
	}))
	t.Cleanup(server.Close)
	destination := filepath.Join(t.TempDir(), "gems", "cache", "solargraph-0.60.2.gem")
	client := server.Client()
	if err := downloadLinuxVerifiedGem(t.Context(), client, server.URL+"/solargraph.gem", digest, destination); err != nil {
		t.Fatalf("first verified Solargraph download: %v", err)
	}
	if err := downloadLinuxVerifiedGem(t.Context(), client, server.URL+"/solargraph.gem", digest, destination); err != nil {
		t.Fatalf("second verified Solargraph download: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("TLS gem request count = %d, want one after cache reuse", requests.Load())
	}
}

func TestLinuxSwiftRubyManagedInstallFullChainAndReuseWithTLSFixtures(t *testing.T) {
	manifest := linuxSwiftRubyManifests()
	manifest.swiftDependencies = nil
	manifest.rubyDependencies = nil
	swiftArchive := buildLinuxSwiftRubyTarGz(t, map[string][]byte{
		manifest.swift.BinaryPath: []byte("#!/bin/sh\nexit 0\n"),
		filepath.ToSlash(filepath.Join(filepath.Dir(manifest.swift.BinaryPath), "swift")): []byte("#!/bin/sh\nexit 0\n"),
	})
	rubyArchive := buildLinuxSwiftRubyTarGz(t, map[string][]byte{
		manifest.ruby.BinaryPath: []byte("#!/bin/sh\nexit 0\n"),
		filepath.ToSlash(filepath.Join(filepath.Dir(manifest.ruby.BinaryPath), "gem")): []byte("#!/bin/sh\nset -eu\ninstall_dir=\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"--install-dir\" ]; then install_dir=\"$2\"; shift 2; else shift; fi\ndone\nif [ -z \"$install_dir\" ]; then exit 2; fi\n/bin/mkdir -p \"$install_dir/bin\"\nprintf '%s\\n' '#!/bin/sh' 'exit 0' > \"$install_dir/bin/solargraph\"\n/bin/chmod 755 \"$install_dir/bin/solargraph\"\n"),
	})
	gemPayload := []byte("solargraph fixture gem")
	archives := map[string][]byte{"/swift": swiftArchive, "/ruby": rubyArchive, "/gem": gemPayload}
	requests := make(map[string]int)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests[request.URL.Path]++
		archive, ok := archives[request.URL.Path]
		if !ok {
			http.NotFound(w, request)
			return
		}
		if _, err := w.Write(archive); err != nil {
			t.Errorf("write fixture %s: %v", request.URL.Path, err)
		}
	}))
	t.Cleanup(server.Close)
	manifest.swift.URL, manifest.swift.SHA256 = server.URL+"/swift", sha256HexLinuxSwiftRuby(swiftArchive)
	manifest.ruby.URL, manifest.ruby.SHA256 = server.URL+"/ruby", sha256HexLinuxSwiftRuby(rubyArchive)
	manifest.solargraph.url, manifest.solargraph.sha256 = server.URL+"/gem", sha256HexLinuxSwiftRuby(gemPayload)
	root := filepath.Join(t.TempDir(), "managed-artifacts")
	provider := lspinstaller.NewProvider()
	if err := registerLinuxSwiftRubyInstallersWithManifest(provider, root, server.Client(), manifest); err != nil {
		t.Fatalf("registerLinuxSwiftRubyInstallersWithManifest: %v", err)
	}
	pathDir := filepath.Join(t.TempDir(), "empty-path")
	if err := os.Mkdir(pathDir, 0o700); err != nil {
		t.Fatalf("create empty PATH: %v", err)
	}
	t.Setenv("PATH", pathDir)
	ctx := lspinstaller.WithInstallCommandCapability(context.Background())
	want := map[string]string{
		contract.LSPServiceSwift: filepath.Join(root, manifest.swift.Name, manifest.swift.Version, "launcher", manifest.swift.LauncherName),
		contract.LSPServiceRuby:  filepath.Join(root, manifest.solargraph.name, manifest.solargraph.version, "launcher", manifest.solargraph.launcher),
	}
	assertLinuxSwiftRubyFirstInstall(t, provider, ctx, pathDir, want)
	assertLinuxSwiftRubyReuse(t, provider, ctx, want)
	for _, path := range []string{"/swift", "/ruby", "/gem"} {
		if requests[path] != 1 {
			t.Fatalf("TLS fixture request count for %s = %d, want one", path, requests[path])
		}
	}
}

// assertLinuxSwiftRubyFirstInstall 校验首次安装与受管启动器执行。
func assertLinuxSwiftRubyFirstInstall(t *testing.T, provider *lspinstaller.Provider, ctx context.Context, pathDir string, want map[string]string) {
	t.Helper()
	for _, language := range []string{contract.LSPServiceSwift, contract.LSPServiceRuby} {
		result, err := provider.EnsureInstalledDetailed(ctx, language)
		if err != nil {
			t.Fatalf("first EnsureInstalledDetailed(%s): %v", language, err)
		}
		if result.Path != want[language] || result.Status != lspinstaller.InstallStatusInstalledPath {
			t.Fatalf("first %s result = %#v, want %q", language, result, want[language])
		}
		info, err := os.Stat(result.Path)
		if err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("%s launcher is not executable: info=%v err=%v", language, info, err)
		}
		command := exec.Command(result.Path)
		command.Env = []string{"PATH=" + pathDir}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("run managed %s launcher: %v; output=%s", language, err, output)
		}
	}
}

// assertLinuxSwiftRubyReuse 校验后续解析复用既有路径。
func assertLinuxSwiftRubyReuse(t *testing.T, provider *lspinstaller.Provider, ctx context.Context, want map[string]string) {
	t.Helper()
	for _, language := range []string{contract.LSPServiceSwift, contract.LSPServiceRuby} {
		result, err := provider.EnsureInstalledDetailed(ctx, language)
		if err != nil {
			t.Fatalf("second EnsureInstalledDetailed(%s): %v", language, err)
		}
		if result.Path != want[language] || result.Status != lspinstaller.InstallStatusPathFound {
			t.Fatalf("second %s result = %#v, want %q", language, result, want[language])
		}
	}
}

func buildLinuxSwiftRubyTarGz(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	for name, content := range entries {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("write tar fixture header %q: %v", name, err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			t.Fatalf("write tar fixture content %q: %v", name, err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar fixture: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip fixture: %v", err)
	}
	return output.Bytes()
}

func sha256HexLinuxSwiftRuby(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
