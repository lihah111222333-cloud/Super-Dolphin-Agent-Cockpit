package remoteci

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"golang.org/x/sync/errgroup"
)

// BenchmarkRemoteWorkloadFingerprintsCandidate 测量真实 Git tree 上同包精确 selector 的 Prepare 指纹阶段。
func BenchmarkRemoteWorkloadFingerprintsCandidate(b *testing.B) {
	repositoryRoot := fingerprintBenchmarkRepositoryRoot(b)
	tree := os.Getenv("REMOTE_CI_FINGERPRINT_TREE")
	if tree == "" {
		tree = "HEAD"
	}
	testNames := fingerprintBenchmarkTestNames(b, repositoryRoot)
	workloads := fingerprintBenchmarkWorkloads(b, testNames)
	ctx := context.Background()
	if _, _, _, err := remoteWorkloadFingerprintsWithSnapshot(ctx, repositoryRoot, tree, workloads); err != nil {
		b.Fatalf("remoteWorkloadFingerprintsWithSnapshot() warm-up: %v", err)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, _, _, err := remoteWorkloadFingerprintsWithSnapshot(ctx, repositoryRoot, tree, workloads); err != nil {
			b.Fatalf("remoteWorkloadFingerprintsWithSnapshot(): %v", err)
		}
	}
}

// BenchmarkRemoteWorkloadFingerprintsCandidateWithMigrationCapture measures the
// current MISS-only closure resolver; normal Prepare deliberately uses the
// benchmark above and captures closures only after an identity MISS.
func BenchmarkRemoteWorkloadFingerprintsCandidateWithMigrationCapture(b *testing.B) {
	repositoryRoot := fingerprintBenchmarkRepositoryRoot(b)
	tree := os.Getenv("REMOTE_CI_FINGERPRINT_TREE")
	if tree == "" {
		tree = "HEAD"
	}
	testNames := fingerprintBenchmarkTestNames(b, repositoryRoot)
	workloads := fingerprintBenchmarkWorkloads(b, testNames)
	ctx := context.Background()
	snapshot, err := loadRemoteGitTreeSnapshot(ctx, repositoryRoot, tree)
	if err != nil {
		b.Fatalf("loadRemoteGitTreeSnapshot() warm-up: %v", err)
	}
	b.ResetTimer()
	for b.Loop() {
		resolver := newRemoteHistoricalInputDigestResolver(RunInput{
			RepositoryRoot:        repositoryRoot,
			Tree:                  tree,
			workloadInputSnapshot: snapshot,
		})
		for _, workload := range workloads {
			if _, err := resolver.currentClosure(ctx, snapshot, gate.GateID(workload.ID)); err != nil {
				b.Fatalf("currentClosure(%q): %v", workload.ID, err)
			}
		}
	}
}

// BenchmarkGoEmbedResolutionCache measures repeated selector resolution after one
// parser/path-match pass; the final metric proves all later iterations hit cache.
func BenchmarkGoEmbedResolutionCache(b *testing.B) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", testGoEmbedSource("all:assets"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/assets/embedded.txt", []byte("embedded-one"))
	source := testGoEmbedSource("all:assets")
	if _, err := snapshot.resolveGoEmbedAssets("fixture", source); err != nil {
		b.Fatalf("warm-up resolveGoEmbedAssets(): %v", err)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := snapshot.resolveGoEmbedAssets("fixture", source); err != nil {
			b.Fatalf("resolveGoEmbedAssets(): %v", err)
		}
	}
	b.StopTimer()
	computations, hits := snapshot.goEmbedResolutionStats()
	if computations != 1 {
		b.Fatalf("go:embed computations = %d, want 1", computations)
	}
	b.ReportMetric(float64(hits), "cache-hits")
}

// fingerprintBenchmarkRepositoryRoot 返回当前测试文件对应的仓库根目录。
func fingerprintBenchmarkRepositoryRoot(b *testing.B) string {
	b.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		b.Fatal("runtime.Caller() failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../.."))
}

