package remoteci

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestExactGoTestDigestReusesAcrossUnrelatedTreeChangesForPureStandardLibraryCalls
// 验证普通标准库调用不会把单 selector 指纹错误扩大为整棵候选树。
func TestExactGoTestDigestReusesAcrossUnrelatedTreeChangesForPureStandardLibraryCalls(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testPureStandardLibraryGoTestSnapshot("baseline")
	changed := testPureStandardLibraryGoTestSnapshot("unrelated candidate change")

	want := testExactGoTestDigest(t, baseline, target)
	if got := testExactGoTestDigest(t, changed, target); got != want {
		t.Fatalf("unrelated tree change invalidated exact selector digest: got %s want %s", got, want)
	}
}

// TestExactGoTestDigestKeepsDynamicProcessObservationTreeScoped
// 验证真正动态的进程观察仍绑定整棵候选树，避免用优化制造错误命中。
func TestExactGoTestDigestKeepsDynamicProcessObservationTreeScoped(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testDynamicProcessGoTestSnapshot("baseline")
	changed := testDynamicProcessGoTestSnapshot("unrelated candidate change")

	want := testExactGoTestDigest(t, baseline, target)
	if got := testExactGoTestDigest(t, changed, target); got == want {
		t.Fatal("dynamic process observation no longer binds the candidate tree")
	}
}

func TestExactGoTestDigestDynamicProductionScopeExcludesOtherPackageTestSources(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testDynamicProcessGoTestSnapshot("baseline")
	changed := testDynamicProcessGoTestSnapshot("baseline")
	testExactGoTestDigestReplaceFile(baseline, "other/unrelated_test.go", []byte("package other\nconst value = 1\n"))
	testExactGoTestDigestReplaceFile(changed, "other/unrelated_test.go", []byte("package other\nconst value = 2\n"))
	if want, got := testExactGoTestDigest(t, baseline, target), testExactGoTestDigest(t, changed, target); got != want {
		t.Fatalf("other package test-only change invalidated dynamic production scope: got %s want %s", got, want)
	}
}

func TestExactGoTestDigestDynamicProductionScopeKeepsTargetPackageTests(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testDynamicProcessGoTestSnapshot("baseline")
	changed := testDynamicProcessGoTestSnapshot("baseline")
	testExactGoTestDigestReplaceFile(baseline, "fixture/unrelated_test.go", []byte("package fixture\nconst value = 1\n"))
	testExactGoTestDigestReplaceFile(changed, "fixture/unrelated_test.go", []byte("package fixture\nconst value = 2\n"))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changed, target) {
		t.Fatal("target package test-only change did not invalidate dynamic production scope")
	}
}

// TestExactGoTestDigestKeepsUnreferencedSamePackageTestBody 锁定同包测试二进制
// 的完整编译输入；未被 selector 调用的 sibling 变化也必须使旧 PASS 失效。
func TestExactGoTestDigestKeepsUnreferencedSamePackageTestBody(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testPureStandardLibraryGoTestSnapshot("same")
	changed := testPureStandardLibraryGoTestSnapshot("same")
	testExactGoTestDigestReplaceFile(baseline, "fixture/sibling_test.go", []byte("package fixture\nimport \"testing\"\nfunc TestSibling(t *testing.T) { _ = 1 }\n"))
	testExactGoTestDigestReplaceFile(changed, "fixture/sibling_test.go", []byte("package fixture\nimport \"testing\"\nfunc TestSibling(t *testing.T) { _ = 2 }\n"))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changed, target) {
		t.Fatal("unreferenced sibling test was omitted from exact selector digest")
	}
}

func TestExactGoTestDigestKeepsReferencedSamePackageHelper(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testExactGoTestDigestSnapshot("")
	changed := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(baseline, "fixture/target_test.go", []byte("package fixture\nimport \"testing\"\nfunc TestX(t *testing.T) { sharedHelper() }\n"))
	testExactGoTestDigestReplaceFile(changed, "fixture/target_test.go", []byte("package fixture\nimport \"testing\"\nfunc TestX(t *testing.T) { sharedHelper() }\n"))
	testExactGoTestDigestReplaceFile(baseline, "fixture/shared_test.go", []byte("package fixture\nfunc sharedHelper() { _ = 1 }\n"))
	testExactGoTestDigestReplaceFile(changed, "fixture/shared_test.go", []byte("package fixture\nfunc sharedHelper() { _ = 2 }\n"))
	assertExactGoTestDigestChanges(t, baseline, changed, target, "referenced sibling helper")
}

func TestExactGoTestDigestKeepsPackageInitialization(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testPureStandardLibraryGoTestSnapshot("same")
	changed := testPureStandardLibraryGoTestSnapshot("same")
	testExactGoTestDigestReplaceFile(baseline, "fixture/shared_test.go", []byte("package fixture\nvar sharedState = 1\nfunc init() { sharedState++ }\n"))
	testExactGoTestDigestReplaceFile(changed, "fixture/shared_test.go", []byte("package fixture\nvar sharedState = 2\nfunc init() { sharedState++ }\n"))
	assertExactGoTestDigestChanges(t, baseline, changed, target, "package initialization")
}

