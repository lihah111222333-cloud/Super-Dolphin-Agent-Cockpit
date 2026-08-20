//go:build windows

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
)

func TestRuntimeASTGrepEnsurerUsesVerifiedBundleCompanion(t *testing.T) {
	bundleDir := t.TempDir()
	companionPath := filepath.Join(bundleDir, "bin", "sg.exe")
	payload := []byte("verified bundle companion")
	if err := os.MkdirAll(filepath.Dir(companionPath), 0o700); err != nil {
		t.Fatalf("create ast-grep bundle directory: %v", err)
	}
	if err := os.WriteFile(companionPath, payload, 0o700); err != nil {
		t.Fatalf("write ast-grep bundle companion: %v", err)
	}
	digest := sha256.Sum256(payload)
	manifestPath := filepath.Join(bundleDir, "manifest.json")
	manifest := fmt.Sprintf(`{"servers":{"%s":{"path":"bin/sg.exe","version":"0.43.0","sha256":"%s","languages":["%s"]}}}`,
		runtimeASTGrepLanguageID, hex.EncodeToString(digest[:]), runtimeASTGrepLanguageID)
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write ast-grep bundle manifest: %v", err)
	}
	bundle, err := runtimeenv.LoadLSPBundle(bundleDir, manifestPath)
	if err != nil {
		t.Fatalf("load ast-grep bundle: %v", err)
	}
	ensure, err := runtimeASTGrepEnsurer(nil, bundle, true)
	if err != nil {
		t.Fatalf("create ast-grep bundle ensurer: %v", err)
	}
	got, err := ensure(context.Background())
	if err != nil {
		t.Fatalf("ensure ast-grep bundle companion: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(companionPath) {
		t.Fatalf("ast-grep bundle companion path = %q, want %q", got, companionPath)
	}
}

func TestRuntimeASTGrepEnsurerMissingBundleUsesInstallerWithoutPATH(t *testing.T) {
	root := t.TempDir()
	installedPath := filepath.Join(root, "cache", "ast-grep.exe")
	pathShimDir := t.TempDir()
	pathShim := filepath.Join(pathShimDir, "ast-grep.exe")
	if err := os.WriteFile(pathShim, []byte("PATH shim"), 0o700); err != nil {
		t.Fatalf("write PATH ast-grep shim: %v", err)
	}
	t.Setenv("PATH", pathShimDir)

	installCalls := 0
	provider := installer.NewProvider()
	provider.Register(runtimeASTGrepLanguageID, installer.InstallerConfig{
		BinaryName:          "ast-grep.exe",
		AllowInstallCommand: true,
		InstalledBinaryPathResolver: func(context.Context) (string, error) {
			return installedPath, nil
		},
		InstallAction: func(context.Context) (installer.InstallResult, error) {
			installCalls++
			if err := os.MkdirAll(filepath.Dir(installedPath), 0o700); err != nil {
				return installer.InstallResult{}, err
			}
			if err := os.WriteFile(installedPath, []byte("installed locked companion"), 0o700); err != nil {
				return installer.InstallResult{}, err
			}
			return installer.InstallResult{Path: installedPath}, nil
		},
	})
	ensure, err := runtimeASTGrepEnsurer(provider, runtimeenv.LSPBundle{}, false)
	if err != nil {
		t.Fatalf("create ast-grep installer ensurer: %v", err)
	}
	got, err := ensure(context.Background())
	if err != nil {
		t.Fatalf("ensure ast-grep through installer: %v", err)
	}
	if installCalls != 1 {
		t.Fatalf("ast-grep installer calls = %d, want 1", installCalls)
	}
	if filepath.Clean(got) != filepath.Clean(installedPath) {
		t.Fatalf("ast-grep installer path = %q, want %q (PATH shim %q must be ignored)", got, installedPath, pathShim)
	}
}
