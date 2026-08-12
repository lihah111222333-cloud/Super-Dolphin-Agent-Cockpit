package remoteci

import (
	"context"
	"crypto/sha1"
	"fmt"
	"path"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestExactGoTestDigestIncludesUnselectedPackageTestCompileInputs 验证同包全部
// 测试编译输入都进入 selector 身份，避免复用已不对应当前测试二进制的 PASS。
func TestExactGoTestDigestIncludesUnselectedPackageTestCompileInputs(t *testing.T) {
	baseline := testExactGoTestDigestSnapshot("")
	variants := map[string]string{
		"syntax": "const unrunMarker = 1\n",
		"type":   "type unrunMarker struct{}\n",
		"import": "import _ \"example.com/fixture/support\"\n",
	}
	targets := []gate.GoTestTarget{
		{Package: "fixture", Name: "TestX"},
		{Package: "fixture", Name: "BenchmarkX"},
	}
	for _, target := range targets {
		t.Run(target.Name, func(t *testing.T) {
			want := testExactGoTestDigest(t, baseline, target)
			for name, declaration := range variants {
				t.Run(name, func(t *testing.T) {
					got := testExactGoTestDigest(t, testExactGoTestDigestSnapshot(declaration), target)
					if got == want {
						t.Fatalf("%s digest omitted unselected package test compile input", target.Name)
					}
				})
			}
		})
	}
}

func TestExactGoTestSemanticDigestCrossChecksBroadCompileMiss(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testExactGoTestDigestSnapshot("")
	siblingChanged := testExactGoTestDigestSnapshot("const siblingCompileMarker = 1\n")
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, siblingChanged, target) {
		t.Fatal("broad digest did not observe sibling compile input")
	}
	if testExactGoTestSemanticDigest(t, baseline, target) != testExactGoTestSemanticDigest(t, siblingChanged, target) {
		t.Fatal("sibling-only compile change widened selector semantic digest")
	}
	selectedChanged := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(selectedChanged, "fixture/target_test.go", []byte("package fixture\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) { t.Log(\"changed\") }\nfunc BenchmarkX(b *testing.B) {}\n"))
	if testExactGoTestSemanticDigest(t, baseline, target) == testExactGoTestSemanticDigest(t, selectedChanged, target) {
		t.Fatal("selected test change was omitted from semantic digest")
	}
}

// TestExactGoTestDigestIgnoresUnrelatedReflectImport 验证无关测试的 reflect 导入
// 不会把静态目标测试退化为全树摘要。
func TestExactGoTestDigestIgnoresUnrelatedReflectImport(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	testSource := `package fixture
import ("os"; "testing")
func TestX(t *testing.T) { _, _ = os.ReadFile("testdata/fixture.txt") }`
	baseline := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "same", "unchanged")
	testExactGoTestDigestReplaceFile(baseline, "fixture/unselected_test.go", []byte(`package fixture
import ("os/exec"; "reflect"; "testing")
func TestReflect(t *testing.T) { _ = exec.Command("sh", "testdata/script.sh").Run(); _ = reflect.DeepEqual(1, 1) }`))
	want := testExactGoTestDigest(t, baseline, target)
	changed := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "same", "unchanged")
	testExactGoTestDigestReplaceFile(changed, "fixture/unselected_test.go", []byte(`package fixture
import ("os/exec"; "reflect"; "testing")
func TestReflect(t *testing.T) { _ = exec.Command("sh", "testdata/script.sh").Run(); _ = reflect.DeepEqual(1, 1) }`))
	testExactGoTestDigestReplaceFile(changed, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", []byte("unrelated project-map change"))
	if got := testExactGoTestDigest(t, changed, target); got != want {
		t.Fatalf("unrelated reflect import widened static target digest: got %s want %s", got, want)
	}
}

// TestExactGoTestDigestIgnoresUnselectedDynamicRuntimeObservation 验证未选 sibling
// 的动态进程观察不扩散 selector 运行时闭包，但 sibling 源码仍属于编译输入。
func TestExactGoTestDigestIgnoresUnselectedDynamicRuntimeObservation(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	testSource := `package fixture
import ("os"; "testing")
func TestX(t *testing.T) { _, _ = os.ReadFile("testdata/fixture.txt") }`
	sibling := `package fixture
import ("os/exec"; "testing")
func TestSibling(t *testing.T) { command := "testdata/script.sh"; _ = exec.Command("sh", command).Run() }`
	baseline := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "same", "unchanged")
	testExactGoTestDigestReplaceFile(baseline, "fixture/unselected_test.go", []byte(sibling))
	want := testExactGoTestDigest(t, baseline, target)
	changedUnrelated := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "same", "unchanged")
	testExactGoTestDigestReplaceFile(changedUnrelated, "fixture/unselected_test.go", []byte(sibling))
	testExactGoTestDigestReplaceFile(changedUnrelated, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", []byte("unrelated project-map change"))
	if got := testExactGoTestDigest(t, changedUnrelated, target); got != want {
		t.Fatalf("unselected dynamic sibling widened selector digest: got %s want %s", got, want)
	}
	changedSibling := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "same", "unchanged")
	testExactGoTestDigestReplaceFile(changedSibling, "fixture/unselected_test.go", []byte(sibling+"\nvar siblingMarker = \"changed\"\n"))
	if got := testExactGoTestDigest(t, changedSibling, target); got == want {
		t.Fatal("unselected dynamic sibling source change was omitted from compile digest")
	}
}

