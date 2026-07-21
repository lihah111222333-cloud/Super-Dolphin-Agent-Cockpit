//go:build e2e

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestMcpLSPBinaryRealTypeScriptLanguageServerUsesSixReadOnlyTools_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX PATH isolation")
	}

	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	npmPrefix := t.TempDir()
	npmBin := filepath.Join(npmPrefix, "bin")
	if err := os.MkdirAll(npmBin, 0o755); err != nil {
		t.Fatalf("mkdir npm prefix bin: %v", err)
	}
	toolBin := symlinkHostToolsForRealToolsE2E(t, "node", "npm")
	path := npmBin + string(os.PathListSeparator) + toolBin + string(os.PathListSeparator) + "/usr/bin:/bin"
	mathTarget, consumerTarget := writeRealTypeScriptToolsFixture(t, root)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{
		"PATH=" + path,
		"NPM_CONFIG_PREFIX=" + npmPrefix,
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	fileDiagnostics := client.callTool(t, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": mathTarget,
	})
	requireMCPToolSuccess(t, client, fileDiagnostics, "real typescript file diagnostics")
	requireRealToolsInstalledBinaries(t, npmBin, []string{"typescript-language-server"})
	requireRealTypeScriptModule(t, npmPrefix)

	grep := client.callTool(t, "grep", map[string]any{
		"action":      "text_search",
		"query":       "realTsToolNeedle",
		"path":        "src",
		"glob":        "**/*.ts",
		"max_results": 10,
	})
	requireMCPToolSuccess(t, client, grep, "real typescript grep")
	requireToolResultContains(t, grep, "realTsToolNeedle", "real typescript grep")

	structure := client.callTool(t, "structure", map[string]any{
		"action":      "document_symbol",
		"file_path":   mathTarget,
		"language_id": "typescript",
		"max_results": 10,
	})
	requireMCPToolSuccess(t, client, structure, "real typescript document_symbol")
	requireToolResultContains(t, structure, "Counter", "real typescript document_symbol")

	definition := client.callTool(t, "inspect", map[string]any{
		"action":      "definition",
		"pos":         consumerTarget + ":3:23",
		"language_id": "typescript",
	})
	requireMCPToolSuccess(t, client, definition, "real typescript definition")
	requireGroupedLocationTotal(t, definition.Result.StructuredContent, 1, "real typescript definition")
	requireToolResultContains(t, definition, filepath.Base(mathTarget), "real typescript definition")

	references := client.callTool(t, "xref", map[string]any{
		"action":              "references",
		"pos":                 mathTarget + ":1:17",
		"language_id":         "typescript",
		"include_declaration": false,
		"max_results":         10,
	})
	requireMCPToolSuccess(t, client, references, "real typescript references")
	requireGroupedLocationTotal(t, references.Result.StructuredContent, 2, "real typescript references")
	requireToolResultContains(t, references, filepath.Base(consumerTarget), "real typescript references")

	completion := client.callTool(t, "completion", map[string]any{
		"pos":         consumerTarget + ":6:9",
		"language_id": "typescript",
		"max_results": 10,
	})
	requireMCPToolSuccess(t, client, completion, "real typescript completion")
	if !stringSliceContains(completionLabelsFromStructuredContent(t, completion.Result.StructuredContent), "inc") {
		t.Fatalf("real typescript completion missing inc; structured=%s text=%q stderr=%s",
			completion.Result.StructuredContent, completion.Result.ContentText(), client.stderrString())
	}
}

func requireRealTypeScriptModule(t *testing.T, npmPrefix string) {
	t.Helper()
	tsserverPath := filepath.Join(npmPrefix, "lib", "node_modules", "typescript", "lib", "tsserver.js")
	if info, err := os.Stat(tsserverPath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("real TypeScript module missing tsserver entry %s: %v", tsserverPath, err)
	}
}

func symlinkHostToolsForRealToolsE2E(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("host tool %s is required for real e2e: %v", name, err)
		}
		link := filepath.Join(dir, name)
		if err := os.Symlink(path, link); err != nil {
			t.Fatalf("symlink %s -> %s: %v", link, path, err)
		}
	}
	return dir
}

func requireRealToolsInstalledBinaries(t *testing.T, binDir string, names []string) {
	t.Helper()
	for _, name := range names {
		path := filepath.Join(binDir, mcpLSPExecutableFileName(name))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("real installed binary %s missing at %s: %v", name, path, err)
		}
		if info.IsDir() || info.Mode()&0111 == 0 {
			t.Fatalf("real installed binary %s at %s is not executable: mode=%s", name, path, info.Mode())
		}
	}
}

func writeRealTypeScriptToolsFixture(t *testing.T, root string) (string, string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"real-tools-e2e","private":true}`+"\n"), 0o600); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	tsconfig := `{
  "compilerOptions": {
    "target": "ES2020",
    "module": "ESNext",
    "moduleResolution": "Node",
    "strict": true,
    "noEmit": true
  },
  "include": ["src/**/*.ts"]
}
`
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(tsconfig), 0o600); err != nil {
		t.Fatalf("write tsconfig.json: %v", err)
	}
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	mathTarget := filepath.Join(src, "math.ts")
	mathSource := `export function add(a: number, b: number): number {
  return a + b;
}

export class Counter {
  value = 0;

  inc(): number {
    this.value = add(this.value, 1);
    return this.value;
  }
}

export const toolNeedle = "realTsToolNeedle";
`
	if err := os.WriteFile(mathTarget, []byte(mathSource), 0o600); err != nil {
		t.Fatalf("write math.ts: %v", err)
	}
	consumerTarget := filepath.Join(src, "consumer.ts")
	consumerSource := `import { add, Counter, toolNeedle } from "./math";

export const answer = add(20, 22);

const counter = new Counter();
counter.

console.log(answer, toolNeedle);
`
	if err := os.WriteFile(consumerTarget, []byte(consumerSource), 0o600); err != nil {
		t.Fatalf("write consumer.ts: %v", err)
	}
	return mathTarget, consumerTarget
}
