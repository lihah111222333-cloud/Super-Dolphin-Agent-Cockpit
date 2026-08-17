//go:build !windows && e2e

package main

// 该 E2E 依赖 POSIX PATH 隔离，仅进入非 Windows 矩阵。

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMcpLSPBinaryRealTypeScriptLanguageServerUsesSixReadOnlyTools_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
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
		"paths":       []string{"src"},
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
	requireGroupedLocationTextTotal(t, definition, 1, "real typescript definition")
	requireToolResultContains(t, definition, filepath.Base(mathTarget), "real typescript definition")

	references := client.callTool(t, "xref", map[string]any{
		"action":              "references",
		"pos":                 mathTarget + ":1:17",
		"language_id":         "typescript",
		"include_declaration": false,
		"max_results":         10,
	})
	requireMCPToolSuccess(t, client, references, "real typescript references")
	requireGroupedLocationTextTotal(t, references, 2, "real typescript references")
	requireToolResultContains(t, references, filepath.Base(consumerTarget), "real typescript references")

	completion := client.callTool(t, "completion", map[string]any{
		"pos":         consumerTarget + ":6:9",
		"language_id": "typescript",
		"max_results": 10,
	})
	requireMCPToolSuccess(t, client, completion, "real typescript completion")
	if !stringSliceContains(completionLabelsFromContent(t, completion), "inc") {
		t.Fatalf("real typescript completion missing inc; text=%q stderr=%s",
			completion.Result.ContentText(), client.stderrString())
	}
}

// TestMcpLSPBinaryJavaScriptReactExportReferences_E2E 守护前端真实 JS 声明到 JSX 消费者的引用链。
func TestMcpLSPBinaryJavaScriptReactExportReferences_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
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
	componentTarget, pageTarget, testTarget := writeRealJavaScriptReactReferencesFixture(t, root)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{
		"PATH=" + path,
		"NPM_CONFIG_PREFIX=" + npmPrefix,
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	diagnostics := client.callTool(t, "file", map[string]any{
		"action":      "diagnostics",
		"file_path":   componentTarget,
		"language_id": "javascript",
	})
	requireMCPToolSuccess(t, client, diagnostics, "real javascript react file diagnostics")
	requireRealToolsInstalledBinaries(t, npmBin, []string{"typescript-language-server"})
	requireRealTypeScriptModule(t, npmPrefix)

	references := client.callTool(t, "xref", map[string]any{
		"action":              "references",
		"pos":                 componentTarget + ":1:17",
		"language_id":         "javascript",
		"include_declaration": false,
		"max_results":         10,
	})
	requireMCPToolSuccess(t, client, references, "real javascript react export references")
	requireGroupedLocationTextTotal(t, references, 4, "real javascript react export references")
	requireToolResultContains(t, references, filepath.Base(pageTarget), "real javascript react export references")
	requireToolResultContains(t, references, filepath.Base(testTarget), "real javascript react export references")
}

// TestMcpLSPBinaryFrontendLanguageExportReferences_E2E 覆盖其余三种前端语言 ID 的真实消费者引用。
func TestMcpLSPBinaryFrontendLanguageExportReferences_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	for _, tc := range []struct {
		name       string
		languageID string
		ext        string
	}{
		{name: "javascriptreact", languageID: "javascriptreact", ext: ".jsx"},
		{name: "typescript", languageID: "typescript", ext: ".ts"},
		{name: "typescriptreact", languageID: "typescriptreact", ext: ".tsx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runRealFrontendLanguageExportReferencesE2E(t, tc.languageID, tc.ext)
		})
	}
}

func runRealFrontendLanguageExportReferencesE2E(t *testing.T, languageID, ext string) {
	t.Helper()
	binary := buildMcpLSPBinaryForTest(t)
	root := t.TempDir()
	npmPrefix := t.TempDir()
	npmBin := filepath.Join(npmPrefix, "bin")
	if err := os.MkdirAll(npmBin, 0o755); err != nil {
		t.Fatalf("mkdir npm prefix bin: %v", err)
	}
	toolBin := symlinkHostToolsForRealToolsE2E(t, "node", "npm")
	path := npmBin + string(os.PathListSeparator) + toolBin + string(os.PathListSeparator) + "/usr/bin:/bin"
	componentTarget, pageTarget, testTarget := writeRealFrontendLanguageReferencesFixture(t, root, ext)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{
		"PATH=" + path,
		"NPM_CONFIG_PREFIX=" + npmPrefix,
	})
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	diagnostics := client.callTool(t, "file", map[string]any{
		"action":      "diagnostics",
		"file_path":   componentTarget,
		"language_id": languageID,
	})
	requireMCPToolSuccess(t, client, diagnostics, "real frontend language diagnostics")
	requireRealToolsInstalledBinaries(t, npmBin, []string{"typescript-language-server"})
	requireRealTypeScriptModule(t, npmPrefix)

	references := client.callTool(t, "xref", map[string]any{
		"action":              "references",
		"pos":                 componentTarget + ":1:17",
		"language_id":         languageID,
		"include_declaration": false,
		"max_results":         10,
	})
	requireMCPToolSuccess(t, client, references, "real frontend language export references")
	requireGroupedLocationTextTotal(t, references, 4, "real frontend language export references")
	requireToolResultContains(t, references, filepath.Base(pageTarget), "real frontend language export references")
	requireToolResultContains(t, references, filepath.Base(testTarget), "real frontend language export references")
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