// TestExactGoTestDigestFailsClosedForDynamicReflection 验证反射动态调用无法静态
// 闭合时仍绑定整个候选 tree。
func TestExactGoTestDigestFailsClosedForDynamicReflection(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	source := `package fixture
import ("os"; "reflect"; "testing")
func TestX(t *testing.T) {
	value := reflect.ValueOf(os.ReadFile)
	_, _ = value.Call([]reflect.Value{reflect.ValueOf("fixture.txt")})
}`
	baseline := testExactGoTestDigestSnapshotWithObservedFiles(source, "same", "unchanged")
	changed := testExactGoTestDigestSnapshotWithObservedFiles(source, "same", "unchanged")
	testExactGoTestDigestReplaceFile(changed, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", []byte("dynamic reflection change"))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changed, target) {
		t.Fatal("dynamic reflection observation did not bind the project map")
	}
}

// TestExactGoTestDigestFailsClosedForPackageReflectAlias 验证包级反射值别名仍按动态调用全树绑定。
func TestExactGoTestDigestFailsClosedForPackageReflectAlias(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	source := `package fixture
import ("os"; "reflect"; "testing")
var readValue = reflect.ValueOf(os.ReadFile)
func TestX(t *testing.T) { _, _ = readValue.Call([]reflect.Value{reflect.ValueOf("fixture.txt")}) }`
	baseline := testExactGoTestDigestSnapshotWithObservedFiles(source, "same", "unchanged")
	changed := testExactGoTestDigestSnapshotWithObservedFiles(source, "same", "unchanged")
	testExactGoTestDigestReplaceFile(changed, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", []byte("package alias reflection change"))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changed, target) {
		t.Fatal("package reflect alias observation did not bind the project map")
	}
}

// TestExactGoTestDigestFailsClosedForDynamicRepositoryObservations 验证动态路径和函数别名收敛到全树。
func TestExactGoTestDigestFailsClosedForDynamicRepositoryObservations(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	cases := map[string]string{
		"read_file": `package fixture
import ("os"; "path/filepath"; "testing")
func TestX(t *testing.T) { name := "fixture.txt"; _, _ = os.ReadFile(filepath.Join("testdata", name)) }`,
		"glob": `package fixture
import ("path/filepath"; "testing")
func TestX(t *testing.T) { pattern := "testdata/*"; _, _ = filepath.Glob(pattern) }`,
		"alias": `package fixture
import ("os"; "testing")
var readFile = os.ReadFile
func TestX(t *testing.T) { _, _ = readFile("testdata/fixture.txt") }`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			baseline := testExactGoTestDigestSnapshotWithObservedFiles(source, "one", "unchanged")
			changed := testExactGoTestDigestSnapshotWithObservedFiles(source, "two", "unchanged")
			if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changed, target) {
				t.Fatalf("%s fixture change reused the prior exact digest", name)
			}
		})
	}
}

// TestExactGoTestDigestFailsClosedForProcessAndCWDObservations 验证子进程和 cwd 逃逸全树闭包。
func TestExactGoTestDigestFailsClosedForProcessAndCWDObservations(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	cases := map[string]string{
		"exec_repository_script": `package fixture
import ("os/exec"; "testing")
func TestX(t *testing.T) { _ = exec.Command("sh", "testdata/script.sh").Run() }`,
		"exec_dynamic_argv": `package fixture
import ("os/exec"; "testing")
func TestX(t *testing.T) { argv := []string{"testdata/script.sh"}; _ = exec.Command("sh", argv...).Run() }`,
		"chdir_then_read": `package fixture
import ("os"; "testing")
func TestX(t *testing.T) { _ = os.Chdir("testdata"); _, _ = os.ReadFile("fixture.txt") }`,
		"process_alias": `package fixture
import ("os/exec"; "testing")
var command = exec.Command
func TestX(t *testing.T) { _ = command("sh", "testdata/script.sh").Run() }`,
		"chdir_alias": `package fixture
import ("os"; "testing")
var changeDirectory = os.Chdir
func TestX(t *testing.T) { _ = changeDirectory("testdata"); _, _ = os.ReadFile("fixture.txt") }`,
		"syscall": `package fixture
import ("syscall"; "testing")
func TestX(t *testing.T) { _, _ = syscall.ForkExec("testdata/script.sh", []string{"script.sh"}, nil) }`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			baseline := testExactGoTestDigestSnapshotWithObservedFiles(source, "same", "one")
			changed := testExactGoTestDigestSnapshotWithObservedFiles(source, "same", "two")
			if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changed, target) {
				t.Fatalf("%s reused the prior exact digest after unrelated candidate-tree input changed", name)
			}
		})
	}
}

// TestExactGoTestDigestFailsClosedForProductionHelperDynamicSource 验证生产 helper 的动态路径无法闭合时绑定全树。
func TestExactGoTestDigestFailsClosedForProductionHelperDynamicSource(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	production := `package fixture
import ("os"; "path/filepath")
func readFixture() { _ = os.Chdir("testdata"); name := "fixture.txt"; _, _ = os.ReadFile(filepath.Join(".", name)) }`
	testSource := `package fixture
import "testing"
func TestX(t *testing.T) { readFixture() }`
	baseline := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "same", "unchanged")
	testExactGoTestDigestReplaceFile(baseline, "fixture/main.go", []byte(production))
	unrelated := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "same", "unchanged")
	testExactGoTestDigestReplaceFile(unrelated, "fixture/main.go", []byte(production))
	testExactGoTestDigestReplaceFile(unrelated, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", []byte("unrelated project-map change"))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, unrelated, target) {
		t.Fatal("production helper dynamic path did not bind the project map")
	}
	changedProduction := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "same", "unchanged")
	testExactGoTestDigestReplaceFile(changedProduction, "fixture/main.go", []byte(production+"\nvar productionMarker = \"changed\"\n"))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changedProduction, target) {
		t.Fatal("production helper source change was omitted from the exact digest")
	}
}

