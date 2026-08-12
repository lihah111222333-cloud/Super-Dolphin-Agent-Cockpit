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
	siblingChanged := testExactGoTestDigestSnapshot("const siblingCompileMarker = 1\n")
	singleVote := remoteWorkloadInputVoteDecisionFor(testExactGoTestInputVotes(t, baseline, target), testExactGoTestInputVotes(t, siblingChanged, target))
	if !singleVote.allowReuse() {
		t.Fatal("single broad compile MISS rejected selector PASS reuse")
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
