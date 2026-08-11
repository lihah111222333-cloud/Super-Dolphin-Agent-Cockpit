package remoteci

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestCoordinatorPrepareReusesPassAcrossWorktreesAndCommits 通过真实 Git
// 提交、真实输入指纹与真实 SQLite origin proof 锁定跨工作树复用边界。
func TestCoordinatorPrepareReusesPassAcrossWorktreesAndCommits(t *testing.T) {
	originRoot, originInput := coordinatorReuseFixture(t)
	originResult := runCoordinatorFreshWorkloads(t, originInput)
	seedCoordinatorWorkloadPassEvidence(t, originInput, originResult, nil)

	candidateRoot := cloneCrossWorktreeCandidate(t, originRoot)
	runCoordinatorGit(t, candidateRoot, "commit", "--quiet", "--allow-empty", "-m", "equivalent commit")
	equivalentInput := crossWorktreeCandidateInput(t, originInput, candidateRoot)
	assertCrossWorktreeWorkerDigest(t, originInput, equivalentInput)
	equivalent, store, runtime := prepareCrossWorktreeCandidate(t, equivalentInput)
	assertEquivalentCommitAllReused(t, originInput, originResult.WorkloadPassIdentities, equivalent)
	assertCoordinatorNoRemoteSideEffects(t, store, runtime)

	writeCoordinatorFixture(t, candidateRoot, "docs/unrelated-pass-reuse.md", "unrelated candidate change\n")
	runCoordinatorGit(t, candidateRoot, "add", "docs/unrelated-pass-reuse.md")
	runCoordinatorGit(t, candidateRoot, "commit", "--quiet", "-m", "unrelated tree change")
	changedInput := crossWorktreeCandidateInput(t, originInput, candidateRoot)
	if changedInput.Tree == originInput.Tree || changedInput.Commit == originInput.Commit {
		t.Fatal("cross-worktree candidate did not produce a distinct tree and commit")
	}
	assertCrossWorktreeWorkerDigest(t, originInput, changedInput)
	changed, changedStore, changedRuntime := prepareCrossWorktreeCandidate(t, changedInput)
	assertUnchangedFingerprintsReused(t, originInput, originResult.WorkloadPassIdentities, changed)
	assertCoordinatorNoRemoteSideEffects(t, changedStore, changedRuntime)
}

// TestCoordinatorPrepareReplaysHistoricalInputProofAcrossWorktreesAndCommits
// 模拟旧粗粒度 input digest；当前 identity 不含树，只有来源 proof 保留历史树以重算精确指纹。
func TestCoordinatorPrepareReplaysHistoricalInputProofAcrossWorktreesAndCommits(t *testing.T) {
	originRoot, originInput := coordinatorReuseFixture(t)
	executionInput := originInput
	executionInput.LedgerStore, executionInput.LedgerSnapshot = newRemoteRunLedgerAuthority(t, originInput.LedgerSnapshot.Ledger)
	originResult := runCoordinatorFreshWorkloads(t, executionInput)
	seedCoordinatorHistoricalInputEvidence(t, originInput, &originResult)

	candidateRoot := cloneCrossWorktreeCandidate(t, originRoot)
	runCoordinatorGit(t, candidateRoot, "commit", "--quiet", "--allow-empty", "-m", "equivalent source replay commit")
	candidateInput := crossWorktreeCandidateInput(t, originInput, candidateRoot)
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.newID = func() (string, error) { return "job-fedcba9876543210fedcba98", nil }
	prepared, err := coordinator.Prepare(context.Background(), candidateInput)
	if err != nil {
		t.Fatalf("source replay Prepare() error = %v", err)
	}
	if !prepared.AllReused() || len(prepared.reuse.cacheMisses) != 0 {
		t.Fatalf("source replay reuse = all=%t reused=%d misses=%d",
			prepared.AllReused(), len(prepared.reuse.reusedWorkloads), len(prepared.reuse.cacheMisses))
	}
	assertHistoricalInputProofProjectedToCurrentIdentity(t, originInput.Tree, prepared)
	result, err := coordinator.RunPrepared(context.Background(), prepared)
	if err != nil || result.Status != gate.ResultStatusPassed || !result.CleanupComplete {
		t.Fatalf("source replay RunPrepared() result=%#v error=%v", result, err)
	}
	assertPersistedReplayTargetsCurrentIdentity(t, candidateInput.LedgerStore, result.JobID, prepared)
	assertCoordinatorNoRemoteSideEffects(t, store, runtime)
}

