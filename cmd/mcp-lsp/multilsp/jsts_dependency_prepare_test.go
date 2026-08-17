package multilsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestJSTSEnsureClientRunsPnpmInstallWhenNodeModulesMissing(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	appRoot := filepath.Join(root, "frontend-app")
	writePnpmProject(t, root, appRoot)
	logPath := installFakePnpm(t, "")
	factory := &recordingClientFactory{}
	mgr := NewManager(Config{
		WorkspaceRoot:    root,
		ClientFactory:    factory,
		LanguageAdapters: NewDefaultLanguageAdapterRegistry(),
	}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	_, err := mgr.EnsureClient(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), filepath.Join(appRoot, "src", "app.tsx"), "typescriptreact")
	if err != nil {
		t.Fatalf("EnsureClient() error = %v", err)
	}
	if !testDirExists(filepath.Join(root, "node_modules")) {
		t.Fatalf("EnsureClient() did not create node_modules via pnpm install")
	}
	log := readPnpmLog(t, logPath)
	if !strings.Contains(log, root) || !strings.Contains(log, "install --frozen-lockfile --ignore-scripts") {
		t.Fatalf("pnpm log = %q, want install --frozen-lockfile --ignore-scripts in %s", log, root)
	}
}

func TestJSTSEnsureClientReturnsPnpmInstallFailure(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	appRoot := filepath.Join(root, "frontend-app")
	writePnpmProject(t, root, appRoot)
	installFakePnpm(t, "42")
	mgr := NewManager(Config{
		WorkspaceRoot:    root,
		ClientFactory:    &recordingClientFactory{},
		LanguageAdapters: NewDefaultLanguageAdapterRegistry(),
	}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	_, err := mgr.EnsureClient(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), filepath.Join(appRoot, "src", "app.tsx"), "typescriptreact")
	if err == nil || !strings.Contains(err.Error(), "pnpm install --frozen-lockfile") {
		t.Fatalf("EnsureClient() error = %v, want pnpm install failure", err)
	}
	if testDirExists(filepath.Join(root, "node_modules")) {
		t.Fatalf("EnsureClient() created node_modules despite pnpm failure")
	}
}

func writePnpmProject(t *testing.T, root, appRoot string) {
	t.Helper()
	writeGenericTestFile(t, filepath.Join(root, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n")
	writeGenericTestFile(t, filepath.Join(appRoot, "package.json"), `{"name":"app","dependencies":{"react":"latest"}}`)
	writeGenericTestFile(t, filepath.Join(appRoot, "src", "app.tsx"), "import React from 'react'\nexport const App = () => React.createElement('div')\n")
}

func installFakePnpm(t *testing.T, exitCode string) string {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "pnpm.log")
	writeFakePnpmExecutable(t, binDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PNPM_LOG", logPath)
	t.Setenv("PNPM_EXIT", exitCode)
	return logPath
}

func writeFakePnpmExecutable(t *testing.T, binDir string) {
	t.Helper()
	writeFakePnpmExecutablePlatform(t, binDir)
}

func readPnpmLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pnpm log: %v", err)
	}
	return string(data)
}

func testDirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