// TestExactGoTestDigestFailsClosedForProductionHelperProcessSource 验证生产 helper 的子进程路径无法闭合时绑定全树。
func TestExactGoTestDigestFailsClosedForProductionHelperProcessSource(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	production := `package fixture
import "os/exec"
func runFixtureScript() { _ = exec.Command("sh", "testdata/script.sh").Run() }`
	testSource := `package fixture
import "testing"
func TestX(t *testing.T) { runFixtureScript() }`
	baseline := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "same", "unchanged")
	testExactGoTestDigestReplaceFile(baseline, "fixture/main.go", []byte(production))
	unrelated := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "same", "unchanged")
	testExactGoTestDigestReplaceFile(unrelated, "fixture/main.go", []byte(production))
	testExactGoTestDigestReplaceFile(unrelated, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", []byte("unrelated project-map change"))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, unrelated, target) {
		t.Fatal("production helper process path did not bind the project map")
	}
	changedProduction := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "same", "unchanged")
	testExactGoTestDigestReplaceFile(changedProduction, "fixture/main.go", []byte(production+"\nvar productionMarker = \"changed\"\n"))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changedProduction, target) {
		t.Fatal("production helper source change was omitted from the exact digest")
	}
	changedAsset := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "same", "unchanged")
	testExactGoTestDigestReplaceFile(changedAsset, "fixture/main.go", []byte(production))
	testExactGoTestDigestReplaceFile(changedAsset, "fixture/testdata/script.sh", []byte("#!/bin/sh\necho changed\n"))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changedAsset, target) {
		t.Fatal("production process package asset change was omitted from the exact compile closure")
	}
}

// TestExactGoTestDigestTargetDynamicObservationIncludesProjectMap 验证目标测试本身的动态仓库观察仍绑定完整候选树。
func TestExactGoTestDigestTargetDynamicObservationIncludesProjectMap(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	source := `package fixture
import ("os"; "testing")
func TestX(t *testing.T) { name := "fixture.txt"; _, _ = os.ReadFile(name) }`
	baseline := testExactGoTestDigestSnapshotWithObservedFiles(source, "same", "unchanged")
	changed := testExactGoTestDigestSnapshotWithObservedFiles(source, "same", "unchanged")
	testExactGoTestDigestReplaceFile(changed, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", []byte("dynamic target observation change"))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changed, target) {
		t.Fatal("target test dynamic observation did not bind the project map")
	}
}

// TestExactGoTestDigestBindsImportedProductionPackageAssets 验证目标包和 imported helper 包的非 Go 资产属于 exact 编译闭包。
func TestExactGoTestDigestBindsImportedProductionPackageAssets(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	production := `package support
import "os"
func ReadFixture() { _, _ = os.ReadFile("testdata/fixture.txt") }`
	testSource := `package fixture
import ("testing"; "example.com/fixture/support")
func TestX(t *testing.T) { support.ReadFixture() }`
	baseline := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "one", "unchanged")
	testExactGoTestDigestReplaceFile(baseline, "support/support.go", []byte(production))
	testExactGoTestDigestReplaceFile(baseline, "support/testdata/fixture.txt", []byte("support-one"))
	changedTarget := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "two", "unchanged")
	testExactGoTestDigestReplaceFile(changedTarget, "support/support.go", []byte(production))
	testExactGoTestDigestReplaceFile(changedTarget, "support/testdata/fixture.txt", []byte("support-one"))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changedTarget, target) {
		t.Fatal("target production package asset was omitted from the exact compile closure")
	}
	changedImported := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "one", "unchanged")
	testExactGoTestDigestReplaceFile(changedImported, "support/support.go", []byte(production))
	testExactGoTestDigestReplaceFile(changedImported, "support/testdata/fixture.txt", []byte("support-two"))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changedImported, target) {
		t.Fatal("imported production package asset change was omitted from the exact compile closure")
	}
	changedProduction := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "one", "unchanged")
	testExactGoTestDigestReplaceFile(changedProduction, "support/support.go", []byte(production+"\nvar productionMarker = \"changed\"\n"))
	testExactGoTestDigestReplaceFile(changedProduction, "support/testdata/fixture.txt", []byte("support-one"))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changedProduction, target) {
		t.Fatal("imported production source change was omitted from the exact digest")
	}
}

// TestExactGoTestDigestKeepsStaticObservationPrecise 验证静态读取不受无关候选文件影响。
func TestExactGoTestDigestKeepsStaticObservationPrecise(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	source := `package fixture
import ("os"; "testing")
func TestX(t *testing.T) { _, _ = os.ReadFile("testdata/fixture.txt") }`
	snapshot := testExactGoTestDigestSnapshotWithObservedFiles(source, "same", "one")
	want := testExactGoTestDigest(t, snapshot, target)
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/unrelated.txt", []byte("two"))
	if got := testExactGoTestDigest(t, snapshot, target); got != want {
		t.Fatal("static observation included an unrelated candidate file")
	}
}