// TestCoordinatorPrepareSourceReplayRejectsChangedWorkloadInput 证明跨树复用仍由 workload 指纹裁决，相关源码变化必须 MISS。
func TestCoordinatorPrepareSourceReplayRejectsChangedWorkloadInput(t *testing.T) {
	originRoot, originInput := coordinatorReuseFixture(t)
	executionInput := originInput
	executionInput.LedgerStore, executionInput.LedgerSnapshot = newRemoteRunLedgerAuthority(t, originInput.LedgerSnapshot.Ledger)
	originResult := runCoordinatorFreshWorkloads(t, executionInput)
	seedCoordinatorHistoricalInputEvidence(t, originInput, &originResult)

	candidateRoot := cloneCrossWorktreeCandidate(t, originRoot)
	writeCoordinatorFixture(t, candidateRoot, "internal/fixture/fixture.go", "package fixture\n\nfunc Value() int { return 2 }\n")
	runCoordinatorGit(t, candidateRoot, "add", "internal/fixture/fixture.go")
	runCoordinatorGit(t, candidateRoot, "commit", "--quiet", "-m", "change fixture workload input")
	prepared, store, runtime := prepareCrossWorktreeCandidate(t, crossWorktreeCandidateInput(t, originInput, candidateRoot))
	assertChangedFixtureWorkloadMissedSourceReplay(t, prepared)
	assertCoordinatorNoRemoteSideEffects(t, store, runtime)
}

// assertChangedFixtureWorkloadMissedSourceReplay 查找真实 go-package workload，并确认相关源码变化未命中历史 proof。
func assertChangedFixtureWorkloadMissedSourceReplay(t *testing.T, prepared *PreparedRun) {
	t.Helper()
	misses := make(map[gate.GateID]struct{}, len(prepared.reuse.cacheMisses))
	for _, workloadID := range prepared.reuse.cacheMisses {
		misses[workloadID] = struct{}{}
	}
	found := false
	for _, identity := range prepared.reuse.identities {
		_, targetKind, target, targeted, err := gate.ParseWorkloadID(string(identity.WorkloadID))
		if err != nil {
			t.Fatal(err)
		}
		if !targeted || targetKind != gate.WorkloadTargetGoPackage || target != "./internal/fixture" {
			continue
		}
		found = true
		if _, missed := misses[identity.WorkloadID]; !missed {
			t.Fatalf("changed workload %q reused historical source proof", identity.WorkloadID)
		}
		if _, reused := prepared.reuse.reused[string(identity.WorkloadID)]; reused {
			t.Fatalf("changed workload %q remained in reused index", identity.WorkloadID)
		}
	}
	if !found {
		t.Fatal("fixture go-package workload was not present in current identities")
	}
}

// seedCoordinatorHistoricalInputEvidence 以旧 catalog/input identity 提升一批可被当前算法重算的直接 PASS。
func seedCoordinatorHistoricalInputEvidence(t *testing.T, input RunInput, result *RunResult) {
	t.Helper()
	catalog := mustCoordinatorCatalog(t, input)
	identities := make(map[gate.GateID]*gate.WorkloadPassIdentity, len(result.WorkloadPassIdentities))
	for index := range result.WorkloadPassIdentities {
		identity := &result.WorkloadPassIdentities[index]
		identity.InputDigest = "sha256:" + strings.Repeat(string(rune('a'+index%6)), 64)
		digest, err := gate.WorkloadPassIdentitySHA256(*identity)
		if err != nil {
			t.Fatalf("digest historical workload identity %q: %v", identity.WorkloadID, err)
		}
		identity.IdentityDigest = digest
		identities[identity.WorkloadID] = identity
	}
	for index := range catalog.Workloads {
		identity, ok := identities[gate.GateID(catalog.Workloads[index].ID)]
		if ok {
			catalog.Workloads[index].InputDigest = identity.InputDigest
		}
	}
	catalogDigest, err := gate.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatalf("digest historical workload catalog: %v", err)
	}
	result.CatalogDigest = catalogDigest
	if err := input.LedgerStore.RecordWorkloadCatalog(catalog, gate.WorkloadCatalogObservation{
		SourceTreeSHA: result.SourceTreeSHA, Entrypoint: result.Entrypoint, Profile: result.Profile,
		AcceptedGeneration: result.AcceptedGeneration, ObservedAt: result.StartedAt,
	}); err != nil {
		t.Fatalf("record historical workload catalog: %v", err)
	}
	promoteCoordinatorFreshWorkloads(t, input, *result)
}