// fingerprintBenchmarkTestNames 返回基准默认样本或显式请求的完整包测试清单。
func fingerprintBenchmarkTestNames(b *testing.B, repositoryRoot string) []string {
	b.Helper()
	testNames := []string{
		"TestExactGoTestDigestIncludesUnselectedPackageTestCompileInputs",
		"TestExactGoTestDigestFailsClosedForDynamicRepositoryObservations",
		"TestExactGoTestDigestFailsClosedForProcessAndCWDObservations",
		"TestExactGoTestDigestBindsProductionHelperDynamicSource",
		"TestExactGoTestDigestBindsProductionHelperProcessSource",
		"TestExactGoTestDigestBindsImportedProductionPackageAssets",
	}
	if os.Getenv("REMOTE_CI_FINGERPRINT_BENCH_ALL") == "" {
		return testNames
	}
	return fingerprintBenchmarkAllTestNames(b, repositoryRoot)
}

// fingerprintBenchmarkAllTestNames 从 Go 测试清单中提取完整的精确测试名集合。
func fingerprintBenchmarkAllTestNames(b *testing.B, repositoryRoot string) []string {
	b.Helper()
	list := exec.Command("go", "test", "./internal/devtools/remoteci", "-list", "^Test")
	list.Dir = repositoryRoot
	output, err := list.Output()
	if err != nil {
		b.Fatalf("list package tests: %v", err)
	}
	testNames := make([]string, 0)
	for line := range strings.SplitSeq(string(output), "\n") {
		if strings.HasPrefix(line, "Test") {
			testNames = append(testNames, strings.TrimSpace(line))
		}
	}
	if len(testNames) == 0 {
		b.Fatal("package test list is empty")
	}
	return testNames
}

// fingerprintBenchmarkWorkloads 把精确测试名转换为远程 CI workload。
func fingerprintBenchmarkWorkloads(b *testing.B, testNames []string) []gate.Workload {
	b.Helper()
	workloads := make([]gate.Workload, 0, len(testNames))
	for _, name := range testNames {
		workload, err := gate.NewGoTestWorkload(
			gate.GateIDBackendTestWithGuard,
			"./internal/devtools/remoteci",
			name,
			100,
		)
		if err != nil {
			b.Fatalf("NewGoTestWorkload(%q): %v", name, err)
		}
		workloads = append(workloads, workload)
	}
	return workloads
}

func TestRemoteGoPackageInputDigestCacheIsScopedToPackageAndProfile(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	ctx := context.Background()
	normal, err := snapshot.goPackageInputDigest(ctx, "./fixture", remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("goPackageInputDigest(normal): %v", err)
	}
	repeated, err := snapshot.goPackageInputDigest(ctx, "./fixture", remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("goPackageInputDigest(normal repeated): %v", err)
	}
	if repeated != normal {
		t.Fatalf("repeated digest = %q, want %q", repeated, normal)
	}
	race, err := snapshot.goPackageInputDigest(ctx, "./fixture", remoteGoBuildProfile{race: true})
	if err != nil {
		t.Fatalf("goPackageInputDigest(race): %v", err)
	}
	raceAgain, err := snapshot.goPackageInputDigest(ctx, "./fixture", remoteGoBuildProfile{race: true})
	if err != nil {
		t.Fatalf("goPackageInputDigest(race repeated): %v", err)
	}
	if raceAgain != race {
		t.Fatalf("repeated race digest = %q, want %q", raceAgain, race)
	}
	if got := len(snapshot.goPackageInputDigestCache); got != 2 {
		t.Fatalf("package digest cache entries = %d, want 2", got)
	}
}