// TestExactGoTestDigestClassifiesReadDirAsTreeObservation 锁定 ReadDir 的目录语义，避免把目录路径当作文件查找。
func TestExactGoTestDigestClassifiesReadDirAsTreeObservation(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	source := `package fixture
import ("os"; "testing")
func TestX(t *testing.T) { _, _ = os.ReadDir(".") }`
	baseline := testExactGoTestDigestSnapshotWithObservedFiles(source, "one", "unchanged")
	want := testExactGoTestDigest(t, baseline, target)
	changedAsset := testExactGoTestDigestSnapshotWithObservedFiles(source, "two", "unchanged")
	if got := testExactGoTestDigest(t, changedAsset, target); got == want {
		t.Fatal("ReadDir tree observation omitted a changed file below the package directory")
	}
	unrelated := testExactGoTestDigestSnapshotWithObservedFiles(source, "one", "unchanged")
	testExactGoTestDigestReplaceFile(unrelated, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", []byte("unrelated project-map change"))
	if got := testExactGoTestDigest(t, unrelated, target); got != want {
		t.Fatal("ReadDir tree observation widened to an unrelated project-map file")
	}
}

// TestGoPackageDigestScopesDynamicObservationsToPackageClosure 验证整包模式的动态读取和进程调用不会扩张到无关 Git tree。
func TestGoPackageDigestScopesDynamicObservationsToPackageClosure(t *testing.T) {
	cases := []struct {
		name   string
		source string
	}{
		{
			name: "dynamic read",
			source: `package fixture
import ("os"; "testing")
func TestX(t *testing.T) { name := "fixture.txt"; _, _ = os.ReadFile(name) }`,
		},
		{
			name: "process",
			source: `package fixture
import ("os/exec"; "testing")
func TestX(t *testing.T) { _ = exec.Command("sh", "testdata/script.sh").Run() }`,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			baseline := testExactGoTestDigestSnapshotWithObservedFiles(test.source, "same", "unchanged")
			want := testGoPackageDigest(t, baseline, "./fixture")
			unrelated := testExactGoTestDigestSnapshotWithObservedFiles(test.source, "same", "unchanged")
			testExactGoTestDigestReplaceFile(unrelated, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", []byte("unrelated project-map change"))
			if got := testGoPackageDigest(t, unrelated, "./fixture"); got != want {
				t.Fatalf("dynamic %s observation widened the package digest to the project map", test.name)
			}
		})
	}
}

// TestGoPackageDigestBindsTestOnlyAndLocalDependencyInputs 验证 test-only 目标和本地生产依赖的源码、资产都属于整包编译闭包。
func TestGoPackageDigestBindsTestOnlyAndLocalDependencyInputs(t *testing.T) {
	testSource := `package fixture
import ("testing"; "example.com/fixture/support")
func TestX(t *testing.T) { support.ReadFixture() }`
	production := `package support
import "os"
func ReadFixture() { _, _ = os.ReadFile("testdata/fixture.txt") }`
	newSnapshot := func() *remoteGitTreeSnapshot {
		snapshot := testExactGoTestDigestSnapshot("")
		testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte("//go:build windows\n\npackage fixture\n"))
		testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(testSource))
		testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/package.txt", []byte("target-one"))
		testExactGoTestDigestReplaceFile(snapshot, "support/support.go", []byte(production))
		testExactGoTestDigestReplaceFile(snapshot, "support/testdata/fixture.txt", []byte("support-one"))
		return snapshot
	}
	baseline := newSnapshot()
	want := testGoPackageDigest(t, baseline, "./fixture")
	variants := []struct {
		name string
		path string
		data []byte
	}{
		{name: "test-only target asset", path: "fixture/testdata/package.txt", data: []byte("target-two")},
		{name: "local dependency asset", path: "support/testdata/fixture.txt", data: []byte("support-two")},
		{name: "local dependency source", path: "support/support.go", data: []byte(production + "\nvar sourceMarker = \"changed\"\n")},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			changed := newSnapshot()
			testExactGoTestDigestReplaceFile(changed, variant.path, variant.data)
			if got := testGoPackageDigest(t, changed, "./fixture"); got == want {
				t.Fatalf("package digest omitted %s change", variant.name)
			}
		})
	}
}

