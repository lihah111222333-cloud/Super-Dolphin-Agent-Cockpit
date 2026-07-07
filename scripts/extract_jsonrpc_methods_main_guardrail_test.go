package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestExtractJSONRPCMethodsMainShapeAndFileOwnership(t *testing.T) {
	scriptPath := extractJSONRPCMethodsScriptPath(t)
	if filepath.Base(scriptPath) != "extract_jsonrpc_methods.go" {
		t.Fatalf("script path = %q, want extract_jsonrpc_methods.go", scriptPath)
	}

	file := parseExtractJSONRPCMethodsScript(t, scriptPath)
	mainDecl := findExtractJSONRPCMethodsMain(t, file)
	assertExtractJSONRPCMethodsMainShape(t, mainDecl)

	roots := extractMainRoots(mainDecl)
	wantRoots := []string{"internal/module", "internal/platform/mcpcontrol"}
	if !reflect.DeepEqual(roots, wantRoots) {
		t.Fatalf("main roots = %v, want %v", roots, wantRoots)
	}
	assertExtractJSONRPCMethodsMainFeatures(t, inspectExtractJSONRPCMethodsScript(file))
}

func parseExtractJSONRPCMethodsScript(t *testing.T, scriptPath string) *ast.File {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, scriptPath, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse script: %v", err)
	}
	return file
}

func findExtractJSONRPCMethodsMain(t *testing.T, file *ast.File) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Name == nil {
			continue
		}
		if fn.Name.Name == "main" {
			return fn
		}
	}
	t.Fatal("main function not found in extract_jsonrpc_methods.go")
	return nil
}

func assertExtractJSONRPCMethodsMainShape(t *testing.T, mainDecl *ast.FuncDecl) {
	t.Helper()
	if mainDecl.Recv != nil {
		t.Fatal("main must not be a method")
	}
	if mainDecl.Type.Params != nil && len(mainDecl.Type.Params.List) > 0 {
		t.Fatalf("main params = %#v, want none", mainDecl.Type.Params.List)
	}
	if mainDecl.Type.Results != nil && len(mainDecl.Type.Results.List) > 0 {
		t.Fatalf("main results = %#v, want none", mainDecl.Type.Results.List)
	}
}

type extractMainFeatures struct {
	hasWalkDir          bool
	hasInspect          bool
	hasSort             bool
	hasNonZeroFailFast  bool
	skipsTestFiles      bool
	walksContractConsts bool
	walksMCPDTOConsts   bool
}

func inspectExtractJSONRPCMethodsScript(file *ast.File) extractMainFeatures {
	features := extractMainFeatures{}
	ast.Inspect(file, func(n ast.Node) bool {
		features.observe(n)
		return true
	})
	return features
}

func (f *extractMainFeatures) observe(n ast.Node) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	if sel.Sel == nil {
		return
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return
	}
	f.observeSelector(pkg.Name, sel.Sel.Name, call.Args)
}

func (f *extractMainFeatures) observeSelector(pkg, name string, args []ast.Expr) {
	observer, ok := extractMainFeatureObservers[pkg+"."+name]
	if !ok {
		return
	}
	observer(f, args)
}

var extractMainFeatureObservers = map[string]func(*extractMainFeatures, []ast.Expr){
	"filepath.WalkDir":  observeWalkDirCall,
	"ast.Inspect":       observeASTInspectCall,
	"sort.Strings":      observeSortStringsCall,
	"strings.HasSuffix": observeStringsHasSuffixCall,
	"os.Exit":           observeOSExitCall,
	"ctx.walkConstRoot": observeConstRootCall,
}

func observeWalkDirCall(f *extractMainFeatures, _ []ast.Expr) {
	f.hasWalkDir = true
}

func observeASTInspectCall(f *extractMainFeatures, _ []ast.Expr) {
	f.hasInspect = true
}

func observeSortStringsCall(f *extractMainFeatures, args []ast.Expr) {
	if callHasIdentArg(args, "out") {
		f.hasSort = true
	}
}

func observeStringsHasSuffixCall(f *extractMainFeatures, args []ast.Expr) {
	if callHasStringArg(args, 1, "_test.go") {
		f.skipsTestFiles = true
	}
}

func observeOSExitCall(f *extractMainFeatures, args []ast.Expr) {
	if callHasIntArg(args, 0, "1") {
		f.hasNonZeroFailFast = true
	}
}

