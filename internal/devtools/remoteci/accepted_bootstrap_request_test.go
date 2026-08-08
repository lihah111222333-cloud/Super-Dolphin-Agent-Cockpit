package remoteci

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestAcceptedBootstrapProjectionFixtureFromSchema14V2Request(t *testing.T) {
	request, data, requestDigest, decoded := acceptedBootstrapProjectionFixture(t)
	if err := ValidateBootstrapIdentity(decoded, request); err != nil {
		t.Fatal(err)
	}
	assertAcceptedBootstrapProjectionWire(t, data)
	assertAcceptedBootstrapProjectionDigests(t, decoded, requestDigest)
}

func acceptedBootstrapProjectionFixture(t *testing.T) (ShardRequest, []byte, string, BootstrapShardRequest) {
	t.Helper()
	request := testSourceBundleShardRequest(t)
	workload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary", 10)
	if err != nil {
		t.Fatal(err)
	}
	workloadID := gate.GateID(workload.ID)
	request.GateIDs = []gate.GateID{workloadID}
	group := gate.CompileGroup{
		PackageTarget: "./internal/archtest", SemanticKey: gate.CompileGroupSemanticGoTestNormal,
		SharedInputDigest: "sha256:" + strings.Repeat("a", 64), ProfileDigest: "sha256:" + strings.Repeat("b", 64),
		ResourceClassID: request.ResourceClass.ID, WorkloadIDs: []gate.GateID{workloadID},
		SelectorEstimates: []gate.CompileSelectorEstimate{{SelectorID: workloadID, BodyEstimateMS: 20}},
		BatchPlan:         []gate.CompileGroupBatch{{BatchID: "batch-000", Wave: 0, SelectorIDs: []gate.GateID{workloadID}, EstimatedBodyMS: 20}},
		CompileEstimateMS: 10, BodyEstimateMS: 20, EstimatedDurationMS: 30,
	}
	group.BatchPlanDigest, err = gate.CompileGroupBatchPlanDigest(group)
	if err != nil {
		t.Fatal(err)
	}
	group.GroupID, err = gate.CompileGroupID(group)
	if err != nil {
		t.Fatal(err)
	}
	request.CompileGroups = []gate.CompileGroup{group}
	request.ShardExecutionManifestDigest, err = request.ComputeShardExecutionManifestDigest()
	if err != nil {
		t.Fatal(err)
	}
	data, requestDigest, err := EncodeBootstrapShardRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBootstrapShardRequest(data)
	if err != nil {
		t.Fatal(err)
	}
	return request, data, requestDigest, decoded
}

func assertAcceptedBootstrapProjectionWire(t *testing.T, data []byte) {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["selector_estimates"]; ok || strings.Contains(string(data), "selector_estimates") {
		t.Fatalf("accepted bootstrap projection leaked v2 selector estimates: %s", data)
	}
}

func assertAcceptedBootstrapProjectionDigests(t *testing.T, decoded BootstrapShardRequest, requestDigest string) {
	t.Helper()
	if got, want := acceptedCompileArtifactFixture(t, decoded.CompileGroups[0]), "sha256:7875abbabf321f37ad874f08e2f1c370ca8474dd4e00fcc6b022412c066b21a5"; got != want {
		t.Fatalf("accepted artifact identity = %s, want frozen fixture %s", got, want)
	}
	if got, want := decoded.CompileGroups[0].GroupID, "sha256:085b093df0a241b9679676da3ab876e73cea95cd96cc1421a53a711c387a7176"; got != want {
		t.Fatalf("accepted group identity = %s, want frozen fixture %s", got, want)
	}
	if got, want := decoded.ShardExecutionManifestDigest, "sha256:b89bfec39d6c2f8d767b2a3de618e9284fce94c0dc893041d2144cc34d9e651f"; got != want {
		t.Fatalf("accepted manifest identity = %s, want frozen fixture %s", got, want)
	}
	if got, want := requestDigest, "bddb1dc580e9dab96fa57c7487731631059cc0c05ada309dad90a2623ec727d5"; got != want {
		t.Fatalf("accepted request content digest = %s, want frozen fixture %s", got, want)
	}
}

func acceptedCompileArtifactFixture(t *testing.T, group acceptedCompileGroup) string {
	t.Helper()
	digest, err := acceptedCompileArtifactKey(group)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestDecodeBootstrapShardRequestRejectsUnknownNestedV2Field(t *testing.T) {
	request := testSourceBundleShardRequest(t)
	request.CompileGroups = []gate.CompileGroup{}
	manifestDigest, err := request.ComputeShardExecutionManifestDigest()
	if err != nil {
		t.Fatal(err)
	}
	request.ShardExecutionManifestDigest = manifestDigest
	data, _, err := EncodeShardRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	groups, ok := raw["compile_groups"].([]any)
	if !ok || len(groups) != 0 {
		t.Fatalf("fixture compile groups = %#v", raw["compile_groups"])
	}
	raw["compile_groups"] = []any{map[string]any{
		"group_id": "sha256:" + strings.Repeat("a", 64), "package_target": "./internal/archtest",
		"semantic_key": gate.CompileGroupSemanticGoTestNormal, "shared_input_digest": "sha256:" + strings.Repeat("b", 64),
		"profile_digest": "sha256:" + strings.Repeat("c", 64), "resource_class_id": "small",
		"workload_ids": []any{string(gate.GateIDBackendTestWithGuard)}, "selector_estimates": []any{},
		"compile_estimate_ms": 1, "body_estimate_ms": 1, "estimated_duration_ms": 2,
	}}
	data, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeBootstrapShardRequest(data); err == nil {
		t.Fatal("accepted bootstrap decoder unexpectedly accepted full v2 request")
	}
}

func TestAcceptedBootstrapProjectionCollapsesNilnessPackagesAndPreservesRace(t *testing.T) {
	request := testSourceBundleShardRequest(t)
	request.Profile = gate.ProfileRelease
	nilnessTargets := []string{"./internal/alpha", "./internal/beta"}
	ids := make([]gate.GateID, 0, len(nilnessTargets)+1)
	for _, target := range nilnessTargets {
		workload, err := gate.NewGoPackageWorkload(gate.GateIDBackendNilness, target, 10)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, gate.GateID(workload.ID))
	}
	raceWorkload, err := gate.NewGoPackageWorkload(gate.GateIDBackendTestGuardWithRace, "./internal/archtest", 10)
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, gate.GateID(raceWorkload.ID))
	request.GateIDs = ids
	request.ShardExecutionManifestDigest, err = request.ComputeShardExecutionManifestDigest()
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := EncodeBootstrapShardRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBootstrapShardRequest(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []gate.GateID{gate.GateIDBackendNilness, gate.GateID(raceWorkload.ID)}
	if !slices.Equal(decoded.GateIDs, want) {
		t.Fatalf("accepted gate IDs = %v, want %v", decoded.GateIDs, want)
	}
	if err := ValidateBootstrapIdentity(decoded, request); err != nil {
		t.Fatalf("projected bootstrap identity rejected: %v", err)
	}
}