// assertHistoricalInputProofProjectedToCurrentIdentity 证明目标 key 只使用当前四段身份，历史树仅保留在来源 proof。
func assertHistoricalInputProofProjectedToCurrentIdentity(t *testing.T, originTree string, prepared *PreparedRun) {
	t.Helper()
	current := make(map[gate.GateID]gate.WorkloadPassIdentity, len(prepared.reuse.identities))
	for _, identity := range prepared.reuse.identities {
		current[identity.WorkloadID] = identity
	}
	for _, evidence := range prepared.reuse.reusedWorkloads {
		identity, ok := current[evidence.Identity.WorkloadID]
		if !ok {
			t.Fatalf("source replay workload %q lacks current identity", evidence.Identity.WorkloadID)
		}
		if identity.WorkloadID != evidence.Identity.WorkloadID ||
			identity.ExecutionDigest != evidence.Identity.ExecutionDigest ||
			identity.EnvironmentDigest != evidence.Identity.EnvironmentDigest {
			t.Fatalf("source replay changed workload execution or environment identity for %q", identity.WorkloadID)
		}
		if identity.InputDigest == evidence.Identity.InputDigest || identity.IdentityDigest == evidence.Identity.IdentityDigest {
			t.Fatalf("source replay %q did not cross the historical input digest boundary", identity.WorkloadID)
		}
		if evidence.OriginSourceTreeSHA != originTree {
			t.Fatalf("source replay %q origin tree = %q, want %q", identity.WorkloadID, evidence.OriginSourceTreeSHA, originTree)
		}
	}
}

// assertPersistedReplayTargetsCurrentIdentity 锁定 consumer 行使用当前 PassKey，而不是来源树的旧 input identity。
func assertPersistedReplayTargetsCurrentIdentity(t *testing.T, store *gate.DurationLedgerStore, jobID string, prepared *PreparedRun) {
	t.Helper()
	record, err := store.LoadRemoteCIRun(jobID)
	if err != nil {
		t.Fatalf("load source replay consumer: %v", err)
	}
	current := make(map[gate.GateID]gate.WorkloadPassIdentity, len(prepared.reuse.identities))
	sources := make(map[gate.GateID]gate.WorkloadPassEvidence, len(prepared.reuse.reusedWorkloads))
	for _, identity := range prepared.reuse.identities {
		current[identity.WorkloadID] = identity
	}
	for _, evidence := range prepared.reuse.reusedWorkloads {
		sources[evidence.Identity.WorkloadID] = evidence
	}
	if len(record.WorkloadResults) != len(current) {
		t.Fatalf("source replay persisted results = %d, want %d", len(record.WorkloadResults), len(current))
	}
	for _, result := range record.WorkloadResults {
		assertPersistedReplayTargetResult(t, result, current, sources)
	}
}

// assertPersistedReplayTargetResult 验证单条 consumer 行投影到当前身份并携带来源重放证明。
func assertPersistedReplayTargetResult(t *testing.T, result gate.RemoteCIWorkloadResult, current map[gate.GateID]gate.WorkloadPassIdentity, sources map[gate.GateID]gate.WorkloadPassEvidence) {
	t.Helper()
	identity, ok := current[result.Identity.WorkloadID]
	if !ok || result.Identity != identity || result.Disposition != gate.WorkloadDispositionReused {
		t.Fatalf("source replay persisted non-current identity for %q", result.Identity.WorkloadID)
	}
	expected, err := gate.WorkloadPassSourceReplaySHA256(identity, sources[result.Identity.WorkloadID])
	if err != nil || result.EvidenceSHA256 != expected {
		t.Fatalf("source replay persisted proof for %q = %q, %v; want %q", result.Identity.WorkloadID, result.EvidenceSHA256, err, expected)
	}
}

