package remoteci

import (
	"testing"
)

// TestGoProductionRuntimeObservationRecursesReachableHelpers 验证只递归实际可达生产 helper。
func TestGoProductionRuntimeObservationRecursesReachableHelpers(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture

import "os"

func readFixture() { _, _ = os.ReadFile("testdata/fixture.txt") }
func unreachable() { _, _ = os.ReadFile("testdata/unreachable.txt") }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture

import "testing"

func TestX(t *testing.T) { readFixture() }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/fixture.txt", []byte("fixture"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/unreachable.txt", []byte("unreachable"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/unrelated.txt", []byte("unrelated"))

	root := runtimeObservationTestDeclaration(t, snapshot)
	selected := make(map[string]remoteGitTreeEntry)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
	if _, ok := selected["fixture/testdata/fixture.txt"]; !ok {
		t.Fatalf("reachable helper omitted static asset: %#v", selected)
	}
	for _, filePath := range []string{"fixture/testdata/unreachable.txt", "fixture/testdata/unrelated.txt"} {
		if _, ok := selected[filePath]; ok {
			t.Fatalf("unreachable or unrelated asset was selected: %q", filePath)
		}
	}
}

// TestGoProductionRuntimeObservationStaticOpenIncludesAsset 验证 os.Open 的静态路径加入候选资产。
func TestGoProductionRuntimeObservationStaticOpenIncludesAsset(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture

import "os"

func readFixture() { file, _ := os.Open("testdata/fixture.txt"); _ = file }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture

import "testing"

func TestX(t *testing.T) { readFixture() }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/fixture.txt", []byte("fixture"))
	root := runtimeObservationTestDeclaration(t, snapshot)
	selected := make(map[string]remoteGitTreeEntry)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
	if _, ok := selected["fixture/testdata/fixture.txt"]; !ok {
		t.Fatal("static os.Open observation omitted candidate asset")
	}
}

// TestGoProductionRuntimeObservationCanonicalAbsoluteReadAndGlob 验证 worker source 根绝对路径映射与 glob 闭包。
func TestGoProductionRuntimeObservationCanonicalAbsoluteReadAndGlob(t *testing.T) {
	cases := map[string]string{
		"read_file": `package fixture
import "os"
func readFixture() { _, _ = os.ReadFile("/workspace/source/fixture/testdata/fixture.txt") }`,
		"glob": `package fixture
import "path/filepath"
func readFixture() { _, _ = filepath.Glob("/workspace/source/fixture/testdata/*.txt") }`,
	}
	for name, production := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot := testExactGoTestDigestSnapshot("")
			testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(production))
			testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { readFixture() }
`))
			testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/fixture.txt", []byte("fixture"))
			testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/other.txt", []byte("other"))
			root := runtimeObservationTestDeclaration(t, snapshot)
			selected := make(map[string]remoteGitTreeEntry)
			scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{})
			if err != nil {
				t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
			}
			if scope != remoteGoTestScopeSelector {
				t.Fatalf("runtime observation scope = %v, want selector", scope)
			}
			if _, ok := selected["fixture/testdata/fixture.txt"]; !ok {
				t.Fatal("canonical absolute observation omitted fixture asset")
			}
			if name == "glob" {
				if _, ok := selected["fixture/testdata/other.txt"]; !ok {
					t.Fatal("canonical absolute glob omitted matching asset")
				}
			}
		})
	}
}

// TestGoProductionRuntimeObservationRejectsForeignAbsolutePath 验证非 canonical source 根绝对路径 fail-fast。
func TestGoProductionRuntimeObservationRejectsForeignAbsolutePath(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "os"
func readFixture() { _, _ = os.ReadFile("/tmp/fixture.txt") }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { readFixture() }
`))
	root := runtimeObservationTestDeclaration(t, snapshot)
	selected := make(map[string]remoteGitTreeEntry)
	if _, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{}); err == nil {
		t.Fatal("foreign absolute production observation did not fail fast")
	}
}

