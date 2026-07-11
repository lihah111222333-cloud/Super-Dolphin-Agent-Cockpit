package multilsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

const typeScriptNavigationTreeScript = `
const fs = require("fs");
const path = require("path");

function fail(error) {
  const message = error && error.stack ? error.stack : String(error);
  process.stderr.write(message + "\n");
  process.exit(1);
}

function diagnosticMessage(ts, diagnostic) {
  return ts.flattenDiagnosticMessageText(diagnostic.messageText, "\n");
}

function isWithin(child, parent) {
  const relative = path.relative(parent, child);
  return relative === "" || (!relative.startsWith("..") && !path.isAbsolute(relative));
}

function findConfigFileWithin(ts, startDir, boundaryDir) {
  let current = path.resolve(startDir);
  const boundary = path.resolve(boundaryDir);
  while (isWithin(current, boundary)) {
    for (const name of ["tsconfig.json", "jsconfig.json"]) {
      const candidate = path.join(current, name);
      if (ts.sys.fileExists(candidate)) {
        return candidate;
      }
    }
    const parent = path.dirname(current);
    if (parent === current) {
      break;
    }
    current = parent;
  }
  return undefined;
}

try {
  const input = JSON.parse(fs.readFileSync(0, "utf8"));
  if (!input.filePath) {
    throw new Error("filePath is required");
  }
  const filePath = path.resolve(input.filePath);
  const projectRoot = path.resolve(input.projectRoot || path.dirname(filePath));
  const bundleDir = process.env.SUPER_DOLPHIN_LSP_BUNDLE_DIR || "";
  const requirePaths = [
    projectRoot,
    path.dirname(filePath),
    bundleDir
  ].filter(Boolean);
  const tsPath = require.resolve("typescript", { paths: requirePaths });
  const ts = require(tsPath);

  let options = {
    allowJs: true,
    jsx: ts.JsxEmit.ReactJSX,
    target: ts.ScriptTarget.Latest,
    module: ts.ModuleKind.ESNext
  };
  let rootNames = [filePath];
  const configPath = findConfigFileWithin(ts, path.dirname(filePath), projectRoot);
  if (configPath) {
    const configFile = ts.readConfigFile(configPath, ts.sys.readFile);
    if (configFile.error) {
      throw new Error("read tsconfig: " + diagnosticMessage(ts, configFile.error));
    }
    const parsed = ts.parseJsonConfigFileContent(configFile.config, ts.sys, path.dirname(configPath));
    if (parsed.errors && parsed.errors.length > 0) {
      throw new Error("parse tsconfig: " + diagnosticMessage(ts, parsed.errors[0]));
    }
    options = Object.assign({}, options, parsed.options);
  }

  const snapshots = new Map();
  const host = {
    getCompilationSettings: () => options,
    getScriptFileNames: () => rootNames,
    getScriptVersion: () => "0",
    getScriptSnapshot: (fileName) => {
      const absolute = path.resolve(fileName);
      if (!fs.existsSync(absolute)) {
        return undefined;
      }
      let text = snapshots.get(absolute);
      if (text === undefined) {
        text = fs.readFileSync(absolute, "utf8");
        snapshots.set(absolute, text);
      }
      return ts.ScriptSnapshot.fromString(text);
    },
    getCurrentDirectory: () => projectRoot,
    getDefaultLibFileName: (compilerOptions) => ts.getDefaultLibFilePath(compilerOptions),
    fileExists: ts.sys.fileExists,
    readFile: ts.sys.readFile,
    readDirectory: ts.sys.readDirectory,
    directoryExists: ts.sys.directoryExists,
    getDirectories: ts.sys.getDirectories,
    realpath: ts.sys.realpath,
    useCaseSensitiveFileNames: () => ts.sys.useCaseSensitiveFileNames
  };
  const service = ts.createLanguageService(host, ts.createDocumentRegistry());
  const tree = service.getNavigationTree(filePath);
  process.stdout.write(JSON.stringify(tree || null));
} catch (error) {
  fail(error);
}
`

type typeScriptNavigationInput struct {
	ProjectRoot string `json:"projectRoot"`
	FilePath    string `json:"filePath"`
}

type typeScriptNavigationTree struct {
	Text          string                     `json:"text"`
	Kind          string                     `json:"kind"`
	KindModifiers string                     `json:"kindModifiers"`
	Spans         []typeScriptTextSpan       `json:"spans"`
	NameSpan      *typeScriptTextSpan        `json:"nameSpan"`
	ChildItems    []typeScriptNavigationTree `json:"childItems"`
}

type typeScriptTextSpan struct {
	Start  int `json:"start"`
	Length int `json:"length"`
}

