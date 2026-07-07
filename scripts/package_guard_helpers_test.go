package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func assertPackageScriptRejectsWhitespaceOnlyPackagedCodexRelayEnv(t *testing.T, scriptPath, goos string) {
	t.Helper()

	fields := []struct {
		name string
		key  string
	}{
		{name: "base_url", key: "SUPER_DOLPHIN_CODEX_RELAY_BASE_URL"},
		{name: "bootstrap_token", key: "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN"},
		{name: "bootstrap_proof", key: "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF"},
	}
	values := []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "space", value: " "},
		{name: "tab", value: "\t"},
	}

	for _, field := range fields {
		for _, value := range values {
			t.Run(field.name+"/"+value.name, func(t *testing.T) {
				env := map[string]string{
					"SUPER_DOLPHIN_CODEX_RELAY_BASE_URL":        "https://relay.example.test",
					"SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN": "bootstrap-token",
					"SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF": "bootstrap-proof",
				}
				env[field.key] = value.value

				output, err := runPackagedRelayEnvResolver(t, scriptPath, goos, env)
				if err == nil {
					t.Fatalf("expected %s to reject %s=%q", scriptPath, field.key, value.value)
				}
				want := field.key + " is required and must not be whitespace-only"
				if !strings.Contains(output, want) {
					t.Fatalf("expected output to contain %q, got:\n%s", want, output)
				}
			})
		}
	}
}

func runPackagedRelayEnvResolver(t *testing.T, scriptPath, goos string, values map[string]string) (string, error) {
	t.Helper()

	script := readScript(t, scriptPath)
	harness := scriptPrefixThroughFunction(t, script, "resolve_packaged_relay_env") + "\nresolve_packaged_relay_env\n"
	harnessPath := filepath.Join(t.TempDir(), filepath.Base(scriptPath))
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatalf("write %s harness: %v", scriptPath, err)
	}

	cmd := exec.Command("bash", bashArg("", harnessPath))
	cmd.Dir = "."
	cmd.Env = packageScriptValidationEnv(t, goos, values)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func packageScriptValidationEnv(t *testing.T, goos string, values map[string]string) []string {
	t.Helper()
	blocked := map[string]bool{
		"GOOS":                               true,
		"GOARCH":                             true,
		"PATH":                               true,
		"SUPER_DOLPHIN_CODEX_RELAY_BASE_URL": true,
		"SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN": true,
		"SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF": true,
		"SUPER_DOLPHIN_CODEX_RELAY_API_KEY":         true,
	}
	env := make([]string, 0, len(os.Environ())+len(values)+2)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if blocked[key] {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "GOOS="+goos, "GOARCH=amd64")
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	env = append(env, "PATH="+bashArg("", writePackageFakeGoBin(t, goos, "amd64"))+":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	return appendWSLEnvKeysWithGitWorktree(t, env,
		"PATH",
		"GOOS",
		"GOARCH",
		"SUPER_DOLPHIN_CODEX_RELAY_BASE_URL",
		"SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN",
		"SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_PROOF",
		"SUPER_DOLPHIN_CODEX_RELAY_API_KEY",
	)
}