// TestGoPackageDigestRetainsStaticObservationAfterDynamic 验证同一文件中动态观察之后仍扫描并绑定显式静态路径。
func TestGoPackageDigestRetainsStaticObservationAfterDynamic(t *testing.T) {
	cases := []struct {
		name   string
		source string
	}{
		{
			name: "dynamic then static",
			source: `package fixture
import ("os"; "testing")
func TestX(t *testing.T) { name := "fixture.txt"; _, _ = os.ReadFile(name); _, _ = os.ReadFile("../docs/doc/codemap/project-map/AI_PROJECT_MAP.md") }`,
		},
		{
			name: "static then dynamic",
			source: `package fixture
import ("os"; "testing")
func TestX(t *testing.T) { _, _ = os.ReadFile("../docs/doc/codemap/project-map/AI_PROJECT_MAP.md"); name := "fixture.txt"; _, _ = os.ReadFile(name) }`,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			baseline := testExactGoTestDigestSnapshotWithObservedFiles(test.source, "same", "unchanged")
			testExactGoTestDigestReplaceFile(baseline, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", []byte("project-map-one"))
			want := testGoPackageDigest(t, baseline, "./fixture")
			changed := testExactGoTestDigestSnapshotWithObservedFiles(test.source, "same", "unchanged")
			testExactGoTestDigestReplaceFile(changed, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", []byte("project-map-two"))
			if got := testGoPackageDigest(t, changed, "./fixture"); got == want {
				t.Fatalf("static observation after/before dynamic call was omitted for %s", test.name)
			}
		})
	}
}

// TestGoEmbedWorkloadDigestsBindOnlyTrackedEmbedAssets 验证 package/exact workload
// 只绑定 go:embed 实际匹配的 asset，而不会因无关 project-map 变化退化为整树摘要。
func TestGoEmbedWorkloadDigestsBindOnlyTrackedEmbedAssets(t *testing.T) {
	tests := []struct {
		name   string
		digest func(t *testing.T, snapshot *remoteGitTreeSnapshot) string
	}{
		{
			name: "package",
			digest: func(t *testing.T, snapshot *remoteGitTreeSnapshot) string {
				digest, err := snapshot.goPackageInputDigest(context.Background(), "./fixture", remoteGoBuildProfile{})
				if err != nil {
					t.Fatalf("goPackageInputDigest(): %v", err)
				}
				return digest
			},
		},
		{
			name: "exact-test",
			digest: func(t *testing.T, snapshot *remoteGitTreeSnapshot) string {
				return testExactGoTestDigest(t, snapshot, gate.GoTestTarget{Package: "fixture", Name: "TestX"})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := testGoEmbedDigestSnapshot(t, "all:assets", "embedded-one")
			want := test.digest(t, baseline)

			unrelated := testGoEmbedDigestSnapshot(t, "all:assets", "embedded-one")
			testExactGoTestDigestReplaceFile(unrelated, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", []byte("unrelated project-map change"))
			if got := test.digest(t, unrelated); got != want {
				t.Fatalf("unrelated project-map change altered %s embed digest: got %q want %q", test.name, got, want)
			}

			assetChanged := testGoEmbedDigestSnapshot(t, "all:assets", "embedded-two")
			if got := test.digest(t, assetChanged); got == want {
				t.Fatalf("actual embed asset change did not alter %s digest: %q", test.name, got)
			}
		})
	}
}

// TestGoEmbedResolutionCacheIsScopedAndImmutable 验证同一包源码只解析一次，
// 目录、源码内容和缓存错误均保持独立，调用方修改返回 map 也不会污染缓存。
func TestGoEmbedResolutionCacheIsScopedAndImmutable(t *testing.T) {
	snapshot := testGoEmbedDigestSnapshot(t, "all:assets", "embedded-one")
	source := testGoEmbedSource("all:assets")
	selected, err := snapshot.resolveGoEmbedAssets("fixture", source)
	if err != nil {
		t.Fatalf("first resolveGoEmbedAssets(): %v", err)
	}
	selected["fixture/assets/embedded.txt"] = remoteGitTreeEntry{}
	repeated, err := snapshot.resolveGoEmbedAssets("fixture", source)
	if err != nil {
		t.Fatalf("repeated resolveGoEmbedAssets(): %v", err)
	}
	if repeated["fixture/assets/embedded.txt"].objectID == "" {
		t.Fatal("mutating the returned embed map changed the immutable cache")
	}
	if _, err := snapshot.resolveGoEmbedAssets("other", source); err == nil {
		t.Fatal("resolveGoEmbedAssets() accepted a directory-scoped unmatched pattern")
	}
	if _, err := snapshot.resolveGoEmbedAssets("other", source); err == nil {
		t.Fatal("cached unmatched go:embed error was lost")
	}
	changedSource := testGoEmbedSource("assets/embedded.txt")
	if _, err := snapshot.resolveGoEmbedAssets("fixture", changedSource); err != nil {
		t.Fatalf("changed-source resolveGoEmbedAssets(): %v", err)
	}
	computations, hits := snapshot.goEmbedResolutionStats()
	if computations != 3 || hits != 2 {
		t.Fatalf("go:embed resolution stats = computations:%d hits:%d, want 3/2", computations, hits)
	}
}

// TestGoEmbedWorkloadDigestsFailFastForInvalidOrUnmatchedPatterns 锁定 go:embed
// pattern 的非法语法与无匹配输入不能静默漏掉依赖或回退到整树。
func TestGoEmbedWorkloadDigestsFailFastForInvalidOrUnmatchedPatterns(t *testing.T) {
	for _, pattern := range []string{"../outside", "missing/*", "["} {
		t.Run(pattern, func(t *testing.T) {
			snapshot := testGoEmbedDigestSnapshot(t, pattern, "embedded-one")
			if _, err := snapshot.goPackageInputDigest(context.Background(), "./fixture", remoteGoBuildProfile{}); err == nil {
				t.Fatal("goPackageInputDigest() accepted invalid or unmatched go:embed pattern")
			}
			if _, err := snapshot.goExactTestInputDigest(context.Background(), gate.GoTestTarget{Package: "fixture", Name: "TestX"}, remoteGoBuildProfile{}); err == nil {
				t.Fatal("goExactTestInputDigest() accepted invalid or unmatched go:embed pattern")
			}
		})
	}
}

// TestGoEmbedRuntimeSeedContractAcceptsOnlyGeneratedWebDist 验证共享 resolver
// 只为 ECI runtime seed 的精确目录/pattern 放行缺失 tracked asset。
func TestGoEmbedRuntimeSeedContractAcceptsOnlyGeneratedWebDist(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	selected, err := snapshot.resolveGoEmbedAssets(remoteGoEmbedRuntimeSeedDirectory, testGoEmbedSource("all:web-dist"))
	if err != nil {
		t.Fatalf("resolveGoEmbedAssets() rejected runtime-seeded web-dist: %v", err)
	}
	if len(selected) != 0 {
		t.Fatalf("runtime-seeded web-dist unexpectedly contributed tracked assets: %#v", selected)
	}

	for _, test := range []struct {
		name      string
		directory string
		pattern   string
	}{
		{name: "unknown pattern in runtime package", directory: remoteGoEmbedRuntimeSeedDirectory, pattern: "all:missing"},
		{name: "runtime pattern in unknown package", directory: "fixture", pattern: remoteGoEmbedRuntimeSeedPattern},
		{name: "non-all runtime pattern", directory: remoteGoEmbedRuntimeSeedDirectory, pattern: "web-dist"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := snapshot.resolveGoEmbedAssets(test.directory, testGoEmbedSource(test.pattern)); err == nil {
				t.Fatalf("resolveGoEmbedAssets() accepted unknown missing embed directory=%q pattern=%q", test.directory, test.pattern)
			}
		})
	}
}

// TestGoEmbedPatternTokenizerSupportsQuotedSpaces 验证共享 resolver 保持 quoted
// pattern 的空格，并继续按 canonical all: 规则匹配嵌套与隐藏 asset。
func TestGoEmbedPatternTokenizerSupportsQuotedSpaces(t *testing.T) {
	snapshot := testGoEmbedDigestSnapshot(t, `"assets/embedded file.txt"`, "embedded-one")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/assets/embedded file.txt", []byte("embedded-one"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/assets/.hidden.txt", []byte("hidden"))
	selected, err := snapshot.resolveGoEmbedAssets("fixture", []byte(`package fixture

import "embed"

//go:embed "assets/embedded file.txt"
//go:embed all:assets
var embeddedAssets embed.FS
`))
	if err != nil {
		t.Fatalf("resolveGoEmbedAssets(): %v", err)
	}
	for _, filePath := range []string{"fixture/assets/embedded file.txt", "fixture/assets/embedded.txt", "fixture/assets/.hidden.txt"} {
		if _, ok := selected[filePath]; !ok {
			t.Fatalf("resolved embed assets omitted %q: %#v", filePath, selected)
		}
	}
}