// TestGoProductionRuntimeObservationUnknownReadFailsClosed 验证未知文件路径绑定整树。
func TestGoProductionRuntimeObservationUnknownReadFailsClosed(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture

import "os"

func readFixture(name string) { _, _ = os.ReadFile(name) }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture

import "testing"

func TestX(t *testing.T) { readFixture("testdata/fixture.txt") }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/fixture.txt", []byte("fixture"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/unrelated.txt", []byte("unrelated"))
	testExactGoTestDigestReplaceFile(snapshot, "docs/unrelated.md", []byte("outside package"))

	root := runtimeObservationTestDeclaration(t, snapshot)
	selected := make(map[string]remoteGitTreeEntry)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeTree {
		t.Fatalf("runtime observation scope = %v, want tree", scope)
	}
}

// TestGoProductionRuntimeObservationFailsClosedForProcessEnvironmentReflectionAndFunctionValues 验证动态输入绑定整树。
func TestGoProductionRuntimeObservationFailsClosedForProcessEnvironmentReflectionAndFunctionValues(t *testing.T) {
	cases := map[string]struct {
		production string
		call       string
	}{
		"process": {production: `package fixture
import "os/exec"
func run() { _ = exec.Command("sh", "testdata/script.sh").Run() }`, call: "run()"},
		"environment": {production: `package fixture
import "os"
func run() { _ = os.Getenv("FIXTURE_INPUT") }`, call: "run()"},
		"environment_variable": {production: `package fixture
import "os"
func run() { _ = os.Args }`, call: "run()"},
		"reflection": {production: `package fixture
import "reflect"
func run() { _ = reflect.ValueOf(1) }`, call: "run()"},
		"external_call": {production: `package fixture
import "example.com/external"
func run() { external.Read() }`, call: "run()"},
		"function_value": {production: `package fixture
var callback = run
func invoke() { callback() }
func run() {}`, call: "invoke()"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot := testExactGoTestDigestSnapshot("")
			testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(test.production))
			testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { `+test.call+` }
`))
			root := runtimeObservationTestDeclaration(t, snapshot)
			selected := make(map[string]remoteGitTreeEntry)
			scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{})
			if err != nil {
				t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
			}
			if scope != remoteGoTestScopeTree {
				t.Fatalf("runtime observation scope = %v, want tree", scope)
			}
		})
	}
}

// TestGoProductionRuntimeObservationRecursesImportedLocalPackage 验证本地导入包的 reachable helper 递归。
func TestGoProductionRuntimeObservationRecursesImportedLocalPackage(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "support/support.go", []byte(`package support
import "os"
func ReadFixture() { _, _ = os.ReadFile("testdata/fixture.txt") }
`))
	testExactGoTestDigestReplaceFile(snapshot, "support/testdata/fixture.txt", []byte("support"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/fixture.txt", []byte("fixture"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import (
	"testing"
	"example.com/fixture/support"
)
func TestX(t *testing.T) { support.ReadFixture() }
`))
	root := runtimeObservationTestDeclaration(t, snapshot)
	selected := make(map[string]remoteGitTreeEntry)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
	if _, ok := selected["fixture/testdata/fixture.txt"]; !ok {
		t.Fatal("imported helper static observation omitted target package asset")
	}
}

// runtimeObservationTestDeclaration 取得夹具中唯一的目标测试声明。
func runtimeObservationTestDeclaration(t *testing.T, snapshot *remoteGitTreeSnapshot) remoteGoTestDeclaration {
	t.Helper()
	files, declarations, fallback := snapshot.remoteGoTestDeclarations("fixture", remoteGoBuildProfile{})
	if fallback || len(files) == 0 || len(declarations["TestX"]) != 1 {
		t.Fatalf("remoteGoTestDeclarations() fallback=%v files=%d declarations=%d", fallback, len(files), len(declarations["TestX"]))
	}
	return declarations["TestX"][0]
}
