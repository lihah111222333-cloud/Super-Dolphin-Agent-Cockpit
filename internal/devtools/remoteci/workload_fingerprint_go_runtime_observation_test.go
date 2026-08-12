package remoteci

import (
	"fmt"
	"path"
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

// TestGoProductionRuntimeObservationAcceptsBoundKernelPaths 验证固定 runner 内核观察不被误判为候选源码越界。
func TestGoProductionRuntimeObservationAcceptsBoundKernelPaths(t *testing.T) {
	for _, staticPath := range []string{"/proc/self/mountinfo", "/proc/sys/kernel/random/boot_id"} {
		t.Run(path.Base(staticPath), func(t *testing.T) {
			snapshot := testExactGoTestDigestSnapshot("")
			production := fmt.Sprintf("package fixture\nimport \"os\"\nfunc readFixture() { _, _ = os.ReadFile(%q) }\n", staticPath)
			testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(production))
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
			if len(selected) != 0 {
				t.Fatalf("bound kernel observation selected candidate paths: %v", selected)
			}
		})
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
		wantScope  remoteGoTestScope
	}{
		"process": {production: `package fixture
import "os/exec"
func run() { _ = exec.Command("sh", "testdata/script.sh").Run() }`, call: "run()", wantScope: remoteGoTestScopeTree},
		"reflection": {production: `package fixture
import "reflect"
func run() { _ = reflect.ValueOf(1) }`, call: "run()", wantScope: remoteGoTestScopeTree},
		"external_call": {production: `package fixture
import "example.com/external"
func run() { external.Read() }`, call: "run()", wantScope: remoteGoTestScopeTree},
		"function_value": {production: `package fixture
var callback = run
func invoke() { callback() }
func run() {}`, call: "invoke()", wantScope: remoteGoTestScopeCompileClosure},
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
			if scope != test.wantScope {
				t.Fatalf("runtime observation scope = %v, want %v", scope, test.wantScope)
			}
		})
	}
}

func TestGoProductionRuntimeObservationDoesNotTreatSelectedTestHelperAsDynamic(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { selectedTestHelper() }
func selectedTestHelper() {}
`))
	root := runtimeObservationTestDeclaration(t, snapshot)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, make(map[string]remoteGitTreeEntry), remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
}

func TestGoProductionRuntimeObservationAcceptsPureTypeConversions(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
func run() { _ = string([]byte("fixture")); _ = rune('x') }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { run() }
`))
	root := runtimeObservationTestDeclaration(t, snapshot)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, make(map[string]remoteGitTreeEntry), remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
}

func TestGoProductionRuntimeObservationKeepsDeclarativeFxOptionsSelectorScoped(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "go.uber.org/fx"
func run() { _ = fx.Module("fixture", fx.Provide(func() string { return "ok" })) }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { run() }
`))
	root := runtimeObservationTestDeclaration(t, snapshot)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, make(map[string]remoteGitTreeEntry), remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
}

func TestGoProductionRuntimeObservationKeepsAuditedUUIDGenerationSelectorScoped(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "github.com/google/uuid"
func run() { _ = uuid.NewString() }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { run() }
`))
	root := runtimeObservationTestDeclaration(t, snapshot)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, make(map[string]remoteGitTreeEntry), remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
}

func TestGoProductionRuntimeObservationUsesBoundEnvironmentScope(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "os"
func run() { _, _ = os.LookupEnv("FIXTURE_INPUT"); _ = os.Args }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { run() }
`))
	root := runtimeObservationTestDeclaration(t, snapshot)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, make(map[string]remoteGitTreeEntry), remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
}

func TestGoProductionRuntimeObservationEnvironmentDerivedFileReadFailsClosed(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "os"
func run() { _, _ = os.ReadFile(os.Getenv("FIXTURE_INPUT")) }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { run() }
`))
	root := runtimeObservationTestDeclaration(t, snapshot)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, make(map[string]remoteGitTreeEntry), remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeTree {
		t.Fatalf("runtime observation scope = %v, want tree", scope)
	}
}

