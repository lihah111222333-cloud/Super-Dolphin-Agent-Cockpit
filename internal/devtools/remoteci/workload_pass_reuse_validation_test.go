package remoteci

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRemoteWorkloadPassInputDigestsRejectsUnboundCatalogWithoutExactTreeFallback(t *testing.T) {
	input := RunInput{RepositoryRoot: "/path/that/must/not/be/read", Tree: "tree-unbound"}
	workloads := []gate.Workload{{ID: "backend:unbound", Shardable: true}}
	_, err := remoteWorkloadPassInputDigests(context.Background(), input, workloads)
	if err == nil || !strings.Contains(err.Error(), "input digest") {
		t.Fatalf("remoteWorkloadPassInputDigests() error = %v, want missing bound input digest", err)
	}
}

func TestRemoteCIWorkloadResultsRejectsFreshExecutionWithoutIdentity(t *testing.T) {
	result := RunResult{
		JobID: "job-fresh-identity", AcceptedGeneration: 1,
		WorkloadPassIdentities:  []gate.WorkloadPassIdentity{{WorkloadID: "guard:present"}},
		FreshWorkloadExecutions: []gate.PlanGateExecution{{GateID: "guard:present"}, {GateID: "guard:missing-identity"}},
	}
	workloadResults, err := remoteCIWorkloadResults(result)
	if err == nil || !strings.Contains(err.Error(), "missing WorkloadPassIdentity") {
		t.Fatalf("remoteCIWorkloadResults() error = %v, want missing identity observation", err)
	}
	if len(workloadResults) != 1 || workloadResults[0].Identity.WorkloadID != "guard:present" {
		t.Fatalf("remoteCIWorkloadResults() = %#v, want preserved verifiable execution", workloadResults)
	}
}

// TestValidateRemoteWorkloadPassEvidenceKeepsOnlyLookupBoundaryGuards locks the
// split between gate.LookupWorkloadPassEvidence's complete evidence validation
// and the coordinator's workload-ID projection.  The projection must not
// re-run execution or EvidenceSHA validation after the gate API returns.
func TestValidateRemoteWorkloadPassEvidenceKeepsOnlyLookupBoundaryGuards(t *testing.T) {
	workload := gate.Workload{ID: "backend:reuse-boundary", CommandDigest: strings.Repeat("d", 64), InputDigest: "sha256:" + strings.Repeat("a", 64), Shardable: true}
	identity, err := remoteWorkloadPassIdentity(workload, nil, "sha256:"+strings.Repeat("e", 64))
	if err != nil {
		t.Fatalf("remoteWorkloadPassIdentity() error = %v", err)
	}
	wanted := remoteWorkloadPassIdentityIndex([]gate.WorkloadPassIdentity{identity})

	// Deliberately omit OriginExecution and EvidenceSHA256.  Such a value can
	// only reach this helper after gate.LookupWorkloadPassEvidence has already
	// validated the stored row; this layer only checks requested identity and
	// duplicate/overlap safety.
	evidence := gate.WorkloadPassEvidence{Identity: identity}
	if err := validateRemoteWorkloadPassEvidence(evidence, wanted, map[string]gate.WorkloadPassEvidence{}); err != nil {
		t.Fatalf("lookup-boundary validation rejected gate-validated projection: %v", err)
	}

	if err := validateRemoteWorkloadPassEvidence(evidence, wanted, map[string]gate.WorkloadPassEvidence{
		string(identity.WorkloadID): evidence,
	}); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate workload evidence error = %v, want duplicate guard", err)
	}

	other := identity
	other.WorkloadID = "backend:not-requested"
	if err := validateRemoteWorkloadPassEvidence(gate.WorkloadPassEvidence{Identity: other}, wanted, map[string]gate.WorkloadPassEvidence{}); err == nil || !strings.Contains(err.Error(), "not requested") {
		t.Fatalf("unrequested workload evidence error = %v, want requested-identity guard", err)
	}
}
