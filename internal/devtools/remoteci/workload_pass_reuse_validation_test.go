package remoteci

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestValidateRemoteWorkloadPassEvidenceKeepsOnlyLookupBoundaryGuards locks the
// split between gate.LookupWorkloadPassEvidence's complete evidence validation
// and the coordinator's workload-ID projection.  The projection must not
// re-run execution or EvidenceSHA validation after the gate API returns.
func TestValidateRemoteWorkloadPassEvidenceKeepsOnlyLookupBoundaryGuards(t *testing.T) {
	identity, _ := migrationIdentityPair(t, "backend:reuse-boundary", "a", "b")
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
