package installer

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRuffFormatterAssetsCoverSupportedArchitectures(t *testing.T) {
	cases := []struct{ goos, goarch, format, suffix string }{
		{"windows", "arm64", NativeArtifactFormatZip, "aarch64-pc-windows-msvc.zip"},
		{"windows", "amd64", NativeArtifactFormatZip, "x86_64-pc-windows-msvc.zip"},
		{"windows", "386", NativeArtifactFormatZip, "i686-pc-windows-msvc.zip"},
		{"linux", "arm64", NativeArtifactFormatTarGz, "aarch64-unknown-linux-gnu.tar.gz"},
		{"linux", "amd64", NativeArtifactFormatTarGz, "x86_64-unknown-linux-gnu.tar.gz"},
		{"linux", "386", NativeArtifactFormatTarGz, "i686-unknown-linux-gnu.tar.gz"},
		{"darwin", "arm64", NativeArtifactFormatTarGz, "aarch64-apple-darwin.tar.gz"},
		{"darwin", "amd64", NativeArtifactFormatTarGz, "x86_64-apple-darwin.tar.gz"},
	}
	for _, tc := range cases {
		t.Run(tc.goos+"/"+tc.goarch, func(t *testing.T) {
			asset, err := RuffFormatterAssetFor(tc.goos, tc.goarch)
			if err != nil {
				t.Fatal(err)
			}
			if asset.Format != tc.format || asset.URL[len(asset.URL)-len(tc.suffix):] != tc.suffix {
				t.Fatalf("asset = %#v", asset)
			}
			if len(asset.SHA256) != 64 {
				t.Fatalf("SHA256 = %q", asset.SHA256)
			}
		})
	}
}

func TestRuffFormatterRejectsUnsupportedArchitecture(t *testing.T) {
	if _, err := RuffFormatterAssetFor("freebsd", "amd64"); err == nil {
		t.Fatal("freebsd/amd64 unexpectedly supported")
	}
}

func TestResolveOrInstallRuffFormatterReusesCompleteCacheWithoutDownload(t *testing.T) {
	root := t.TempDir()
	asset, err := RuffFormatterAssetFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("RuffFormatterAssetFor(): %v", err)
	}
	finalDir := filepath.Join(root, "cache", "lsp-formatters", "ruff", RuffFormatterVersion)
	binaryPath := filepath.Join(finalDir, "payload", filepath.FromSlash(asset.BinaryPath))
	launcherPath := filepath.Join(finalDir, "launcher", "ruff")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatalf("create cached Ruff binary directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(launcherPath), 0o700); err != nil {
		t.Fatalf("create cached Ruff launcher directory: %v", err)
	}
	if err := os.WriteFile(binaryPath, []byte("cached Ruff"), 0o700); err != nil {
		t.Fatalf("write cached Ruff binary: %v", err)
	}
	launcher := "#!/bin/sh\nexec " + shellQuote(binaryPath) + " \"$@\"\n"
	if err := os.WriteFile(launcherPath, []byte(launcher), 0o700); err != nil {
		t.Fatalf("write cached Ruff launcher: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := ResolveOrInstallRuffFormatter(ctx, root)
	if err != nil {
		t.Fatalf("ResolveOrInstallRuffFormatter() cached path: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(binaryPath) {
		t.Fatalf("ResolveOrInstallRuffFormatter() = %q, want cached binary %q", got, binaryPath)
	}
}
