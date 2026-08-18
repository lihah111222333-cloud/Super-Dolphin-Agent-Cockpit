//go:build !windows && e2e

package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type fakeProtocolBundleLayout struct {
	binary       string
	bundleDir    string
	manifestPath string
	extraEnv     []string
}

func writeFakeProtocolBundle(t *testing.T, binary, fakeServersBinDir, serverName, languageID string) fakeProtocolBundleLayout {
	t.Helper()
	bundleDir := t.TempDir()
	bundleBinDir := filepath.Join(bundleDir, "bin")
	if err := os.MkdirAll(bundleBinDir, 0o755); err != nil {
		t.Fatalf("create fake %s protocol bundle bin dir: %v", languageID, err)
	}
	fakeServer, err := os.ReadFile(filepath.Join(fakeServersBinDir, serverName))
	if err != nil {
		t.Fatalf("read fake %s server: %v", serverName, err)
	}
	serverPath := filepath.Join(bundleBinDir, serverName)
	if err := os.WriteFile(serverPath, fakeServer, 0o700); err != nil {
		t.Fatalf("write fake %s protocol bundle server: %v", serverName, err)
	}
	digest := sha256.Sum256(fakeServer)
	manifest := fmt.Appendf(nil, "{\n  \"servers\": {\n    %q: {\"path\": %q, \"sha256\": %q, \"languages\": [%q]}\n  }\n}\n",
		serverName, filepath.ToSlash(filepath.Join("bin", serverName)), fmt.Sprintf("%x", digest[:]), languageID)
	if err := os.WriteFile(filepath.Join(bundleDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatalf("write fake %s protocol bundle manifest: %v", languageID, err)
	}
	return fakeProtocolBundleLayout{
		binary:       binary,
		bundleDir:    bundleDir,
		manifestPath: filepath.Join(bundleDir, "manifest.json"),
	}
}

func startFakeProtocolBundleClientForTest(t *testing.T, ctx context.Context, bundle fakeProtocolBundleLayout, root, fakeServersBinDir string, extraEnv []string, serverName string) *mcpLSPBinaryClient {
	t.Helper()
	return startMcpLSPBinaryForTestWithEnv(t, ctx, bundle.binary, root, fakeServersBinDir, extraEnv)
}
