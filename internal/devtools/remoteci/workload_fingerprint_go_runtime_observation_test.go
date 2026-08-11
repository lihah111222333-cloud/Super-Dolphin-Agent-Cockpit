package remoteci

import (
	"crypto/sha1"
	"fmt"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
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

func TestGoProductionRuntimeObservationIncludesBlankImportedPackageInitializers(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "support/support.go", []byte(`package support
import "os"
var packageInput = readFixture()
func readFixture() string { _, _ = os.ReadFile("../support/testdata/imported-init.txt"); return "ok" }
`))
	testExactGoTestDigestReplaceFile(snapshot, "support/testdata/imported-init.txt", []byte("imported-init"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import (
	_ "example.com/fixture/support"
	"testing"
)
func TestX(t *testing.T) {}
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
	if _, ok := selected["support/testdata/imported-init.txt"]; !ok {
		t.Fatal("blank-imported package initializer observation omitted")
	}
}

func TestGoProductionRuntimeObservationRejectsEscapingRelativePath(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "os"
func readFixture() { _, _ = os.ReadFile("../../../outside/config") }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { readFixture() }
`))
	root := runtimeObservationTestDeclaration(t, snapshot)
	selected := make(map[string]remoteGitTreeEntry)
	if _, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{}); err == nil {
		t.Fatal("escaping relative production observation did not fail closed")
	}
}

func TestGoProductionRuntimeObservationDotImportsFailClosed(t *testing.T) {
	cases := map[string]string{
		"os": `package fixture
import . "os"
func run() { _ = Getenv("INPUT") }
`,
		"os_exec": `package fixture
import . "os/exec"
func run() { _ = Command("sh") }
`,
		"syscall": `package fixture
import . "syscall"
func run() { _ = Getpid() }
`,
		"x_sys_unix": `package fixture
import . "golang.org/x/sys/unix"
func run() { _ = Getpid() }
`,
		"reflect": `package fixture
import . "reflect"
func run() { _ = ValueOf(1) }
`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot := testExactGoTestDigestSnapshot("")
			testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(source))
			testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { run() }
`))
			root := runtimeObservationTestDeclaration(t, snapshot)
			selected := make(map[string]remoteGitTreeEntry)
			scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{})
			if err != nil {
				t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
			}
			if scope != remoteGoTestScopeTree {
				t.Fatalf("dot-import %s scope = %v, want tree", name, scope)
			}
		})
	}
}

func TestGoTestDotImportsFailClosed(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import (
	. "os"
	"testing"
)
func TestX(t *testing.T) { _ = Getenv("INPUT") }
`))
	files, declarations, fallback := snapshot.remoteGoTestDeclarations("fixture", remoteGoBuildProfile{})
	if fallback || len(declarations["TestX"]) != 1 {
		t.Fatalf("remoteGoTestDeclarations() fallback=%v declarations=%d", fallback, len(declarations["TestX"]))
	}
	selected := make(map[string]remoteGitTreeEntry)
	wholeTree, err := snapshot.addGoTestFileObservedEntries("fixture", files[0].file, declarations["TestX"][0].declaration, selected)
	if err != nil {
		t.Fatalf("addGoTestFileObservedEntries(): %v", err)
	}
	if !wholeTree {
		t.Fatal("dot-import os test observation was not bound to whole tree")
	}
}

func TestGoProductionRuntimeObservationSensitiveAliasesFailClosedAcrossFiles(t *testing.T) {
	cases := map[string]struct {
		aliasSource string
		call        string
	}{
		"read_file": {aliasSource: `package fixture
import "os"
var read = os.ReadFile
`, call: `_, _ = read("testdata/fixture.txt")`},
		"nested_function_value": {aliasSource: `package fixture
import "os"
var readers = struct{ read func(string) ([]byte, error) }{read: os.ReadFile}
`, call: `_, _ = readers.read("testdata/fixture.txt")`},
		"process": {aliasSource: `package fixture
import "os/exec"
var command = exec.Command
`, call: `_ = command("sh")`},
		"chdir": {aliasSource: `package fixture
import "os"
var chdir = os.Chdir
`, call: `_ = chdir(".")`},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot := testExactGoTestDigestSnapshot("")
			testExactGoTestDigestReplaceFile(snapshot, "fixture/aliases.go", []byte(test.aliasSource))
			testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte("package fixture\nfunc run() { "+test.call+" }\n"))
			testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { run() }
`))
			root := runtimeObservationTestDeclaration(t, snapshot)
			selected := make(map[string]remoteGitTreeEntry)
			scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{})
			if err != nil {
				t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
			}
			if scope != remoteGoTestScopeTree {
				t.Fatalf("sensitive alias %s scope = %v, want tree", name, scope)
			}
		})
	}
}