// cloneCrossWorktreeCandidate 把 origin 克隆为独立工作树，避免测试只替换路径字符串。
func cloneCrossWorktreeCandidate(t *testing.T, originRoot string) string {
	t.Helper()
	parent := t.TempDir()
	candidateRoot := filepath.Join(parent, "candidate")
	runCoordinatorGit(t, parent, "clone", "--quiet", originRoot, candidateRoot)
	runCoordinatorGit(t, candidateRoot, "config", "user.email", "remote-ci@example.invalid")
	runCoordinatorGit(t, candidateRoot, "config", "user.name", "Remote CI")
	return candidateRoot
}

// crossWorktreeCandidateInput 把生产输入绑定到候选工作树的真实 commit/tree。
func crossWorktreeCandidateInput(t *testing.T, origin RunInput, candidateRoot string) RunInput {
	t.Helper()
	candidate := origin
	candidate.RepositoryRoot = candidateRoot
	candidate.Commit = coordinatorGitOutput(t, candidateRoot, "rev-parse", "HEAD")
	candidate.Tree = coordinatorGitOutput(t, candidateRoot, "rev-parse", "HEAD^{tree}")
	candidate.Source = gate.SourceSpec{Kind: gate.SourceKindTree, ObjectFormat: gate.GitObjectFormatSHA1,
		Tree: &gate.TreeSource{SHA: candidate.Tree, ParentCommitSHA: candidate.Commit}, SourceTreeSHA: candidate.Tree}
	clearCoordinatorAllHitExecutionIdentity(&candidate)
	return candidate
}

// prepareCrossWorktreeCandidate 只运行无副作用 Prepare，返回可审计的复用决策。
func prepareCrossWorktreeCandidate(t *testing.T, input RunInput) (*PreparedRun, *coordinatorStore, *coordinatorRuntime) {
	t.Helper()
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	prepared, err := newTestCoordinator(t, store, runtime).Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("cross-worktree Prepare() error = %v", err)
	}
	return prepared, store, runtime
}

// assertCrossWorktreeWorkerDigest 证明全 workload 环境摘要中的 Worker closure 未被无关提交扩大。
func assertCrossWorktreeWorkerDigest(t *testing.T, origin, candidate RunInput) {
	t.Helper()
	originDigest, err := ResolveWorkerExecutionDigest(context.Background(), origin.RepositoryRoot, origin.Tree)
	if err != nil {
		t.Fatalf("origin worker execution digest: %v", err)
	}
	candidateDigest, err := ResolveWorkerExecutionDigest(context.Background(), candidate.RepositoryRoot, candidate.Tree)
	if err != nil {
		t.Fatalf("candidate worker execution digest: %v", err)
	}
	if candidateDigest != originDigest {
		t.Fatal("unrelated cross-worktree commit changed worker execution digest")
	}
}

// assertEquivalentCommitAllReused 证明同一代码树的不同 commit 在 SQLite 中全命中。
func assertEquivalentCommitAllReused(t *testing.T, origin RunInput, identities []gate.WorkloadPassIdentity, prepared *PreparedRun) {
	t.Helper()
	if prepared.input.Commit == origin.Commit || prepared.input.RepositoryRoot == origin.RepositoryRoot {
		t.Fatal("equivalent candidate did not cross commit and worktree boundaries")
	}
	if !prepared.AllReused() || len(prepared.reuse.cacheMisses) != 0 || len(prepared.input.WorkloadCompileGroupInputs) != 0 {
		t.Fatalf("equivalent commit reuse = all=%t reused=%d misses=%d compile=%d",
			prepared.AllReused(), len(prepared.reuse.reusedWorkloads), len(prepared.reuse.cacheMisses), len(prepared.input.WorkloadCompileGroupInputs))
	}
	if len(prepared.reuse.identities) != len(identities) {
		t.Fatalf("equivalent identity count = %d, want %d", len(prepared.reuse.identities), len(identities))
	}
	for index := range identities {
		if prepared.reuse.identities[index] != identities[index] {
			t.Fatalf("equivalent commit changed workload identity at index %d", index)
		}
	}
	assertOriginProofCrossedTreeBoundary(t, origin.Tree, prepared.reuse.reusedWorkloads)
}

