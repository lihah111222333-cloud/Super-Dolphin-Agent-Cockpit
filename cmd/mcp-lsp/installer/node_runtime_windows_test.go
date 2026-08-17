//go:build windows

package installer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsNodeRuntimeManifestHasExactNativeAssets(t *testing.T) {
	facts := WindowsNodeRuntimeAssetFacts()
	manifest := WindowsNodeRuntimeManifest()
	if err := manifest.Validate(); err != nil {
		t.Fatalf("WindowsNodeRuntimeManifest().Validate(): %v", err)
	}
	for _, architecture := range []string{WindowsHostArchARM64, WindowsHostArchX64, WindowsHostArchX86} {
		fact, ok := facts["windows-"+architecture]
		if !ok {
			t.Fatalf("missing Node asset fact for %s", architecture)
		}
		asset, ok := manifest.Assets[architecture]
		if !ok {
			t.Fatalf("missing Node manifest asset for %s", architecture)
		}
		if asset.Version != WindowsNodeRuntimeVersion || asset.Architecture != architecture {
			t.Fatalf("Node %s asset identity = %#v", architecture, asset)
		}
		if !strings.HasPrefix(asset.URL, "https://nodejs.org/dist/v"+WindowsNodeRuntimeVersion+"/") {
			t.Fatalf("Node %s URL = %q, want official locked URL", architecture, asset.URL)
		}
		if asset.SHA256 != fact.SHA256 || asset.BinaryPath != fact.NodePath {
			t.Fatalf("Node %s manifest/fact mismatch: asset=%#v fact=%#v", architecture, asset, fact)
		}
	}
}

func TestWindowsNodeRuntimeAssetSelectionRequiresNativeWindowsIdentity(t *testing.T) {
	cases := []WindowsHostPlatform{
		{OS: "linux", NativeArch: WindowsHostArchX64, WindowsVersion: "10.0", WindowsBuild: 26100},
		{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX64, WindowsVersion: "6.1", WindowsBuild: 7601},
		{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX64, WindowsVersion: "10.0", WindowsBuild: 19040},
	}
	for index, platform := range cases {
		if _, err := WindowsNodeRuntimeAssetForPlatform(platform); err == nil {
			t.Fatalf("case %d selected a Node asset for invalid platform %#v", index, platform)
		}
	}
	for _, architecture := range []string{WindowsHostArchARM64, WindowsHostArchX64, WindowsHostArchX86} {
		asset, err := WindowsNodeRuntimeAssetForPlatform(WindowsHostPlatform{
			OS:             WindowsHostOSWindows,
			NativeArch:     architecture,
			WindowsVersion: "10.0",
			WindowsBuild:   26100,
		})
		if err != nil {
			t.Fatalf("select exact Node asset for %s: %v", architecture, err)
		}
		if asset.Architecture != architecture {
			t.Fatalf("selected Node architecture = %q, want native %q", asset.Architecture, architecture)
		}
	}
	asset, err := WindowsNodeRuntimeAssetForPlatform(WindowsHostPlatform{
		OS:             WindowsHostOSWindows,
		NativeArch:     WindowsHostArchX64,
		ProcessArch:    WindowsHostArchX86,
		WindowsVersion: "10.0",
		WindowsBuild:   26100,
	})
	if err != nil || asset.Architecture != WindowsHostArchX64 {
		t.Fatalf("WOW64 Node selection = %#v, %v; want native x64 asset", asset, err)
	}
}

func TestWindowsNodeRuntimeInstallUsesPrefixOSLock(t *testing.T) {
	prefix := t.TempDir()
	lockPath := windowsNodeRuntimeInstallLockPath(prefix)
	if filepath.Dir(lockPath) != prefix || filepath.Base(lockPath) != ".windows-node-install.lock" {
		t.Fatalf("Windows Node install lock path = %q, want lock inside prefix", lockPath)
	}
	if _, err := os.Stat(prefix); err != nil {
		t.Fatalf("stat Node npm prefix: %v", err)
	}
	first, err := acquireAssetOSLock(context.Background(), lockPath)
	if err != nil {
		t.Fatalf("acquire first Windows Node prefix lock: %v", err)
	}
	defer first.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()
	if _, err := acquireAssetOSLock(ctx, lockPath); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second Windows Node prefix lock error = %v, want context deadline", err)
	}
}

func TestWindowsNodeRuntimeNPMFailureSummaryRedactsPackagesAndPaths(t *testing.T) {
	secretOutput := "secret-npm-output-token"
	secretPackage := "@private/secret-package@9.9.9"
	npmDir := filepath.Join(t.TempDir(), "user-private")
	if err := os.MkdirAll(npmDir, 0o700); err != nil {
		t.Fatal(err)
	}
	npmPath := filepath.Join(npmDir, "npm.cmd")
	if err := os.WriteFile(npmPath, []byte("@echo off\r\necho "+secretOutput+"\r\nexit /b 23\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(t.TempDir(), "user-private", "node-prefix")
	if err := os.MkdirAll(prefix, 0o700); err != nil {
		t.Fatal(err)
	}
	err := installWindowsNodeRuntimePackages(context.Background(), npmPath, prefix, []string{secretPackage})
	if err == nil {
		t.Fatal("npm install returned nil error")
	}
	var summary *ProcessFailureError
	if !errors.As(err, &summary) {
		t.Fatalf("error = %T, want *ProcessFailureError: %v", err, err)
	}
	if !summary.ExitCodePresent || summary.ExitCode != 23 || summary.PackageCount != 1 || summary.ArgsCount != 5 || summary.OutputBytes == 0 || summary.OutputSHA256 == "" {
		t.Fatalf("npm process summary = %+v", summary)
	}
	if got := err.Error(); strings.Contains(got, secretOutput) || strings.Contains(got, secretPackage) || strings.Contains(got, npmPath) || strings.Contains(got, prefix) {
		t.Fatalf("npm failure leaked process data: %q", got)
	}
	receipt, marshalErr := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: err.Error()})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if got := string(receipt); strings.Contains(got, secretOutput) || strings.Contains(got, secretPackage) || strings.Contains(got, prefix) {
		t.Fatalf("npm receipt leaked process data: %q", got)
	}
}

func TestWindowsNodeRuntimeNPMInstallUsesShortPathsBeyondMAXPATH(t *testing.T) {
	npmDir := t.TempDir()
	prefix := t.TempDir()
	for range 8 {
		npmDir = filepath.Join(npmDir, "windows-node-runtime-long-component")
		prefix = filepath.Join(prefix, "windows-node-prefix-long-component")
	}
	if err := os.MkdirAll(npmDir, 0o700); err != nil {
		t.Fatalf("create deep npm command directory: %v", err)
	}
	if err := os.MkdirAll(prefix, 0o700); err != nil {
		t.Fatalf("create deep npm prefix directory: %v", err)
	}
	npmPath := filepath.Join(npmDir, "npm.cmd")
	if err := os.WriteFile(npmPath, []byte("@echo off\r\nexit /b 0\r\n"), 0o600); err != nil {
		t.Fatalf("write deep npm command fixture: %v", err)
	}
	if len(npmPath) < 260 || len(prefix) < 260 {
		t.Fatalf("deep npm fixture lengths command=%d prefix=%d, want both at least 260", len(npmPath), len(prefix))
	}
	if err := installWindowsNodeRuntimePackages(context.Background(), npmPath, prefix, []string{"proof-package@1.0.0"}); err != nil {
		t.Fatalf("install through short Windows process paths: %v", err)
	}
}
