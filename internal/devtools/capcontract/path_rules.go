package capcontract

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const defaultCapabilityRootsSource = "scripts/capcontract/main.go"

// PathRules 保存由 capability generator 源码动态读取的默认扫描根目录。
type PathRules struct {
	DefaultRoots []string
}

type capabilityPathRule struct {
	Kind string
	Path string
}

// LoadPathRules 从 generator 的 defaultCapabilityRoots AST 读取唯一的扫描根目录事实源。
func LoadPathRules(repoRoot string) (PathRules, error) {
	repoRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return PathRules{}, fmt.Errorf("resolve repository root: %w", err)
	}
	sourcePath := filepath.Join(repoRoot, filepath.FromSlash(defaultCapabilityRootsSource))
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return PathRules{}, fmt.Errorf("read default capability roots source %s: %w", sourcePath, err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), sourcePath, source, parser.AllErrors)
	if err != nil {
		return PathRules{}, fmt.Errorf("parse default capability roots source %s: %w", sourcePath, err)
	}
	roots, err := defaultCapabilityRootsFromAST(parsed)
	if err != nil {
		return PathRules{}, fmt.Errorf("read default capability roots from %s: %w", sourcePath, err)
	}

	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		if err := validateRepositoryPath(root); err != nil {
			return PathRules{}, fmt.Errorf("default capability root %q must be a normalized repository-relative path: %w", root, err)
		}
		if _, ok := seen[root]; ok {
			return PathRules{}, fmt.Errorf("duplicate default capability root %q", root)
		}
		seen[root] = struct{}{}
		info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(root)))
		if err != nil {
			return PathRules{}, fmt.Errorf("default capability root does not exist %q: %w", root, err)
		}
		if !info.IsDir() {
			return PathRules{}, fmt.Errorf("default capability root is not a directory %q", root)
		}
	}
	return PathRules{DefaultRoots: append([]string(nil), roots...)}, nil
}

// FindRepoRoot 从 start 向上查找包含 go.mod 和 CLAUDE.md 的仓库根目录。
func FindRepoRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve repository root start: %w", err)
	}
	for {
		if regularFile(filepath.Join(dir, "go.mod")) && regularFile(filepath.Join(dir, "CLAUDE.md")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repository root from %s", start)
		}
		dir = parent
	}
}

// Match 判断仓库相对路径是否会影响 capability contract；非法输入直接报错。
func (rules PathRules) Match(candidate string) (bool, error) {
	if err := validateRepositoryPath(candidate); err != nil {
		return false, fmt.Errorf("invalid capability-contract candidate path %q: %w", candidate, err)
	}
	pathRules, err := rules.allRules()
	if err != nil {
		return false, err
	}
	for _, rule := range pathRules {
		switch rule.Kind {
		case "exact":
			if candidate == rule.Path {
				return true, nil
			}
		case "tree":
			if candidate == rule.Path || strings.HasPrefix(candidate, rule.Path+"/") {
				return true, nil
			}
		default:
			return false, fmt.Errorf("unsupported capability-contract path rule kind %q", rule.Kind)
		}
	}
	return false, nil
}

// MachineLines 生成供 shell gate 使用的稳定 TSV；每行是 kind<TAB>path。
func (rules PathRules) MachineLines() ([]string, error) {
	pathRules, err := rules.allRules()
	if err != nil {
		return nil, err
	}
	lines := make([]string, 0, len(pathRules))
	for _, rule := range pathRules {
		lines = append(lines, rule.Kind+"\t"+rule.Path)
	}
	return lines, nil
}

// allRules 合并 AST 根目录与固定工具链路径，并拒绝空集、非法项和重复项。
func (rules PathRules) allRules() ([]capabilityPathRule, error) {
	if len(rules.DefaultRoots) == 0 {
		return nil, fmt.Errorf("default capability roots are empty")
	}
	pathRules := make([]capabilityPathRule, 0, len(rules.DefaultRoots)+4)
	seen := make(map[string]struct{}, len(rules.DefaultRoots)+4)
	appendRule := func(kind, rulePath string) error {
		if kind != "exact" && kind != "tree" {
			return fmt.Errorf("unsupported capability-contract path rule kind %q", kind)
		}
		if err := validateRepositoryPath(rulePath); err != nil {
			return fmt.Errorf("invalid capability-contract %s rule %q: %w", kind, rulePath, err)
		}
		key := kind + "\x00" + rulePath
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate capability-contract %s rule %q", kind, rulePath)
		}
		seen[key] = struct{}{}
		pathRules = append(pathRules, capabilityPathRule{Kind: kind, Path: rulePath})
		return nil
	}
	for _, root := range rules.DefaultRoots {
		if err := appendRule("tree", root); err != nil {
			return nil, err
		}
	}
	for _, rule := range []capabilityPathRule{
		{Kind: "tree", Path: "docs/doc/codemap/capability-contract"},
		{Kind: "tree", Path: "internal/devtools/capcontract"},
		{Kind: "exact", Path: "scripts/capcontract.go"},
		{Kind: "tree", Path: "scripts/capcontract"},
	} {
		if err := appendRule(rule.Kind, rule.Path); err != nil {
			return nil, err
		}
	}
	return pathRules, nil
}