func TestRemoteGitTreeSnapshotPrepareGoSourcesIsConcurrentSafe(t *testing.T) {
	repository := t.TempDir()
	runInventoryGit(t, repository, "init", "--quiet")
	runInventoryGit(t, repository, "config", "user.name", "CI Fingerprint")
	runInventoryGit(t, repository, "config", "user.email", "ci@example.invalid")
	writeInventoryFile(t, repository, "go.mod", "module example.test/fingerprint\n\ngo 1.26\n")
	writeInventoryFile(t, repository, "fixture.go", "package fingerprint\n")
	runInventoryGit(t, repository, "add", ".")
	runInventoryGit(t, repository, "commit", "--quiet", "-m", "初始化")
	tree := inventoryGitOutput(t, repository, "rev-parse", "HEAD^{tree}")

	snapshot, err := loadRemoteGitTreeSnapshot(context.Background(), repository, tree)
	if err != nil {
		t.Fatal(err)
	}
	const callers = 8
	start := make(chan struct{})
	var callersGroup errgroup.Group
	for range callers {
		callersGroup.Go(func() error {
			<-start
			return snapshot.prepareGoSources(context.Background())
		})
	}
	close(start)
	if err := callersGroup.Wait(); err != nil {
		t.Fatalf("prepareGoSources() error = %v", err)
	}
	if string(snapshot.goSources["go.mod"]) != "module example.test/fingerprint\n\ngo 1.26\n" {
		t.Fatalf("go.mod source = %q", snapshot.goSources["go.mod"])
	}
	if len(snapshot.moduleMappings) != 1 || snapshot.moduleMappings[0].importPath != "example.test/fingerprint" {
		t.Fatalf("module mappings = %#v", snapshot.moduleMappings)
	}
}

func TestRemoteCompileGroupInputsByGateIDValidatesTransportIdentity(t *testing.T) {
	workload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/devtools/gate", "TestCompileGroup", 100)
	if err != nil {
		t.Fatalf("NewGoTestWorkload() error = %v", err)
	}
	input := gate.CompileGroupInput{
		PackageTarget:     "./internal/devtools/gate",
		SemanticKey:       "go-test-selector/v1/race=false",
		SharedInputDigest: "sha256:" + strings.Repeat("1", 64),
		ProfileDigest:     "sha256:" + strings.Repeat("2", 64),
	}
	converted, err := remoteCompileGroupInputsByGateID(map[string]gate.CompileGroupInput{workload.ID: input})
	if err != nil {
		t.Fatalf("remoteCompileGroupInputsByGateID() error = %v", err)
	}
	if _, ok := converted[gate.GateID(workload.ID)]; !ok {
		t.Fatalf("converted map missing workload %q", workload.ID)
	}
	if _, err := remoteCompileGroupInputsByGateID(map[string]gate.CompileGroupInput{"bad::go-test::eA": input}); err == nil {
		t.Fatal("remoteCompileGroupInputsByGateID() accepted malformed workload identity")
	}
}

func TestRemoteCompileGroupInputsForExecutionRejectsMissingSelectorInput(t *testing.T) {
	workload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/devtools/gate", "TestCompileGroup", 100)
	if err != nil {
		t.Fatalf("NewGoTestWorkload() error = %v", err)
	}
	if _, _, err := remoteCompileGroupInputsForExecution([]gate.GateID{gate.GateID(workload.ID)}, nil); err == nil {
		t.Fatal("remoteCompileGroupInputsForExecution() accepted a missing compile input")
	}
}

func TestRemoteCompileGroupInputsForExecutionFiltersPassSelectors(t *testing.T) {
	pass, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/devtools/gate", "TestCompileGroupPass", 100)
	if err != nil {
		t.Fatalf("NewGoTestWorkload(pass) error = %v", err)
	}
	miss, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/devtools/gate", "TestCompileGroupMiss", 100)
	if err != nil {
		t.Fatalf("NewGoTestWorkload(miss) error = %v", err)
	}
	input := gate.CompileGroupInput{
		PackageTarget:     "./internal/devtools/gate",
		SemanticKey:       gate.CompileGroupSemanticGoTestNormal,
		SharedInputDigest: "sha256:" + strings.Repeat("3", 64),
		ProfileDigest:     "sha256:" + strings.Repeat("4", 64),
	}
	filtered, compileAware, err := remoteCompileGroupInputsForExecution(
		[]gate.GateID{gate.GateID(miss.ID)},
		map[string]gate.CompileGroupInput{pass.ID: input, miss.ID: input},
	)
	if err != nil {
		t.Fatalf("remoteCompileGroupInputsForExecution() error = %v", err)
	}
	if !compileAware {
		t.Fatal("remoteCompileGroupInputsForExecution() did not enable compile-aware planning for the miss")
	}
	if len(filtered) != 1 {
		t.Fatalf("filtered compile inputs = %#v, want only the miss selector", filtered)
	}
	if _, ok := filtered[gate.GateID(pass.ID)]; ok {
		t.Fatal("PASS selector compile input leaked into planner inputs")
	}
	if _, ok := filtered[gate.GateID(miss.ID)]; !ok {
		t.Fatal("miss selector compile input was dropped")
	}
}