var typeScriptNavigationSymbolKinds = map[string]protocol.SymbolKind{
	"module":               protocol.SymbolKindModule,
	"external module name": protocol.SymbolKindModule,
	"class":                protocol.SymbolKindClass,
	"local class":          protocol.SymbolKindClass,
	"interface":            protocol.SymbolKindInterface,
	"enum":                 protocol.SymbolKindEnum,
	"enum member":          protocol.SymbolKindEnumMember,
	"const":                protocol.SymbolKindConstant,
	"let":                  protocol.SymbolKindVariable,
	"var":                  protocol.SymbolKindVariable,
	"local var":            protocol.SymbolKindVariable,
	"using":                protocol.SymbolKindVariable,
	"await using":          protocol.SymbolKindVariable,
	"function":             protocol.SymbolKindFunction,
	"local function":       protocol.SymbolKindFunction,
	"method":               protocol.SymbolKindMethod,
	"getter":               protocol.SymbolKindMethod,
	"setter":               protocol.SymbolKindMethod,
	"call signature":       protocol.SymbolKindMethod,
	"construct signature":  protocol.SymbolKindMethod,
	"index signature":      protocol.SymbolKindMethod,
	"property":             protocol.SymbolKindProperty,
	"accessor":             protocol.SymbolKindProperty,
	"jsx attribute":        protocol.SymbolKindProperty,
	"member variable":      protocol.SymbolKindProperty,
	"constructor":          protocol.SymbolKindConstructor,
	"type":                 protocol.SymbolKindStruct,
	"type parameter":       protocol.SymbolKindTypeParameter,
	"parameter":            protocol.SymbolKindVariable,
	"directory":            protocol.SymbolKindPackage,
	"string":               protocol.SymbolKindString,
}

// typeScriptNavigationDocumentSymbols 用 TypeScript 官方 LanguageService 生成大纲。
// 它只在 LSP 返回空结果后运行，失败会直接返回错误，避免重新落回手写猜测。
func (m *manager) typeScriptNavigationDocumentSymbols(ctx context.Context, ref documentRef) ([]protocol.DocumentSymbol, error) {
	content, err := os.ReadFile(ref.absPath)
	if err != nil {
		return nil, err
	}
	cfg, err := m.resolveWorkspaceForDocument(ctx, ref)
	if err != nil {
		return nil, err
	}
	projectRoot := firstNonEmpty(cfg.projectRoot, cfg.rootPath, filepath.Dir(ref.absPath))
	if projectRoot == "" {
		return nil, fmt.Errorf("typescript navigation fallback workspace root is empty for %s", ref.raw)
	}
	tree, err := runTypeScriptNavigationTree(ctx, projectRoot, ref.absPath)
	if err != nil {
		return nil, err
	}
	return documentSymbolsFromTypeScriptNavigationTree(tree, string(content)), nil
}

// runTypeScriptNavigationTree 启动 node 并让项目本地 TypeScript 解析文件导航树。
// stdout 只接收 JSON 结果；stderr 仅在失败时附带短摘要，避免污染 MCP stdio。
func runTypeScriptNavigationTree(ctx context.Context, projectRoot, filePath string) (typeScriptNavigationTree, error) {
	input, err := json.Marshal(typeScriptNavigationInput{ProjectRoot: projectRoot, FilePath: filePath})
	if err != nil {
		return typeScriptNavigationTree{}, fmt.Errorf("marshal typescript navigation input: %w", err)
	}
	cmd := hiddenexec.CommandContext(ctx, "node", "-e", typeScriptNavigationTreeScript)
	cmd.Dir = projectRoot
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return typeScriptNavigationTree{}, fmt.Errorf(
			"run typescript navigation tree: %w%s",
			err,
			commandOutputPreview(stderr.String()),
		)
	}
	if strings.TrimSpace(stdout.String()) == "" {
		return typeScriptNavigationTree{}, fmt.Errorf("run typescript navigation tree: empty stdout")
	}
	var tree *typeScriptNavigationTree
	if err := json.Unmarshal(stdout.Bytes(), &tree); err != nil {
		return typeScriptNavigationTree{}, fmt.Errorf(
			"decode typescript navigation tree: %w%s",
			err,
			commandOutputPreview(stdout.String()),
		)
	}
	if tree == nil {
		return typeScriptNavigationTree{}, nil
	}
	return *tree, nil
}