// defaultCapabilityRootsFromAST 校验声明形状并解码默认根目录字面量。
func defaultCapabilityRootsFromAST(file *ast.File) ([]string, error) {
	declarations := defaultCapabilityRootDeclarations(file)
	if len(declarations) == 0 {
		return nil, fmt.Errorf("defaultCapabilityRoots declaration not found")
	}
	if len(declarations) > 1 {
		return nil, fmt.Errorf("multiple defaultCapabilityRoots declarations")
	}
	literal, err := defaultCapabilityRootsLiteral(declarations[0])
	if err != nil {
		return nil, err
	}
	return decodeDefaultCapabilityRoots(literal)
}

// defaultCapabilityRootDeclarations 定位所有同名 var 声明，供调用方检查唯一性。
func defaultCapabilityRootDeclarations(file *ast.File) []*ast.ValueSpec {
	var declarations []*ast.ValueSpec
	for _, declarationNode := range file.Decls {
		gen, ok := declarationNode.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, specNode := range gen.Specs {
			if spec, ok := specNode.(*ast.ValueSpec); ok && valueSpecHasName(spec, "defaultCapabilityRoots") {
				declarations = append(declarations, spec)
			}
		}
	}
	return declarations
}

// valueSpecHasName 判断单个 ValueSpec 是否声明指定变量名。
func valueSpecHasName(spec *ast.ValueSpec, want string) bool {
	for _, name := range spec.Names {
		if name.Name == want {
			return true
		}
	}
	return false
}

// defaultCapabilityRootsLiteral 验证默认根声明必须是唯一的 []string 复合字面量。
func defaultCapabilityRootsLiteral(declaration *ast.ValueSpec) (*ast.CompositeLit, error) {
	if len(declaration.Names) != 1 || len(declaration.Values) != 1 {
		return nil, fmt.Errorf("defaultCapabilityRoots must have exactly one literal value")
	}
	literal, ok := declaration.Values[0].(*ast.CompositeLit)
	if !ok {
		return nil, fmt.Errorf("defaultCapabilityRoots must be a []string composite literal")
	}
	arrayType, ok := literal.Type.(*ast.ArrayType)
	if !ok || arrayType.Len != nil {
		return nil, fmt.Errorf("defaultCapabilityRoots must be a []string composite literal")
	}
	elementType, ok := arrayType.Elt.(*ast.Ident)
	if !ok || elementType.Name != "string" {
		return nil, fmt.Errorf("defaultCapabilityRoots must be a []string composite literal")
	}
	if len(literal.Elts) == 0 {
		return nil, fmt.Errorf("defaultCapabilityRoots must not be empty")
	}
	return literal, nil
}

// decodeDefaultCapabilityRoots 解码根目录字符串，并拒绝任何非字符串元素。
func decodeDefaultCapabilityRoots(literal *ast.CompositeLit) ([]string, error) {
	roots := make([]string, 0, len(literal.Elts))
	for _, element := range literal.Elts {
		basic, ok := element.(*ast.BasicLit)
		if !ok || basic.Kind != token.STRING {
			return nil, fmt.Errorf("defaultCapabilityRoots must contain only string literals")
		}
		root, err := strconv.Unquote(basic.Value)
		if err != nil {
			return nil, fmt.Errorf("decode default capability root %s: %w", basic.Value, err)
		}
		roots = append(roots, root)
	}
	return roots, nil
}

// validateRepositoryPath 要求路径使用规范 slash 形式且不能逃逸仓库根目录。
func validateRepositoryPath(value string) error {
	if value == "" {
		return fmt.Errorf("path is empty")
	}
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("path has surrounding whitespace")
	}
	if strings.ContainsAny(value, "\\\t\r\n\x00") {
		return fmt.Errorf("path contains a separator or control character that is not portable")
	}
	if filepath.IsAbs(value) || path.IsAbs(value) {
		return fmt.Errorf("path is absolute")
	}
	if clean := path.Clean(value); clean != value || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("path is not normalized")
	}
	return nil
}

func regularFile(filePath string) bool {
	info, err := os.Stat(filePath)
	return err == nil && !info.IsDir()
}