func writeRealJavaScriptReactReferencesFixture(t *testing.T, root string) (string, string, string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"real-jsx-references-e2e","private":true}`+"\n"), 0o600); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	jsconfig := `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "allowJs": true,
    "checkJs": false,
    "jsx": "react-jsx",
    "noEmit": true
  },
  "include": ["src/**/*.js", "src/**/*.jsx"]
}
`
	if err := os.WriteFile(filepath.Join(root, "jsconfig.json"), []byte(jsconfig), 0o600); err != nil {
		t.Fatalf("write jsconfig.json: %v", err)
	}
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	componentTarget := filepath.Join(src, "ChatActionFeedback.js")
	if err := os.WriteFile(componentTarget, []byte("export function ChatActionFeedback() { return null; }\n"), 0o600); err != nil {
		t.Fatalf("write ChatActionFeedback.js: %v", err)
	}
	pageTarget := filepath.Join(src, "ChatPage.jsx")
	pageSource := "import { ChatActionFeedback } from './ChatActionFeedback.js';\n\nexport function ChatPage() { return <ChatActionFeedback />; }\n"
	if err := os.WriteFile(pageTarget, []byte(pageSource), 0o600); err != nil {
		t.Fatalf("write ChatPage.jsx: %v", err)
	}
	testTarget := filepath.Join(src, "ChatActionFeedback.test.jsx")
	testSource := "import { ChatActionFeedback } from './ChatActionFeedback.js';\n\nexport const fixture = <ChatActionFeedback />;\n"
	if err := os.WriteFile(testTarget, []byte(testSource), 0o600); err != nil {
		t.Fatalf("write ChatActionFeedback.test.jsx: %v", err)
	}
	return componentTarget, pageTarget, testTarget
}

func writeRealFrontendLanguageReferencesFixture(t *testing.T, root, ext string) (string, string, string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"real-frontend-language-references-e2e","private":true}`+"\n"), 0o600); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	config := `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "Bundler",
    "allowJs": true,
    "checkJs": false,
    "jsx": "react-jsx",
    "noEmit": true
  },
  "include": ["src/**/*"]
}
`
	configName := map[string]string{
		".jsx": "jsconfig.json",
		".ts":  "tsconfig.json",
		".tsx": "tsconfig.json",
	}[ext]
	if err := os.WriteFile(filepath.Join(root, configName), []byte(config), 0o600); err != nil {
		t.Fatalf("write %s: %v", configName, err)
	}
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	componentTarget := filepath.Join(src, "ChatActionFeedback"+ext)
	componentSource := map[string]string{
		".jsx": "export function ChatActionFeedback() { return <output />; }\n",
		".ts":  "export function ChatActionFeedback() { return null; }\n",
		".tsx": "export function ChatActionFeedback() { return <output />; }\n",
	}[ext]
	if err := os.WriteFile(componentTarget, []byte(componentSource), 0o600); err != nil {
		t.Fatalf("write component: %v", err)
	}
	pageTarget := filepath.Join(src, "ChatPage"+ext)
	pageSource := "import { ChatActionFeedback } from './ChatActionFeedback" + ext + "';\n\nexport function ChatPage() { return <ChatActionFeedback />; }\n"
	testSource := "import { ChatActionFeedback } from './ChatActionFeedback" + ext + "';\n\nexport const fixture = <ChatActionFeedback />;\n"
	if ext == ".ts" {
		pageSource = "import { ChatActionFeedback } from './ChatActionFeedback.ts';\n\nexport const feedback = ChatActionFeedback();\n"
		testSource = "import { ChatActionFeedback } from './ChatActionFeedback.ts';\n\nexport const fixture = ChatActionFeedback();\n"
	}
	if err := os.WriteFile(pageTarget, []byte(pageSource), 0o600); err != nil {
		t.Fatalf("write page: %v", err)
	}
	testTarget := filepath.Join(src, "ChatActionFeedback.test"+ext)
	if err := os.WriteFile(testTarget, []byte(testSource), 0o600); err != nil {
		t.Fatalf("write test: %v", err)
	}
	return componentTarget, pageTarget, testTarget
}