// TestGoEmbedPatternsUseOnlyParserCommentNodes 验证字符串字面量中的伪 directive 不会参与解析，
// 而真实的 //go:embed comment node 仍然绑定 tracked asset。
func TestGoEmbedPatternsUseOnlyParserCommentNodes(t *testing.T) {
	snapshot := testGoEmbedDigestSnapshot(t, "all:assets", "embedded-one")
	cases := []struct {
		name       string
		source     []byte
		wantAssets []string
	}{
		{
			name:   "interpreted string",
			source: []byte("package fixture\n\nvar interpreted = \"//go:embed missing/*\"\n"),
		},
		{
			name:   "raw string",
			source: []byte("package fixture\n\nvar raw = `//go:embed missing/*`\n"),
		},
		{
			name:       "real comment",
			source:     testGoEmbedSource("all:assets"),
			wantAssets: []string{"fixture/assets/embedded.txt"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			selected, err := snapshot.resolveGoEmbedAssets("fixture", test.source)
			if err != nil {
				t.Fatalf("resolveGoEmbedAssets(): %v", err)
			}
			if len(selected) != len(test.wantAssets) {
				t.Fatalf("resolved assets = %#v, want paths %v", selected, test.wantAssets)
			}
			for _, filePath := range test.wantAssets {
				if _, ok := selected[filePath]; !ok {
					t.Fatalf("resolved embed assets omitted %q: %#v", filePath, selected)
				}
			}
		})
	}
}

// TestGoEmbedPatternsAcceptDeclarationFragments 验证 worker declaration fragment 仍由 AST comment nodes 解析。
func TestGoEmbedPatternsAcceptDeclarationFragments(t *testing.T) {
	snapshot := testGoEmbedDigestSnapshot(t, "all:assets", "embedded-one")
	cases := []struct {
		name       string
		source     []byte
		wantAssets []string
	}{
		{
			name:   "function raw string pseudo directive",
			source: []byte("func execute() { _ = `//go:embed missing/*` }"),
		},
		{
			name:       "var declaration real comment",
			source:     []byte("//go:embed all:assets\nvar embeddedAssets embed.FS"),
			wantAssets: []string{"fixture/assets/embedded.txt"},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			selected, err := snapshot.resolveGoEmbedAssets("fixture", test.source)
			if err != nil {
				t.Fatalf("resolveGoEmbedAssets(): %v", err)
			}
			if len(selected) != len(test.wantAssets) {
				t.Fatalf("resolved assets = %#v, want paths %v", selected, test.wantAssets)
			}
			for _, filePath := range test.wantAssets {
				if _, ok := selected[filePath]; !ok {
					t.Fatalf("resolved embed assets omitted %q: %#v", filePath, selected)
				}
			}
		})
	}
}

// TestGoEmbedPatternsRejectParseErrors 验证源码语法错误不会被当作可解析的 embed 输入。
func TestGoEmbedPatternsRejectParseErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		source []byte
	}{
		{name: "complete package syntax error", source: []byte("package fixture\nfunc")},
		{name: "expression fragment", source: []byte("fmt.Println()")},
		{name: "garbage fragment", source: []byte("not valid Go")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := remoteGoEmbedPatterns(test.source); err == nil {
				t.Fatal("remoteGoEmbedPatterns() accepted invalid Go source")
			}
		})
	}
}

