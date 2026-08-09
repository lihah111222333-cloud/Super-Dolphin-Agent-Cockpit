package codemapindex

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// validateCodemapCounts 校验显式 codemap-count 声明。
func validateCodemapCounts(root string, doc SemanticMarkdown) []string {
	var problems []string
	for _, lineIndex := range narrativeLineIndexes(doc.Lines) {
		line := doc.Lines[lineIndex]
		matches := codemapCountRe.FindAllStringSubmatch(line, -1)
		if strings.Contains(line, "<!--") && strings.Contains(line, "codemap-count") && len(matches) == 0 {
			problems = append(problems, fmt.Sprintf("%s:%d malformed codemap-count declaration", doc.File, lineIndex+1))
			continue
		}
		for _, match := range matches {
			if problem := validateCodemapCount(root, doc.File, lineIndex+1, match); problem != "" {
				problems = append(problems, problem)
			}
		}
	}
	return problems
}

// validateCodemapCount 校验单条自动计数声明可被当前生成器解析。
func validateCodemapCount(root, codemapFile string, lineNumber int, match []string) string {
	countRoot, err := resolveRepoPath(root, match[1])
	if err != nil {
		return fmt.Sprintf("%s:%d invalid codemap count path %s: %v", codemapFile, lineNumber, match[1], err)
	}
	_, err = countCodemapFiles(countRoot, match[2])
	if err != nil {
		return fmt.Sprintf("%s:%d codemap count %s: %v", codemapFile, lineNumber, match[1], err)
	}
	return ""
}

// collectCodemapCounts 计算所有声明的当前值，并按 path/kind 去重、稳定排序。
func collectCodemapCounts(root string, mds []parsedMD) ([]CodemapCount, error) {
	countsByKey := make(map[string]CodemapCount)
	for _, doc := range semanticMarkdownDocs(mds) {
		for _, lineIndex := range narrativeLineIndexes(doc.Lines) {
			for _, match := range codemapCountRe.FindAllStringSubmatch(doc.Lines[lineIndex], -1) {
				countRoot, err := resolveRepoPath(root, match[1])
				if err != nil {
					return nil, fmt.Errorf("collect codemap count %s kind=%s: %w", match[1], match[2], err)
				}
				value, err := countCodemapFiles(countRoot, match[2])
				if err != nil {
					return nil, fmt.Errorf("collect codemap count %s kind=%s: %w", match[1], match[2], err)
				}
				key := match[1] + "\x00" + match[2]
				countsByKey[key] = CodemapCount{Path: match[1], Kind: match[2], Value: value}
			}
		}
	}
	keys := make([]string, 0, len(countsByKey))
	for key := range countsByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	counts := make([]CodemapCount, 0, len(keys))
	for _, key := range keys {
		counts = append(counts, countsByKey[key])
	}
	return counts, nil
}

// countCodemapFiles 计算显式支持的源码、SQL、一级 Go 目录或 Fx 子模块数量。
func countCodemapFiles(root, kind string) (int, error) {
	switch kind {
	case "go-files", "go-test-files":
		return countDirectGoFiles(root, kind == "go-test-files")
	case "go-child-dirs":
		return countDirectGoChildDirs(root)
	case "go-files-recursive", "go-test-files-recursive":
		return countRecursiveFiles(root, ".go", kind == "go-test-files-recursive")
	case "sql-files":
		return countRecursiveFiles(root, ".sql", false)
	case "fx-module-refs":
		return countFXModuleReferences(root)
	default:
		return 0, fmt.Errorf("unsupported kind %q", kind)
	}
}

// countRecursiveFiles 递归统计指定扩展名文件。
func countRecursiveFiles(root, extension string, wantGoTests bool) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != extension {
			return nil
		}
		if extension == ".go" && strings.HasSuffix(entry.Name(), "_test.go") != wantGoTests {
			return nil
		}
		count++
		return nil
	})
	return count, err
}

// countDirectGoFiles 统计目录直属的生产或测试 Go 文件。
func countDirectGoFiles(root string, wantTests bool) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		isTest := strings.HasSuffix(entry.Name(), "_test.go")
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && wantTests == isTest {
			count++
		}
	}
	return count, nil
}

// countDirectGoChildDirs 统计直接拥有 Go 文件的一级子目录。
func countDirectGoChildDirs(root string) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			hasGoFile, err := directoryHasDirectGoFile(filepath.Join(root, entry.Name()))
			if err != nil {
				return 0, err
			}
			if hasGoFile {
				count++
			}
		}
	}
	return count, nil
}

// directoryHasDirectGoFile 判断目录是否直接拥有 Go 源文件。
func directoryHasDirectGoFile(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") {
			return true, nil
		}
	}
	return false, nil
}

// countFXModuleReferences 统计 var Module 初始化式中除 fx.Module 外的子模块引用。
func countFXModuleReferences(path string) (int, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return 0, err
	}
	var moduleValue ast.Expr
	for _, declaration := range file.Decls {
		moduleValue = findModuleValue(declaration, moduleValue)
	}
	if moduleValue == nil {
		return 0, fmt.Errorf("var Module initializer not found")
	}
	count := 0
	ast.Inspect(moduleValue, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Module" && !isFXSelector(selector) {
			count++
		}
		return true
	})
	return count, nil
}

// findModuleValue 从单个声明中提取 var Module 的初始化表达式。
func findModuleValue(declaration ast.Decl, current ast.Expr) ast.Expr {
	general, ok := declaration.(*ast.GenDecl)
	if !ok || general.Tok != token.VAR {
		return current
	}
	for _, spec := range general.Specs {
		value, ok := moduleValueSpec(spec)
		if ok {
			return value
		}
	}
	return current
}

// moduleValueSpec 识别恰好带初始化值的 Module ValueSpec。
func moduleValueSpec(spec ast.Spec) (ast.Expr, bool) {
	valueSpec, ok := spec.(*ast.ValueSpec)
	if !ok || len(valueSpec.Names) != 1 || len(valueSpec.Values) != 1 {
		return nil, false
	}
	if valueSpec.Names[0].Name != "Module" {
		return nil, false
	}
	return valueSpec.Values[0], true
}

// isFXSelector 排除根构造器 fx.Module 本身。
func isFXSelector(selector *ast.SelectorExpr) bool {
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == "fx"
}
