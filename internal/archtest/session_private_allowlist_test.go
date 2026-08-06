package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionPrivateAllowlistIntegrity(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	for _, entry := range sessionPrivateRuntimeAllowlist() {
		t.Run(entry.Symbol, func(t *testing.T) {
			t.Parallel()
			if !sessionPrivateEntryComplete(entry) {
				t.Fatalf("incomplete session-private allowlist entry: %+v", entry)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(entry.DefinitionPath))); err != nil {
				t.Fatalf("definition path missing for %s: %v", entry.Symbol, err)
			}
			if !goFileDefinesSymbol(t, root, entry.DefinitionPath, entry.Symbol) {
				t.Fatalf("symbol %s not found in %s", entry.Symbol, entry.DefinitionPath)
			}
		})
	}
}

func TestSessionPrivateRuntimeAllowlistReturnsIndependentSnapshots(t *testing.T) {
	first := sessionPrivateRuntimeAllowlist()
	second := sessionPrivateRuntimeAllowlist()
	if len(first) == 0 || len(second) == 0 {
		t.Fatal("session-private runtime allowlist snapshot is empty")
	}
	if &first[0] == &second[0] {
		t.Fatal("session-private runtime allowlist snapshots share backing storage")
	}
	first[0].Reason = "local mutation"
	if second[0].Reason == "local mutation" {
		t.Fatal("session-private runtime allowlist mutation leaked into another snapshot")
	}
}

func TestBindRuntimeSafeGoHasSessionPrivateAllowlistEntry(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	rel := "internal/app/runner.go"
	launches := sessionPrivateSafeGoLaunchesInSymbol(t, root, rel, "BindRuntime")
	if len(launches) == 0 {
		t.Fatal("BindRuntime SafeGo launch not found; integrity test must track root runtime bridge shape")
	}
	if !isSessionPrivateDefinition(rel, "BindRuntime") {
		t.Fatalf("BindRuntime SafeGo launch is missing session-private allowlist entry with DefinitionPath=%q Symbol=%q", rel, "BindRuntime")
	}
}

func TestSessionPrivateRuntimeAllowlist(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	allow := map[string]bool{}
	for _, entry := range sessionPrivateRuntimeAllowlist() {
		line := symbolLine(t, root, entry.DefinitionPath, entry.Symbol)
		t.Logf("[P22.1 ALLOW] session-private %s:%d %s", entry.DefinitionPath, line, entry.Symbol)
		if kind := sessionPrivateLaunchKind(entry); kind != "" {
			allow[kind+"::"+entry.DefinitionPath+"::"+entry.Symbol] = true
		}
	}
	for _, rel := range sessionPrivateRuntimeScopeFiles(t, root) {
		for _, launch := range sessionPrivateRuntimeLaunchesInFile(t, root, rel) {
			if allow[launch.Kind+"::"+rel+"::"+launch.Symbol] {
				continue
			}
			t.Errorf("unallowlisted session-private runtime launch at %s:%d enclosing=%s", rel, launch.Line, launch.Enclosing)
		}
	}
}

type sessionPrivateRuntimeLaunch struct {
	Line      int
	Enclosing string
	Symbol    string
	Kind      string
}

func sessionPrivateRuntimeScopeFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	for _, scope := range []string{"internal/provider/codexapp", "internal/app"} {
		absScope := filepath.Join(root, filepath.FromSlash(scope))
		err := filepath.WalkDir(absScope, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				t.Fatalf("walk %s: %v", path, walkErr)
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("rel %s: %v", path, err)
			}
			files = append(files, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", absScope, err)
		}
	}
	return files
}

func sessionPrivateSafeGoLaunchesInSymbol(t *testing.T, root, rel, symbol string) []sessionPrivateRuntimeLaunch {
	t.Helper()
	fset, funcs := parseSessionPrivateFuncDecls(t, root, rel)
	fn := requireSessionPrivateFunc(t, funcs, symbol, rel)
	launches := collectSessionPrivateSafeGo(fset, symbol, fn.Body)
	for _, helper := range calledSessionPrivateHelpers(fn.Body, funcs) {
		launches = append(launches, collectSessionPrivateSafeGo(fset, symbol, helper.Body)...)
	}
	return launches
}

func parseSessionPrivateFuncDecls(t *testing.T, root, rel string) (*token.FileSet, map[string]*ast.FuncDecl) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	funcs := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			funcs[funcDeclSymbol(fn)] = fn
		}
	}
	return fset, funcs
}

func requireSessionPrivateFunc(t *testing.T, funcs map[string]*ast.FuncDecl, symbol, rel string) *ast.FuncDecl {
	t.Helper()
	fn := funcs[symbol]
	if fn == nil {
		t.Fatalf("symbol %s not found in %s", symbol, rel)
	}
	return fn
}

func collectSessionPrivateSafeGo(fset *token.FileSet, symbol string, body *ast.BlockStmt) []sessionPrivateRuntimeLaunch {
	var launches []sessionPrivateRuntimeLaunch
	for _, launch := range sessionPrivateLaunchesInNode(fset, symbol, body) {
		if launch.Kind == "safego" {
			launches = append(launches, launch)
		}
	}
	return launches
}