func TestRemoteCompileGroupInputsForExecutionAllHitReturnsNoPlannerInputs(t *testing.T) {
	pass, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/devtools/gate", "TestCompileGroupAllHit", 100)
	if err != nil {
		t.Fatalf("NewGoTestWorkload() error = %v", err)
	}
	input := gate.CompileGroupInput{
		PackageTarget:     "./internal/devtools/gate",
		SemanticKey:       gate.CompileGroupSemanticGoTestNormal,
		SharedInputDigest: "sha256:" + strings.Repeat("5", 64),
		ProfileDigest:     "sha256:" + strings.Repeat("6", 64),
	}
	filtered, compileAware, err := remoteCompileGroupInputsForExecution(nil, map[string]gate.CompileGroupInput{pass.ID: input})
	if err != nil {
		t.Fatalf("remoteCompileGroupInputsForExecution() error = %v", err)
	}
	if compileAware || len(filtered) != 0 {
		t.Fatalf("all-hit planner inputs = %#v, compileAware=%t; want no planner inputs", filtered, compileAware)
	}
}

func TestCompileGroupTargetExcludesWholePackageWorkload(t *testing.T) {
	workload, err := gate.NewGoPackageWorkload(gate.GateIDBackendTestWithGuard, "./internal/devtools/gate", 100)
	if err != nil {
		t.Fatalf("NewGoPackageWorkload() error = %v", err)
	}
	_, targetKind, target, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil || !targeted {
		t.Fatalf("ParseWorkloadID() = kind=%q target=%q targeted=%t err=%v", targetKind, target, targeted, err)
	}
	if _, _, err := compileGroupTarget(targetKind, target, remoteGoBuildProfile{}); err == nil {
		t.Fatal("compileGroupTarget() accepted a whole-package workload")
	}
}

func TestCompileGroupTargetUsesIndependentSelectorSemantics(t *testing.T) {
	tests := []struct {
		name     string
		workload func() (gate.Workload, error)
		semantic string
		profile  remoteGoBuildProfile
	}{
		{
			name: "exact test",
			workload: func() (gate.Workload, error) {
				return gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/devtools/gate", "TestCompileGroup", 100)
			},
			semantic: "go-test-selector/v1/race=false",
		},
		{
			name: "race exact test",
			workload: func() (gate.Workload, error) {
				return gate.NewGoTestWorkload(gate.GateIDBackendTestGuardWithRace, "./internal/devtools/gate", "TestCompileGroup", 100)
			},
			semantic: "go-test-selector/v1/race=true",
			profile:  remoteGoBuildProfile{race: true},
		},
		{
			name: "benchmark",
			workload: func() (gate.Workload, error) {
				return gate.NewGoBenchmarkWorkload(gate.GateIDBackendTestWithGuard, "./internal/devtools/gate", "BenchmarkCompileGroup", 100)
			},
			semantic: "go-benchmark-selector/v1/race=false",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workload, err := test.workload()
			if err != nil {
				t.Fatalf("construct workload: %v", err)
			}
			_, targetKind, target, targeted, err := gate.ParseWorkloadID(workload.ID)
			if err != nil || !targeted {
				t.Fatalf("ParseWorkloadID() = kind=%q target=%q targeted=%t err=%v", targetKind, target, targeted, err)
			}
			packageTarget, semantic, err := compileGroupTarget(targetKind, target, test.profile)
			if err != nil {
				t.Fatalf("compileGroupTarget() error = %v", err)
			}
			if packageTarget != "./internal/devtools/gate" {
				t.Fatalf("package target = %q", packageTarget)
			}
			if semantic != test.semantic {
				t.Fatalf("semantic = %q, want %q", semantic, test.semantic)
			}
		})
	}
}
