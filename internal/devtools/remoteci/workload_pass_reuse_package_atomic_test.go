package remoteci

import (
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestClassifyRemoteWorkloadPassesMixedSamePackageKeepsConfirmedPass(t *testing.T) {
	hit := reusePackageTestIdentity(t, gate.GateIDBackendTestWithGuard, "./fixture/shared", "TestHit")
	miss := reusePackageTestIdentity(t, gate.GateIDBackendTestWithGuard, "./fixture/shared", "TestMiss")
	reused, misses, err := classifyRemoteWorkloadPassesStrict(
		[]gate.WorkloadPassIdentity{hit, miss},
		map[string]gate.WorkloadPassEvidence{string(hit.WorkloadID): {Identity: hit}},
	)
	if err != nil {
		t.Fatalf("classifyRemoteWorkloadPassesStrict() error = %v", err)
	}
	if len(reused) != 1 || reused[0].Identity.WorkloadID != hit.WorkloadID || !slices.Equal(misses, []gate.GateID{miss.WorkloadID}) {
		t.Fatalf("same-package mixed reuse = reused=%#v misses=%#v, want only confirmed MISS", reused, misses)
	}
	effectiveReused, err := indexRemoteWorkloadPassEvidence(reused)
	if err != nil {
		t.Fatalf("indexRemoteWorkloadPassEvidence() error = %v", err)
	}
	if err := validateRemoteWorkloadMissIDs(misses, effectiveReused); err != nil {
		t.Fatalf("validateRemoteWorkloadMissIDs() error = %v", err)
	}
}

func TestRemoteWorkloadReuseDiagnosticSeparatesDirectAndConfirmedMisses(t *testing.T) {
	preparation := remoteWorkloadReusePreparation{
		directHits: 1, exactHits: 1, directMisses: 1, replayMisses: 1,
		reusedWorkloads: []gate.WorkloadPassEvidence{{Identity: gate.WorkloadPassIdentity{WorkloadID: "direct-hit"}}},
		cacheMisses:     []gate.GateID{"direct-miss"},
	}
	diagnostic := preparation.diagnostic()
	if diagnostic.DirectHits != 1 || diagnostic.ExactHits != 1 || diagnostic.DirectMisses != 1 ||
		diagnostic.MissConfirmationThreshold != 2 || diagnostic.RecoveredDirectMisses != 0 ||
		diagnostic.ReplayMisses != 1 || diagnostic.AtomicDemoted != 0 ||
		diagnostic.EffectiveHits != 1 || diagnostic.EffectiveMisses != 1 {
		t.Fatalf("reuse diagnostic = %#v", diagnostic)
	}
}

func TestRemoteWorkloadReuseDiagnosticMarksForcedBypass(t *testing.T) {
	diagnostic := (remoteWorkloadReusePreparation{
		forced: true, directMisses: 1, replayMisses: 1,
		cacheMisses: []gate.GateID{"forced"},
	}).diagnostic()
	if !diagnostic.Forced || diagnostic.MissConfirmationThreshold != 0 || diagnostic.EffectiveMisses != 1 {
		t.Fatalf("forced reuse diagnostic = %#v", diagnostic)
	}
}

func TestRemoteReuseMissRequiresTwoIndependentConfirmations(t *testing.T) {
	identity := gate.WorkloadPassIdentity{WorkloadID: "confirmed-miss"}
	identities := []gate.WorkloadPassIdentity{identity}
	confirmations := remoteReuseMissConfirmations{string(identity.WorkloadID): remoteReuseDirectMiss}
	if err := validateRemoteReuseMissConsensus(identities, nil, confirmations); err == nil {
		t.Fatal("single lookup MISS entered remote execution")
	}
	confirmations.confirm(identity.WorkloadID, remoteReuseSourceMiss)
	if err := validateRemoteReuseMissConsensus(identities, nil, confirmations); err != nil {
		t.Fatalf("two independent MISS confirmations were rejected: %v", err)
	}
	if err := validateRemoteReuseMissConsensus(identities, map[string]gate.WorkloadPassEvidence{string(identity.WorkloadID): {}}, nil); err != nil {
		t.Fatalf("authoritative PASS was subjected to MISS voting: %v", err)
	}
}

func TestRemoteReuseDiagnosticGroupsKeepConfirmedSamePackagePass(t *testing.T) {
	hit := reusePackageTestIdentity(t, gate.GateIDBackendTestWithGuard, "./fixture/shared", "TestHit")
	miss := reusePackageTestIdentity(t, gate.GateIDBackendTestWithGuard, "./fixture/shared", "TestMiss")
	queried := map[string]gate.WorkloadPassEvidence{string(hit.WorkloadID): {Identity: hit}}
	groups, err := remoteReuseDiagnosticGroups([]gate.WorkloadPassIdentity{hit, miss}, queried, queried)
	if err != nil {
		t.Fatal(err)
	}
	want := ReuseDiagnosticGroup{TargetKind: "go-test", TargetGroup: "./fixture/shared", ExactHits: 1, DirectMisses: 1, EffectiveHits: 1, EffectiveMisses: 1}
	if len(groups) != 1 || groups[0] != want {
		t.Fatalf("reuse diagnostic groups = %#v, want %#v", groups, want)
	}
}

func TestRemoteCIWorkloadResultsKeepAtomicReexecutionOnCanonicalProof(t *testing.T) {
	result, proofResult := atomicReexecutionTestResult(t)
	results, err := remoteCIWorkloadResults(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0] != proofResult {
		t.Fatalf("atomic reexecution result = %#v, want canonical proof result %#v", results, proofResult)
	}
}

func TestRemoteCIWorkloadResultsRejectAtomicProofWithoutFreshExecution(t *testing.T) {
	result, _ := atomicReexecutionTestResult(t)
	result.FreshWorkloadExecutions = nil
	if _, err := remoteCIWorkloadResults(result); err == nil {
		t.Fatal("atomic proof without fresh execution was accepted")
	}
}

func TestRemoteReexecutedWorkloadResultsProjectOnlyDemotedHits(t *testing.T) {
	_, proofResult := atomicReexecutionTestResult(t)
	evidence := gate.WorkloadPassEvidence{Identity: proofResult.Identity, OriginJobID: proofResult.OriginJobID, OriginAcceptedGeneration: proofResult.OriginAcceptedGeneration, EvidenceSHA256: proofResult.EvidenceSHA256}
	queried := map[string]gate.WorkloadPassEvidence{string(proofResult.Identity.WorkloadID): evidence}
	results, err := remoteReexecutedWorkloadResults([]gate.WorkloadPassIdentity{proofResult.Identity}, queried, map[string]gate.WorkloadPassEvidence{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0] != proofResult {
		t.Fatalf("demoted proof projection = %#v, want %#v", results, proofResult)
	}
	results, err = remoteReexecutedWorkloadResults([]gate.WorkloadPassIdentity{proofResult.Identity}, queried, queried, nil)
	if err != nil || len(results) != 0 {
		t.Fatalf("effective hit entered reexecution projection: results=%#v error=%v", results, err)
	}
}

func atomicReexecutionTestResult(t *testing.T) (RunResult, gate.RemoteCIWorkloadResult) {
	t.Helper()
	identity := gate.WorkloadPassIdentity{WorkloadID: "atomic-reexecuted", ExecutionDigest: "sha256:" + strings.Repeat("a", 64), InputDigest: "sha256:" + strings.Repeat("b", 64), EnvironmentDigest: "sha256:" + strings.Repeat("c", 64)}
	identityDigest, err := gate.WorkloadPassIdentitySHA256(identity)
	if err != nil {
		t.Fatal(err)
	}
	identity.IdentityDigest = identityDigest
	proofResult := gate.RemoteCIWorkloadResult{Identity: identity, Disposition: gate.WorkloadDispositionReused, OriginJobID: "canonical-origin", OriginAcceptedGeneration: 7, EvidenceSHA256: "sha256:" + strings.Repeat("d", 64)}
	result := RunResult{
		JobID: "atomic-consumer", AcceptedGeneration: 7,
		WorkloadPassIdentities:    []gate.WorkloadPassIdentity{identity},
		FreshWorkloadExecutions:   []gate.PlanGateExecution{{GateID: identity.WorkloadID}},
		ReexecutedWorkloadResults: []gate.RemoteCIWorkloadResult{proofResult},
	}
	return result, proofResult
}

func TestStrictRemoteWorkloadPassIndexKeepsConfirmedSamePackageHits(t *testing.T) {
	hit := reusePackageTestIdentity(t, gate.GateIDBackendTestWithGuard, "./fixture/shared", "TestHit")
	miss := reusePackageTestIdentity(t, gate.GateIDBackendTestWithGuard, "./fixture/shared", "TestMiss")
	classified, misses, err := classifyRemoteWorkloadPassesStrict(
		[]gate.WorkloadPassIdentity{hit, miss},
		map[string]gate.WorkloadPassEvidence{string(hit.WorkloadID): {Identity: hit}},
	)
	if err != nil {
		t.Fatalf("classifyRemoteWorkloadPassesStrict() error = %v", err)
	}
	indexed, err := indexRemoteWorkloadPassEvidence(classified)
	if err != nil {
		t.Fatalf("index strict reuse evidence: %v", err)
	}
	if err := validateRemoteWorkloadMissIDs(misses, indexed); err != nil {
		t.Fatalf("strict reuse/miss partition overlaps: %v", err)
	}
	if len(indexed) != 1 || indexed[string(hit.WorkloadID)].Identity.WorkloadID != hit.WorkloadID || !slices.Equal(misses, []gate.GateID{miss.WorkloadID}) {
		t.Fatalf("strict same-package partition = indexed=%#v misses=%#v", indexed, misses)
	}
}

func TestClassifyRemoteWorkloadPassesMixedDifferentPackagesKeepsPartialReuse(t *testing.T) {
	hit := reusePackageTestIdentity(t, gate.GateIDBackendTestWithGuard, "./fixture/one", "TestHit")
	miss := reusePackageTestIdentity(t, gate.GateIDBackendTestWithGuard, "./fixture/two", "TestMiss")
	reused, misses, err := classifyRemoteWorkloadPassesStrict(
		[]gate.WorkloadPassIdentity{hit, miss},
		map[string]gate.WorkloadPassEvidence{string(hit.WorkloadID): {Identity: hit}},
	)
	if err != nil {
		t.Fatalf("classifyRemoteWorkloadPassesStrict() error = %v", err)
	}
	if len(reused) != 1 || reused[0].Identity.WorkloadID != hit.WorkloadID || !slices.Equal(misses, []gate.GateID{miss.WorkloadID}) {
		t.Fatalf("different-package mixed reuse = reused=%#v misses=%#v, want partial reuse", reused, misses)
	}
}

func TestClassifyRemoteWorkloadPassesKeepsBenchmarkAndRaceSemanticsSeparate(t *testing.T) {
	normal := reusePackageTestIdentity(t, gate.GateIDBackendTestWithGuard, "./fixture/shared", "TestNormal")
	benchmarkWorkload, err := gate.NewGoBenchmarkWorkload(gate.GateIDBackendTestWithGuard, "./fixture/shared", "BenchmarkShared", 1)
	if err != nil {
		t.Fatalf("NewGoBenchmarkWorkload() error = %v", err)
	}
	benchmark := gate.WorkloadPassIdentity{WorkloadID: gate.GateID(benchmarkWorkload.ID)}
	race := reusePackageTestIdentity(t, gate.GateIDBackendTestGuardWithRace, "./fixture/shared", "TestRace")
	reused, misses, err := classifyRemoteWorkloadPassesStrict(
		[]gate.WorkloadPassIdentity{normal, benchmark, race},
		map[string]gate.WorkloadPassEvidence{
			string(normal.WorkloadID):    {Identity: normal},
			string(benchmark.WorkloadID): {Identity: benchmark},
		},
	)
	if err != nil {
		t.Fatalf("classifyRemoteWorkloadPassesStrict() error = %v", err)
	}
	if len(reused) != 2 || !slices.Equal([]gate.GateID{reused[0].Identity.WorkloadID, reused[1].Identity.WorkloadID}, []gate.GateID{normal.WorkloadID, benchmark.WorkloadID}) {
		t.Fatalf("normal/benchmark reuse = %#v, want both independent hits", reused)
	}
	if !slices.Equal(misses, []gate.GateID{race.WorkloadID}) {
		t.Fatalf("race boundary misses = %v, want %v", misses, []gate.GateID{race.WorkloadID})
	}
}

func reusePackageTestIdentity(t *testing.T, parent gate.GateID, packageTarget, name string) gate.WorkloadPassIdentity {
	t.Helper()
	workload, err := gate.NewGoTestWorkload(parent, packageTarget, name, 1)
	if err != nil {
		t.Fatalf("NewGoTestWorkload(%q, %q) error = %v", packageTarget, name, err)
	}
	return gate.WorkloadPassIdentity{WorkloadID: gate.GateID(workload.ID)}
}
