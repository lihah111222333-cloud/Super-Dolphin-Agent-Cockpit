//go:build ignore

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type scanFile struct {
	file *ast.File
}

type scanContext struct {
	fset        *token.FileSet
	methods     map[string]struct{}
	consts      map[string]string
	diagnostics []string
}

// main 扫描指定模块中的 JSON-RPC 注册点，并按字典序输出 method 名称。
func main() {
	roots := []string{
		"internal/module",
		"internal/platform/mcpcontrol",
	}
	ctx := scanContext{
		fset:    token.NewFileSet(),
		methods: map[string]struct{}{},
		consts:  map[string]string{},
	}

	files := make([]scanFile, 0, 128)
	for _, root := range roots {
		ctx.walkRoot(root, &files)
	}
	ctx.walkConstRoot("internal/contract")
	ctx.walkConstRoot("internal/dto/mcp")

	for _, item := range files {
		collectStringConsts(item.file, ctx.consts)
	}
	for _, item := range files {
		ctx.collectFileMethods(item.file)
	}
	if len(ctx.diagnostics) > 0 {
		for _, diagnostic := range ctx.diagnostics {
			fmt.Fprintln(os.Stderr, diagnostic)
		}
		os.Exit(1)
	}

	out := make([]string, 0, len(ctx.methods))
	for m := range ctx.methods {
		out = append(out, m)
	}
	sort.Strings(out)
	for _, m := range out {
		fmt.Println(m)
	}
}

func (ctx *scanContext) walkRoot(root string, files *[]scanFile) {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if ctx.shouldSkipWalkPath(path, d, walkErr, strings.HasSuffix(path, "_test.go")) {
			return nil
		}
		ctx.parseAndCollectFile(path, files)
		return nil
	})
	if err != nil {
		ctx.addDiagnostic("extract_jsonrpc_methods: walk %s: %v", root, err)
	}
}

func (ctx *scanContext) walkConstRoot(root string) {
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if ctx.shouldSkipWalkPath(path, d, walkErr, strings.HasSuffix(path, "_test.go")) {
			return nil
		}
		file, ok := ctx.parseFile(path)
		if ok {
			collectStringConsts(file, ctx.consts)
		}
		return nil
	})
	if err != nil {
		ctx.addDiagnostic("extract_jsonrpc_methods: walk %s: %v", root, err)
	}
}

func (ctx *scanContext) addDiagnostic(format string, args ...any) {
	ctx.diagnostics = append(ctx.diagnostics, fmt.Sprintf(format, args...))
}

// shouldSkipWalkPath 判断 WalkDir 当前路径是否需要跳过，并把遍历错误记录为 fail-fast 诊断。
func (ctx *scanContext) shouldSkipWalkPath(path string, d fs.DirEntry, walkErr error, isTestFile bool) bool {
	if walkErr != nil {
		ctx.addDiagnostic("extract_jsonrpc_methods: walk %s: %v", path, walkErr)
		return true
	}
	if d == nil {
		ctx.addDiagnostic("extract_jsonrpc_methods: walk %s: nil dir entry", path)
		return true
	}
	if d.IsDir() {
		return true
	}
	return !strings.HasSuffix(path, ".go") || isTestFile
}

// parseAndCollectFile 解析 Go 文件；解析失败记录诊断并让 main 以非零退出。
func (ctx *scanContext) parseAndCollectFile(path string, files *[]scanFile) {
	file, ok := ctx.parseFile(path)
	if !ok {
		return
	}
	*files = append(*files, scanFile{file: file})
}

func (ctx *scanContext) parseFile(path string) (*ast.File, bool) {
	file, err := parser.ParseFile(ctx.fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		ctx.addDiagnostic("extract_jsonrpc_methods: parse %s: %v", path, err)
		return nil, false
	}
	return file, true
}

// collectFileMethods 从单个文件 AST 中提取已注册的 JSON-RPC method 名称。
func (ctx *scanContext) collectFileMethods(file *ast.File) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ctx.collectFunctionMethods(fn)
	}
}