func observeConstRootCall(f *extractMainFeatures, args []ast.Expr) {
	if callHasStringArg(args, 0, "internal/contract") {
		f.walksContractConsts = true
	}
	if callHasStringArg(args, 0, "internal/dto/mcp") {
		f.walksMCPDTOConsts = true
	}
}

func callHasIdentArg(args []ast.Expr, want string) bool {
	if len(args) != 1 {
		return false
	}
	ident, ok := args[0].(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == want
}

func callHasStringArg(args []ast.Expr, index int, want string) bool {
	if len(args) <= index {
		return false
	}
	lit, ok := args[index].(*ast.BasicLit)
	if !ok {
		return false
	}
	return strings.Trim(lit.Value, "\"") == want
}

func callHasIntArg(args []ast.Expr, index int, want string) bool {
	if len(args) <= index {
		return false
	}
	lit, ok := args[index].(*ast.BasicLit)
	if !ok {
		return false
	}
	return lit.Value == want
}

func assertExtractJSONRPCMethodsMainFeatures(t *testing.T, features extractMainFeatures) {
	t.Helper()
	if !features.hasWalkDir {
		t.Fatal("main must walk the configured roots with filepath.WalkDir")
	}
	if !features.hasInspect {
		t.Fatal("main must inspect parsed files with ast.Inspect")
	}
	if !features.hasSort {
		t.Fatal("main must sort extracted methods before printing")
	}
	if !features.skipsTestFiles {
		t.Fatal("main must explicitly skip _test.go files")
	}
	if !features.hasNonZeroFailFast {
		t.Fatal("main must exit non-zero when extraction diagnostics are present")
	}
	if !features.walksContractConsts {
		t.Fatal("main must load internal/contract as a const-only method key source")
	}
	if !features.walksMCPDTOConsts {
		t.Fatal("main must load internal/dto/mcp as a const-only method key source")
	}
}

func TestExtractJSONRPCMethodsScript_EmitsSortedUniqueValidMethods(t *testing.T) {
	repoRoot := t.TempDir()
	writeExtractJSONRPCMethodsSuccessFixture(t, repoRoot)

	stdout, stderr, err := runExtractJSONRPCMethodsScript(t, repoRoot)
	if err != nil {
		t.Fatalf("run script err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	got := nonEmptyLines(stdout)
	want := []string{
		"alpha/method",
		"beta/method",
		"ctl/register",
		"dashboard/agentStatus",
		"dashboard/insights/list",
		"memory/consolidate",
		"zeta/method",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stdout lines = %v, want %v", got, want)
	}
	if strings.Contains(stdout, "bad method!") {
		t.Fatalf("stdout should not include invalid methods: %q", stdout)
	}
	if strings.Contains(stdout, "test-only/method") {
		t.Fatalf("stdout should ignore _test.go files: %q", stdout)
	}
}

func writeExtractJSONRPCMethodsSuccessFixture(t *testing.T, repoRoot string) {
	t.Helper()
	mkdirExtractJSONRPCMethodsFixtureDirs(t, repoRoot)
	writeExtractJSONRPCMethodsMethodFixtures(t, repoRoot)
	writeExtractJSONRPCMethodsConstFixtures(t, repoRoot)
}

func mkdirExtractJSONRPCMethodsFixtureDirs(t *testing.T, repoRoot string) {
	t.Helper()
	for _, dir := range []string{
		"internal/module/thread",
		"internal/module/dashboard",
		"internal/module/memory",
		"internal/platform/mcpcontrol",
		"internal/contract",
		"internal/dto/mcp",
	} {
		if err := os.MkdirAll(filepath.Join(repoRoot, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
}

func writeExtractJSONRPCMethodsMethodFixtures(t *testing.T, repoRoot string) {
	t.Helper()
	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/module/thread/scan.go", strings.Join([]string{
		"package thread",
		"",
		"const zetaMethod = \"zeta/method\"",
		"",
		"func register(string) {}",
		"",
		"func collect() {",
		"\tregister(zetaMethod)",
		"\tregister(\"bad method!\")",
		"\ts.methods[\"alpha/method\"] = 0",
		"}",
		"",
	}, "\n"))
	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/module/dashboard/scan.go", strings.Join([]string{
		"package dashboard",
		"",
		"const dashboardPrefix = \"dashboard/\"",
		"",
		"func bindMethods(handler.Map) {}",
		"",
		"func collectAgain() {",
		"\tm := handler.Map{}",
		"\tm[dashboardPrefix+\"agentStatus\"] = nil",
		"\tbindMethods(handler.Map{\"beta/method\": nil})",
		"}",
		"",
		"func addDashboardInsightHandlers(m handler.Map) {",
		"\tm[\"dashboard/insights/list\"] = nil",
		"}",
		"",
	}, "\n"))
	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/module/memory/scan.go", strings.Join([]string{
		"package memory",
		"",
		"func helperReturn() handler.Map {",
		"\treturn handler.Map{\"memory/consolidate\": nil}",
		"}",
		"",
	}, "\n"))
	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/platform/mcpcontrol/scan.go", strings.Join([]string{
		"package mcpcontrol",
		"",
		"func collectMCPControl() {",
		"\t_ = handler.Map{dto.MethodRegister: nil}",
		"}",
		"",
	}, "\n"))
	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/module/thread/ignored_test.go", strings.Join([]string{
		"package thread",
		"",
		"func register(string) {}",
		"",
		"func ignored() {",
		"\tregister(\"test-only/method\")",
		"}",
		"",
	}, "\n"))
}

