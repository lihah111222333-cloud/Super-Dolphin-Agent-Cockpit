package remoteci

import (
	"context"
	"crypto/sha1"
	"fmt"
	"path"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestExactGoTestDigestIncludesUnselectedPackageTestCompileInputs 验证精确运行仍绑定整个同包测试编译输入。
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
						t.Fatalf("%s digest did not include the unselected package test compile input", target.Name)
					}
				})
			}
		})
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

// TestExactGoTestDigestFailsClosedForProductionHelperDynamicRepositoryObservations
// 验证被测生产 helper 的动态仓库读取同样不能复用旧 PASS。
func TestExactGoTestDigestFailsClosedForProductionHelperDynamicRepositoryObservations(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	production := `package fixture
import ("os"; "path/filepath")
func readFixture() { name := "fixture.txt"; _, _ = os.ReadFile(filepath.Join("testdata", name)) }`
	testSource := `package fixture
import "testing"
func TestX(t *testing.T) { readFixture() }`
	baseline := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "one", "unchanged")
	testExactGoTestDigestReplaceFile(baseline, "fixture/main.go", []byte(production))
	changed := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "two", "unchanged")
	testExactGoTestDigestReplaceFile(changed, "fixture/main.go", []byte(production))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changed, target) {
		t.Fatal("production helper dynamic fixture change reused the prior exact digest")
	}
}

// TestExactGoTestDigestFailsClosedForProductionHelperProcessObservations
// 验证被测生产导入闭包中的子进程同样不能复用旧 PASS。
func TestExactGoTestDigestFailsClosedForProductionHelperProcessObservations(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	production := `package fixture
import "os/exec"
func runFixtureScript() { _ = exec.Command("sh", "testdata/script.sh").Run() }`
	testSource := `package fixture
import "testing"
func TestX(t *testing.T) { runFixtureScript() }`
	baseline := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "same", "one")
	testExactGoTestDigestReplaceFile(baseline, "fixture/main.go", []byte(production))
	changed := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "same", "two")
	testExactGoTestDigestReplaceFile(changed, "fixture/main.go", []byte(production))
	if testExactGoTestDigest(t, baseline, target) == testExactGoTestDigest(t, changed, target) {
		t.Fatal("production helper process observation reused the prior exact digest")
	}
}

// TestExactGoTestDigestUsesTargetPackageCWDForImportedProductionHelper
// 验证 imported helper 的相对读取仍以被测包目录而非 helper 源码目录解析。
func TestExactGoTestDigestUsesTargetPackageCWDForImportedProductionHelper(t *testing.T) {
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
		t.Fatal("target package fixture change reused the imported helper PASS")
	}
	changedImported := testExactGoTestDigestSnapshotWithObservedFiles(testSource, "one", "unchanged")
	testExactGoTestDigestReplaceFile(changedImported, "support/support.go", []byte(production))
	testExactGoTestDigestReplaceFile(changedImported, "support/testdata/fixture.txt", []byte("support-two"))
	if testExactGoTestDigest(t, baseline, target) != testExactGoTestDigest(t, changedImported, target) {
		t.Fatal("imported helper fixture was bound despite target package CWD")
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
