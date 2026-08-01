package remoteci

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestGoWorkloadFingerprintsIsolateCanonicalOnlyRunnerSemantics(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(t, repository, "scripts/test_with_guard.sh", fingerprintGuardScriptWithCanonicalBlock(1, 1))
	packageBefore := goPackageFingerprint(t, repository, "./internal/a")
	testWorkload := fingerprintGoTestWorkload(t, "TestValue", "./internal/a")
	testBefore := fingerprintDigests(t, repository, []gate.Workload{testWorkload})[testWorkload.ID]
	canonicalWorkload := gate.Workload{ID: string(gate.GateIDBackendTestWithGuard)}
	canonicalBefore := fingerprintDigests(t, repository, []gate.Workload{canonicalWorkload})[canonicalWorkload.ID]

	commitFingerprintChange(t, repository, "scripts/test_with_guard.sh", fingerprintGuardScriptWithCanonicalBlock(1, 2))
	if got := goPackageFingerprint(t, repository, "./internal/a"); got != packageBefore {
		t.Fatal("canonical-only runner helper changed package workload fingerprint")
	}
	if got := fingerprintDigests(t, repository, []gate.Workload{testWorkload})[testWorkload.ID]; got != testBefore {
		t.Fatal("canonical-only runner helper changed exact test workload fingerprint")
	}
	if got := fingerprintDigests(t, repository, []gate.Workload{canonicalWorkload})[canonicalWorkload.ID]; got == canonicalBefore {
		t.Fatal("canonical runner helper did not change canonical workload fingerprint")
	}

	commitFingerprintChange(t, repository, "scripts/test_with_guard.sh", fingerprintGuardScriptWithCanonicalBlock(2, 2))
	if got := goPackageFingerprint(t, repository, "./internal/a"); got == packageBefore {
		t.Fatal("shared runner semantics did not change package workload fingerprint")
	}
	if got := fingerprintDigests(t, repository, []gate.Workload{testWorkload})[testWorkload.ID]; got == testBefore {
		t.Fatal("shared runner semantics did not change exact test workload fingerprint")
	}

	packageAfterShared := goPackageFingerprint(t, repository, "./internal/a")
	testAfterShared := fingerprintDigests(t, repository, []gate.Workload{testWorkload})[testWorkload.ID]
	commitFingerprintChange(t, repository, "internal/devtools/gate/executor_mapping.go", "package gate\n\nconst CandidateExecutorMapping = 2\n")
	if got := goPackageFingerprint(t, repository, "./internal/a"); got == packageAfterShared {
		t.Fatal("candidate executor mapping changed without invalidating package workload fingerprint")
	}
	if got := fingerprintDigests(t, repository, []gate.Workload{testWorkload})[testWorkload.ID]; got == testAfterShared {
		t.Fatal("candidate executor mapping changed without invalidating exact test workload fingerprint")
	}
}

func TestGoWorkloadSharedScriptRejectsAmbiguousCanonicalBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{name: "missing", script: "#!/bin/sh\n"},
		{name: "duplicate begin", script: remoteCanonicalScriptFingerprintBegin + remoteCanonicalScriptFingerprintBegin + remoteCanonicalScriptFingerprintEnd},
		{name: "reversed", script: remoteCanonicalScriptFingerprintEnd + remoteCanonicalScriptFingerprintBegin},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := remoteGoWorkloadSharedScript([]byte(test.script)); err == nil {
				t.Fatal("ambiguous canonical script boundary was accepted")
			}
		})
	}
}

func fingerprintGuardScriptWithCanonicalBlock(sharedMarker int, canonicalMarker int) string {
	return fmt.Sprintf(`#!/bin/sh
shared_runner=%d
# REMOTE_WORKLOAD_FINGERPRINT_CANONICAL_BEGIN
canonical_runner=%d
# REMOTE_WORKLOAD_FINGERPRINT_CANONICAL_END
`, sharedMarker, canonicalMarker)
}

