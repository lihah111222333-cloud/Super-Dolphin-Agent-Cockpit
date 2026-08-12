package remoteci

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestRemoteWorkloadMissVoteConsensus 验证单个 broad MISS 不扩大分片，
// 而目标声明变化与 broad 变化组成两票后必须确认 MISS。
func TestRemoteWorkloadMissVoteConsensus(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testExactGoTestDigestSnapshot("")
	baselineVotes := testExactGoTestInputVotes(t, baseline, target)
	singleVote := remoteWorkloadInputVoteDecisionFor(baselineVotes, baselineVotes)
	if !singleVote.allowReuse() {
		t.Fatal("single broad MISS rejected selector PASS reuse")
	}
	selectedChanged := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(selectedChanged, "fixture/target_test.go", []byte("package fixture\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) { t.Log(\"changed\") }\nfunc BenchmarkX(b *testing.B) {}\n"))
	confirmed := remoteWorkloadInputVoteDecisionFor(testExactGoTestInputVotes(t, baseline, target), testExactGoTestInputVotes(t, selectedChanged, target))
	if confirmed.allowReuse() {
		t.Fatal("two independent MISS votes reused changed selector")
	}
	diagnostic := ReuseReplayDiagnostic{}
	diagnostic.observeSourceInputVoteDecision(singleVote)
	diagnostic.observeSourceInputVoteDecision(confirmed)
	if diagnostic.SourceSingleVoteRecovered != 1 || diagnostic.SourceConfirmedMisses != 1 || diagnostic.SourceDeclarationMissVotes != 1 {
		t.Fatalf("vote diagnostic = %#v", diagnostic)
	}
}

// TestRemoteWorkloadMissVotesRejectSiblingCompileFailure 验证同包未选测试的
// 编译闭包变化必须成为独立 MISS 票，禁止把无法编译的候选复用为 PASS。
func TestRemoteWorkloadMissVotesRejectSiblingCompileFailure(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testExactGoTestDigestSnapshot("")
	compileFailure := testExactGoTestDigestSnapshot("type Broken doesNotExist\n")
	decision := remoteWorkloadInputVoteDecisionFor(
		testExactGoTestInputVotes(t, baseline, target),
		testExactGoTestInputVotes(t, compileFailure, target),
	)
	if decision.allowReuse() {
		t.Fatal("sibling compile failure reused selector PASS")
	}
	if !decision.compileMiss || decision.missVotes < remoteReuseMissConfirmationThreshold {
		t.Fatalf("sibling compile-failure decision = %+v, want confirmed compile MISS", decision)
	}
	diagnostic := ReuseReplayDiagnostic{}
	diagnostic.observeSourceInputVoteDecision(decision)
	if diagnostic.SourceConfirmedMisses != 1 || diagnostic.SourceCompileMissVotes != 1 {
		t.Fatalf("sibling compile-failure diagnostic = %#v, want one confirmed compile MISS", diagnostic)
	}
}

// TestRemoteWorkloadCompileMismatchSkipsSemanticDigests 验证编译闭包不一致已经提供两票时，
// replay 必须在解析 selector 声明和运行时观察前早停。
func TestRemoteWorkloadCompileMismatchSkipsSemanticDigests(t *testing.T) {
	workload := testRemoteGoWorkload(t, "TestX")
	source := testExactGoTestDigestSnapshot("")
	source.repositoryRoot = "repo"
	source.tree = "source"
	target := testExactGoTestDigestSnapshot("type Broken doesNotExist\n")
	target.repositoryRoot = "repo"
	target.tree = "target"
	cache, err := newRemoteReplayCache("repo", "target", target)
	if err != nil {
		t.Fatalf("newRemoteReplayCache: %v", err)
	}
	decision := testRemoteSemanticInputDecision(t, cache, workload, source, target)
	if decision.allowReuse() || !decision.compileMiss {
		t.Fatalf("compile mismatch decision = %+v, want compile MISS", decision)
	}
	sibling := testRemoteGoWorkload(t, "TestUnselected")
	decision = testRemoteSemanticInputDecision(t, cache, sibling, source, target)
	if decision.allowReuse() || !decision.compileMiss {
		t.Fatalf("sibling compile mismatch decision = %+v, want compile MISS", decision)
	}
	if cache.compileComputations != 2 {
		t.Fatalf("compile computations = %d, want one grouped computation per tree", cache.compileComputations)
	}
	if cache.semanticComputations != 0 {
		t.Fatalf("semantic computations = %d, want 0 after compile-first early stop", cache.semanticComputations)
	}
}