func writeExtractJSONRPCMethodsConstFixtures(t *testing.T, repoRoot string) {
	t.Helper()
	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/dto/mcp/constants.go", strings.Join([]string{
		"package mcp",
		"",
		"const MethodRegister = \"ctl/register\"",
		"",
	}, "\n"))
	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/contract/constants.go", strings.Join([]string{
		"package contract",
		"",
		"const ThreadRPCStart = \"thread/start\"",
		"",
	}, "\n"))
}

func TestExtractJSONRPCMethodsScript_FailsFastOnWalkAndParseErrors(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, "internal", "module", "thread"), 0o755); err != nil {
		t.Fatalf("mkdir internal/module/thread: %v", err)
	}

	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/module/thread/good.go", strings.Join([]string{
		"package thread",
		"",
		"func register(string) {}",
		"",
		"func collect() {",
		"\tregister(\"turn/interrupt\")",
		"}",
		"",
	}, "\n"))
	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/module/thread/broken.go", "package thread\nfunc broken(\n")

	stdout, stderr, err := runExtractJSONRPCMethodsScript(t, repoRoot)
	if err == nil {
		t.Fatalf("run script err=nil stdout=%q stderr=%q", stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty on fail-fast diagnostics", stdout)
	}
	normalizedStderr := filepath.ToSlash(stderr)
	for _, wantErr := range []string{
		"extract_jsonrpc_methods: parse internal/module/thread/broken.go",
		"extract_jsonrpc_methods: walk internal/platform/mcpcontrol",
		"extract_jsonrpc_methods: walk internal/contract",
		"extract_jsonrpc_methods: walk internal/dto/mcp",
	} {
		if !strings.Contains(normalizedStderr, wantErr) {
			t.Fatalf("stderr = %q, want substring %q", stderr, wantErr)
		}
	}
}

func extractJSONRPCMethodsScriptPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "extract_jsonrpc_methods.go")
}

func runExtractJSONRPCMethodsScript(t *testing.T, repoRoot string) (string, string, error) {
	t.Helper()
	cmd := exec.Command("go", "run", extractJSONRPCMethodsScriptPath(t))
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func writeExtractJSONRPCMethodsFixture(t *testing.T, repoRoot, relPath, content string) {
	t.Helper()
	path := filepath.Join(repoRoot, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relPath, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func nonEmptyLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func extractMainRoots(mainDecl *ast.FuncDecl) []string {
	for _, stmt := range mainDecl.Body.List {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok {
			continue
		}
		for i, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok || ident.Name != "roots" || i >= len(assign.Rhs) {
				continue
			}
			lit, ok := assign.Rhs[i].(*ast.CompositeLit)
			if !ok {
				return nil
			}
			roots := make([]string, 0, len(lit.Elts))
			for _, elt := range lit.Elts {
				str, ok := elt.(*ast.BasicLit)
				if !ok {
					return nil
				}
				roots = append(roots, strings.Trim(str.Value, "\""))
			}
			return roots
		}
	}
	return nil
}
