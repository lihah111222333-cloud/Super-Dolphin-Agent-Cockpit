package processprobe_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
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

func TestProcessProbeSnapshotIsSealedAndReadOnly(t *testing.T) {
	var snapshotFields *ast.FieldList
	for _, path := range processProbeSourceFiles(t) {
		fields := inspectProcessProbeSource(t, path)
		if fields != nil {
			snapshotFields = fields
		}
	}
	assertSnapshotFields(t, snapshotFields)
}

func processProbeSourceFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(processProbeRoot(t), "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("processprobe has no source files")
	}
	return files
}

func inspectProcessProbeSource(t *testing.T, path string) *ast.FieldList {
	t.Helper()
	if strings.HasSuffix(path, "_test.go") {
		return nil
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, imported := range file.Imports {
		name := strings.Trim(imported.Path.Value, "\"")
		if name == "unsafe" || name == "reflect" {
			t.Fatalf("processprobe imports capability-bearing package %q", name)
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		return validateProcessProbeASTNode(t, path, node)
	})
	return snapshotFieldsFromFile(t, file)
}

func validateProcessProbeASTNode(t *testing.T, path string, node ast.Node) bool {
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
	case "Signal", "TerminateJobObject":
		t.Fatalf("forbidden destructive capability %s in %s", selector.Sel.Name, path)
	case "Kill":
		if len(call.Args) != 2 || !isZeroLiteral(call.Args[1]) {
			t.Fatalf("non-zero process capability in %s", path)
		}
	case "Command", "CommandContext":
		if !allowlistedCommandCall(selector.Sel.Name, call.Args) {
			t.Fatalf("non-allowlisted command in %s", path)
		}
	}
	return true
}

func allowlistedCommandCall(name string, args []ast.Expr) bool {
	argumentIndex := 0
	if name == "CommandContext" {
		argumentIndex = 1
	}
	return len(args) > argumentIndex && isAllowlistedProcessQuery(args[argumentIndex])
}

func snapshotFieldsFromFile(t *testing.T, file *ast.File) *ast.FieldList {
	t.Helper()
	for _, declaration := range file.Decls {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok || gen.Tok.String() != "type" {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != "Snapshot" {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				t.Fatal("Snapshot is not a struct")
			}
			return structType.Fields
		}
	}
	return nil
}

func assertSnapshotFields(t *testing.T, snapshotFields *ast.FieldList) {
	t.Helper()
	if snapshotFields == nil || len(snapshotFields.List) == 0 {
		t.Fatal("Snapshot fields were not found")
	}
	for _, field := range snapshotFields.List {
		for _, name := range field.Names {
			if name.IsExported() {
				t.Fatalf("Snapshot exposes mutable field %s", name.Name)
			}
		}
		if _, forbidden := field.Type.(*ast.InterfaceType); forbidden {
			t.Fatal("Snapshot contains an interface capability")
		}
		if _, forbidden := field.Type.(*ast.FuncType); forbidden {
			t.Fatal("Snapshot contains a function capability")
		}
	}
}

func TestProcessProbeTypesAndSSACompile(t *testing.T) {
	root := processProbeRoot(t)
	config := &packages.Config{Mode: packages.LoadSyntax, Dir: root}
	loaded, err := packages.Load(config, ".")
	if err != nil {
		t.Fatalf("load processprobe: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded %d processprobe packages", len(loaded))
	}
	pkg := loaded[0]
	if len(pkg.Errors) != 0 {
		t.Fatalf("processprobe type errors: %v", pkg.Errors)
	}
	if pkg.Types == nil || pkg.TypesInfo == nil {
		t.Fatal("processprobe go/types evidence is missing")
	}
	program := ssa.NewProgram(pkg.Fset, ssa.SanityCheckFunctions)
	for _, imported := range pkg.Types.Imports() {
		program.CreatePackage(imported, nil, nil, true)
	}
	ssaPackage := program.CreatePackage(pkg.Types, pkg.Syntax, pkg.TypesInfo, true)
	ssaPackage.Build()
	if len(ssaPackage.Members) == 0 {
		t.Fatal("processprobe SSA package is empty")
	}
}

func TestProcessProbeCapabilityCallsAreTypedAndAllowlisted(t *testing.T) {
	root := processProbeRoot(t)
	if err := analyzeCapabilityPackage(t, root); err != nil {
		t.Fatal(err)
	}
}

func TestProcessProbeCapabilityGuardRejectsMutationFixtures(t *testing.T) {
	fixtures := map[string]string{
		"kill_function_value_alias": `package fixture

import "syscall"

func Run(pid int) {
	killFn := syscall.Kill
	killFn(pid, 9)
}
`,
		"kill_import_alias": `package fixture

import unix "syscall"

func Run(pid int) {
	unix.Kill(pid, 9)
}
`,
		"exec_function_value_alias": `package fixture

import (
	"context"
	"os/exec"
)

func Run(ctx context.Context) {
	command := "sh"
	run := exec.CommandContext
	run(ctx, command)
}
`,
	}
	for name, source := range fixtures {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.26\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := analyzeCapabilityPackage(t, root); err == nil {
				t.Fatal("mutation fixture unexpectedly passed the capability guard")
			}
		})
	}
}