func TestExactGoTestCompileGroupKeepsUnreferencedSiblingTest(t *testing.T) {
	baseline := testPureStandardLibraryGoTestSnapshot("same")
	changed := testPureStandardLibraryGoTestSnapshot("same")
	testExactGoTestDigestReplaceFile(baseline, "fixture/sibling_test.go", []byte("package fixture\nfunc sibling() { _ = 1 }\n"))
	testExactGoTestDigestReplaceFile(changed, "fixture/sibling_test.go", []byte("package fixture\nfunc sibling() { _ = 2 }\n"))
	want, err := baseline.goPackageInputDigest(t.Context(), "./fixture", remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("baseline compile-group digest: %v", err)
	}
	got, err := changed.goPackageInputDigest(t.Context(), "./fixture", remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("changed compile-group digest: %v", err)
	}
	if got == want {
		t.Fatal("unreferenced sibling test change was omitted from compile-group identity")
	}
}

func TestExactGoTestDigestKeepsSelectedTestEmbedAsset(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testExactGoTestDigestSnapshot("")
	changed := testExactGoTestDigestSnapshot("")
	source := []byte("package fixture\nimport (\n _ \"embed\"\n \"testing\"\n)\n//go:embed testdata/selected.txt\nvar selected string\nfunc TestX(t *testing.T) { _ = selected }\n")
	testExactGoTestDigestReplaceFile(baseline, "fixture/target_test.go", source)
	testExactGoTestDigestReplaceFile(changed, "fixture/target_test.go", source)
	testExactGoTestDigestReplaceFile(baseline, "fixture/testdata/selected.txt", []byte("one"))
	testExactGoTestDigestReplaceFile(changed, "fixture/testdata/selected.txt", []byte("two"))
	assertExactGoTestDigestChanges(t, baseline, changed, target, "selected test embed asset")
}

func TestExactGoTestDigestKeepsUnreferencedProductionFunctionBody(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline, changed := productionSemanticCrossCheckSnapshots(t)
	testExactGoTestDigestReplaceFile(baseline, "fixture/main.go", []byte("package fixture\nfunc Value() int { return 1 }\nfunc Unused() int { return 1 }\n"))
	testExactGoTestDigestReplaceFile(changed, "fixture/main.go", []byte("package fixture\nfunc Value() int { return 1 }\nfunc Unused() int { return 2 }\n"))
	assertExactGoTestDigestChanges(t, baseline, changed, target, "unreferenced production compile input")
}

func TestExactGoTestDigestKeepsProductionConstantAndTypeDependencies(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline, changed := productionSemanticCrossCheckSnapshots(t)
	testExactGoTestDigestReplaceFile(baseline, "fixture/main.go", []byte("package fixture\nconst value = 1\ntype result struct { Value int }\nfunc Value() result { return result{Value: value} }\n"))
	testExactGoTestDigestReplaceFile(changed, "fixture/main.go", []byte("package fixture\nconst value = 2\ntype result struct { Value int64 }\nfunc Value() result { return result{Value: value} }\n"))
	assertExactGoTestDigestChanges(t, baseline, changed, target, "production constant/type dependencies")
}

func TestExactGoTestDigestKeepsUnreferencedImportedProductionFunction(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline, changed := productionSemanticCrossCheckSnapshots(t)
	mainSource := []byte("package fixture\nimport \"example.com/fixture/support\"\nfunc Value() int { return support.Read() }\n")
	testExactGoTestDigestReplaceFile(baseline, "fixture/main.go", mainSource)
	testExactGoTestDigestReplaceFile(changed, "fixture/main.go", mainSource)
	testExactGoTestDigestReplaceFile(baseline, "support/support.go", []byte("package support\nfunc Read() int { return 1 }\nfunc Unused() int { return 1 }\n"))
	testExactGoTestDigestReplaceFile(changed, "support/support.go", []byte("package support\nfunc Read() int { return 1 }\nfunc Unused() int { return 2 }\n"))
	assertExactGoTestDigestChanges(t, baseline, changed, target, "unreferenced imported compile input")
}

func productionSemanticCrossCheckSnapshots(t *testing.T) (*remoteGitTreeSnapshot, *remoteGitTreeSnapshot) {
	t.Helper()
	baseline := testExactGoTestDigestSnapshot("")
	changed := testExactGoTestDigestSnapshot("")
	testSource := []byte("package fixture\nimport \"testing\"\nfunc TestX(t *testing.T) { _ = Value() }\n")
	testExactGoTestDigestReplaceFile(baseline, "fixture/target_test.go", testSource)
	testExactGoTestDigestReplaceFile(changed, "fixture/target_test.go", testSource)
	return baseline, changed
}

func assertExactGoTestDigestChanges(t *testing.T, baseline, changed *remoteGitTreeSnapshot, target gate.GoTestTarget, reason string) {
	t.Helper()
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changed, target) {
		t.Fatalf("%s did not invalidate exact selector digest", reason)
	}
}

// testPureStandardLibraryGoTestSnapshot 构造只调用无仓库观察能力标准库函数的 selector。
func testPureStandardLibraryGoTestSnapshot(unrelated string) *remoteGitTreeSnapshot {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "errors"
func pureStandardLibraryCall() error { return errors.New("fixed") }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { _ = pureStandardLibraryCall() }
`))
	testExactGoTestDigestReplaceFile(snapshot, "docs/unrelated.md", []byte(unrelated))
	return snapshot
}

// testDynamicProcessGoTestSnapshot 构造必须继续保守绑定整树的动态进程 selector。
func testDynamicProcessGoTestSnapshot(unrelated string) *remoteGitTreeSnapshot {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte(`package fixture
import "os/exec"
func dynamicProcess(command string) error { return exec.Command(command).Run() }
`))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(`package fixture
import "testing"
func TestX(t *testing.T) { _ = dynamicProcess("true") }
`))
	testExactGoTestDigestReplaceFile(snapshot, "docs/unrelated.md", []byte(unrelated))
	return snapshot
}