// TestExactGoTestDigestHonorsLockedGoReleaseTags 验证 release tags 从远程锁定 Go 工具链导出。
func TestExactGoTestDigestHonorsLockedGoReleaseTags(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	tests := []struct {
		name       string
		constraint string
		wantChange bool
	}{
		{name: "locked_release", constraint: "go1.26 && gc && amd64.v1", wantChange: true},
		{name: "future_release", constraint: "go1.27", wantChange: false},
		{name: "unknown_custom_is_fail_closed", constraint: "go1.27 && custom", wantChange: true},
		{name: "unknown_or_is_fail_closed", constraint: "linux || custom", wantChange: true},
		{name: "unknown_negated_or_is_fail_closed", constraint: "!linux || custom", wantChange: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := testExactGoTestDigestSnapshot("")
			changed := testExactGoTestDigestSnapshot("")
			testExactGoTestDigestReplaceFile(baseline, "fixture/unselected_test.go",
				[]byte("//go:build "+test.constraint+"\n\npackage fixture\n\nconst buildInput = \"one\"\n"))
			testExactGoTestDigestReplaceFile(changed, "fixture/unselected_test.go",
				[]byte("//go:build "+test.constraint+"\n\npackage fixture\n\nconst buildInput = \"two\"\n"))
			gotChanged := testExactGoTestDigest(t, baseline, target) != testExactGoTestDigest(t, changed, target)
			if gotChanged != test.wantChange {
				t.Fatalf("constraint %q changed digest=%v, want %v", test.constraint, gotChanged, test.wantChange)
			}
		})
	}
}

// TestExactGoTestDigestUsesRaceBuildProfile 验证 parent race gate 的编译 profile 只纳入 race 适用输入。
func TestExactGoTestDigestUsesRaceBuildProfile(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testExactGoTestDigestSnapshot("")
	changed := testExactGoTestDigestSnapshot("")
	for _, input := range []struct {
		path string
		name string
	}{
		{path: "fixture/race_test.go", name: "raceTestInput"},
		{path: "fixture/race.go", name: "raceProductionInput"},
	} {
		testExactGoTestDigestReplaceFile(baseline, input.path, []byte("//go:build race\n\npackage fixture\n\nconst "+input.name+" = \"one\"\n"))
		testExactGoTestDigestReplaceFile(changed, input.path, []byte("//go:build race\n\npackage fixture\n\nconst "+input.name+" = \"two\"\n"))
	}
	if testExactGoTestDigest(t, baseline, target) != testExactGoTestDigest(t, changed, target) {
		t.Fatal("normal profile included race-only compile inputs")
	}
	raceProfile := remoteGoBuildProfile{race: true}
	if testExactGoTestDigestWithProfile(t, baseline, target, raceProfile) == testExactGoTestDigestWithProfile(t, changed, target, raceProfile) {
		t.Fatal("race profile omitted race-only production or test compile input")
	}
	raceBeforeUnrelated := testExactGoTestDigestWithProfile(t, baseline, target, raceProfile)
	for _, filePath := range []string{"docs/unrelated.md", "frontend-app/unrelated.ts"} {
		testExactGoTestDigestReplaceFile(baseline, filePath, []byte("unrelated change"))
	}
	if testExactGoTestDigest(t, baseline, target) != testExactGoTestDigest(t, changed, target) {
		t.Fatal("normal profile included unrelated docs or frontend input")
	}
	if got := testExactGoTestDigestWithProfile(t, baseline, target, raceProfile); got != raceBeforeUnrelated {
		t.Fatal("race profile included unrelated docs or frontend input")
	}
	testExactGoTestDigestRaceWorkloadProfiles(t, baseline, changed)
}

// testExactGoTestDigestRaceWorkloadProfiles 验证父 workload 将 race profile 精确传给输入摘要计算。
func testExactGoTestDigestRaceWorkloadProfiles(t *testing.T, baseline, changed *remoteGitTreeSnapshot) {
	t.Helper()
	raceWorkload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestGuardWithRace, "./fixture", "TestX", 1)
	if err != nil {
		t.Fatalf("NewGoTestWorkload(race): %v", err)
	}
	normalWorkload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./fixture", "TestX", 1)
	if err != nil {
		t.Fatalf("NewGoTestWorkload(normal): %v", err)
	}
	if testWorkloadInputDigest(t, baseline, raceWorkload) == testWorkloadInputDigest(t, changed, raceWorkload) {
		t.Fatal("race parent workload did not propagate the race profile")
	}
	if testWorkloadInputDigest(t, baseline, normalWorkload) != testWorkloadInputDigest(t, changed, normalWorkload) {
		t.Fatal("normal parent workload included race-only files")
	}
}

// testExactGoTestDigest 计算测试夹具的精确测试或基准输入摘要。
func testExactGoTestDigest(t *testing.T, snapshot *remoteGitTreeSnapshot, target gate.GoTestTarget) string {
	return testExactGoTestDigestWithProfile(t, snapshot, target, remoteGoBuildProfile{})
}

func testExactGoTestSemanticDigest(t *testing.T, snapshot *remoteGitTreeSnapshot, target gate.GoTestTarget) string {
	t.Helper()
	digest, err := snapshot.goExactTestSemanticInputDigest(context.Background(), target, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("goExactTestSemanticInputDigest(%s): %v", target.Name, err)
	}
	return digest
}

