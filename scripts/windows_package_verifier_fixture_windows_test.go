//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type windowsVerifierFixture struct {
	root   string
	helper string
}

func TestWindowsPackageVerifierAcceptsRealFixture(t *testing.T) {
	fixture := newWindowsVerifierFixture(t)
	verifier := filepath.Join(scriptRepoRoot(t), "scripts", "verify_packaged_app_windows.ps1")
	powershell := windowsVerifierPowerShell(t)
	cmd := exec.Command(powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", verifier, fixture.root)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("verify real Windows package fixture: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Windows package verified:") {
		t.Fatalf("Windows verifier output missing success evidence:\n%s", output)
	}
}

func newWindowsVerifierFixture(t *testing.T) windowsVerifierFixture {
	t.Helper()
	stage := filepath.Join(t.TempDir(), "super-dolphin-0.1.0-windows-"+runtime.GOARCH)
	fixture := windowsVerifierFixture{root: stage, helper: buildWindowsVerifierHelper(t)}
	fixture.writeNativeFiles(t)
	fixture.writePackageFiles(t)
	fixture.writeLSPManifest(t)
	fixture.writeRuntimeManifests(t)
	return fixture
}

func buildWindowsVerifierHelper(t *testing.T) string {
	t.Helper()
	sourceDir := filepath.Join(t.TempDir(), "fixture-helper")
	windowsVerifierWriteFile(t, filepath.Join(sourceDir, "go.mod"), "module fixturehelper\n\ngo 1.24\n", 0o644)
	source := `package main
import (
    "fmt"
    "os"
    "path/filepath"
    "strings"
)
func main() {
    if strings.EqualFold(filepath.Base(os.Args[0]), "node.exe") {
        fmt.Println("v22.5.0")
    }
}
`
	windowsVerifierWriteFile(t, filepath.Join(sourceDir, "main.go"), source, 0o644)
	output := filepath.Join(sourceDir, "fixture-helper.exe")
	cmd := exec.Command("go", "build", "-o", output, ".")
	cmd.Dir = sourceDir
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Windows verifier fixture helper: %v\n%s", err, combined)
	}
	return output
}

func (fixture windowsVerifierFixture) writeNativeFiles(t *testing.T) {
	t.Helper()
	paths := []string{
		"bin/agent-terminal.exe", "bin/mcp-orch.exe", "bin/mcp-lsp.exe",
		"bin/mcp-schema-compiler-helper.exe", "bin/mcp-ida.exe", "bin/codex.exe",
		"bin/ffmpeg.exe", "bin/gopls.exe", "lsp/node/node.exe", "lsp/bin/gopls.exe",
		"lsp/bin/rust-analyzer.exe", "lsp/bin/sqruff.exe", "lsp/bin/sg.exe",
		"lsp/bin/shellcheck.exe", "lsp/bin/ast-grep.exe", "lsp/bin/vcruntime140.dll",
	}
	for _, rel := range paths {
		windowsVerifierCopyFile(t, fixture.helper, filepath.Join(fixture.root, filepath.FromSlash(rel)))
	}
}

func (fixture windowsVerifierFixture) writePackageFiles(t *testing.T) {
	t.Helper()
	windowsVerifierWriteFile(t, filepath.Join(fixture.root, ".env"), "# verifier fixture\n", 0o644)
	windowsVerifierWriteFile(t, filepath.Join(fixture.root, "models.yaml"), "models: []\n", 0o644)
	windowsVerifierWriteFile(t, filepath.Join(fixture.root, "run.cmd"), "@exit /b 0\r\n", 0o644)
	windowsVerifierWriteFile(t, filepath.Join(fixture.root, "run.ps1"), "exit 0\r\n", 0o644)
	helperManifest := filepath.Join(fixture.root, filepath.FromSlash("bin/mcp-schema-compiler-helper.exe.manifest.json"))
	windowsVerifierWriteFile(t, helperManifest, "{}\n", 0o644)
	migration := filepath.Join(fixture.root, filepath.FromSlash("internal/platform/db/sqlite/migrations/001_fixture.sql"))
	windowsVerifierWriteFile(t, migration, "SELECT 1;\n", 0o644)
	for _, name := range []string{"typescript-language-server.cmd", "vscode-css-language-server.cmd", "pyright-langserver.cmd", "bash-language-server.cmd", "go.cmd", "python.cmd", "python3.cmd"} {
		windowsVerifierWriteFile(t, filepath.Join(fixture.root, "lsp", "bin", name), "@exit /b 0\r\n", 0o644)
	}
}

func (fixture windowsVerifierFixture) writeRuntimeManifests(t *testing.T) {
	t.Helper()
	runtimeManifest := map[string]string{
		"bundled_codex_path": "bin/codex.exe", "bundled_gopls_path": "bin/gopls.exe",
		"lsp_bundle_path": "lsp", "lsp_manifest_path": "lsp/lsp-manifest.json", "model_registry_path": "models.yaml",
	}
	windowsVerifierWriteJSON(t, filepath.Join(fixture.root, "runtime-manifest.json"), runtimeManifest)
	codexSHA := windowsVerifierSHA256(t, filepath.Join(fixture.root, "bin", "codex.exe"))
	codexManifest := map[string]any{"codex": map[string]string{
		"path": "bin/codex.exe", "source_sha256": codexSHA, "package_sha256": codexSHA,
	}}
	windowsVerifierWriteJSON(t, filepath.Join(fixture.root, "codex-manifest.json"), codexManifest)
}

func (fixture windowsVerifierFixture) writeLSPManifest(t *testing.T) {
	t.Helper()
	paths := map[string]string{
		"gopls": "bin/gopls.exe", "typescript-language-server": "bin/typescript-language-server.cmd",
		"vscode-langservers-extracted": "bin/vscode-css-language-server.cmd", "pyright": "bin/pyright-langserver.cmd",
		"rust-analyzer": "bin/rust-analyzer.exe", "bash-language-server": "bin/bash-language-server.cmd",
		"sqruff": "bin/sqruff.exe", "sg": "bin/sg.exe", "go": "bin/go.cmd", "shellcheck": "bin/shellcheck.exe",
	}
	servers := make(map[string]any, len(paths))
	for id, rel := range paths {
		fullRel := filepath.ToSlash(filepath.Join("lsp", rel))
		servers[id] = map[string]any{
			"path": fullRel, "version": "fixture", "languages": []string{"fixture"},
			"sha256": windowsVerifierSHA256(t, filepath.Join(fixture.root, filepath.FromSlash(fullRel))),
		}
	}
	windowsVerifierWriteJSON(t, filepath.Join(fixture.root, "lsp", "lsp-manifest.json"), map[string]any{
		"profile": "minimal", "servers": servers,
	})
}

func windowsVerifierPowerShell(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Fatal("Windows package verifier requires pwsh.exe or powershell.exe")
	return ""
}

func windowsVerifierWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture JSON %s: %v", path, err)
	}
	windowsVerifierWriteFile(t, path, string(raw)+"\n", 0o644)
}

func windowsVerifierWriteFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("write fixture file %s: %v", path, err)
	}
}

func windowsVerifierCopyFile(t *testing.T, source, destination string) {
	t.Helper()
	body, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read fixture helper %s: %v", source, err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatalf("create fixture directory for %s: %v", destination, err)
	}
	if err := os.WriteFile(destination, body, 0o755); err != nil {
		t.Fatalf("copy fixture helper to %s: %v", destination, err)
	}
}

func windowsVerifierSHA256(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("hash fixture file %s: %v", path, err)
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
