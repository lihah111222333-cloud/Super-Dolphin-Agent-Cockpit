//go:build linux && amd64

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

func TestLinuxJavaKotlinManifestsArePinnedOfficialAmd64Assets(t *testing.T) {
	artifacts := linuxJavaKotlinArtifactSpecs()
	want := []struct {
		name, version, url, sha256, format, binary, launcher string
		spec                                                 lspinstaller.NativeArtifactSpec
	}{
		{name: "temurin-jre", version: linuxJavaRuntimeVersion, url: "https://github.com/adoptium/temurin21-binaries/releases/download/jdk-21.0.11%2B10/OpenJDK21U-jre_x64_linux_hotspot_21.0.11_10.tar.gz", sha256: "e5038aae3ca9ff670bc696496b0728dbd23d280026bad30291cb919221ecfdcb", format: lspinstaller.NativeArtifactFormatTarGz, binary: "jdk-21.0.11+10-jre/bin/java", launcher: "java", spec: artifacts.javaRuntime},
		{name: "jdtls", version: linuxJDTLSVersion, url: "https://download.eclipse.org/jdtls/milestones/1.60.0/jdt-language-server-1.60.0-202606262232.tar.gz", sha256: "e94c303d8198f977930803582738771fd18c52c5492878410bf222b1aa81ef1d", format: lspinstaller.NativeArtifactFormatTarGz, binary: "bin/jdtls", launcher: "jdtls", spec: artifacts.jdtls},
		{name: "kotlin-language-server", version: linuxKotlinLSVersion, url: "https://github.com/fwcd/kotlin-language-server/releases/download/1.3.13/server.zip", sha256: "4fe7d71d087b307c7869036171bd9d8c6a4284cd7c25b89098b0a24eb2d9b6d2", format: lspinstaller.NativeArtifactFormatZip, binary: "server/bin/kotlin-language-server", launcher: "kotlin-language-server", spec: artifacts.kotlin},
	}
	for _, tc := range want {
		t.Run(tc.name, func(t *testing.T) {
			assertLinuxJavaKotlinArtifact(t, tc.spec, tc.name, tc.version, tc.url, tc.sha256, tc.format, tc.binary, tc.launcher)
		})
	}
}

// assertLinuxJavaKotlinArtifact 校验 Java/Kotlin 工件固定到官方 HTTPS 摘要。
func assertLinuxJavaKotlinArtifact(t *testing.T, spec lspinstaller.NativeArtifactSpec, name, version, url, digest, format, binary, launcher string) {
	t.Helper()
	if spec.Name != name || spec.Version != version {
		t.Fatalf("artifact identity = %#v, want fixed official manifest", spec)
	}
	assertLinuxJavaKotlinArtifactSource(t, spec, url, digest)
	assertLinuxJavaKotlinArtifactLayout(t, spec, format, binary, launcher)
}

// assertLinuxJavaKotlinArtifactSource 校验工件下载源。
func assertLinuxJavaKotlinArtifactSource(t *testing.T, spec lspinstaller.NativeArtifactSpec, url, digest string) {
	t.Helper()
	if spec.URL != url || spec.SHA256 != digest {
		t.Fatalf("artifact source = %#v, want fixed official manifest", spec)
	}
	if !strings.HasPrefix(spec.URL, "https://") {
		t.Fatalf("artifact URL is not HTTPS: %#v", spec)
	}
	if len(spec.SHA256) != sha256.Size*2 {
		t.Fatalf("artifact SHA256 length is not pinned: %#v", spec)
	}
	if strings.Trim(spec.SHA256, "0123456789abcdef") != "" {
		t.Fatalf("artifact SHA256 is not pinned: %#v", spec)
	}
}

// assertLinuxJavaKotlinArtifactLayout 校验归档与启动器布局。
func assertLinuxJavaKotlinArtifactLayout(t *testing.T, spec lspinstaller.NativeArtifactSpec, format, binary, launcher string) {
	t.Helper()
	if spec.Format != format || spec.BinaryPath != binary {
		t.Fatalf("artifact layout = %#v, want fixed official manifest", spec)
	}
	if spec.LauncherName != launcher {
		t.Fatalf("artifact launcher = %#v, want %q", spec, launcher)
	}
}