func writePackageFakeGoBin(t *testing.T, goos, goarch string) string {
	t.Helper()
	binDir := t.TempDir()
	content := `#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = "env" ]; then
  case "${2:-}" in
    GOOS) printf '%s\n' ` + bashQuote(goos) + `; exit 0 ;;
    GOARCH) printf '%s\n' ` + bashQuote(goarch) + `; exit 0 ;;
  esac
fi
echo "fake go only supports: go env GOOS|GOARCH" >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte(content), 0o700); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	return binDir
}

func scriptPrefixThroughFunction(t *testing.T, script, name string) string {
	t.Helper()
	script = strings.ReplaceAll(script, "\r\n", "\n")
	startMarker := name + "() {"
	start := strings.Index(script, startMarker)
	if start < 0 {
		t.Fatalf("script missing function %s", name)
	}
	rest := script[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("script function %s has no closing brace", name)
	}
	return script[:start+end+len("\n}\n")]
}

func functionBody(t *testing.T, script, name string) string {
	t.Helper()
	script = strings.ReplaceAll(script, "\r\n", "\n")
	startMarker := name + "() {"
	start := strings.Index(script, startMarker)
	if start < 0 {
		t.Fatalf("script missing function %s", name)
	}
	rest := script[start:]
	body, _, ok := strings.Cut(rest, "\n}\n")
	if !ok {
		t.Fatalf("script function %s has no closing brace", name)
	}
	return body
}

func assertScriptContains(t *testing.T, script, want string) {
	t.Helper()
	if !strings.Contains(script, want) {
		t.Fatalf("script missing %q", want)
	}
}

func assertScriptDoesNotContain(t *testing.T, script, unwanted string) {
	t.Helper()
	if strings.Contains(script, unwanted) {
		t.Fatalf("script still contains %q", unwanted)
	}
}

func assertScriptOrder(t *testing.T, script, first, second string) {
	t.Helper()
	firstIndex := strings.Index(script, first)
	if firstIndex < 0 {
		t.Fatalf("script missing %q", first)
	}
	secondIndex := strings.Index(script, second)
	if secondIndex < 0 {
		t.Fatalf("script missing %q", second)
	}
	if firstIndex >= secondIndex {
		t.Fatalf("script order wrong: %q must appear before %q", first, second)
	}
}

func assertScriptOrderAfter(t *testing.T, script, anchor, first, second string) {
	t.Helper()
	anchorIndex := strings.Index(script, anchor)
	if anchorIndex < 0 {
		t.Fatalf("script missing %q", anchor)
	}
	assertScriptOrder(t, script[anchorIndex:], first, second)
}

type testLSPServerFixture struct {
	id      string
	path    string
	version string
}

type testLSPManifest struct {
	SchemaVersion int                              `json:"schema_version"`
	BundlePath    string                           `json:"bundle_path"`
	Servers       map[string]testLSPManifestServer `json:"servers"`
}

type testLSPManifestServer struct {
	Path      string   `json:"path"`
	Version   string   `json:"version"`
	SHA256    string   `json:"sha256"`
	Languages []string `json:"languages"`
}

type testReorderedLSPManifest struct {
	Servers       map[string]testReorderedLSPManifestServer `json:"servers"`
	SchemaVersion int                                       `json:"schema_version"`
	BundlePath    string                                    `json:"bundle_path,omitempty"`
}

type testReorderedLSPManifestServer struct {
	Languages []string `json:"languages"`
	SHA256    string   `json:"sha256"`
	Version   string   `json:"version"`
	Path      string   `json:"path"`
}

func testLSPServerFixtures() []testLSPServerFixture {
	return []testLSPServerFixture{
		{id: "gopls", path: "bin/gopls", version: "gopls-test"},
		{id: "go", path: "bin/go", version: "go-test"},
		{id: "typescript-language-server", path: "bin/typescript-language-server", version: "typescript-language-server-test"},
		{id: "vscode-langservers-extracted", path: "bin/vscode-css-language-server", version: "vscode-langservers-extracted-test"},
		{id: "pyright", path: "bin/pyright-langserver", version: "pyright-test"},
		{id: "rust-analyzer", path: "bin/rust-analyzer", version: "rust-analyzer-test"},
		{id: "bash-language-server", path: "bin/bash-language-server", version: "bash-language-server-test"},
		{id: "sql-language-server", path: "bin/sql-language-server", version: "sql-language-server-test"},
		{id: "shellcheck", path: "bin/shellcheck", version: "shellcheck-test"},
		{id: "sg", path: "bin/sg", version: "sg-test"},
		{id: "jdtls", path: "bin/jdtls", version: "jdtls-test"},
	}
}

func testLSPServerLanguages() map[string][]string {
	return map[string][]string{
		"gopls":                        {"go", "gomod"},
		"go":                           {"go", "gomod"},
		"typescript-language-server":   {"javascript", "typescript"},
		"vscode-langservers-extracted": {"css", "html", "json"},
		"pyright":                      {"python"},
		"rust-analyzer":                {"rust"},
		"bash-language-server":         {"shellscript"},
		"sql-language-server":          {"sql"},
		"shellcheck":                   {"shellcheck"},
		"sg":                           {"ast-grep"},
		"jdtls":                        {"java"},
	}
}

func writePackageLSPStage(t *testing.T) string {
	t.Helper()

	bundleRoot := t.TempDir()
	for _, server := range testLSPServerFixtures() {
		writeExecutable(t, filepath.Join(bundleRoot, "lsp", server.path))
	}
	for _, shadow := range []string{"python", "python3", "go"} {
		writeExecutable(t, filepath.Join(bundleRoot, "lsp", "bin", shadow))
	}
	return bundleRoot
}

func writeMinifiedReorderedLSPManifest(t *testing.T, lspDir, pathPrefix string) {
	t.Helper()

	raw, err := json.Marshal(buildReorderedLSPManifest(t, lspDir, pathPrefix))
	if err != nil {
		t.Fatalf("marshal minified LSP manifest: %v", err)
	}
	writeFile(t, filepath.Join(lspDir, "lsp-manifest.json"), string(raw), 0o644)
}

func writePrettyLSPManifestWithLanguages(t *testing.T, lspDir, pathPrefix string) {
	t.Helper()

	raw, err := json.MarshalIndent(buildReorderedLSPManifest(t, lspDir, pathPrefix), "", "  ")
	if err != nil {
		t.Fatalf("marshal pretty LSP manifest: %v", err)
	}
	writeFile(t, filepath.Join(lspDir, "lsp-manifest.json"), string(raw)+"\n", 0o644)
}

func buildReorderedLSPManifest(t *testing.T, lspDir, pathPrefix string) testReorderedLSPManifest {
	t.Helper()

	servers := make(map[string]testReorderedLSPManifestServer)
	for _, server := range testLSPServerFixtures() {
		raw, err := os.ReadFile(filepath.Join(lspDir, server.path))
		if err != nil {
			t.Fatalf("read LSP server %s: %v", server.path, err)
		}
		digest := sha256.Sum256(raw)
		servers[server.id] = testReorderedLSPManifestServer{
			Languages: testLSPServerLanguages()[server.id],
			SHA256:    hex.EncodeToString(digest[:]),
			Version:   server.version,
			Path:      pathPrefix + server.path,
		}
	}
	return testReorderedLSPManifest{
		Servers:       servers,
		SchemaVersion: 1,
	}
}

func readLSPManifest(t *testing.T, path string) testLSPManifest {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read LSP manifest: %v", err)
	}
	var manifest testLSPManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("unmarshal LSP manifest: %v\n%s", err, raw)
	}
	return manifest
}

func assertPackagedLSPServerPathsAndDigests(t *testing.T, bundleRoot string, manifest testLSPManifest) {
	t.Helper()

	for _, server := range testLSPServerFixtures() {
		entry, ok := manifest.Servers[server.id]
		if !ok {
			if server.id == "jdtls" {
				continue
			}
			t.Fatalf("packaged LSP manifest missing server %s", server.id)
		}
		wantPath := "lsp/" + server.path
		if entry.Path != wantPath {
			t.Fatalf("packaged LSP server %s path = %q, want %q", server.id, entry.Path, wantPath)
		}
		if entry.Version != server.version {
			t.Fatalf("packaged LSP server %s version = %q, want %q", server.id, entry.Version, server.version)
		}
		raw, err := os.ReadFile(filepath.Join(bundleRoot, "lsp", server.path))
		if err != nil {
			t.Fatalf("read packaged LSP server %s: %v", server.id, err)
		}
		digest := sha256.Sum256(raw)
		wantSHA := hex.EncodeToString(digest[:])
		if entry.SHA256 != wantSHA {
			t.Fatalf("packaged LSP server %s sha256 = %q, want %q", server.id, entry.SHA256, wantSHA)
		}
	}
}

func assertLSPManifestLanguages(t *testing.T, manifest testLSPManifest) {
	t.Helper()

	for serverID, wantLanguages := range testLSPServerLanguages() {
		entry, ok := manifest.Servers[serverID]
		if !ok {
			if serverID == "jdtls" {
				continue
			}
			t.Fatalf("packaged LSP manifest missing server %s", serverID)
		}
		if len(entry.Languages) != len(wantLanguages) {
			t.Fatalf("packaged LSP manifest server %s languages = %v, want %v", serverID, entry.Languages, wantLanguages)
		}
		for i := range wantLanguages {
			if entry.Languages[i] != wantLanguages[i] {
				t.Fatalf("packaged LSP manifest server %s languages = %v, want %v", serverID, entry.Languages, wantLanguages)
			}
		}
	}
}

func runPackageWriteLSPManifest(t *testing.T, scriptPath, goos, bundleRoot string) (string, error) {
	t.Helper()

	script := readScript(t, scriptPath)
	endMarker := "\nwrite_codex_manifest() {"
	end := strings.Index(script, endMarker)
	if end < 0 {
		t.Fatalf("script missing function write_codex_manifest after write_lsp_manifest")
	}
	harness := script[:end] + "\nwrite_lsp_manifest \"$1\"\n"
	harnessPath := filepath.Join(t.TempDir(), filepath.Base(scriptPath))
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatalf("write %s harness: %v", scriptPath, err)
	}
	cmd := exec.Command("bash", bashArg("", harnessPath), bashArg("", bundleRoot))
	cmd.Dir = "."
	cmd.Env = packageScriptValidationEnv(t, goos, nil)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func writeMinimalPackagedMacOSApp(t *testing.T) string {
	t.Helper()

	app := filepath.Join(t.TempDir(), "Super Dolphin TestApp")
	macos := filepath.Join(app, "Contents", "MacOS")
	resources := filepath.Join(app, "Contents", "Resources")

	for _, path := range []string{
		filepath.Join(macos, "agent-terminal"),
		filepath.Join(resources, "bin", "mcp-orch"),
		filepath.Join(resources, "bin", "mcp-lsp"),
		filepath.Join(resources, "bin", "mcp-ida"),
		filepath.Join(resources, "bin", "super-dolphin-updater"),
		filepath.Join(resources, "bin", "codex"),
		filepath.Join(resources, "bin", "ffmpeg"),
		filepath.Join(resources, "bin", "gopls"),
		filepath.Join(resources, "bin", "typescript-language-server"),
		filepath.Join(resources, "bin", "vscode-css-language-server"),
		filepath.Join(resources, "bin", "pyright-langserver"),
		filepath.Join(resources, "bin", "rust-analyzer"),
		filepath.Join(resources, "bin", "bash-language-server"),
		filepath.Join(resources, "bin", "sql-language-server"),
		filepath.Join(resources, "bin", "shellcheck"),
		filepath.Join(resources, "bin", "jdtls"),
		filepath.Join(resources, "lsp", "bin", "gopls"),
		filepath.Join(resources, "lsp", "bin", "typescript-language-server"),
		filepath.Join(resources, "lsp", "bin", "vscode-css-language-server"),
		filepath.Join(resources, "lsp", "bin", "pyright-langserver"),
		filepath.Join(resources, "lsp", "bin", "rust-analyzer"),
		filepath.Join(resources, "lsp", "bin", "bash-language-server"),
		filepath.Join(resources, "lsp", "bin", "sql-language-server"),
		filepath.Join(resources, "lsp", "bin", "shellcheck"),
		filepath.Join(resources, "lsp", "bin", "sg"),
		filepath.Join(resources, "lsp", "bin", "jdtls"),
		filepath.Join(resources, "lsp", "bin", "python"),
		filepath.Join(resources, "lsp", "bin", "python3"),
		filepath.Join(resources, "lsp", "bin", "go"),
		filepath.Join(resources, "bin", "git"),
	} {
		writeExecutable(t, path)
	}
	pythonShadow := "#!/bin/sh\necho Packaged Super Dolphin does not bundle a Python interpreter e62\nexit 1\n"
	writeFile(t, filepath.Join(resources, "lsp", "bin", "python"), pythonShadow, 0o755)
	writeFile(t, filepath.Join(resources, "lsp", "bin", "python3"), pythonShadow, 0o755)
	writeFile(t, sqliteMigrationsPath(resources, "0001.sql"), "select 1;\n", 0o644)
	writeFile(t, filepath.Join(resources, "models.yaml"), "models: []\n", 0o644)
	writeCodexManifest(t, resources)
	writeLSPManifest(t, resources)
	return app
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	writeFile(t, path, "#!/bin/sh\nexit 0\n", 0o755)
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeCodexManifest(t *testing.T, resources string) {
	t.Helper()
	content := "#!/bin/sh\nexit 0\n"
	digest := sha256.Sum256([]byte(content))
	checksum := hex.EncodeToString(digest[:])
	writeFile(t, filepath.Join(resources, "codex-manifest.json"), `{
  "codex": {
    "path": "bin/codex",
    "version": "test",
    "source_sha256": "`+checksum+`",
    "package_sha256": "`+checksum+`"
  }
}
`, 0o644)
}

func writeLSPManifest(t *testing.T, resources string) {
	t.Helper()
	servers := []struct {
		id      string
		path    string
		version string
	}{
		{id: "gopls", path: "lsp/bin/gopls", version: "gopls-test"},
		{id: "go", path: "lsp/bin/go", version: "go-test"},
		{id: "typescript-language-server", path: "lsp/bin/typescript-language-server", version: "typescript-language-server-test"},
		{id: "vscode-langservers-extracted", path: "lsp/bin/vscode-css-language-server", version: "vscode-langservers-extracted-test"},
		{id: "pyright", path: "lsp/bin/pyright-langserver", version: "pyright-test"},
		{id: "rust-analyzer", path: "lsp/bin/rust-analyzer", version: "rust-analyzer-test"},
		{id: "bash-language-server", path: "lsp/bin/bash-language-server", version: "bash-language-server-test"},
		{id: "sql-language-server", path: "lsp/bin/sql-language-server", version: "sql-language-server-test"},
		{id: "shellcheck", path: "lsp/bin/shellcheck", version: "shellcheck-test"},
		{id: "sg", path: "lsp/bin/sg", version: "sg-test"},
		{id: "jdtls", path: "lsp/bin/jdtls", version: "jdtls-test"},
	}
	var builder strings.Builder
	builder.WriteString("{\n  \"schema_version\": 1,\n  \"servers\": {\n")
	for i, server := range servers {
		raw, err := os.ReadFile(filepath.Join(resources, server.path))
		if err != nil {
			t.Fatalf("read LSP server %s: %v", server.path, err)
		}
		digest := sha256.Sum256(raw)
		if i > 0 {
			builder.WriteString(",\n")
		}
		builder.WriteString("    \"")
		builder.WriteString(server.id)
		builder.WriteString("\": {\n")
		builder.WriteString("      \"path\": \"")
		builder.WriteString(server.path)
		builder.WriteString("\",\n")
		builder.WriteString("      \"version\": \"")
		builder.WriteString(server.version)
		builder.WriteString("\",\n")
		builder.WriteString("      \"sha256\": \"")
		builder.WriteString(hex.EncodeToString(digest[:]))
		builder.WriteString("\"\n    }")
	}
	builder.WriteString("\n  }\n}\n")
	writeFile(t, filepath.Join(resources, "lsp", "lsp-manifest.json"), builder.String(), 0o644)
}

func writeRuntimeManifest(t *testing.T, resources string, fields map[string]string) {
	t.Helper()
	keys := []string{"bundled_codex_path", "bundled_gopls_path", "lsp_bundle_path", "lsp_manifest_path", "model_registry_path"}
	var builder strings.Builder
	builder.WriteString("{\n")
	first := true
	for _, key := range keys {
		value, ok := fields[key]
		if !ok {
			continue
		}
		if !first {
			builder.WriteString(",\n")
		}
		first = false
		builder.WriteString("  \"")
		builder.WriteString(key)
		builder.WriteString("\": \"")
		builder.WriteString(value)
		builder.WriteString("\"")
	}
	builder.WriteString("\n}\n")
	writeFile(t, filepath.Join(resources, "runtime-manifest.json"), builder.String(), 0o644)
}

func runVerifyPackagedAppMacOS(t *testing.T, app string) (string, error) {
	t.Helper()
	return runVerifyPackagedAppMacOSWithEnv(t, app, nil)
}

func runVerifyPackagedAppMacOSWithEnv(t *testing.T, app string, env []string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "verify_packaged_app_macos.sh", bashArg("", app))
	cmd.Dir = "."
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
		cmd.Env = appendWSLEnvKeys(cmd.Env, "PATH")
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func readScript(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func shellSingleQuoted(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func writeMinimalPackagedLinuxStage(t *testing.T) string {
	t.Helper()

	stage := filepath.Join(t.TempDir(), "super-dolphin-0.1.0-linux-amd64")
	for _, path := range []string{
		filepath.Join(stage, "bin", "agent-terminal"),
		filepath.Join(stage, "bin", "mcp-orch"),
		filepath.Join(stage, "bin", "mcp-lsp"),
		filepath.Join(stage, "bin", "mcp-ida"),
		filepath.Join(stage, "bin", "codex"),
		filepath.Join(stage, "bin", "gopls"),
		filepath.Join(stage, "bin", "typescript-language-server"),
		filepath.Join(stage, "bin", "vscode-css-language-server"),
		filepath.Join(stage, "bin", "pyright-langserver"),
		filepath.Join(stage, "bin", "rust-analyzer"),
		filepath.Join(stage, "bin", "bash-language-server"),
		filepath.Join(stage, "bin", "sql-language-server"),
		filepath.Join(stage, "bin", "shellcheck"),
		filepath.Join(stage, "bin", "sg"),
		filepath.Join(stage, "bin", "jdtls"),
		filepath.Join(stage, "lsp", "bin", "gopls"),
		filepath.Join(stage, "lsp", "bin", "typescript-language-server"),
		filepath.Join(stage, "lsp", "bin", "vscode-css-language-server"),
		filepath.Join(stage, "lsp", "bin", "pyright-langserver"),
		filepath.Join(stage, "lsp", "bin", "rust-analyzer"),
		filepath.Join(stage, "lsp", "bin", "bash-language-server"),
		filepath.Join(stage, "lsp", "bin", "sql-language-server"),
		filepath.Join(stage, "lsp", "bin", "shellcheck"),
		filepath.Join(stage, "lsp", "bin", "sg"),
		filepath.Join(stage, "lsp", "bin", "jdtls"),
		filepath.Join(stage, "lsp", "bin", "python"),
		filepath.Join(stage, "lsp", "bin", "python3"),
		filepath.Join(stage, "lsp", "bin", "go"),
	} {
		writeExecutable(t, path)
	}
	pythonShadow := "#!/bin/sh\necho Packaged Super Dolphin does not bundle a Python interpreter >&2\nexit 1\n"
	writeFile(t, filepath.Join(stage, "lsp", "bin", "python"), pythonShadow, 0o755)
	writeFile(t, filepath.Join(stage, "lsp", "bin", "python3"), pythonShadow, 0o755)
	writeFile(t, sqliteMigrationsPath(stage, "0001.sql"), "select 1;\n", 0o644)
	writeFile(t, filepath.Join(stage, "models.yaml"), "models: []\n", 0o644)
	writeCodexManifest(t, stage)
	writeLSPManifest(t, stage)
	return stage
}

func runVerifyPackagedAppLinux(t *testing.T, target string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "verify_packaged_app_linux.sh", bashArg("", target))
	cmd.Dir = "."
	output, err := cmd.CombinedOutput()
	return string(output), err
}
