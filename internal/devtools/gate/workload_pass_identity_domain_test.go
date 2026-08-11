package gate

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
)

// TestWorkloadPassIdentitySHA256DomainVector 固定 pass-identity/v2 的规范向量，防止域材料漂移。
func TestWorkloadPassIdentitySHA256DomainVector(t *testing.T) {
	identity := WorkloadPassIdentity{
		WorkloadID:        "guard:vector",
		ExecutionDigest:   "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		InputDigest:       "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		EnvironmentDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	got, err := WorkloadPassIdentitySHA256(identity)
	if err != nil {
		t.Fatalf("WorkloadPassIdentitySHA256() error = %v", err)
	}
	const want = "sha256:c6f93f3f1aac07914ba0eecbdc490eb239fa4e945e932ea2807980895865eaec"
	if got != want {
		t.Fatalf("WorkloadPassIdentitySHA256() = %q, want fixed vector %q", got, want)
	}
}

// TestValidateCanonicalWorkloadPassIdentityRejectsInvalidDigestMaterial 锁定 authority 不能接受未经 Validate 的 identity。
func TestValidateCanonicalWorkloadPassIdentityRejectsInvalidDigestMaterial(t *testing.T) {
	workload := Workload{
		ID:            "guard:identity-validation",
		CommandDigest: "command-v1",
		InputDigest:   digestForWorkloadPass("input-v1"),
		Shardable:     true,
	}
	identity := WorkloadPassIdentity{
		WorkloadID:        GateID(workload.ID),
		ExecutionDigest:   WorkloadPassExecutionDigest(workload),
		InputDigest:       workload.InputDigest,
		EnvironmentDigest: "invalid-environment-digest",
	}
	if err := validateCanonicalWorkloadPassIdentity(identity, map[GateID]Workload{GateID(workload.ID): workload}); err == nil {
		t.Fatal("validateCanonicalWorkloadPassIdentity accepted invalid digest material")
	}
}

// TestWorkloadPassIdentityLegacyNoDomainIsNaturalMiss 锁定旧无域 identity 只能 MISS，不能兼容映射命中。
func TestWorkloadPassIdentityLegacyNoDomainIsNaturalMiss(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, receipts := recordWorkloadPassRun(t, store, "domain-v2-origin", 1, "domain-v2-workload")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatalf("finalize domain-v2 origin: %v", err)
	}
	oldDigest := legacyWorkloadPassIdentityDigestForTest(t, identity)
	if oldDigest == identity.IdentityDigest {
		t.Fatal("legacy no-domain digest unexpectedly equals v2 identity digest")
	}
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	if _, err := database.Exec(`UPDATE ci_workload_pass_evidence SET identity_digest = ? WHERE identity_digest = ?`, oldDigest, identity.IdentityDigest); err != nil {
		t.Fatalf("rewrite fixture as old no-domain evidence: %v", err)
	}
	got, err := store.LookupWorkloadPassEvidence([]WorkloadPassIdentity{identity})
	if err != nil {
		t.Fatalf("lookup v2 identity against old evidence: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("old no-domain evidence was reused: %#v", got)
	}
}

// legacyWorkloadPassIdentityDigestForTest 独立重现旧无域摘要，避免测试复用生产迁移分类器。
func legacyWorkloadPassIdentityDigestForTest(t *testing.T, identity WorkloadPassIdentity) string {
	t.Helper()
	payload, err := json.Marshal(struct {
		WorkloadID        GateID `json:"workload_id"`
		ExecutionDigest   string `json:"execution_digest"`
		InputDigest       string `json:"input_digest"`
		EnvironmentDigest string `json:"environment_digest"`
	}{identity.WorkloadID, identity.ExecutionDigest, identity.InputDigest, identity.EnvironmentDigest})
	if err != nil {
		t.Fatalf("encode legacy no-domain identity: %v", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest)
}
