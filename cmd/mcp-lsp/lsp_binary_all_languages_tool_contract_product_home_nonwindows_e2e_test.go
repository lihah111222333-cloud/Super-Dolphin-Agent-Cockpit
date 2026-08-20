//go:build !windows && e2e

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

type allLanguageToolContractBinaryPackage struct {
	binary   string
	bundle   string
	manifest string
	nodePath string
}

func allLanguageToolContractWorkDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func writeAllLanguageToolContractBundle(t *testing.T, fakeBinDir string) string {
	t.Helper()
	return writeFakeAllLanguagesProtocolBundle(t, fakeBinDir)
}

func allLanguageToolContractNodePath(t *testing.T, _ string, fakeBinDir string) string {
	t.Helper()
	return filepath.Join(fakeBinDir, allLanguageToolContractExecutableName("node"))
}

func prepareAllLanguageToolContractBinaryPackage(t *testing.T, binary, _ string, fakeBundle, fakeBinDir string) allLanguageToolContractBinaryPackage {
	t.Helper()
	return allLanguageToolContractBinaryPackage{
		binary:   binary,
		bundle:   fakeBundle,
		manifest: filepath.Join(fakeBundle, "manifest.json"),
		nodePath: allLanguageToolContractNodePath(t, fakeBundle, fakeBinDir),
	}
}

func startAllLanguageToolContractBinaryForTest(t *testing.T, ctx context.Context, binary, root, fakeBinDir string, extraEnv []string) *mcpLSPBinaryClient {
	t.Helper()
	return startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeBinDir, extraEnv)
}

// prepareAllLanguageToolContractProductHome 保持非 Windows fake 合同原有的 0700 私有目录策略。
func prepareAllLanguageToolContractProductHome(t *testing.T, root string) string {
	t.Helper()
	productHome := filepath.Join(root, ".super-dolphin")
	if err := os.MkdirAll(productHome, 0o700); err != nil {
		t.Fatalf("create isolated all-language product home: %v", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(productHome, 0o700); err != nil {
		t.Fatalf("restrict isolated all-language product home: %v", err)
	}
	return productHome
}
