package remoteci

import (
	"slices"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestClassifyRemoteWorkloadPassesMixedSamePackageIsAtomicMiss(t *testing.T) {
	hit := reusePackageTestIdentity(t, gate.GateIDBackendTestWithGuard, "./fixture/shared", "TestHit")
	miss := reusePackageTestIdentity(t, gate.GateIDBackendTestWithGuard, "./fixture/shared", "TestMiss")
	reused, misses, err := classifyRemoteWorkloadPassesStrict(
		[]gate.WorkloadPassIdentity{hit, miss},
		map[string]gate.WorkloadPassEvidence{string(hit.WorkloadID): {Identity: hit}},
	)
	if err != nil {
		t.Fatalf("classifyRemoteWorkloadPassesStrict() error = %v", err)
	}
	if len(reused) != 0 || !slices.Equal(misses, []gate.GateID{hit.WorkloadID, miss.WorkloadID}) {
		t.Fatalf("same-package mixed reuse = reused=%#v misses=%#v, want atomic MISS", reused, misses)
	}
	effectiveReused, err := indexRemoteWorkloadPassEvidence(reused)
	if err != nil {
		t.Fatalf("indexRemoteWorkloadPassEvidence() error = %v", err)
	}
	if err := validateRemoteWorkloadMissIDs(misses, effectiveReused); err != nil {
		t.Fatalf("validateRemoteWorkloadMissIDs() error = %v", err)
	}
}

func TestStrictRemoteWorkloadPassIndexExcludesAtomicPackageMisses(t *testing.T) {
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