// TestRemoteWorkloadCompileMismatchSkipsFullInputDigest 验证真实 candidate 匹配路径
// 在 compile MISS 后不再计算完整 workload InputDigest。
func TestRemoteWorkloadCompileMismatchSkipsFullInputDigest(t *testing.T) {
	workload := testRemoteGoWorkload(t, "TestX")
	source := testExactGoTestDigestSnapshot("")
	source.repositoryRoot = "repo"
	source.tree = "source"
	target := testExactGoTestDigestSnapshot("type Broken doesNotExist\n")
	target.repositoryRoot = "repo"
	target.tree = "target"
	cache, err := newRemoteReplayCache("repo", "target", target)
	if err != nil {
		t.Fatalf("newRemoteReplayCache: %v", err)
	}
	cache.snapshots[remoteReplayTreeKey{repositoryRoot: "repo", tree: "source"}] = remoteReplaySnapshotResult{snapshot: source, available: true}
	diagnostic := ReuseReplayDiagnostic{}
	matches, err := matchesRemoteWorkloadPassSourceCandidate(
		context.Background(),
		"repo",
		gate.WorkloadPassIdentity{WorkloadID: gate.GateID(workload.ID), InputDigest: "target-input"},
		workload,
		gate.WorkloadPassEvidence{OriginSourceTreeSHA: "source"},
		target,
		cache,
		&diagnostic,
	)
	if err != nil {
		t.Fatalf("matchesRemoteWorkloadPassSourceCandidate: %v", err)
	}
	if matches {
		t.Fatal("compile mismatch reused source candidate")
	}
	if cache.inputComputations != 0 || cache.semanticComputations != 0 {
		t.Fatalf("expensive computations input=%d semantic=%d, want 0", cache.inputComputations, cache.semanticComputations)
	}
	if diagnostic.SourceCompileMissVotes != 1 || diagnostic.SourceConfirmedMisses != 1 {
		t.Fatalf("compile-first diagnostic = %#v, want one confirmed compile MISS", diagnostic)
	}
}

// testRemoteGoWorkload 创建 compile-first 单测使用的规范 Go selector workload。
func testRemoteGoWorkload(t *testing.T, name string) gate.Workload {
	t.Helper()
	workload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./fixture", name, 1)
	if err != nil {
		t.Fatalf("NewGoTestWorkload(%s): %v", name, err)
	}
	return workload
}

// testRemoteSemanticInputDecision 执行 replay 语义裁决并把错误收敛到调用测试。
func testRemoteSemanticInputDecision(t *testing.T, cache *remoteReplayCache, workload gate.Workload, source, target *remoteGitTreeSnapshot) remoteWorkloadInputVoteDecision {
	t.Helper()
	decision, err := cache.semanticInputDecision(context.Background(), workload, source, target)
	if err != nil {
		t.Fatalf("semanticInputDecision(%s): %v", workload.ID, err)
	}
	return decision
}

// TestCanonicalRemoteWorkloadPassSourceCandidates 验证同一来源树只评估一次，
// 同时保留 SQLite 已确定排序中的首个 provenance。
func TestCanonicalRemoteWorkloadPassSourceCandidates(t *testing.T) {
	candidates := []gate.WorkloadPassEvidence{
		{OriginSourceTreeSHA: "tree-a", OriginJobID: "first"},
		{OriginSourceTreeSHA: "tree-a", OriginJobID: "duplicate"},
		{OriginSourceTreeSHA: "tree-b", OriginJobID: "second"},
	}
	canonical := canonicalRemoteWorkloadPassSourceCandidates(candidates)
	if len(canonical) != 2 {
		t.Fatalf("canonical candidates = %d, want 2", len(canonical))
	}
	if canonical[0].OriginJobID != "first" || canonical[1].OriginJobID != "second" {
		t.Fatalf("canonical provenance = %#v, want first candidate per source tree", canonical)
	}
}