// assertUnchangedFingerprintsReused 证明 changed tree 中每个未变 PassKey 都命中 origin，且不会进入 compile 投影。
func assertUnchangedFingerprintsReused(t *testing.T, origin RunInput, identities []gate.WorkloadPassIdentity, prepared *PreparedRun) {
	t.Helper()
	originByID := make(map[gate.GateID]gate.WorkloadPassIdentity, len(identities))
	for _, identity := range identities {
		originByID[identity.WorkloadID] = identity
	}
	misses := make(map[gate.GateID]struct{}, len(prepared.reuse.cacheMisses))
	for _, id := range prepared.reuse.cacheMisses {
		misses[id] = struct{}{}
	}
	unchanged := 0
	for _, identity := range prepared.reuse.identities {
		if originByID[identity.WorkloadID] != identity {
			continue
		}
		unchanged++
		assertUnchangedFingerprintReused(t, origin.Tree, identity, misses, prepared)
	}
	if unchanged == 0 {
		t.Fatal("unrelated tree change preserved no workload fingerprints")
	}
	assertCompileInputsAreStrictMisses(t, misses, prepared.input.WorkloadCompileGroupInputs)
	assertOriginProofCrossedTreeBoundary(t, origin.Tree, prepared.reuse.reusedWorkloads)
}

// assertUnchangedFingerprintReused 检查单个相同四段身份及最终 PassKey 的命中投影。
func assertUnchangedFingerprintReused(t *testing.T, originTree string, identity gate.WorkloadPassIdentity, misses map[gate.GateID]struct{}, prepared *PreparedRun) {
	t.Helper()
	if _, missed := misses[identity.WorkloadID]; missed {
		t.Fatalf("unchanged workload %q entered MISS projection", identity.WorkloadID)
	}
	evidence, reused := prepared.reuse.reused[string(identity.WorkloadID)]
	if !reused || evidence.Identity != identity || evidence.OriginSourceTreeSHA != originTree {
		t.Fatalf("unchanged workload %q did not reuse the cross-tree SQLite origin proof", identity.WorkloadID)
	}
	if _, compiled := prepared.input.WorkloadCompileGroupInputs[string(identity.WorkloadID)]; compiled {
		t.Fatalf("unchanged workload %q entered compile projection", identity.WorkloadID)
	}
}

// assertCompileInputsAreStrictMisses 拒绝把任一 PASS 命中再次送入编译。
func assertCompileInputsAreStrictMisses(t *testing.T, misses map[gate.GateID]struct{}, compileInputs map[string]gate.CompileGroupInput) {
	t.Helper()
	for id := range compileInputs {
		if _, missed := misses[gate.GateID(id)]; !missed {
			t.Fatalf("compile input %q is not a strict fingerprint MISS", id)
		}
	}
}

// assertOriginProofCrossedTreeBoundary 锁定命中来自旧树，而不是候选树自写证据。
func assertOriginProofCrossedTreeBoundary(t *testing.T, originTree string, evidences []gate.WorkloadPassEvidence) {
	t.Helper()
	if len(evidences) == 0 {
		t.Fatal("cross-worktree lookup returned no PASS evidence")
	}
	for _, evidence := range evidences {
		if evidence.OriginSourceTreeSHA != originTree {
			t.Fatalf("PASS evidence origin tree drifted for %q", evidence.Identity.WorkloadID)
		}
	}
}

// TestPreparedRunWorkloadReuseDecisionReturnsCopies 锁定执行前审计不会获得可变内部状态。
func TestPreparedRunWorkloadReuseDecisionReturnsCopies(t *testing.T) {
	prepared := &PreparedRun{reuse: remoteWorkloadReusePreparation{
		identities:  []gate.WorkloadPassIdentity{{WorkloadID: "normal::go-test::./example::TestOne"}},
		cacheMisses: []gate.GateID{"normal::go-test::./example::TestTwo"},
	}}
	identities, misses := prepared.WorkloadReuseDecision()
	identities[0].WorkloadID = "mutated"
	misses[0] = "mutated"
	identities, misses = prepared.WorkloadReuseDecision()
	if identities[0].WorkloadID != "normal::go-test::./example::TestOne" {
		t.Fatal("workload reuse identity snapshot mutated PreparedRun")
	}
	if misses[0] != "normal::go-test::./example::TestTwo" {
		t.Fatal("workload reuse MISS snapshot mutated PreparedRun")
	}
}
