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

// main 扫描指定模块中的 JSON-RPC 注册点，并按字典序输出 method 名称。
func main() {
	roots := []string{
		"internal/module/thread",
		"internal/module/turn",
		"internal/module/uistate",
	}
	fset := token.NewFileSet()
	methods := map[string]struct{}{}
	for _, root := range roots {
		consts := map[string]string{}
		files := make([]*ast.File, 0, 32)
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if shouldSkipWalkPath(path, d, walkErr, strings.HasSuffix(path, "_test.go")) {
				return nil
			}
			parseAndCollectFile(fset, path, &files, consts)
			return nil
		})
		for _, file := range files {
			ast.Inspect(file, func(n ast.Node) bool {
				collectExtractedMethods(n, consts, methods)
				return true
			})
		}
	}

	out := make([]string, 0, len(methods))
	for m := range methods {
		out = append(out, m)
	}
	sort.Strings(out)
	for _, m := range out {
		fmt.Println(m)
	}
}

// shouldSkipWalkPath 判断 WalkDir 当前路径是否需要跳过，并把遍历错误写到 stderr。
func shouldSkipWalkPath(path string, d fs.DirEntry, walkErr error, isTestFile bool) bool {
	if walkErr != nil {
		fmt.Fprintf(os.Stderr, "extract_jsonrpc_methods: walk %s: %v\n", path, walkErr)
		return true
	}
	if d.IsDir() {
		return true
	}
	return !strings.HasSuffix(path, ".go") || isTestFile
}

// parseAndCollectFile 解析 Go 文件并收集其中的字符串常量，解析失败只记录错误后跳过该文件。
func parseAndCollectFile(fset *token.FileSet, path string, files *[]*ast.File, consts map[string]string) {
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		fmt.Fprintf(os.Stderr, "extract_jsonrpc_methods: parse %s: %v\n", path, err)
		return
	}
	*files = append(*files, file)
	collectStringConsts(file, consts)
}

// collectExtractedMethods 从 AST 节点中提取已注册的 JSON-RPC method 名称。
func collectExtractedMethods(n ast.Node, consts map[string]string, methods map[string]struct{}) {
	if call, ok := n.(*ast.CallExpr); ok {
		collectBindMethods(call, consts, methods)
	}
	if lit, ok := n.(*ast.CompositeLit); ok {
		collectCompositeLiteralMethods(lit, consts, methods)
	}
	if name := extractedMethod(n, consts); name != "" {
		methods[name] = struct{}{}
	}
}

// collectCompositeLiteralMethods 从 handler.Map 字面量 key 中收集 method 名称。
func collectCompositeLiteralMethods(lit *ast.CompositeLit, consts map[string]string, methods map[string]struct{}) {
	if !isMethodMapLiteral(lit) {
		return
	}
	for _, elt := range lit.Elts {
		entry, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if name, ok := methodFromExpr(entry.Key, consts); ok && validMethod(name) {
			methods[name] = struct{}{}
		}
	}
}

// collectBindMethods 从 bindMethods(handler.Map{...}) 调用中收集 method 名称。
func collectBindMethods(call *ast.CallExpr, consts map[string]string, methods map[string]struct{}) {
	if !isBindMethodsCall(call) || len(call.Args) == 0 {
		return
	}
	lit, ok := call.Args[0].(*ast.CompositeLit)
	if !ok {
		return
	}
	for _, elt := range lit.Elts {
		entry, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if name, ok := methodFromExpr(entry.Key, consts); ok && validMethod(name) {
			methods[name] = struct{}{}
		}
	}
}

// extractedMethod 从 register 调用或 s.methods[...] 索引表达式中提取 method 名称。
func extractedMethod(n ast.Node, consts map[string]string) string {
	switch x := n.(type) {
	case *ast.IndexExpr:
		if name, ok := methodFromMethodsIndex(x, consts); ok && validMethod(name) {
			return name
		}
	case *ast.CallExpr:
		if !isRegisterCall(x) || len(x.Args) == 0 {
			return ""
		}
		if name, ok := methodFromExpr(x.Args[0], consts); ok && validMethod(name) {
			return name
		}
	}
	return ""
}

// isBindMethodsCall 判断调用是否是本脚本关注的 bindMethods。
func isBindMethodsCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel != nil && sel.Sel.Name == "bindMethods"
}

// isMethodMapLiteral 判断复合字面量是否是 handler.Map。
func isMethodMapLiteral(lit *ast.CompositeLit) bool {
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != "Map" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "handler"
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
				}
			}
		}
	}
}

// methodFromMethodsIndex 从 s.methods[...] 形式中解析 method 名称。
func methodFromMethodsIndex(idx *ast.IndexExpr, consts map[string]string) (string, bool) {
	sel, ok := idx.X.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != "methods" {
		return "", false
	}
	root, ok := sel.X.(*ast.Ident)
	if !ok || root.Name != "s" {
		return "", false
	}
	return methodFromExpr(idx.Index, consts)
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
		if v.Kind != token.STRING {
			return "", false
		}
		return strings.Trim(v.Value, `"`), true
	case *ast.Ident:
		if consts == nil {
			return "", false
		}
		s, ok := consts[v.Name]
		return s, ok
	default:
		return "", false
	}
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