// testGoPackageDigest 计算测试夹具的整包输入摘要。
func testGoPackageDigest(t *testing.T, snapshot *remoteGitTreeSnapshot, target string) string {
	t.Helper()
	digest, err := snapshot.goPackageInputDigest(context.Background(), target, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("goPackageInputDigest(%s): %v", target, err)
	}
	return digest
}

func testExactGoTestDigestWithProfile(t *testing.T, snapshot *remoteGitTreeSnapshot, target gate.GoTestTarget, profile remoteGoBuildProfile) string {
	t.Helper()
	digest, err := snapshot.goExactTestInputDigest(context.Background(), target, profile)
	if err != nil {
		t.Fatalf("goExactTestInputDigest(%s): %v", target.Name, err)
	}
	return digest
}

func testWorkloadInputDigest(t *testing.T, snapshot *remoteGitTreeSnapshot, workload gate.Workload) string {
	t.Helper()
	digest, err := snapshot.workloadInputDigest(context.Background(), workload)
	if err != nil {
		t.Fatalf("workloadInputDigest(%s): %v", workload.ID, err)
	}
	return digest
}

// testExactGoTestDigestSnapshot 创建含有未运行同包测试文件的精确 Git 树夹具。
func testExactGoTestDigestSnapshot(extraDeclaration string) *remoteGitTreeSnapshot {
	sources := map[string][]byte{
		"go.mod":                                     []byte("module example.com/fixture\n\ngo 1.26.5\n"),
		"fixture/main.go":                            []byte("package fixture\n"),
		"fixture/target_test.go":                     []byte("package fixture\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\nfunc BenchmarkX(b *testing.B) {}\n"),
		"fixture/unselected_test.go":                 []byte("package fixture\n\nimport \"testing\"\n\n" + extraDeclaration + "func TestUnselected(t *testing.T) {}\n"),
		"support/support.go":                         []byte("package support\n"),
		"go.sum":                                     []byte(""),
		"build/gate/runtime-proxy/go.mod":            []byte("module example.com/runtime-proxy\n"),
		"build/gate/runtime-proxy/go.sum":            []byte(""),
		"internal/devtools/gate/executor_mapping.go": []byte("package gate\n"),
		"scripts/check_nested_go_modules.sh":         []byte("#!/bin/sh\n"),
		"scripts/real_go_resolver.sh":                []byte("#!/bin/sh\n"),
	}
	entries := make([]remoteGitTreeEntry, 0, len(sources))
	byPath := make(map[string]remoteGitTreeEntry, len(sources))
	for filePath, source := range sources {
		sum := sha1.Sum(source)
		entry := remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: fmt.Sprintf("%x", sum), path: filePath}
		entries = append(entries, entry)
		byPath[filePath] = entry
	}
	shared := remoteGitTreeEntry{mode: "100644", kind: "semantic", objectID: fmt.Sprintf("%040x", 1), path: remoteGoWorkloadSharedScriptPath}
	return &remoteGitTreeSnapshot{
		entries: entries,
		byPath:  byPath,
		goSources: map[string][]byte{
			"go.mod":                     sources["go.mod"],
			"fixture/main.go":            sources["fixture/main.go"],
			"fixture/target_test.go":     sources["fixture/target_test.go"],
			"fixture/unselected_test.go": sources["fixture/unselected_test.go"],
			"support/support.go":         sources["support/support.go"],
		},
		moduleMappings:         []remoteGoModuleMapping{{importPath: "example.com/fixture", directory: "."}},
		goWorkloadSharedScript: &shared,
	}
}

// testExactGoTestDigestSnapshotWithObservedFiles 添加运行时观察文件与无关候选文件。
func testExactGoTestDigestSnapshotWithObservedFiles(testSource, fixture, unrelated string) *remoteGitTreeSnapshot {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte(testSource))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/fixture.txt", []byte(fixture))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/unrelated.txt", []byte(unrelated))
	return snapshot
}

// testGoEmbedDigestSnapshot 创建带有明确 tracked embed asset 的 Go workload 夹具。
func testGoEmbedDigestSnapshot(t *testing.T, pattern, asset string) *remoteGitTreeSnapshot {
	t.Helper()
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte("package fixture\n\nimport \"embed\"\n\n//go:embed "+pattern+"\nvar embeddedAssets embed.FS\n"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/assets/embedded.txt", []byte(asset))
	return snapshot
}

// testGoEmbedSource 生成包含合法 go:embed directive 的 Go 源码夹具。
func testGoEmbedSource(pattern string) []byte {
	return []byte("package fixture\n\nimport \"embed\"\n\n//go:embed " + pattern + "\nvar embeddedAssets embed.FS\n")
}

// testExactGoTestDigestReplaceFile 更新夹具快照中文件的对象身份和可读取源码。
func testExactGoTestDigestReplaceFile(snapshot *remoteGitTreeSnapshot, filePath string, source []byte) {
	sum := sha1.Sum(source)
	entry := remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: fmt.Sprintf("%x", sum), path: filePath}
	snapshot.byPath[filePath] = entry
	for index, candidate := range snapshot.entries {
		if candidate.path == filePath {
			snapshot.entries[index] = entry
			if _, goSource := snapshot.goSources[filePath]; goSource {
				snapshot.goSources[filePath] = source
			}
			return
		}
	}
	snapshot.entries = append(snapshot.entries, entry)
	if path.Ext(filePath) == ".go" {
		snapshot.goSources[filePath] = source
	}
}
