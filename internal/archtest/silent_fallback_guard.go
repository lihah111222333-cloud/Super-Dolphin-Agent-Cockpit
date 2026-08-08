package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func silentFallbackReturnViolations(repoRoot string, scanRoots []string) []Violation {
	if !isSuperAgentV3Repo(repoRoot) {
		return nil
	}
	g := silentFallbackReturnGuard{
		repoRoot: repoRoot,
		skipDirs: DefaultSkipDirs(),
	}
	for _, root := range scanRoots {
		g.scanRoot(root)
	}
	return g.violations
}

type silentFallbackReturnGuard struct {
	repoRoot   string
	skipDirs   map[string]bool
	violations []Violation
}

// scanRoot 扫描根目录。
func (g *silentFallbackReturnGuard) scanRoot(root string) {
	absRoot := filepath.Join(g.repoRoot, root)
	if _, err := os.Stat(absRoot); err != nil {
		g.addScanViolation(filepath.ToSlash(root), fmt.Sprintf("silent fallback guard stat error: %v", err))
		return
	}
	if err := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			g.addScanViolation(g.relPath(path), fmt.Sprintf("silent fallback guard walk error: %v", walkErr))
			return walkErr
		}
		if d.IsDir() {
			if g.skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		g.scanFile(path)
		return nil
	}); err != nil {
		g.addScanViolation(filepath.ToSlash(root), fmt.Sprintf("silent fallback guard walk root error: %v", err))
	}
}

// scanFile 扫描文件。
func (g *silentFallbackReturnGuard) scanFile(path string) {
	rel, err := filepath.Rel(g.repoRoot, path)
	if err != nil {
		g.addScanViolation(filepath.ToSlash(path), fmt.Sprintf("silent fallback guard rel error: %v", err))
		return
	}
	rel = filepath.ToSlash(rel)
	data, err := os.ReadFile(path)
	if err != nil {
		g.addScanViolation(rel, fmt.Sprintf("silent fallback guard read error: %v", err))
		return
	}
	if IsGeneratedSQLCFile(rel, data) {
		return
	}
	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, path, data, parser.SkipObjectResolution)
	if err != nil {
		g.addScanViolation(rel, fmt.Sprintf("silent fallback guard parse error: %v", err))
		return
	}
	for _, decl := range fileNode.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		resultCount, returnsError := funcErrorResultCount(fn.Type)
		if !returnsError {
			resultCount = 0
		}
		g.scanBlock(fset, rel, fn.Body, resultCount)
	}
}

func (g *silentFallbackReturnGuard) relPath(path string) string {
	rel, err := filepath.Rel(g.repoRoot, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func (g *silentFallbackReturnGuard) addScanViolation(rel, message string) {
	g.violations = append(g.violations, Violation{Kind: ViolationFile, File: rel, Message: fmt.Sprintf("%s: %s", rel, message)})
}

func (g *silentFallbackReturnGuard) scanBlock(fset *token.FileSet, rel string, block *ast.BlockStmt, resultCount int) {
	ast.Inspect(block, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			if nestedCount, returnsError := funcErrorResultCount(node.Type); returnsError && node.Body != nil {
				g.scanBlock(fset, rel, node.Body, nestedCount)
			}
			return false
		case *ast.IfStmt:
			g.scanIfStmt(fset, rel, node, resultCount)
		}
		return true
	})
}

// funcErrorResultCount 返回函数结果数量，并标记最后一个返回值是否为 error。
// silent fallback guard 用它判断 return 语句末尾 nil 是否会吞掉原始错误。
func funcErrorResultCount(fn *ast.FuncType) (int, bool) {
	if fn == nil || fn.Results == nil || len(fn.Results.List) == 0 {
		return 0, false
	}
	count := 0
	for _, result := range fn.Results.List {
		if len(result.Names) == 0 {
			count++
			continue
		}
		count += len(result.Names)
	}
	last := fn.Results.List[len(fn.Results.List)-1]
	ident, ok := last.Type.(*ast.Ident)
	return count, ok && ident.Name == "error"
}

