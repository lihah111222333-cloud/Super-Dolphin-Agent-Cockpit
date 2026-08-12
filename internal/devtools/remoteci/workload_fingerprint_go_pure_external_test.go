package remoteci

import "testing"

func TestGoProductionRuntimeObservationKeepsFxSelfSelectorScoped(t *testing.T) {
	assertGoProductionRuntimeSelectorScope(t, `package fixture
import "go.uber.org/fx"
func run() { _ = fx.Annotate(func() string { return "ok" }, fx.As(fx.Self())) }
`)
}

func TestGoProductionRuntimeObservationKeepsPureReflectionSelectorScoped(t *testing.T) {
	assertGoProductionRuntimeSelectorScope(t, `package fixture
import "reflect"
func run() { _ = reflect.TypeFor[string](); _ = reflect.TypeOf("value"); _ = reflect.DeepEqual("a", "b") }
`)
}

func TestGoProductionRuntimeObservationAcceptsLocalNamedTypeConversion(t *testing.T) {
	assertGoProductionRuntimeSelectorScope(t, `package fixture
type Service string
func run() { _ = (Service)("value") }
`)
}

func TestGoProductionRuntimeObservationResolvesStandardLibrarySelectorChain(t *testing.T) {
	assertGoProductionRuntimeSelectorScope(t, `package fixture
import (
	"encoding/base64"
	"os"
)
func run() { _ = base64.RawURLEncoding.Strict().EncodeToString([]byte("value")); _, _ = os.Stderr.WriteString("message") }
`)
}

func TestGoProductionRuntimeObservationRecursesImportedReceiverMethod(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "support/support.go", []byte("package support\nimport \"os\"\ntype Reader struct{}\nfunc (Reader) Read() { _, _ = os.ReadFile(\"testdata/input.txt\") }\n"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/input.txt", []byte("input"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte("package fixture\nimport \"example.com/fixture/support\"\nfunc run() { support.Reader{}.Read() }\n"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte("package fixture\nimport \"testing\"\nfunc TestX(t *testing.T) { run() }\n"))
	root := runtimeObservationTestDeclaration(t, snapshot)
	selected := make(map[string]remoteGitTreeEntry)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
	if _, ok := selected["fixture/testdata/input.txt"]; !ok {
		t.Fatal("imported receiver method observation omitted target asset")
	}
}

func TestGoProductionRuntimeObservationAcceptsImportedValueMethod(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "support/support.go", []byte("package support\nimport \"time\"\nvar Duration = time.Second\n"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte("package fixture\nimport \"example.com/fixture/support\"\nfunc run() { _ = support.Duration.Milliseconds() }\n"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte("package fixture\nimport \"testing\"\nfunc TestX(t *testing.T) { run() }\n"))
	root := runtimeObservationTestDeclaration(t, snapshot)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, make(map[string]remoteGitTreeEntry), remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
}

func TestGoProductionRuntimeObservationRecursesLocalGenericCall(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte("package fixture\nimport \"os\"\nfunc read[T any]() { _, _ = os.ReadFile(\"testdata/input.txt\") }\nfunc run() { read[string]() }\n"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/input.txt", []byte("input"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte("package fixture\nimport \"testing\"\nfunc TestX(t *testing.T) { run() }\n"))
	root := runtimeObservationTestDeclaration(t, snapshot)
	selected := make(map[string]remoteGitTreeEntry)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
	if _, ok := selected["fixture/testdata/input.txt"]; !ok {
		t.Fatal("local generic helper observation omitted target asset")
	}
}

func TestGoProductionRuntimeObservationResolvesExplicitRangeCallbacks(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "os"
func readA() error { _, err := os.ReadFile("testdata/a.txt"); return err }
func readB() error { _, err := os.ReadFile("testdata/b.txt"); return err }
func run() error {
	for _, validate := range []func() error{readA, readB} {
		if err := validate(); err != nil { return err }
	}
	return nil
}
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/a.txt", []byte("a"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/b.txt", []byte("b"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte("package fixture\nimport \"testing\"\nfunc TestX(t *testing.T) { _ = run() }\n"))
	root := runtimeObservationTestDeclaration(t, snapshot)
	selected := make(map[string]remoteGitTreeEntry)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
	for _, path := range []string{"fixture/testdata/a.txt", "fixture/testdata/b.txt"} {
		if _, ok := selected[path]; !ok {
			t.Fatalf("range callback observation omitted %s", path)
		}
	}
}

func assertGoProductionRuntimeSelectorScope(t *testing.T, production string) {
	t.Helper()
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(production))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte("package fixture\nimport \"testing\"\nfunc TestX(t *testing.T) { run() }\n"))
	root := runtimeObservationTestDeclaration(t, snapshot)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, make(map[string]remoteGitTreeEntry), remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
}