func TestRemoteWorkloadInputDigestsTrackProductionInputs(t *testing.T) {
	repository := newFingerprintRepository(t)
	workloads := fingerprintWorkloads(t, repository)
	initial := fingerprintDigests(t, repository, workloads)

	commitFingerprintChange(t, repository, "docs/note.md", "unrelated\n")
	unrelated := fingerprintDigests(t, repository, workloads)
	assertFingerprintEqual(t, initial, unrelated, "unrelated documentation")

	commitFingerprintChange(t, repository, "internal/c/c.go", "package c\n\nconst Value = 2\n")
	unrelatedGo := fingerprintDigests(t, repository, workloads)
	assertFingerprintEqual(t, unrelated, unrelatedGo, "unrelated Go package")

	goID, vitestID := fingerprintWorkloadIDs(t, workloads)
	commitFingerprintChange(t, repository, "frontend-app/src/other.test.ts", "test('other changed', () => {})\n")
	unrelatedTest := fingerprintDigests(t, repository, workloads)
	if unrelatedTest[vitestID] == unrelatedGo[vitestID] {
		t.Fatal("another Vitest target did not invalidate the shared frontend test fingerprint")
	}
	if unrelatedTest[goID] != unrelatedGo[goID] {
		t.Fatal("another Vitest target changed Go package fingerprint")
	}

	commitFingerprintChange(t, repository, "internal/b/b.go", "package b\n\nconst Value = 2\n")
	dependencyChanged := fingerprintDigests(t, repository, workloads)
	if dependencyChanged[goID] == unrelatedTest[goID] {
		t.Fatal("transitive Go production dependency did not change package fingerprint")
	}
	if dependencyChanged[vitestID] != unrelatedTest[vitestID] {
		t.Fatal("Go production dependency changed Vitest fingerprint")
	}

	commitFingerprintChange(t, repository, "frontend-app/src/shared.ts", "export const value = 2\n")
	frontendChanged := fingerprintDigests(t, repository, workloads)
	if frontendChanged[vitestID] == dependencyChanged[vitestID] {
		t.Fatal("frontend production source did not change Vitest fingerprint")
	}
	if frontendChanged[goID] != dependencyChanged[goID] {
		t.Fatal("frontend production source changed Go package fingerprint")
	}

	commitFingerprintChange(t, repository, "scripts/real_go_resolver.sh", "#!/bin/sh\n# changed\n")
	resolverChanged := fingerprintDigests(t, repository, workloads)
	if resolverChanged[goID] == frontendChanged[goID] {
		t.Fatal("Go resolver change did not invalidate Go package fingerprint")
	}
	if resolverChanged[vitestID] != frontendChanged[vitestID] {
		t.Fatal("Go resolver change invalidated Vitest fingerprint")
	}
}

func TestGoTestFingerprintTracksStaticRepositoryRootInput(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(t, repository, ".gitignore", "*.local\n")
	commitFingerprintChange(t, repository, "internal/a/a_test.go", `package a

import (
	"os"
	"testing"
)

func TestReadsRootIgnore(t *testing.T) {
	_, _ = os.ReadFile("../../.gitignore")
}
`)
	workload := fingerprintGoTestWorkload(t, "TestReadsRootIgnore", "./internal/a")
	initial := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]

	commitFingerprintChange(t, repository, ".gitignore", "*.local\n*.cache\n")
	if got := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]; got == initial {
		t.Fatal("static root .gitignore observation did not change Go test fingerprint")
	}
}

func TestGoTestFingerprintTracksDynamicPackageDiscovery(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(t, repository, "internal/a/a_test.go", `package a

import (
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestDynamicPackages(t *testing.T) {
	_, _ = packages.Load(&packages.Config{}, "./...")
}
`)
	workload := fingerprintGoTestWorkload(t, "TestDynamicPackages", "./internal/a")
	initial := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]

	commitFingerprintChange(t, repository, "internal/c/c.go", "package c\n\nconst Value = 2\n")
	if got := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]; got == initial {
		t.Fatal("dynamic Go package discovery did not bind the full repository tree")
	}
}

