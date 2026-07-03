package multilsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

func TestRunTypeScriptNavigationTreeUsesProjectTypeScript(t *testing.T) {
	requireNodeForNavigationTest(t)
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions":{"strict":true}}`)
	writeGenericTestFile(t, target, "export function actualSymbol() { return 1; }\n")
	writeFakeTypeScriptModule(t, root)

	tree, err := runTypeScriptNavigationTree(context.Background(), root, target)
	if err != nil {
		t.Fatalf("runTypeScriptNavigationTree: %v", err)
	}
	symbols := documentSymbolsFromTypeScriptNavigationTree(tree, "export function actualSymbol() { return 1; }\n")
	requireSymbolNamesContain(t, collectDocumentSymbolNames(symbols), []string{"FromFakeTypeScript"})
}

func TestRunTypeScriptNavigationTreeFailsWhenTypeScriptMissing(t *testing.T) {
	requireNodeForNavigationTest(t)
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1;\n")
	t.Setenv("SUPER_DOLPHIN_LSP_BUNDLE_DIR", "")
	t.Setenv("NODE_PATH", "")

	_, err := runTypeScriptNavigationTree(context.Background(), root, target)
	if err == nil {
		t.Fatal("runTypeScriptNavigationTree error = nil, want missing typescript failure")
	}
	if !strings.Contains(err.Error(), "Cannot find module 'typescript'") {
		t.Fatalf("runTypeScriptNavigationTree error = %v, want missing typescript detail", err)
	}
}

func TestDocumentSymbolsFromTypeScriptNavigationTreeCoversSpansKindsAndUTF16(t *testing.T) {
	content := "const rocket🚀Name = 1;\nclass Service {}\n"
	tree := typeScriptNavigationTree{
		Text:  "<global>",
		Kind:  "script",
		Spans: []typeScriptTextSpan{{Start: 0, Length: utf16Len(content)}},
		ChildItems: []typeScriptNavigationTree{
			{
				Text:     "IgnoredAlias",
				Kind:     "alias",
				Spans:    []typeScriptTextSpan{{Start: 0, Length: 5}},
				NameSpan: &typeScriptTextSpan{Start: 0, Length: len("IgnoredAlias")},
			},
			{
				Text:     "RocketConstant",
				Kind:     "const",
				Spans:    []typeScriptTextSpan{{Start: 0, Length: 5}, {Start: 24, Length: 16}},
				NameSpan: &typeScriptTextSpan{Start: 6, Length: 12},
				ChildItems: []typeScriptNavigationTree{{
					Text:     "AliasType",
					Kind:     "type",
					Spans:    []typeScriptTextSpan{{Start: 0, Length: 5}},
					NameSpan: &typeScriptTextSpan{Start: 6, Length: 6},
				}},
			},
			{
				Text:     "Service",
				Kind:     "class",
				Spans:    []typeScriptTextSpan{{Start: 24, Length: 16}},
				NameSpan: &typeScriptTextSpan{Start: 30, Length: 7},
				ChildItems: []typeScriptNavigationTree{{
					Text:     "build",
					Kind:     "method",
					Spans:    []typeScriptTextSpan{{Start: 24, Length: 16}},
					NameSpan: &typeScriptTextSpan{Start: 30, Length: 7},
				}},
			},
		},
	}

	symbols := documentSymbolsFromTypeScriptNavigationTree(tree, content)
	names := collectDocumentSymbolNames(symbols)
	requireSymbolNamesContain(t, names, []string{"RocketConstant", "AliasType", "Service", "build"})
	requireSymbolNamesNotContain(t, names, []string{"IgnoredAlias"})
	if symbols[0].Kind != protocol.SymbolKindConstant {
		t.Fatalf("RocketConstant kind = %d, want constant", symbols[0].Kind)
	}
	if symbols[0].Range.End.Line != 1 || symbols[0].Range.End.Character != 16 {
		t.Fatalf("RocketConstant range end = %#v, want line 1 char 16", symbols[0].Range.End)
	}
	if symbols[0].SelectionRange.Start.Character != 6 || symbols[0].SelectionRange.End.Character != 18 {
		t.Fatalf("RocketConstant selection = %#v, want UTF-16 chars 6..18", symbols[0].SelectionRange)
	}
	if symbols[0].Children[0].Kind != protocol.SymbolKindStruct {
		t.Fatalf("AliasType kind = %d, want struct", symbols[0].Children[0].Kind)
	}
	if symbols[1].Kind != protocol.SymbolKindClass || symbols[1].Children[0].Kind != protocol.SymbolKindMethod {
		t.Fatalf("Service symbols = %#v, want class with method child", symbols[1])
	}
}

func requireNodeForNavigationTest(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node is required for TypeScript navigation helper tests: %v", err)
	}
}

func writeFakeTypeScriptModule(t *testing.T, root string) {
	t.Helper()
	moduleDir := filepath.Join(root, "node_modules", "typescript")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatalf("mkdir fake typescript module: %v", err)
	}
	body := `
const fs = require("fs");
const path = require("path");

const sys = {
  fileExists: fs.existsSync,
  readFile: (file) => fs.readFileSync(file, "utf8"),
  readDirectory: () => [],
  directoryExists: (dir) => fs.existsSync(dir) && fs.statSync(dir).isDirectory(),
  getDirectories: () => [],
  realpath: (value) => fs.realpathSync(value),
  useCaseSensitiveFileNames: true
};

module.exports = {
  sys,
  JsxEmit: { ReactJSX: 4 },
  ScriptTarget: { Latest: 99 },
  ModuleKind: { ESNext: 99 },
  ScriptSnapshot: { fromString: (text) => ({ text }) },
  createDocumentRegistry: () => ({}),
  getDefaultLibFilePath: () => path.join(__dirname, "lib.d.ts"),
  flattenDiagnosticMessageText: (message) => String(message),
  readConfigFile: (file, readFile) => ({ config: JSON.parse(readFile(file)) }),
  parseJsonConfigFileContent: (config) => ({ errors: [], options: { fakeConfigLoaded: !!config.compilerOptions }, fileNames: [] }),
  createLanguageService: (host) => ({
    getNavigationTree: (file) => {
      const settings = host.getCompilationSettings();
      if (!settings.fakeConfigLoaded) {
        throw new Error("tsconfig settings were not loaded");
      }
      const text = host.getScriptSnapshot(file).text;
      const start = text.indexOf("actualSymbol");
      return {
        text: "<global>",
        kind: "script",
        spans: [{ start: 0, length: text.length }],
        childItems: [{
          text: "FromFakeTypeScript",
          kind: "function",
          spans: [{ start: 0, length: text.length }],
          nameSpan: { start, length: "actualSymbol".length }
        }]
      };
    }
  })
};
`
	if err := os.WriteFile(filepath.Join(moduleDir, "index.js"), []byte(body), 0o644); err != nil {
		t.Fatalf("write fake typescript module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "lib.d.ts"), []byte(""), 0o644); err != nil {
		t.Fatalf("write fake lib.d.ts: %v", err)
	}
}

func utf16Len(content string) int {
	total := 0
	for _, r := range content {
		total += typeScriptUTF16Width(r)
	}
	return total
}