func calledSessionPrivateHelpers(body *ast.BlockStmt, funcs map[string]*ast.FuncDecl) []*ast.FuncDecl {
	var helpers []*ast.FuncDecl
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		if helper := funcs[ident.Name]; helper != nil {
			helpers = append(helpers, helper)
		}
		return true
	})
	return helpers
}

func sessionPrivateRuntimeLaunchesInFile(t *testing.T, root, rel string) []sessionPrivateRuntimeLaunch {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	var launches []sessionPrivateRuntimeLaunch
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		symbol := funcDeclSymbol(fn)
		if isRootBridgeException(rel, symbol) {
			continue
		}
		if isSessionPrivateDefinition(rel, symbol) {
			launches = append(launches, sessionPrivateLaunchesInNode(fset, symbol, fn.Body)...)
			continue
		}
		launches = append(launches, sessionPrivateOnStartLaunches(fset, symbol, fn.Body)...)
	}
	return launches
}

func sessionPrivateOnStartLaunches(fset *token.FileSet, symbol string, node ast.Node) []sessionPrivateRuntimeLaunch {
	var launches []sessionPrivateRuntimeLaunch
	ast.Inspect(node, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok || !isFxHookComposite(cl) {
			return true
		}
		for _, elt := range cl.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok || keyName(kv.Key) != "OnStart" {
				continue
			}
			fn, ok := kv.Value.(*ast.FuncLit)
			if !ok || fn.Body == nil {
				continue
			}
			launches = append(launches, sessionPrivateLaunchesInNode(fset, symbol, fn.Body)...)
		}
		return true
	})
	return launches
}

func sessionPrivateLaunchesInNode(fset *token.FileSet, symbol string, node ast.Node) []sessionPrivateRuntimeLaunch {
	var launches []sessionPrivateRuntimeLaunch
	ast.Inspect(node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.GoStmt:
			launches = append(launches, sessionPrivateRuntimeLaunch{Line: fset.Position(x.Go).Line, Enclosing: symbol, Symbol: goLaunchSymbol(x.Call, symbol), Kind: goLaunchKind(x.Call)})
		case *ast.CallExpr:
			if isRuntimeSafeGoCall(x) {
				launches = append(launches, sessionPrivateRuntimeLaunch{Line: fset.Position(x.Pos()).Line, Enclosing: symbol, Symbol: symbol, Kind: "safego"})
			}
		}
		return true
	})
	return launches
}

func isSessionPrivateDefinition(rel, symbol string) bool {
	for _, entry := range sessionPrivateRuntimeAllowlist() {
		if entry.DefinitionPath == rel && entry.Symbol == symbol {
			return true
		}
	}
	return false
}

func sessionPrivateLaunchKind(entry sessionPrivateRuntimeException) string {
	switch entry.BridgeShape {
	case "session_reader":
		return "go_func"
	case "session_health", "session_recovery":
		return "go_named"
	case "desktop_watcher":
		return "safego"
	default:
		return ""
	}
}

func goLaunchKind(call *ast.CallExpr) string {
	if call == nil {
		return "go_unknown"
	}
	if _, ok := call.Fun.(*ast.FuncLit); ok {
		return "go_func"
	}
	return "go_named"
}

func goLaunchSymbol(call *ast.CallExpr, enclosing string) string {
	if call == nil {
		return enclosing
	}
	switch fun := call.Fun.(type) {
	case *ast.FuncLit:
		return enclosing
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		if strings.HasPrefix(enclosing, "(") {
			if idx := strings.Index(enclosing, ")."); idx >= 0 {
				return enclosing[:idx+2] + fun.Sel.Name
			}
		}
		return fun.Sel.Name
	default:
		return enclosing
	}
}

func isRuntimeSafeGoCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selectorXName(sel) == "runtimesafe" && sel.Sel.Name == "SafeGo"
}

func funcDeclSymbol(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return "(" + receiverTypeName(fn.Recv.List[0].Type) + ")." + fn.Name.Name
}

func receiverTypeName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return "*" + receiverTypeName(x.X)
	case *ast.SelectorExpr:
		if prefix := selectorXName(x); prefix != "" {
			return prefix + "." + x.Sel.Name
		}
		return x.Sel.Name
	default:
		return ""
	}
}

func sessionPrivateEntryComplete(entry sessionPrivateRuntimeException) bool {
	return entry.DefinitionPath != "" && entry.CallSitePath != "" && entry.Symbol != "" &&
		entry.BridgeShape != "" && entry.ExceptionClass != "" && entry.Reason != "" &&
		entry.RemoveWhen != "" && entry.RollbackWhen != "" && entry.RollbackAction != ""
}

func goFileDefinesSymbol(t *testing.T, root, rel, symbol string) bool {
	t.Helper()
	return symbolLine(t, root, rel, symbol) > 0
}

func symbolLine(t *testing.T, root, rel, symbol string) int {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	name := shortSymbolName(symbol)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fset.Position(fn.Pos()).Line
		}
	}
	return 0
}

func shortSymbolName(symbol string) string {
	if idx := strings.LastIndex(symbol, ")."); idx >= 0 {
		return symbol[idx+2:]
	}
	if idx := strings.LastIndex(symbol, "."); idx >= 0 {
		return symbol[idx+1:]
	}
	return symbol
}