func TestGoTestFingerprintKeepsClosedPackageIndependent(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(t, repository, "internal/c/c_test.go", `package c

import "testing"

func TestClosed(t *testing.T) {}
`)
	workload := fingerprintGoTestWorkload(t, "TestClosed", "./internal/c")
	initial := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]

	commitFingerprintChange(t, repository, "internal/provider/sample/sample.go", "package sample\n\nconst Value = 2\n")
	if got := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]; got != initial {
		t.Fatal("unrelated business source changed a closed Go test package fingerprint")
	}
}

func TestGoTestFingerprintTracksOnlyTargetAndHelperClosure(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(t, repository, "internal/a/a_test.go", `package a

import "testing"

func TestExactFingerprint(t *testing.T) {
	t.Helper()
	sharedTestHelper()
}
`)
	commitFingerprintChange(t, repository, "internal/a/a_helper_test.go", `package a

func sharedTestHelper() {}
`)
	commitFingerprintChange(t, repository, "internal/a/a_unrelated_test.go", `package a

import "testing"

func TestUnrelated(t *testing.T) {}
`)
	commitFingerprintChange(t, repository, "internal/a/testdata/input.txt", "first\n")
	workload := fingerprintGoTestWorkload(t, "TestExactFingerprint", "./internal/a")
	initial := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]

	commitFingerprintChange(t, repository, "internal/a/a_test.go", `package a

import "testing"

func TestExactFingerprint(t *testing.T) {
	t.Helper()
	sharedTestHelper()
	_ = 1
}
`)
	targetChanged := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]
	if targetChanged == initial {
		t.Fatal("target Go test source did not change its fingerprint")
	}

	commitFingerprintChange(t, repository, "internal/a/a_unrelated_test.go", `package a

import "testing"

func TestUnrelated(t *testing.T) { t.Helper() }
`)
	siblingChanged := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]
	if siblingChanged != targetChanged {
		t.Fatal("unrelated sibling Go test source changed exact test fingerprint")
	}

	commitFingerprintChange(t, repository, "internal/a/a_helper_test.go", `package a

func sharedTestHelper() { _ = 1 }
`)
	helperChanged := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]
	if helperChanged == siblingChanged {
		t.Fatal("shared Go test helper did not change exact test fingerprint")
	}

	commitFingerprintChange(t, repository, "internal/a/a.go", "package a\n\nimport \"example.com/fingerprint/internal/b\"\n\nvar Value = b.Value + 1\n")
	productionChanged := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]
	if productionChanged == helperChanged {
		t.Fatal("Go production source did not change exact test fingerprint")
	}

	commitFingerprintChange(t, repository, "internal/a/testdata/input.txt", "second\n")
	if got := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]; got == productionChanged {
		t.Fatal("Go package build asset did not change exact test fingerprint")
	}
}

func TestGoPackageFingerprintTracksTestObservationClosure(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(t, repository, ".gitignore", "*.local\n")
	commitFingerprintChange(t, repository, "internal/a/a_test.go", `package a

import (
	"os"
	"testing"
)

func TestReadsRootIgnore(t *testing.T) {
	_, _ = os.ReadFile("../../.gitignore")
}
`)
	workload, err := gate.NewGoPackageWorkload(gate.GateIDBackendTestWithGuard, "./internal/a", 1)
	if err != nil {
		t.Fatal(err)
	}
	initial := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]
	commitFingerprintChange(t, repository, ".gitignore", "*.local\n*.cache\n")
	if got := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]; got == initial {
		t.Fatal("package Go test fingerprint omitted its test observation closure")
	}
}

