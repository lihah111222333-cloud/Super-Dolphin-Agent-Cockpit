//go:build windows && e2e

package multilsp

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestTypeScriptNavigationFallbackResolvesPATHToolchain verifies that the
// fallback process uses the TypeScript installation paired with the
// typescript-language-server executable selected from the Windows PATH.
func TestTypeScriptNavigationFallbackResolvesPATHToolchain(t *testing.T) {
	requireWindowsTypeScriptNavigationExecutable(t, "node")
	requireWindowsTypeScriptNavigationExecutable(t, "typescript-language-server")
	t.Setenv("SUPER_DOLPHIN_LSP_BUNDLE_DIR", "")
	t.Setenv("NODE_PATH", "")

	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "src", "app.ts")
	content := "export function WindowsNavigationFallback(): number { return 42; }\n"
	writeGenericTestFile(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"strict":true}}`)
	writeGenericTestFile(t, target, content)

	tree, err := runTypeScriptNavigationTree(context.Background(), root, target)
	if err != nil {
		t.Fatalf("runTypeScriptNavigationTree with PATH toolchain: %v", err)
	}
	symbols := documentSymbolsFromTypeScriptNavigationTree(tree, content)
	requireSymbolNamesContain(t, collectDocumentSymbolNames(symbols), []string{"WindowsNavigationFallback"})
}

func requireWindowsTypeScriptNavigationExecutable(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Fatalf("required Windows E2E executable %s is unavailable: %v", name, err)
	}
}