func TestGoProductionRuntimeObservationAcceptsBoundConcurrencyMethods(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import (
	"encoding/json"
	"runtime"
	"strings"
	"sync"
)
type state struct{ mu sync.Mutex }
func run() {
	var current state
	current.mu.Lock()
	current.mu.Unlock()
	decoder := json.NewDecoder(strings.NewReader("{}"))
	decoder.UseNumber()
	_ = decoder.Decode(&map[string]any{})
	frames := runtime.CallersFrames(nil)
	_, _ = frames.Next()
}
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { run() }
`))
	root := runtimeObservationTestDeclaration(t, snapshot)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, make(map[string]remoteGitTreeEntry), remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeCompileClosure {
		t.Fatalf("runtime observation scope = %v, want compile closure", scope)
	}
}

func TestGoProductionRuntimeObservationDoesNotHideLocalMethodBehindConcurrencyName(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "os"
type state struct{}
func (state) Store() { _, _ = os.ReadFile(os.Getenv("FIXTURE_INPUT")) }
func run() { var current state; current.Store() }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { run() }
`))
	root := runtimeObservationTestDeclaration(t, snapshot)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, make(map[string]remoteGitTreeEntry), remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeTree {
		t.Fatalf("runtime observation scope = %v, want tree", scope)
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

// TestGoTestUnusedSensitiveAliasInSiblingFileStaysSelectorScoped 验证未被目标调用的敏感别名不会污染整包 PASS 身份。
func TestGoTestUnusedSensitiveAliasInSiblingFileStaysSelectorScoped(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/helper_test.go", []byte(`package fixture
import "os"
var readTestFile = os.ReadFile
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) {}
`))
	root := runtimeObservationTestDeclaration(t, snapshot)
	selected := make(map[string]remoteGitTreeEntry)
	scope, err := snapshot.addGoTestFileObservedEntriesScoped("fixture", root.file, root.declaration, selected, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoTestFileObservedEntriesScoped(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("unused sibling alias scope = %v, want selector", scope)
	}
}

func TestGoTestSensitiveAliasNameDoesNotCollideWithLocalReceiver(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/helper_test.go", []byte(`package fixture
import "os"
var t = os.ReadFile
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { t.Helper() }
`))
	root := runtimeObservationTestDeclaration(t, snapshot)
	scope, err := snapshot.addGoTestFileObservedEntriesScoped("fixture", root.file, root.declaration, make(map[string]remoteGitTreeEntry), remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoTestFileObservedEntriesScoped(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("local receiver collision scope = %v, want selector", scope)
	}
}

// TestGoProductionUnusedSensitiveAliasInSiblingFileStaysSelectorScoped 验证未被可达生产代码调用的别名不会绑定整树。
func TestGoProductionUnusedSensitiveAliasInSiblingFileStaysSelectorScoped(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/aliases.go", []byte(`package fixture
import "os"
var readTestFile = os.ReadFile
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte("package fixture\nfunc run() {}\n"))
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
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("unused production alias scope = %v, want selector", scope)
	}
}

func TestGoProductionRuntimeObservationResolvesTrackedSymlinkTarget(t *testing.T) {
	snapshot := trackedSymlinkRuntimeObservationSnapshot("v1")
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
	changed := trackedSymlinkRuntimeObservationSnapshot("v2")
	digestBefore := testExactGoTestDigest(t, snapshot, gate.GoTestTarget{Package: "fixture", Name: "TestX"})
	digestAfter := testExactGoTestDigest(t, changed, gate.GoTestTarget{Package: "fixture", Name: "TestX"})
	if digestBefore == digestAfter {
		t.Fatal("tracked symlink target mutation did not change exact test digest")
	}
}

func trackedSymlinkRuntimeObservationSnapshot(target string) *remoteGitTreeSnapshot {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "os"
func readFixture() { _, _ = os.ReadFile("testdata/link.txt") }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/target.txt", []byte(target))
	testExactGoTestDigestReplaceSymlink(snapshot, "fixture/testdata/link.txt", "target.txt")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { readFixture() }
`))
	return snapshot
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

func TestGoTestDirectEnvironmentObservationUsesBoundEnvironmentScope(t *testing.T) {
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
	if wholeTree {
		t.Fatal("direct os.Getenv observation widened the candidate source scope")
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