func TestLinuxJavaKotlinManagedInstallUsesJREWithoutSystemPATHAndReusesIt(t *testing.T) {
	artifacts := linuxJavaKotlinArtifactSpecs()
	archives := map[string][]byte{
		"jre":    buildLinuxNativeTestArchive(t, artifacts.javaRuntime, []byte("#!/bin/sh\necho 'openjdk 21 managed'\n")),
		"jdtls":  buildLinuxNativeTestArchive(t, artifacts.jdtls, []byte("#!/bin/sh\necho 'jdtls managed'\n")),
		"kotlin": buildLinuxNativeTestArchive(t, artifacts.kotlin, []byte("#!/bin/sh\necho 'kotlin managed'\n")),
	}
	requests := make(map[string]int, len(archives))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		key := strings.TrimPrefix(request.URL.Path, "/")
		archive, ok := archives[key]
		if !ok {
			http.NotFound(w, request)
			return
		}
		requests[key]++
		if _, err := io.Copy(w, strings.NewReader(string(archive))); err != nil {
			t.Errorf("write fake %s artifact: %v", key, err)
		}
	}))
	t.Cleanup(server.Close)
	artifacts.javaRuntime.URL, artifacts.javaRuntime.SHA256 = server.URL+"/jre", linuxTestSHA256(archives["jre"])
	artifacts.jdtls.URL, artifacts.jdtls.SHA256 = server.URL+"/jdtls", linuxTestSHA256(archives["jdtls"])
	artifacts.kotlin.URL, artifacts.kotlin.SHA256 = server.URL+"/kotlin", linuxTestSHA256(archives["kotlin"])

	root := filepath.Join(t.TempDir(), "managed-artifacts")
	nativeInstaller, err := lspinstaller.NewNativeArtifactInstaller(lspinstaller.NativeArtifactInstallerConfig{InstallRoot: root, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewNativeArtifactInstaller: %v", err)
	}
	provider := lspinstaller.NewProvider()
	if err := registerLinuxJavaKotlinInstallers(provider, nativeInstaller, root, artifacts); err != nil {
		t.Fatalf("registerLinuxJavaKotlinInstallers: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	ctx := lspinstaller.WithInstallCommandCapability(context.Background())

	assertLinuxJavaInstall(t, provider, ctx, root, artifacts)
	assertLinuxKotlinInstall(t, provider, ctx, root, artifacts)
	for _, key := range []string{"jre", "jdtls", "kotlin"} {
		if requests[key] != 1 {
			t.Fatalf("fake TLS request count for %s = %d, want one", key, requests[key])
		}
	}
	assertLinuxJavaKotlinCached(t, provider, ctx)
}

// assertLinuxJavaKotlinCached 校验第二次解析只复用受管路径。
func assertLinuxJavaKotlinCached(t *testing.T, provider *lspinstaller.Provider, ctx context.Context) {
	t.Helper()
	secondJava, err := provider.EnsureInstalledDetailed(ctx, "java")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(java) cached: %v", err)
	}
	secondKotlin, err := provider.EnsureInstalledDetailed(ctx, "kotlin")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(kotlin) cached: %v", err)
	}
	if secondJava.Status != lspinstaller.InstallStatusPathFound || secondKotlin.Status != lspinstaller.InstallStatusPathFound {
		t.Fatalf("cached results = %#v, %#v, want path_found", secondJava, secondKotlin)
	}
}

// assertLinuxJavaInstall 校验 Java 启动器固定受管 JRE。
func assertLinuxJavaInstall(t *testing.T, provider *lspinstaller.Provider, ctx context.Context, root string, artifacts linuxJavaKotlinArtifacts) {
	t.Helper()
	java, err := provider.EnsureInstalledDetailed(ctx, "java")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(java): %v", err)
	}
	want := filepath.Join(root, artifacts.jdtls.Name, artifacts.jdtls.Version, "launcher", artifacts.jdtls.LauncherName)
	if java.Path != want || java.Status != lspinstaller.InstallStatusInstalledPath || !filepath.IsAbs(java.Path) {
		t.Fatalf("java result = %#v, want managed absolute launcher %q", java, want)
	}
	launcher, err := os.ReadFile(java.Path)
	if err != nil {
		t.Fatalf("read Java launcher: %v", err)
	}
	wantPayload := filepath.Join(root, artifacts.javaRuntime.Name, artifacts.javaRuntime.Version, "payload")
	if !strings.Contains(string(launcher), "JAVA_HOME=") || !strings.Contains(string(launcher), wantPayload) {
		t.Fatalf("Java launcher does not pin managed JAVA_HOME: %s", launcher)
	}
}

// assertLinuxKotlinInstall 校验 Kotlin 启动器来自受管安装目录。
func assertLinuxKotlinInstall(t *testing.T, provider *lspinstaller.Provider, ctx context.Context, root string, artifacts linuxJavaKotlinArtifacts) {
	t.Helper()
	kotlin, err := provider.EnsureInstalledDetailed(ctx, "kotlin")
	if err != nil {
		t.Fatalf("EnsureInstalledDetailed(kotlin): %v", err)
	}
	want := filepath.Join(root, artifacts.kotlin.Name, artifacts.kotlin.Version, "launcher", artifacts.kotlin.LauncherName)
	if kotlin.Path != want || kotlin.Status != lspinstaller.InstallStatusInstalledPath || !filepath.IsAbs(kotlin.Path) {
		t.Fatalf("kotlin result = %#v, want managed absolute launcher %q", kotlin, want)
	}
}

func linuxTestSHA256(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