// documentSymbolsFromTypeScriptNavigationTree 把 TS NavigationTree 转成 LSP DocumentSymbol。
// TypeScript 会把 import 名称标为 alias；这些不是 documentSymbol 大纲项，因此跳过。
func documentSymbolsFromTypeScriptNavigationTree(tree typeScriptNavigationTree, content string) []protocol.DocumentSymbol {
	if len(tree.ChildItems) == 0 {
		if isTypeScriptNavigationRoot(tree) {
			return nil
		}
		return typeScriptNavigationSymbols([]typeScriptNavigationTree{tree}, content)
	}
	return typeScriptNavigationSymbols(tree.ChildItems, content)
}

func typeScriptNavigationSymbols(items []typeScriptNavigationTree, content string) []protocol.DocumentSymbol {
	symbols := make([]protocol.DocumentSymbol, 0, len(items))
	for _, item := range items {
		if !includeTypeScriptNavigationItem(item) {
			continue
		}
		symbol := protocol.DocumentSymbol{
			Name:           strings.TrimSpace(item.Text),
			Kind:           typeScriptNavigationSymbolKind(item.Kind),
			Range:          typeScriptTextSpanRange(content, typeScriptNavigationOuterSpan(item.Spans)),
			SelectionRange: typeScriptTextSpanRange(content, typeScriptNavigationSelectionSpan(item)),
			Children:       typeScriptNavigationSymbols(item.ChildItems, content),
		}
		symbols = append(symbols, symbol)
	}
	return symbols
}

func includeTypeScriptNavigationItem(item typeScriptNavigationTree) bool {
	if strings.TrimSpace(item.Text) == "" {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(item.Kind))
	return kind != "alias" && kind != "script"
}

func isTypeScriptNavigationRoot(tree typeScriptNavigationTree) bool {
	kind := strings.ToLower(strings.TrimSpace(tree.Kind))
	return kind == "script" || strings.TrimSpace(tree.Text) == "<global>"
}

func typeScriptNavigationOuterSpan(spans []typeScriptTextSpan) typeScriptTextSpan {
	if len(spans) == 0 {
		return typeScriptTextSpan{}
	}
	start := normalizeTypeScriptOffset(spans[0].Start)
	end := typeScriptSpanEnd(spans[0])
	for _, span := range spans[1:] {
		spanStart := normalizeTypeScriptOffset(span.Start)
		if spanStart < start {
			start = spanStart
		}
		if spanEnd := typeScriptSpanEnd(span); spanEnd > end {
			end = spanEnd
		}
	}
	return typeScriptTextSpan{Start: start, Length: maxInt(0, end-start)}
}

func typeScriptNavigationSelectionSpan(item typeScriptNavigationTree) typeScriptTextSpan {
	if item.NameSpan != nil {
		return *item.NameSpan
	}
	return typeScriptNavigationOuterSpan(item.Spans)
}

func typeScriptSpanEnd(span typeScriptTextSpan) int {
	return normalizeTypeScriptOffset(span.Start) + maxInt(0, span.Length)
}

func typeScriptTextSpanRange(content string, span typeScriptTextSpan) protocol.Range {
	start := normalizeTypeScriptOffset(span.Start)
	end := start + maxInt(0, span.Length)
	if end < start {
		end = start
	}
	return protocol.Range{
		Start: positionForTypeScriptOffset(content, start),
		End:   positionForTypeScriptOffset(content, end),
	}
}

func positionForTypeScriptOffset(content string, target int) protocol.Position {
	if target <= 0 {
		return protocol.Position{}
	}
	line, character, offset := 0, 0, 0
	for _, r := range content {
		if offset >= target {
			break
		}
		unitWidth := typeScriptUTF16Width(r)
		offset += unitWidth
		if r == '\n' {
			line++
			character = 0
			continue
		}
		character += unitWidth
	}
	return protocol.Position{Line: line, Character: character}
}

func typeScriptUTF16Width(r rune) int {
	if r > 0xFFFF {
		return 2
	}
	return 1
}

// typeScriptNavigationSymbolKind 将 TypeScript ScriptElementKind 映射成 LSP SymbolKind。
// 未识别的 kind 保持变量类型，避免因为新枚举值导致整个 document_symbol 失败。
func typeScriptNavigationSymbolKind(kind string) protocol.SymbolKind {
	if symbolKind, ok := typeScriptNavigationSymbolKinds[strings.ToLower(strings.TrimSpace(kind))]; ok {
		return symbolKind
	}
	return protocol.SymbolKindVariable
}

func normalizeTypeScriptOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func commandOutputPreview(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return ""
	}
	const maxPreviewBytes = 2048
	if len(trimmed) > maxPreviewBytes {
		trimmed = trimmed[:maxPreviewBytes] + "...(truncated)"
	}
	return ": " + trimmed
}