func TestGoTestFingerprintUsesLinuxAMD64CompileInputs(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(t, repository, "internal/a/compile_linux_amd64.go", `//go:build linux && amd64

package a

const linuxCompileInput = 1
`)
	commitFingerprintChange(t, repository, "internal/a/compile_darwin.go", `//go:build darwin

package a

const darwinCompileInput = 1
`)
	commitFingerprintChange(t, repository, "internal/a/linux_amd64_test.go", `//go:build linux && amd64

package a

import "testing"

func TestLinuxSibling(t *testing.T) {}
`)
	commitFingerprintChange(t, repository, "internal/a/darwin_test.go", `//go:build darwin

package a

func TestDarwinSibling(t *testing.T) {}
`)
	workload := fingerprintGoTestWorkload(t, "TestValue", "./internal/a")
	initial := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]
	commitFingerprintChange(t, repository, "internal/a/darwin_test.go", `//go:build darwin

package a

func TestDarwinSibling(t *testing.T) { _ = 1 }
`)
	if got := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]; got != initial {
		t.Fatal("darwin-only test source changed linux/amd64 exact fingerprint")
	}
	commitFingerprintChange(t, repository, "internal/a/compile_darwin.go", `//go:build darwin

package a

const darwinCompileInput = 2
`)
	if got := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]; got != initial {
		t.Fatal("darwin-only production source changed linux/amd64 exact fingerprint")
	}
	commitFingerprintChange(t, repository, "internal/a/compile_linux_amd64.go", `//go:build linux && amd64

package a

const linuxCompileInput = 2
`)
	linuxProductionChanged := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]
	if linuxProductionChanged == initial {
		t.Fatal("linux/amd64 production compile source did not change exact fingerprint")
	}
	commitFingerprintChange(t, repository, "internal/a/linux_amd64_test.go", `//go:build linux && amd64

package a

import "testing"

func TestLinuxSibling(t *testing.T) { _ = 1 }
`)
	if got := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]; got != linuxProductionChanged {
		t.Fatal("unrelated linux/amd64 sibling test source changed exact fingerprint")
	}
}

func TestRemoteBuildSourceUsesOnlyTrailingPlatformSuffix(t *testing.T) {
	testCases := []struct {
		path    string
		source  string
		applies bool
	}{
		{path: "scripts/package_windows_guard_test.go", source: "package main\n", applies: true},
		{path: "scripts/package_guard_windows_test.go", source: "package main\n", applies: false},
		{path: "internal/a/worker_linux_amd64.go", source: "package a\n", applies: true},
		{path: "internal/a/worker_darwin_arm64.go", source: "package a\n", applies: false},
		{path: "internal/a/worker.go", source: "//go:build ignore\n\npackage a\n", applies: false},
	}
	for _, testCase := range testCases {
		if got := remoteBuildSourceAppliesLinuxAMD64(
			testCase.path,
			[]byte(testCase.source),
		); got != testCase.applies {
			t.Fatalf("%s applies=%t, want %t", testCase.path, got, testCase.applies)
		}
	}
}

func TestGoTestFingerprintTracksPackageTestInitialization(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(t, repository, "internal/a/main_test.go", `package a

import (
	"os"
	"testing"
)

var packageFixture = 1

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestPackageInitialization(t *testing.T) {
	t.Helper()
}
`)
	workload := fingerprintGoTestWorkload(t, "TestPackageInitialization", "./internal/a")
	initial := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]

	commitFingerprintChange(t, repository, "internal/a/main_test.go", `package a

import (
	"os"
	"testing"
)

var packageFixture = 2

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestPackageInitialization(t *testing.T) {
	t.Helper()
}
`)
	if got := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]; got == initial {
		t.Fatal("package-level test initialization did not change exact test fingerprint")
	}
}

