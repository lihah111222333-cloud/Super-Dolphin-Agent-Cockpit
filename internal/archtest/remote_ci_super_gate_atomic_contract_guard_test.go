package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	gate "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestRemoteCISuperDolphinGateAtomicContract 锁定 super-dolphin-gate 的 exact
// catalog、helper、manual、copylocks 与 bounded compile-group 边界。
func TestRemoteCISuperDolphinGateAtomicContract(t *testing.T) {
	root := findRepoRoot(t)
	sources := map[string]string{
		"catalog":    readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_catalog.go")),
		"helpers":    readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/go_test_helpers.go")),
		"inventory":  readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/inventory.go")),
		"executor":   readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/executor_mapping.go")),
		"planner":    readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_compile_planning.go")),
		"validation": readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/compile_group.go")),
		"manifest":   readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_planning.go")),
	}
	assertSuperGateMarkers(t, "catalog", sources["catalog"], []string{"AtomicSuperDolphinGatePackageTarget", "func AtomicGoPackageTargets() []string", "splitAtomicGoTestTargets"})
	assertSuperGateMarkers(t, "helpers", sources["helpers"], []string{"CanonicalGoTestHelperTargets", "TestRemoteHookConcurrentProcessHelper", "IsCanonicalGoTestHelper"})
	assertSuperGateMarkers(t, "inventory", sources["inventory"], []string{"gate.IsCanonicalGoTestHelper", "inventoryGoTestTargets"})
	assertSuperGateMarkers(t, "executor", sources["executor"], []string{"IsCanonicalGoTestHelper", "canonical subprocess helper"})
	assertSuperGateMarkers(t, "planner", sources["planner"], []string{"CompileGroupMaxSelectors", "isAtomicGoPackageTarget", "AtomicSuperDolphinGatePackageTarget"})
	assertSuperGateMarkers(t, "compile validation", sources["validation"], []string{"AtomicSuperDolphinGatePackageTarget", "len(group.BatchPlan) != 1", "CompileGroupMaxSelectors"})
	assertSuperGateMarkers(t, "manifest", sources["manifest"], []string{"isBoundedAtomicCompileGroup", "CompileArtifactKey"})
}

func TestRemoteCISuperGateAtomicHelperDeclarationsMatchCanonicalOwner(t *testing.T) {
	declared := collectAtomicSourceDeclaredHelpers(t, findRepoRoot(t))
	processTargets := collectAtomicSourceProcessTargets(t, findRepoRoot(t))
	registered := gate.CanonicalGoTestHelperTargets()
	sortGoTestTargets(declared)
	sortGoTestTargets(registered)
	if !slices.Equal(declared, registered) {
		t.Fatalf("atomic source-declared helper set = %#v, canonical owner = %#v", declared, registered)
	}
	if missing := missingHelperOwners(processTargets, declared, registered); len(missing) != 0 {
		t.Fatalf("atomic subprocess helper targets without source declaration and canonical owner = %#v", missing)
	}
}

func TestAtomicHelperScannerRejectsUnmarkedProcessFixture(t *testing.T) {
	fixtureTarget := "-test." + "run=^TestUnmarked$"
	source := "package fixture\nvar _ = " + strconv.Quote(fixtureTarget) + "\n"
	file, err := parser.ParseFile(token.NewFileSet(), "fixture_test.go", source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	processTargets := collectFileProcessTargets(file, "./fixture")
	if !slices.Contains(processTargets, (gate.GoTestTarget{Package: "./fixture", Name: "TestUnmarked"})) {
		t.Fatalf("fixture process targets = %#v, want TestUnmarked", processTargets)
	}
	if missing := missingHelperOwners(processTargets, nil, nil); len(missing) != 1 || missing[0].Name != "TestUnmarked" {
		t.Fatalf("unmarked helper fixture missing owners = %#v", missing)
	}
}

func collectAtomicSourceDeclaredHelpers(t *testing.T, root string) []gate.GoTestTarget {
	t.Helper()
	var declared []gate.GoTestTarget
	for _, packageTarget := range gate.AtomicGoPackageTargets() {
		declared = append(declared, collectAtomicPackageHelpers(t, root, packageTarget)...)
	}
	sortGoTestTargets(declared)
	return declared
}

func collectAtomicPackageHelpers(t *testing.T, root, packageTarget string) []gate.GoTestTarget {
	t.Helper()
	directory := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(packageTarget, "./")))
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read atomic package %q: %v", packageTarget, err)
	}
	var declared []gate.GoTestTarget
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		declared = append(declared, collectAtomicFileHelpers(t, directory, packageTarget, entry.Name())...)
	}
	return declared
}