func TestGoTestSensitiveAliasDeclaredInSiblingFileFailsClosed(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/helper_test.go", []byte(`package fixture
import "os"
var readTestFile = os.ReadFile
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { _, _ = readTestFile("testdata/fixture.txt") }
`))
	files, declarations, fallback := snapshot.remoteGoTestDeclarations("fixture", remoteGoBuildProfile{})
	if fallback || len(declarations["TestX"]) != 1 {
		t.Fatalf("remoteGoTestDeclarations() fallback=%v declarations=%d", fallback, len(declarations["TestX"]))
	}
	selected := make(map[string]remoteGitTreeEntry)
	wholeTree, err := snapshot.addGoTestFileObservedEntries("fixture", files[0].file, declarations["TestX"][0].declaration, selected)
	if err != nil {
		t.Fatalf("addGoTestFileObservedEntries(): %v", err)
	}
	if !wholeTree {
		t.Fatal("sibling test-file sensitive alias was not bound to whole tree")
	}
}

func TestGoProductionRuntimeObservationResolvesTrackedSymlinkTarget(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "os"
func readFixture() { _, _ = os.ReadFile("testdata/link.txt") }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/target.txt", []byte("v1"))
	testExactGoTestDigestReplaceSymlink(snapshot, "fixture/testdata/link.txt", "target.txt")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { readFixture() }
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
	if _, ok := selected["fixture/testdata/link.txt"]; !ok {
		t.Fatal("tracked symlink entry omitted")
	}
	if _, ok := selected["fixture/testdata/target.txt"]; !ok {
		t.Fatal("tracked symlink target omitted")
	}
	digestBefore := testExactGoTestDigest(t, snapshot, gate.GoTestTarget{Package: "fixture", Name: "TestX"})
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/target.txt", []byte("v2"))
	digestAfter := testExactGoTestDigest(t, snapshot, gate.GoTestTarget{Package: "fixture", Name: "TestX"})
	if digestBefore == digestAfter {
		t.Fatal("tracked symlink target mutation did not change exact test digest")
	}
}

func TestGoProductionRuntimeObservationRejectsSymlinkEscapeMissingAndCycle(t *testing.T) {
	cases := map[string]struct {
		links map[string]string
	}{
		"escape":  {links: map[string]string{"fixture/testdata/link.txt": "../../outside.txt"}},
		"missing": {links: map[string]string{"fixture/testdata/link.txt": "missing.txt"}},
		"cycle": {links: map[string]string{
			"fixture/testdata/link-a.txt": "link-b.txt",
			"fixture/testdata/link-b.txt": "link-a.txt",
		}},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot := testExactGoTestDigestSnapshot("")
			readPath := "testdata/link.txt"
			if name == "cycle" {
				readPath = "testdata/link-a.txt"
			}
			testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "os"
func readFixture() { _, _ = os.ReadFile("`+readPath+`") }
`))
			for filePath, target := range test.links {
				testExactGoTestDigestReplaceSymlink(snapshot, filePath, target)
			}
			testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { readFixture() }
`))
			root := runtimeObservationTestDeclaration(t, snapshot)
			selected := make(map[string]remoteGitTreeEntry)
			if _, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{}); err == nil {
				t.Fatal("invalid tracked symlink was accepted")
			}
		})
	}
}

func TestGoProductionRuntimeObservationIncludesPackageInitializers(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "os"
var packageInput = readFixture()
func init() { _, _ = os.ReadFile("testdata/init.txt") }
func readFixture() string { return "ok" }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { _ = packageInput }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/init.txt", []byte("init"))
	root := runtimeObservationTestDeclaration(t, snapshot)
	selected := make(map[string]remoteGitTreeEntry)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
	if _, ok := selected["fixture/testdata/init.txt"]; !ok {
		t.Fatal("package init static observation omitted")
	}
}

func TestGoTestDirectEnvironmentObservationFailsClosed(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import (
	"os"
	"testing"
)
func TestX(t *testing.T) { _ = os.Getenv("INPUT") }
`))
	files, declarations, fallback := snapshot.remoteGoTestDeclarations("fixture", remoteGoBuildProfile{})
	if fallback || len(declarations["TestX"]) != 1 {
		t.Fatalf("remoteGoTestDeclarations() fallback=%v declarations=%d", fallback, len(declarations["TestX"]))
	}
	selected := make(map[string]remoteGitTreeEntry)
	wholeTree, err := snapshot.addGoTestFileObservedEntries("fixture", files[0].file, declarations["TestX"][0].declaration, selected)
	if err != nil {
		t.Fatalf("addGoTestFileObservedEntries(): %v", err)
	}
	if !wholeTree {
		t.Fatal("direct os.Getenv observation was not bound to whole tree")
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

func testExactGoTestDigestReplaceSymlink(snapshot *remoteGitTreeSnapshot, filePath, target string) {
	sum := sha1.Sum([]byte(target))
	entry := remoteGitTreeEntry{mode: "120000", kind: "blob", objectID: fmt.Sprintf("%x", sum), path: filePath}
	snapshot.byPath[filePath] = entry
	snapshot.rememberRemoteGitBlob(entry.objectID, []byte(target))
	for index, candidate := range snapshot.entries {
		if candidate.path == filePath {
			snapshot.entries[index] = entry
			delete(snapshot.goSources, filePath)
			return
		}
	}
	snapshot.entries = append(snapshot.entries, entry)
}