func (g *silentFallbackReturnGuard) scanIfStmt(fset *token.FileSet, rel string, stmt *ast.IfStmt, resultCount int) {
	errVars := errorVarsInCondition(stmt.Cond)
	if len(errVars) == 0 {
		return
	}
	for _, candidate := range fallbackReturnCandidates(stmt.Body, errVars, false) {
		if !isSilentFallbackReturn(candidate.stmt, resultCount, candidate.explicitAbsence) {
			continue
		}
		line := fset.Position(candidate.stmt.Pos()).Line
		g.violations = append(g.violations, Violation{
			Kind:    ViolationFile,
			File:    rel,
			Line:    line,
			Message: fmt.Sprintf("%s:%d silent fallback: error branch returns nil error; propagate or wrap the original error", rel, line),
		})
	}
}

func errorVarsInCondition(expr ast.Expr) map[string]struct{} {
	out := map[string]struct{}{}
	ast.Inspect(expr, func(n ast.Node) bool {
		binary, ok := n.(*ast.BinaryExpr)
		if !ok || binary.Op != token.NEQ {
			return true
		}
		if name, ok := errorIdentComparedWithNil(binary.X, binary.Y); ok {
			out[name] = struct{}{}
		}
		if name, ok := errorIdentComparedWithNil(binary.Y, binary.X); ok {
			out[name] = struct{}{}
		}
		return true
	})
	return out
}

func errorIdentComparedWithNil(candidate, other ast.Expr) (string, bool) {
	ident, ok := candidate.(*ast.Ident)
	if !ok || !looksLikeErrorIdent(ident.Name) || !isNilIdent(other) {
		return "", false
	}
	return ident.Name, true
}

// looksLikeErrorIdent 判断标识符命名是否像 error 变量。
// 它覆盖 err、err2、sampleErr、sampleError 等常见写法，降低漏扫 error 分支的概率。
func looksLikeErrorIdent(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "e" || lower == "err" {
		return true
	}
	if strings.HasSuffix(lower, "err") || strings.HasSuffix(lower, "error") {
		return true
	}
	return strings.HasPrefix(lower, "err") && hasOnlyDigits(lower[len("err"):])
}

func hasOnlyDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

type fallbackReturnCandidate struct {
	stmt            *ast.ReturnStmt
	explicitAbsence bool
}

// fallbackReturnCandidates 收集 error 分支内可能吞错的 return 语句。
// 嵌套 if 只有在仍引用同一组 error 变量时才继续下钻，避免把无关分支误报为兜底。
func fallbackReturnCandidates(block *ast.BlockStmt, errVars map[string]struct{}, explicitAbsence bool) []fallbackReturnCandidate {
	if block == nil {
		return nil
	}
	var out []fallbackReturnCandidate
	for _, stmt := range block.List {
		switch node := stmt.(type) {
		case *ast.ReturnStmt:
			out = append(out, fallbackReturnCandidate{stmt: node, explicitAbsence: explicitAbsence})
		case *ast.IfStmt:
			if !conditionMentionsAny(node.Cond, errVars) {
				continue
			}
			out = append(out, fallbackReturnCandidates(node.Body, errVars, explicitAbsence || isExplicitAbsenceCondition(node.Cond))...)
			out = append(out, fallbackReturnCandidates(elseBlock(node.Else), errVars, explicitAbsence)...)
		}
	}
	return out
}

func elseBlock(stmt ast.Stmt) *ast.BlockStmt {
	switch node := stmt.(type) {
	case *ast.BlockStmt:
		return node
	case *ast.IfStmt:
		return &ast.BlockStmt{List: []ast.Stmt{node}}
	default:
		return nil
	}
}

func conditionMentionsAny(expr ast.Expr, names map[string]struct{}) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if ok {
			_, found = names[ident.Name]
		}
		return !found
	})
	return found
}