func TestGoTestFingerprintTracksTestOnlyLocalImport(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(t, repository, "internal/c/c_test.go", `package c

import (
	"testing"

	"example.com/fingerprint/internal/provider/sample"
)

func TestTestOnlyImport(t *testing.T) {
	if sample.Value == 0 {
		t.Fatal("unexpected zero value")
	}
}
`)
	workload := fingerprintGoTestWorkload(t, "TestTestOnlyImport", "./internal/c")
	initial := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]

	commitFingerprintChange(
		t,
		repository,
		"internal/provider/sample/sample.go",
		"package sample\n\nconst Value = 2\n",
	)
	if got := fingerprintDigests(t, repository, []gate.Workload{workload})[workload.ID]; got == initial {
		t.Fatal("test-only local production dependency did not change exact test fingerprint")
	}
}

func TestGoPackageFingerprintSupportsTestOnlyPackage(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(
		t,
		repository,
		"internal/testonly/test_only_test.go",
		"package testonly_test\n\nimport \"testing\"\n\nfunc TestOnly(t *testing.T) {}\n",
	)
	initial := goPackageFingerprint(t, repository, "./internal/testonly")

	commitFingerprintChange(
		t,
		repository,
		"internal/testonly/test_only_test.go",
		"package testonly_test\n\nimport \"testing\"\n\nfunc TestOnly(t *testing.T) { t.Log(\"changed\") }\n",
	)
	if got := goPackageFingerprint(t, repository, "./internal/testonly"); got == initial {
		t.Fatal("test-only package source did not change Go package fingerprint")
	}
}