// TestRemoteWorkloadMissVotesDoNotDoubleCountWholeTreeFallback 验证 broad 与全树
// runtime fallback 只算一票；独立包编译闭包也变化时才确认 MISS。
func TestRemoteWorkloadMissVotesDoNotDoubleCountWholeTreeFallback(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	source := `package fixture
import ("os"; "reflect"; "testing")
func TestX(t *testing.T) { _, _ = reflect.ValueOf(os.ReadFile).Call([]reflect.Value{reflect.ValueOf("fixture.txt")}) }`
	baseline := testExactGoTestDigestSnapshotWithObservedFiles(source, "same", "unchanged")
	unrelated := testExactGoTestDigestSnapshotWithObservedFiles(source, "same", "unchanged")
	testExactGoTestDigestReplaceFile(unrelated, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", []byte("unrelated"))
	baselineVotes := testExactGoTestInputVotes(t, baseline, target)
	unrelatedVotes := testExactGoTestInputVotes(t, unrelated, target)
	if !baselineVotes.runtimeFallback || !unrelatedVotes.runtimeFallback {
		t.Fatal("dynamic reflection did not select whole-tree runtime fallback")
	}
	if !remoteWorkloadInputVotesAllowReuse(baselineVotes, unrelatedVotes) {
		t.Fatal("correlated broad and whole-tree fallback changes counted as two MISS votes")
	}
	productionChanged := testExactGoTestDigestSnapshotWithObservedFiles(source, "same", "unchanged")
	testExactGoTestDigestReplaceFile(productionChanged, "fixture/main.go", []byte("package fixture\n\nconst changedProduction = true\n"))
	if remoteWorkloadInputVotesAllowReuse(baselineVotes, testExactGoTestInputVotes(t, productionChanged, target)) {
		t.Fatal("broad and package compile MISS votes reused changed production input")
	}
}

// TestRemoteWorkloadMissVotesTreatProductionTreeScopeAsFallback 验证生产调用闭包
// 扩大到全树时仍只算 broad 一票；项目地图变化不能让无关 Go selector MISS。
func TestRemoteWorkloadMissVotesTreatProductionTreeScopeAsFallback(t *testing.T) {
	target := gate.GoTestTarget{Package: "fixture", Name: "TestX"}
	baseline := testDynamicProcessGoTestSnapshot("baseline map")
	mapChanged := testDynamicProcessGoTestSnapshot("refreshed map")
	baselineVotes := testExactGoTestInputVotes(t, baseline, target)
	changedVotes := testExactGoTestInputVotes(t, mapChanged, target)
	if !baselineVotes.runtimeFallback || !changedVotes.runtimeFallback {
		t.Fatal("production whole-tree observation was not marked as runtime fallback")
	}
	if !remoteWorkloadInputVotesAllowReuse(baselineVotes, changedVotes) {
		t.Fatal("project-map-only change invalidated unrelated Go selector PASS")
	}
}

func testExactGoTestInputVotes(t *testing.T, snapshot *remoteGitTreeSnapshot, target gate.GoTestTarget) remoteWorkloadInputVoteDigests {
	t.Helper()
	compile, err := snapshot.goPackageInputDigest(context.Background(), target.Package, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("goPackageInputDigest(%s): %v", target.Name, err)
	}
	declaration, err := snapshot.goExactTestDeclarationInputDigest(context.Background(), target, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("goExactTestDeclarationInputDigest(%s): %v", target.Name, err)
	}
	runtime, fallback, err := snapshot.goExactTestRuntimeInputDigest(context.Background(), target, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("goExactTestRuntimeInputDigest(%s): %v", target.Name, err)
	}
	return remoteWorkloadInputVoteDigests{compile: compile, declaration: declaration, runtime: runtime, runtimeFallback: fallback}
}