// isExplicitAbsenceCondition 判断条件是否显式表达“对象不存在/进程已退出”。
// 这类分支允许返回 tri-state absence 信号，不能按普通吞错路径误报。
func isExplicitAbsenceCondition(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if found {
			return false
		}
		name := strings.ToLower(exprNameFromNode(n))
		found = strings.Contains(name, "notfound") ||
			strings.Contains(name, "notexist") ||
			strings.Contains(name, "norows") ||
			strings.Contains(name, "nosuchprocess") ||
			strings.Contains(name, "processgone") ||
			strings.Contains(name, "gone") ||
			strings.Contains(name, "exitcode")
		return true
	})
	return found
}

func exprNameFromNode(n ast.Node) string {
	expr, ok := n.(ast.Expr)
	if !ok {
		return ""
	}
	return exprName(expr)
}

// isSilentFallbackReturn 判断 return 是否在 error 分支中静默返回 nil error。
// 若前置返回值已经携带失败或明确 absence 信号，则不按吞错处理。
func isSilentFallbackReturn(ret *ast.ReturnStmt, resultCount int, explicitAbsence bool) bool {
	if resultCount <= 0 || len(ret.Results) == 0 {
		return false
	}
	if !isNilIdent(ret.Results[len(ret.Results)-1]) {
		return false
	}
	if resultCount == 1 {
		return true
	}
	results := ret.Results[:len(ret.Results)-1]
	if hasExplicitFailureSignal(results) {
		return false
	}
	return !hasExplicitTriStateAbsenceSignal(results, explicitAbsence)
}

func hasExplicitFailureSignal(exprs []ast.Expr) bool {
	for _, expr := range exprs {
		if isFailureComposite(expr) || isFailureCall(expr) {
			return true
		}
	}
	return false
}

// isFailureComposite 判断复合字面量是否显式表达失败状态。
// 失败类型名或失败字段可作为非静默兜底的信号。
func isFailureComposite(expr ast.Expr) bool {
	composite, ok := expr.(*ast.CompositeLit)
	if !ok {
		return false
	}
	typeName := strings.ToLower(exprName(composite.Type))
	if strings.Contains(typeName, "failure") || strings.Contains(typeName, "error") {
		return true
	}
	for _, elt := range composite.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if ok && isFailureKeyValue(kv) {
			return true
		}
	}
	return false
}

func isFailureKeyValue(kv *ast.KeyValueExpr) bool {
	key := strings.ToLower(exprName(kv.Key))
	switch key {
	case "error", "errorsummary", "failureclass":
		return true
	case "success", "ok":
		return isFalseIdent(kv.Value)
	case "status":
		return isFailedStatusExpr(kv.Value)
	default:
		return false
	}
}

func isFalseIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "false"
}

func isFailedStatusExpr(expr ast.Expr) bool {
	return strings.Contains(strings.ToLower(exprName(expr)), "failed")
}

func isFailureCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	name := strings.ToLower(exprName(call.Fun))
	return strings.Contains(name, "failure") ||
		strings.Contains(name, "failed") ||
		strings.Contains(name, "errorresult") ||
		strings.Contains(name, "erroroutcome")
}

func hasExplicitTriStateAbsenceSignal(exprs []ast.Expr, explicitAbsence bool) bool {
	if !explicitAbsence {
		return false
	}
	for _, expr := range exprs {
		if isFalseIdent(expr) || isEmptyComposite(expr) {
			return true
		}
	}
	return false
}

func isEmptyComposite(expr ast.Expr) bool {
	composite, ok := expr.(*ast.CompositeLit)
	return ok && len(composite.Elts) == 0
}

func exprName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.Ident:
		return node.Name
	case *ast.SelectorExpr:
		return exprName(node.X) + "." + node.Sel.Name
	case *ast.StarExpr:
		return exprName(node.X)
	case *ast.CallExpr:
		return exprName(node.Fun)
	default:
		return ""
	}
}

func isNilIdent(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil"
}