func (ctx *scanContext) collectFunctionMethods(fn *ast.FuncDecl) {
	handlerMaps := functionHandlerMaps(fn)
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			ctx.collectBindMethods(node)
			ctx.collectRegisterCall(node)
		case *ast.CompositeLit:
			ctx.collectCompositeLiteralMethods(node)
		case *ast.AssignStmt:
			ctx.collectAssignmentMethods(node, handlerMaps)
			rememberHandlerMapAssignments(node, handlerMaps)
		}
		return true
	})
}

func functionHandlerMaps(fn *ast.FuncDecl) map[string]struct{} {
	out := map[string]struct{}{}
	for _, field := range fn.Type.Params.List {
		if !isHandlerMapExpr(field.Type) {
			continue
		}
		for _, name := range field.Names {
			out[name.Name] = struct{}{}
		}
	}
	return out
}

func rememberHandlerMapAssignments(assign *ast.AssignStmt, handlerMaps map[string]struct{}) {
	for i, rhs := range assign.Rhs {
		if !isHandlerMapExpr(rhs) || i >= len(assign.Lhs) {
			continue
		}
		if ident, ok := assign.Lhs[i].(*ast.Ident); ok {
			handlerMaps[ident.Name] = struct{}{}
		}
	}
}

// collectAssignmentMethods 从已知 handler.Map 变量或参数的索引赋值中收集 method。
func (ctx *scanContext) collectAssignmentMethods(assign *ast.AssignStmt, handlerMaps map[string]struct{}) {
	for _, lhs := range assign.Lhs {
		idx, ok := lhs.(*ast.IndexExpr)
		if !ok || !isTrackedHandlerMapIndex(idx, handlerMaps) {
			continue
		}
		if name, ok := methodFromExpr(idx.Index, ctx.consts); ok && validMethod(name) {
			ctx.methods[name] = struct{}{}
		}
	}
}

func isTrackedHandlerMapIndex(idx *ast.IndexExpr, handlerMaps map[string]struct{}) bool {
	switch root := idx.X.(type) {
	case *ast.Ident:
		_, ok := handlerMaps[root.Name]
		return ok
	case *ast.SelectorExpr:
		return root.Sel != nil && root.Sel.Name == "methods"
	default:
		return false
	}
}

// collectCompositeLiteralMethods 从 handler.Map 字面量 key 中收集 method 名称。
func (ctx *scanContext) collectCompositeLiteralMethods(lit *ast.CompositeLit) {
	if !isMethodMapLiteral(lit) {
		return
	}
	for _, elt := range lit.Elts {
		entry, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		ctx.collectRequiredMethodKey(entry.Key)
	}
}

// collectBindMethods 从 bindMethods(handler.Map{...}) 调用中收集 method 名称。
func (ctx *scanContext) collectBindMethods(call *ast.CallExpr) {
	if !isBindMethodsCall(call) || len(call.Args) == 0 {
		return
	}
	lit, ok := call.Args[0].(*ast.CompositeLit)
	if !ok {
		return
	}
	ctx.collectCompositeLiteralMethods(lit)
}

func (ctx *scanContext) collectRegisterCall(call *ast.CallExpr) {
	if !isRegisterCall(call) || len(call.Args) == 0 {
		return
	}
	if name, ok := methodFromExpr(call.Args[0], ctx.consts); ok && validMethod(name) {
		ctx.methods[name] = struct{}{}
	}
}

func (ctx *scanContext) collectRequiredMethodKey(expr ast.Expr) {
	name, ok := methodFromExpr(expr, ctx.consts)
	if !ok {
		ctx.addDiagnostic("extract_jsonrpc_methods: unresolved handler.Map method key %s", exprString(expr))
		return
	}
	if validMethod(name) {
		ctx.methods[name] = struct{}{}
	}
}