func analyzeCapabilityPackage(t *testing.T, root string) error {
	t.Helper()
	config := &packages.Config{
		Mode: packages.LoadAllSyntax,
		Dir:  root,
	}
	loaded, err := packages.Load(config, ".")
	if err != nil {
		return fmt.Errorf("load capability package: %w", err)
	}
	if len(loaded) != 1 {
		return fmt.Errorf("loaded %d capability packages", len(loaded))
	}
	pkg := loaded[0]
	if len(pkg.Errors) != 0 {
		return fmt.Errorf("capability package type errors: %v", pkg.Errors)
	}
	program, ssaPackages := ssautil.AllPackages(loaded, ssa.SanityCheckFunctions)
	program.Build()
	return validateCapabilitySSAPackages(program, ssaPackages)
}

func validateCapabilitySSAPackages(program *ssa.Program, packages []*ssa.Package) error {
	for _, ssaPackage := range packages {
		if err := validateCapabilitySSAPackage(program, ssaPackage); err != nil {
			return err
		}
	}
	return nil
}

func validateCapabilitySSAPackage(program *ssa.Program, ssaPackage *ssa.Package) error {
	if ssaPackage == nil {
		return nil
	}
	for function := range ssautil.AllFunctions(program) {
		if function.Package() != ssaPackage {
			continue
		}
		if err := validateCapabilitySSAFunction(function); err != nil {
			return err
		}
	}
	return nil
}

func validateCapabilitySSAFunction(function *ssa.Function) error {
	for _, block := range function.Blocks {
		if err := validateCapabilitySSABlock(function, block); err != nil {
			return err
		}
	}
	return nil
}

func validateCapabilitySSABlock(function *ssa.Function, block *ssa.BasicBlock) error {
	for _, instruction := range block.Instrs {
		call, ok := instruction.(interface{ Common() *ssa.CallCommon })
		if !ok {
			continue
		}
		if err := validateStaticCapabilityCall(call.Common()); err != nil {
			return fmt.Errorf("%s: %w", function.String(), err)
		}
	}
	return nil
}

func validateStaticCapabilityCall(common *ssa.CallCommon) error {
	callee := staticCallee(common)
	if callee == nil {
		return nil
	}
	object, _ := callee.Object().(*types.Func)
	if object == nil || object.Pkg() == nil {
		return nil
	}
	return validateCapabilityTarget(object, common.Args)
}

func staticCallee(common *ssa.CallCommon) *ssa.Function {
	if common == nil {
		return nil
	}
	if callee := common.StaticCallee(); callee != nil {
		return callee
	}
	function, _ := common.Value.(*ssa.Function)
	return function
}

func validateCapabilityTarget(object *types.Func, args []ssa.Value) error {
	pkgPath := object.Pkg().Path()
	name := object.Name()
	if strings.Contains(pkgPath, "/hiddenexec") || strings.Contains(pkgPath, "/multilsp") {
		return fmt.Errorf("forbidden helper package %s", pkgPath)
	}
	switch pkgPath {
	case "syscall":
		if name == "Kill" {
			return validateSignalZero(args)
		}
	case "os/exec":
		if name == "CommandContext" {
			return validateStaticPS(args)
		}
	}
	if restrictedCapabilityPackage(pkgPath) && forbiddenCapabilityName(name) {
		return fmt.Errorf("forbidden process capability %s.%s", pkgPath, name)
	}
	return nil
}

func validateSignalZero(args []ssa.Value) error {
	if len(args) != 2 {
		return errors.New("syscall.Kill must use constant signal zero")
	}
	if !ssaZero(args[1]) {
		return errors.New("syscall.Kill must use constant signal zero")
	}
	return nil
}

func validateStaticPS(args []ssa.Value) error {
	if len(args) < 2 {
		return errors.New("exec.CommandContext must use constant command ps")
	}
	if !ssaString(args[1], "ps") {
		return errors.New("exec.CommandContext must use constant command ps")
	}
	return nil
}

func restrictedCapabilityPackage(pkgPath string) bool {
	return pkgPath == "os/exec" || pkgPath == "syscall" || strings.HasSuffix(pkgPath, "/unix") || pkgPath == "os" || pkgPath == "golang.org/x/sys/windows"
}

func forbiddenCapabilityName(name string) bool {
	switch name {
	case "Command", "CommandContext", "Kill", "Signal", "TerminateJobObject":
		return true
	default:
		return false
	}
}

func ssaZero(value ssa.Value) bool {
	constantValue, ok := value.(*ssa.Const)
	return ok && constantValue.Value != nil && constant.Sign(constantValue.Value) == 0
}

func ssaString(value ssa.Value, expected string) bool {
	constantValue, ok := value.(*ssa.Const)
	return ok && constantValue.Value != nil && constant.StringVal(constantValue.Value) == expected
}

func processProbeRoot(t *testing.T) string {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(current)
}

func isZeroLiteral(expression ast.Expr) bool {
	literal, ok := expression.(*ast.BasicLit)
	return ok && literal.Value == "0"
}

func isAllowlistedProcessQuery(expression ast.Expr) bool {
	literal, ok := expression.(*ast.BasicLit)
	if ok {
		return strings.Trim(literal.Value, "\"") == "ps"
	}
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Name == "processQueryBinary"
}
