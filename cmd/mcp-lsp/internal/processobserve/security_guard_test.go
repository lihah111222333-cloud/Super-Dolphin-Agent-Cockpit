package processobserve_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func TestProcessObserverHasNoProcessControlCapability(t *testing.T) {
	for _, path := range observerSourceFiles(t) {
		validateObserverSource(t, path)
	}
}

func observerSourceFiles(t *testing.T) []string {
	t.Helper()
	root := processObserverRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func validateObserverSource(t *testing.T, path string) {
	t.Helper()
	if strings.HasSuffix(path, "_test.go") {
		return
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, imported := range file.Imports {
		name := strings.Trim(imported.Path.Value, "\"")
		if name == "os/exec" || strings.Contains(name, "/hiddenexec") || strings.Contains(name, "/multilsp") {
			t.Fatalf("observer imports process-control package %q", name)
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		return validateObserverASTNode(t, path, node)
	})
}

func validateObserverASTNode(t *testing.T, path string, node ast.Node) bool {
	t.Helper()
	call, ok := node.(*ast.CallExpr)
	if !ok {
		return true
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return true
	}
	switch selector.Sel.Name {
	case "Kill", "Signal", "TerminateJobObject", "Command", "CommandContext":
		t.Fatalf("observer contains process-control call %s in %s", selector.Sel.Name, path)
	}
	return true
}

func TestProcessObserverTypesAndSSACompile(t *testing.T) {
	root := processObserverRoot(t)
	if err := analyzeObserverCapabilityPackage(t, root); err != nil {
		t.Fatal(err)
	}
}

func TestProcessObserverCapabilityGuardRejectsMutationFixture(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const source = `package fixture

import "syscall"

func Run(pid int) {
	killFn := syscall.Kill
	killFn(pid, 9)
}
`
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := analyzeObserverCapabilityPackage(t, root); err == nil {
		t.Fatal("observer mutation fixture unexpectedly passed capability guard")
	}
}

func analyzeObserverCapabilityPackage(t *testing.T, root string) error {
	t.Helper()
	loaded, err := packages.Load(&packages.Config{Mode: packages.LoadAllSyntax, Dir: root}, ".")
	if err != nil {
		return fmt.Errorf("load observer capability package: %w", err)
	}
	if len(loaded) != 1 {
		return fmt.Errorf("loaded %d observer capability packages", len(loaded))
	}
	if len(loaded[0].Errors) != 0 {
		return fmt.Errorf("observer capability package type errors: %v", loaded[0].Errors)
	}
	program, ssaPackages := ssautil.AllPackages(loaded, ssa.SanityCheckFunctions)
	program.Build()
	return validateObserverSSAPackages(program, ssaPackages)
}

func validateObserverSSAPackages(program *ssa.Program, packages []*ssa.Package) error {
	for _, ssaPackage := range packages {
		if err := validateObserverSSAPackage(program, ssaPackage); err != nil {
			return err
		}
	}
	return nil
}

func validateObserverSSAPackage(program *ssa.Program, ssaPackage *ssa.Package) error {
	if ssaPackage == nil {
		return nil
	}
	for function := range ssautil.AllFunctions(program) {
		if function.Package() != ssaPackage {
			continue
		}
		if err := validateObserverSSAFunction(function); err != nil {
			return err
		}
	}
	return nil
}

func validateObserverSSAFunction(function *ssa.Function) error {
	for _, block := range function.Blocks {
		if err := validateObserverSSABlock(function, block); err != nil {
			return err
		}
	}
	return nil
}

func validateObserverSSABlock(function *ssa.Function, block *ssa.BasicBlock) error {
	for _, instruction := range block.Instrs {
		call, ok := instruction.(interface{ Common() *ssa.CallCommon })
		if !ok {
			continue
		}
		if err := validateObserverCapabilityCall(call.Common()); err != nil {
			return fmt.Errorf("%s: %w", function.String(), err)
		}
	}
	return nil
}

func validateObserverCapabilityCall(common *ssa.CallCommon) error {
	callee := observerStaticCallee(common)
	if callee == nil {
		return nil
	}
	object, _ := callee.Object().(*types.Func)
	if object == nil || object.Pkg() == nil {
		return nil
	}
	return validateObserverCapabilityTarget(object)
}

func observerStaticCallee(common *ssa.CallCommon) *ssa.Function {
	if common == nil {
		return nil
	}
	if callee := common.StaticCallee(); callee != nil {
		return callee
	}
	function, _ := common.Value.(*ssa.Function)
	return function
}

func validateObserverCapabilityTarget(object *types.Func) error {
	pkgPath := object.Pkg().Path()
	name := object.Name()
	if observerHelperPackage(pkgPath) {
		return fmt.Errorf("forbidden helper package %s", pkgPath)
	}
	if pkgPath == "os/exec" {
		return fmt.Errorf("forbidden process execution package %s", pkgPath)
	}
	if observerRestrictedPackage(pkgPath) && observerForbiddenName(name) {
		return fmt.Errorf("forbidden process capability %s.%s", pkgPath, name)
	}
	return nil
}

func observerHelperPackage(pkgPath string) bool {
	return strings.Contains(pkgPath, "/hiddenexec") || strings.Contains(pkgPath, "/multilsp")
}

func observerRestrictedPackage(pkgPath string) bool {
	return pkgPath == "os" || pkgPath == "syscall" || strings.HasSuffix(pkgPath, "/unix") || pkgPath == "golang.org/x/sys/windows"
}

func observerForbiddenName(name string) bool {
	switch name {
	case "Kill", "Signal", "TerminateJobObject":
		return true
	default:
		return false
	}
}

func processObserverRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(current)
}