// exprString 为 unresolved method 诊断生成稳定的表达式标签。
func exprString(expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.BasicLit:
		return v.Value
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		if root, ok := v.X.(*ast.Ident); ok && v.Sel != nil {
			return root.Name + "." + v.Sel.Name
		}
	}
	return fmt.Sprintf("%T", expr)
}

// isBindMethodsCall 判断调用是否是本脚本关注的 bindMethods。
func isBindMethodsCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if ok && sel.Sel != nil && sel.Sel.Name == "bindMethods" {
		return true
	}
	ident, ok := call.Fun.(*ast.Ident)
	return ok && ident.Name == "bindMethods"
}

// isMethodMapLiteral 判断复合字面量是否是 handler.Map。
func isMethodMapLiteral(lit *ast.CompositeLit) bool {
	return isHandlerMapExpr(lit)
}

// isHandlerMapExpr 判断表达式是否是 handler.Map 类型或 handler.Map 字面量。
func isHandlerMapExpr(expr ast.Expr) bool {
	switch v := expr.(type) {
	case *ast.CompositeLit:
		return isHandlerMapExpr(v.Type)
	case *ast.SelectorExpr:
		if v.Sel == nil || v.Sel.Name != "Map" {
			return false
		}
		pkg, ok := v.X.(*ast.Ident)
		return ok && pkg.Name == "handler"
	case *ast.Ellipsis:
		return isHandlerMapExpr(v.Elt)
	default:
		return false
	}
}

// collectStringConsts 收集文件级字符串常量，供 method 常量引用解析使用。
func collectStringConsts(file *ast.File, out map[string]string) {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				if s, ok := methodFromExpr(vs.Values[i], nil); ok {
					out[name.Name] = s
					out[file.Name.Name+"."+name.Name] = s
				}
			}
		}
	}
}

// isRegisterCall 判断调用是否是 register 函数或方法。
func isRegisterCall(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return strings.EqualFold(fn.Name, "register")
	case *ast.SelectorExpr:
		return fn.Sel != nil && strings.EqualFold(fn.Sel.Name, "register")
	default:
		return false
	}
}

// methodFromExpr 从字符串字面量或已收集常量中解析 method 名称。
func methodFromExpr(expr ast.Expr, consts map[string]string) (string, bool) {
	switch v := expr.(type) {
	case *ast.BasicLit:
		return methodFromBasicLit(v)
	case *ast.Ident:
		return methodFromIdent(v, consts)
	case *ast.SelectorExpr:
		return methodFromSelector(v, consts)
	case *ast.BinaryExpr:
		return methodFromBinary(v, consts)
	}
	return "", false
}

func methodFromBasicLit(lit *ast.BasicLit) (string, bool) {
	if lit.Kind != token.STRING {
		return "", false
	}
	return strings.Trim(lit.Value, `"`), true
}

func methodFromIdent(ident *ast.Ident, consts map[string]string) (string, bool) {
	if consts == nil {
		return "", false
	}
	s, ok := consts[ident.Name]
	return s, ok
}

func methodFromSelector(sel *ast.SelectorExpr, consts map[string]string) (string, bool) {
	if consts == nil || sel.Sel == nil {
		return "", false
	}
	if root, ok := sel.X.(*ast.Ident); ok {
		if s, ok := consts[root.Name+"."+sel.Sel.Name]; ok {
			return s, true
		}
	}
	s, ok := consts[sel.Sel.Name]
	return s, ok
}

func methodFromBinary(expr *ast.BinaryExpr, consts map[string]string) (string, bool) {
	if expr.Op != token.ADD {
		return "", false
	}
	left, ok := methodFromExpr(expr.X, consts)
	if !ok {
		return "", false
	}
	right, ok := methodFromExpr(expr.Y, consts)
	if !ok {
		return "", false
	}
	return left + right, true
}

// validMethod 校验 method 名称只包含 JSON-RPC 路径常用字符。
func validMethod(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			strings.ContainsRune("._/-", r) {
			continue
		}
		return false
	}
	return true
}
