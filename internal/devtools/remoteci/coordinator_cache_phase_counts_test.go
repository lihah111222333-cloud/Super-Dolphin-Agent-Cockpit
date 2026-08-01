package remoteci

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRemoteGoTestCachePhaseCountsKeepsParentAndChildCatalogsSeparate(t *testing.T) {
	lookup := remoteWorkloadCacheLookup{
		workerWorkloads: []gate.Workload{{ID: "parent-a"}, {ID: "parent-b"}, {ID: "parent-c"}},
		resume: remoteGoTestResumeSet{
			workloadsByParent: map[string][]gate.Workload{
				"parent-c": {{ID: "child-a"}, {ID: "child-b"}},
			},
			entries: []remoteWorkloadCacheEntry{{workloadID: "child-a"}, {workloadID: "child-b"}},
		},
	}
	cached := map[string]gate.PlanGateExecution{
		"parent-a": {GateID: "parent-a"},
		"parent-b": {GateID: "parent-b"},
		"child-a":  {GateID: "child-a"},
		"child-b":  {GateID: "child-b"},
	}

	child, projection, err := remoteGoTestCachePhaseCounts(lookup, cached)
	if err != nil {
		t.Fatal(err)
	}
	if child != (remoteCIPhaseCounts{workloads: 2, cacheHits: 2}) {
		t.Fatalf("child counts = %#v", child)
	}
	if projection != (remoteCIPhaseCounts{workloads: 4, cacheHits: 4}) {
		t.Fatalf("projection counts = %#v", projection)
	}
}

func TestRemoteGoTestCachePhaseCountsRejectsUnknownChildHit(t *testing.T) {
	lookup := remoteWorkloadCacheLookup{
		workerWorkloads: []gate.Workload{{ID: "parent"}},
		resume: remoteGoTestResumeSet{
			workloadsByParent: map[string][]gate.Workload{"parent": {{ID: "child"}}},
			entries:           []remoteWorkloadCacheEntry{{workloadID: "child"}},
		},
	}
	cached := map[string]gate.PlanGateExecution{
		"child":   {GateID: "child"},
		"unknown": {GateID: "unknown"},
	}

	if _, _, err := remoteGoTestCachePhaseCounts(lookup, cached); err == nil {
		t.Fatal("unknown child cache hit was accepted")
	}
}