func TestGoPackageFingerprintRequiresExplicitChildExpansion(t *testing.T) {
	repository := newFingerprintRepository(t)
	commitFingerprintChange(
		t,
		repository,
		"internal/a/a_test.go",
		`package a

import "testing"

func TestValue(t *testing.T) {}
func FuzzValue(f *testing.F) {}
func ExampleValue() {}
`,
	)
	parent, err := gate.NewGoPackageWorkload(
		gate.GateIDBackendTestWithGuard,
		"./internal/a",
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	parentDigests := fingerprintDigests(t, repository, []gate.Workload{parent})
	if len(parentDigests) != 1 || parentDigests[parent.ID] == "" {
		t.Fatalf("parent digests = %#v, want exactly the requested package", parentDigests)
	}
	tree := coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	digests, err := remoteExactGoTestInputDigests(
		context.Background(),
		repository,
		tree,
		[]gate.Workload{parent},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TestValue", "FuzzValue", "ExampleValue"} {
		child, err := gate.NewGoTestWorkload(
			gate.GateIDBackendTestWithGuard,
			"./internal/a",
			name,
			1,
		)
		if err != nil {
			t.Fatal(err)
		}
		if digests[child.ID] == "" {
			t.Fatalf("%s has no independent input digest", name)
		}
	}
	if len(digests) != 3 {
		t.Fatalf("child digest count = %d, want only the 3 explicit children", len(digests))
	}
}

func TestRemoteWorkloadCacheIsSharedAcrossWorktreePaths(t *testing.T) {
	firstRepository := newFingerprintRepository(t)
	workloads := fingerprintWorkloads(t, firstRepository)
	workloads = append(workloads, splitGoGuardFingerprintWorkloads(t, firstRepository)...)
	first := fingerprintDigests(t, firstRepository, workloads)

	secondRepository := filepath.Join(t.TempDir(), "second-worktree")
	runCoordinatorGit(t, "", "clone", "--quiet", "--no-hardlinks", firstRepository, secondRepository)
	second := fingerprintDigests(t, secondRepository, workloads)
	assertFingerprintEqual(t, first, second, "worktree path")

	firstEntries, err := remoteWorkloadCacheEntries("passed/", workloads, first, remoteWorkloadCacheInputFixture())
	if err != nil {
		t.Fatal(err)
	}
	secondEntries, err := remoteWorkloadCacheEntries("passed/", workloads, second, remoteWorkloadCacheInputFixture())
	if err != nil {
		t.Fatal(err)
	}
	for index := range firstEntries {
		if firstEntries[index].key != secondEntries[index].key {
			t.Fatalf("worktree path changed cache key %d: %q != %q", index, firstEntries[index].key, secondEntries[index].key)
		}
	}
}

func TestSplitGoGuardFingerprintsIgnoreUnrelatedTrees(t *testing.T) {
	repository := newFingerprintRepository(t)
	workloads := splitGoGuardFingerprintWorkloads(t, repository)
	initial := fingerprintDigests(t, repository, workloads)

	commitFingerprintChange(t, repository, "internal/provider/sample/sample.go", "package sample\n\nconst Value = 2\n")
	changed := fingerprintDigests(t, repository, workloads)
	for _, workload := range workloads {
		_, _, target, _, err := gate.ParseWorkloadID(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		shouldChange := target == gate.GoGuardTargetSource || target == gate.GoGuardTargetSourceRawGoTest ||
			target == gate.GoGuardTargetSourceCodeSize || target == gate.GoGuardTargetCopylocksProvider ||
			target == gate.GoGuardTargetAIMaintenanceUnit || target == gate.GoGuardTargetAIMaintenanceGate
		if (changed[workload.ID] != initial[workload.ID]) != shouldChange {
			t.Fatalf("guard target %q changed=%t, want %t", target, changed[workload.ID] != initial[workload.ID], shouldChange)
		}
	}
}

func TestSourceCodeSizeFingerprintIgnoresNonGoProjectMapChanges(t *testing.T) {
	repository := newFingerprintRepository(t)
	workloads := splitGoGuardFingerprintWorkloads(t, repository)
	initial := fingerprintDigests(t, repository, workloads)

	commitFingerprintChange(t, repository, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", "generated map v2\n")
	changed := fingerprintDigests(t, repository, workloads)
	for _, workload := range workloads {
		_, _, target, targeted, err := gate.ParseWorkloadID(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		if targeted && target == gate.GoGuardTargetSourceCodeSize && changed[workload.ID] != initial[workload.ID] {
			t.Fatal("source-code-size fingerprint changed for a non-Go project-map update")
		}
	}
}

func newFingerprintRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runCoordinatorGit(t, repository, "init", "--quiet")
	runCoordinatorGit(t, repository, "config", "user.email", "fingerprint@example.invalid")
	runCoordinatorGit(t, repository, "config", "user.name", "Fingerprint")
	files := map[string]string{
		"Makefile":                                          fingerprintWorkerMakefile(1, 1),
		"go.mod":                                            "module example.com/fingerprint\n\ngo 1.25\n",
		"go.sum":                                            "",
		"build/gate/runtime-proxy/go.mod":                   "module example.com/fingerprint/runtime-proxy\n\ngo 1.25\n",
		"build/gate/runtime-proxy/go.sum":                   "",
		"build/gate/runtime-proxy/proxy.go":                 "package proxy\n",
		"build/gate/runtime-tools/go.mod":                   "module example.com/fingerprint/runtime-tools\n\ngo 1.25\n",
		"build/gate/runtime-tools/go.sum":                   "",
		"build/gate/runtime-tools/tools.go":                 "package tools\n",
		"build/gate/inputs.json":                            "{\"schema_version\":\"2\",\"dockerfile\":\"build/gate/Dockerfile\",\"inputs\":[],\"gate_compile_inputs\":[\"cmd/super-dolphin-gate/main.go\",\"go.mod\",\"go.sum\",\"internal/devtools/gate/worker.go\",\"internal/devtools/gateprivate/runtime.go\"]}\n",
		"third_party/kelindar-event/go.mod":                 "module example.com/fingerprint/kelindar-event\n\ngo 1.25\n",
		"third_party/kelindar-event/go.sum":                 "",
		"third_party/kelindar-event/event.go":               "package event\n",
		"scripts/test_with_guard.sh":                        fingerprintWorkerGuardScript(1),
		"scripts/check_nested_go_modules.sh":                "#!/bin/sh\n",
		"scripts/real_go_resolver.sh":                       "#!/bin/sh\nresolve_real_go() { printf go; }\n",
		"scripts/worker_command.sh":                         "#!/bin/sh\nexit 0\n",
		"cmd/super-dolphin-gate/main.go":                    fingerprintWorkerMainSource(1),
		"cmd/super-dolphin-gate/remote_materialize.go":      fingerprintWorkerMaterializerSource(1),
		"cmd/super-dolphin-gate/worker_tool.go":             fingerprintWorkerToolSource(1),
		"cmd/super-dolphin-gate/coordinator.go":             "package main\n\nconst coordinatorOnly = 1\n",
		"internal/devtools/gate/ci_query_store.go":          "package gate\n\nconst queryStoreOnly = 1\n",
		"internal/devtools/gate/executor.go":                fingerprintWorkerExecutorSource(1),
		"internal/devtools/gate/executor_mapping.go":        fingerprintWorkerExecutorMappingSource("./scripts/test_with_guard.sh"),
		"internal/devtools/gate/executor_runtime.go":        fingerprintWorkerExecutorRuntimeSource(1),
		"internal/devtools/gate/worker_asset.txt":           "worker-v1\n",
		"internal/devtools/gate/ledger_store_sqlite.go":     "package gate\n\nconst sqliteLedgerOnly = 1\n",
		"internal/devtools/gate/worker_test.go":             "package gate\n\nconst testOnly = 1\n",
		"internal/devtools/gateprivate/runtime.go":          "package gateprivate\n\nconst Runtime = 1\n",
		"internal/devtools/remoteci/coordinator_request.go": fingerprintWorkerRequestSource(1),
		"internal/devtools/remoteci/protocol.go":            fingerprintWorkerProtocolSource(1),
		"internal/devtools/remoteci/worker_supervisor.go": `package remoteci

func remoteWorkerSupervisorCommand(command string) []string {
	return []string{command, "worker"}
}
`,
		"internal/devtools/remoteci/worker_execution_contract.go": "package remoteci\n\nconst contractAlgorithmOnly = 1\n",
		"internal/devtools/remoteci/workload_fingerprint.go":      "package remoteci\n\nconst fingerprintOnly = 1\n",
		"internal/a/a.go":                    "package a\n\nimport \"example.com/fingerprint/internal/b\"\n\nvar Value = b.Value\n",
		"internal/a/a_test.go":               "package a\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {}\nfunc TestRetry(t *testing.T) {}\n",
		"internal/b/b.go":                    "package b\n\nconst Value = 1\n",
		"internal/c/c.go":                    "package c\n\nconst Value = 1\n",
		"internal/provider/sample/sample.go": "package sample\n\nconst Value = 1\n",
		"internal/platform/sample/sample.go": "package sample\n\nconst Value = 1\n",
		"internal/module/thread/sample.go":   "package thread\n\nconst Value = 1\n",
		"frontend-app/package.json":          "{}\n",
		"frontend-app/package-lock.json":     "{}\n",
		"frontend-app/src/shared.ts":         "export const value = 1\n",
		"frontend-app/src/target.test.ts":    "import { value } from './shared'\ntest('target', () => value)\n",
		"frontend-app/src/other.test.ts":     "test('other', () => {})\n",
	}
	for relative, content := range files {
		writeFingerprintFile(t, repository, relative, content)
	}
	runCoordinatorGit(t, repository, "add", ".")
	runCoordinatorGit(t, repository, "commit", "--quiet", "-m", "initial")
	return repository
}

func workerExecutionDigest(t *testing.T, repository string) string {
	t.Helper()
	tree := coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	digest, err := ResolveWorkerExecutionDigest(context.Background(), repository, tree)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func goPackageFingerprint(t *testing.T, repository string, target string) string {
	t.Helper()
	tree := coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	snapshot, err := loadRemoteGitTreeSnapshot(context.Background(), repository, tree)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := snapshot.goPackageInputDigest(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func splitGoGuardFingerprintWorkloads(t *testing.T, repository string) []gate.Workload {
	t.Helper()
	commit := coordinatorGitOutput(t, repository, "rev-parse", "HEAD")
	tree := coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	plan, err := gate.BuildGatePlan(gate.ProfileLocalFast, gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: commit}, SourceTreeSHA: tree,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := gate.BuildExpandedWorkloadCatalog(plan, gate.DefaultWorkloadBootstrapPolicy(), gate.WorkloadInventory{
		GoPackages: []string{"./internal/a"},
		NestedGoModules: []string{
			"build/gate/runtime-proxy",
			"build/gate/runtime-tools",
			"third_party/kelindar-event",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	workloads := make([]gate.Workload, 0, 10)
	for _, workload := range catalog.Workloads {
		_, kind, _, targeted, parseErr := gate.ParseWorkloadID(workload.ID)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if targeted && kind == gate.WorkloadTargetGoGuard {
			workloads = append(workloads, workload)
		}
	}
	if len(workloads) != 10 {
		t.Fatalf("split Go guard workloads = %d, want 10", len(workloads))
	}
	return workloads
}

func fingerprintWorkloads(t *testing.T, repository string) []gate.Workload {
	t.Helper()
	commit := coordinatorGitOutput(t, repository, "rev-parse", "HEAD")
	tree := coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	plan, err := gate.BuildGatePlan(gate.ProfileLocalFast, gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: commit}, SourceTreeSHA: tree,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := gate.BuildSelectedTestWorkloadCatalog(plan, gate.WorkloadInventory{
		GoPackages:        []string{"./internal/a"},
		FrontendFullTests: []string{"src/target.test.ts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	testWorkload := fingerprintGoTestWorkload(t, "TestValue", "./internal/a")
	return append(catalog.Workloads, testWorkload)
}

func fingerprintGoTestWorkload(t *testing.T, testName string, packageTarget string) gate.Workload {
	t.Helper()
	workload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, packageTarget, testName, 1_000)
	if err != nil {
		t.Fatal(err)
	}
	return workload
}

func fingerprintDigests(t *testing.T, repository string, workloads []gate.Workload) map[string]string {
	t.Helper()
	tree := coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	digests, err := remoteWorkloadInputDigests(context.Background(), repository, tree, workloads)
	if err != nil {
		t.Fatal(err)
	}
	return digests
}

func fingerprintWorkloadIDs(t *testing.T, workloads []gate.Workload) (goID string, vitestID string) {
	t.Helper()
	for _, workload := range workloads {
		_, kind, _, _, err := gate.ParseWorkloadID(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case gate.WorkloadTargetGoPackage:
			goID = workload.ID
		case gate.WorkloadTargetVitest:
			vitestID = workload.ID
		}
	}
	if goID == "" || vitestID == "" {
		t.Fatalf("workload IDs are incomplete: go=%q vitest=%q", goID, vitestID)
	}
	return goID, vitestID
}

func assertFingerprintEqual(t *testing.T, left map[string]string, right map[string]string, change string) {
	t.Helper()
	if len(left) != len(right) {
		t.Fatalf("%s fingerprint count changed", change)
	}
	for workloadID, digest := range left {
		if right[workloadID] != digest {
			t.Fatalf("%s changed workload %q fingerprint", change, workloadID)
		}
	}
}

func commitFingerprintChange(t *testing.T, repository string, relative string, content string) {
	t.Helper()
	writeFingerprintFile(t, repository, relative, content)
	runCoordinatorGit(t, repository, "add", relative)
	runCoordinatorGit(t, repository, "commit", "--quiet", "-m", "change "+filepath.Base(relative))
}

func writeFingerprintFile(t *testing.T, repository string, relative string, content string) {
	t.Helper()
	fullPath := filepath.Join(repository, relative)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