func collectAtomicSourceProcessTargets(t *testing.T, root string) []gate.GoTestTarget {
	t.Helper()
	seen := make(map[gate.GoTestTarget]struct{})
	for _, packageTarget := range gate.AtomicGoPackageTargets() {
		for _, target := range collectAtomicPackageProcessTargets(t, root, packageTarget) {
			seen[target] = struct{}{}
		}
	}
	result := make([]gate.GoTestTarget, 0, len(seen))
	for target := range seen {
		result = append(result, target)
	}
	sortGoTestTargets(result)
	return result
}

func collectAtomicPackageProcessTargets(t *testing.T, root, packageTarget string) []gate.GoTestTarget {
	t.Helper()
	directory := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(packageTarget, "./")))
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read atomic package %q: %v", packageTarget, err)
	}
	var targets []gate.GoTestTarget
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse atomic process source %s: %v", path, err)
		}
		targets = append(targets, collectFileProcessTargets(file, packageTarget)...)
	}
	return targets
}

func collectFileProcessTargets(file *ast.File, packageTarget string) []gate.GoTestTarget {
	seen := make(map[string]struct{})
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`-test\.run=\^?(Test[A-Za-z0-9_]+)\$?`),
		regexp.MustCompile(`-test\.run[[:space:]]+['"]?\^?(Test[A-Za-z0-9_]+)\$?`),
	}
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		for _, pattern := range patterns {
			for _, match := range pattern.FindAllStringSubmatch(value, -1) {
				seen[match[1]] = struct{}{}
			}
		}
		return true
	})
	result := make([]gate.GoTestTarget, 0, len(seen))
	for name := range seen {
		result = append(result, gate.GoTestTarget{Package: packageTarget, Name: name})
	}
	sortGoTestTargets(result)
	return result
}

func missingHelperOwners(processTargets, declared, registered []gate.GoTestTarget) []gate.GoTestTarget {
	declaredSet := make(map[gate.GoTestTarget]struct{}, len(declared))
	registeredSet := make(map[gate.GoTestTarget]struct{}, len(registered))
	for _, target := range declared {
		declaredSet[target] = struct{}{}
	}
	for _, target := range registered {
		registeredSet[target] = struct{}{}
	}
	missing := make([]gate.GoTestTarget, 0)
	for _, target := range processTargets {
		if _, ok := declaredSet[target]; !ok {
			missing = append(missing, target)
			continue
		}
		if _, ok := registeredSet[target]; !ok {
			missing = append(missing, target)
		}
	}
	return missing
}

func collectAtomicFileHelpers(t *testing.T, directory, packageTarget, name string) []gate.GoTestTarget {
	t.Helper()
	path := filepath.Join(directory, name)
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse atomic helper source %s: %v", path, err)
	}
	var declared []gate.GoTestTarget
	for _, declaration := range file.Decls {
		function, ok := canonicalHelperDecl(declaration)
		if !ok {
			continue
		}
		declared = append(declared, gate.GoTestTarget{Package: packageTarget, Name: function.Name.Name})
	}
	return declared
}

func canonicalHelperDecl(declaration ast.Decl) (*ast.FuncDecl, bool) {
	function, ok := declaration.(*ast.FuncDecl)
	if !ok || function.Recv != nil || function.Doc == nil {
		return nil, false
	}
	return function, strings.Contains(function.Doc.Text(), "super-dolphin-ci: helper")
}

func sortGoTestTargets(targets []gate.GoTestTarget) {
	sort.Slice(targets, func(left, right int) bool {
		if targets[left].Package != targets[right].Package {
			return targets[left].Package < targets[right].Package
		}
		return targets[left].Name < targets[right].Name
	})
}

func assertSuperGateMarkers(t *testing.T, owner, source string, markers []string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(source, marker) {
			t.Errorf("%s is missing marker %q", owner, marker)
		}
	}
}
