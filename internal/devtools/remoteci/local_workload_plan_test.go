package remoteci

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// requireDarwinLocalWorkloadReceiptTest 只在具备生产 sandbox-exec 的 Darwin 主机封存真实本地收据。
func requireDarwinLocalWorkloadReceiptTest(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("production local executor receipt requires Darwin sandbox-exec")
	}
}

func TestLocalWorkloadPlanSelectionRejectsEmptyDuplicateAndUnknown(t *testing.T) {
	_, catalog, err := canonicalLocalWorkloadCatalog(strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range [][]gate.GateID{
		nil,
		{gate.GateIDFrontendLint, gate.GateIDFrontendLint},
		{gate.GateID("unknown:workload")},
	} {
		if err := validateLocalWorkloadPlanSelection(catalog, selected); err == nil {
			t.Fatalf("validateLocalWorkloadPlanSelection(%v) unexpectedly succeeded", selected)
		}
	}
}

func TestBuildLocalWorkloadPlanItemsAllIneligibleAllowNilReceiptAsDirectRemote(t *testing.T) {
	workloads := []gate.Workload{
		{ID: string(gate.GateIDFrontendLint)},
		{ID: string(gate.GateIDReleaseLayeredCheck)},
		{ID: string(gate.GateIDLSPChangedDiagnostics)},
	}
	projections := []gate.LocalWorkloadPlanningProjection{
		{WorkloadID: gate.GateIDFrontendLint, PredictedDurationMS: 1, ResourceCPU: 1, ResourceMemoryGiB: 1},
		{WorkloadID: gate.GateIDReleaseLayeredCheck, PredictedDurationMS: 1, ResourceCPU: 1, ResourceMemoryGiB: 1},
		{WorkloadID: gate.GateIDLSPChangedDiagnostics, PredictedDurationMS: 1, ResourceCPU: 1, ResourceMemoryGiB: 1},
	}
	items, proofs, err := buildLocalWorkloadPlanItems(gate.WorkloadCatalog{Workloads: workloads}, projections, nil)
	if err != nil {
		t.Fatalf("buildLocalWorkloadPlanItems() error = %v", err)
	}
	if len(proofs) != 0 || len(items) != len(projections) {
		t.Fatalf("direct-remote plan = items=%#v proofs=%#v", items, proofs)
	}
	for _, item := range items {
		if item.LocalKey != (gate.WorkloadPassKey{}) || item.LocalIdentity != (gate.WorkloadPassIdentity{}) || item.LocalEligible {
			t.Fatalf("direct-remote item carries local state: %#v", item)
		}
	}
}

func TestBuildLocalWorkloadPlanItemEligibleRejectsMissingReceiptCoverage(t *testing.T) {
	workloadID := gate.GateIDCodemapCheck
	workload := gate.Workload{ID: string(workloadID), InputDigest: "sha256:" + strings.Repeat("a", 64)}
	_, _, err := buildLocalWorkloadPlanItem(map[gate.GateID]gate.Workload{workloadID: workload}, gate.LocalWorkloadPlanningProjection{
		WorkloadID: workloadID, PredictedDurationMS: 1, ResourceCPU: 1, ResourceMemoryGiB: 1,
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "producer-sealed executor receipt") {
		t.Fatalf("eligible missing receipt error = %v, want receipt coverage rejection", err)
	}
}

func TestBuildLocalWorkloadPlanItemsMixedReceiptOnlyCarriesEligibleIdentity(t *testing.T) {
	requireDarwinLocalWorkloadReceiptTest(t)
	repositoryRoot, tree := localWorkloadPlanTestRepositoryRootAndTree(t)
	eligibleID := gate.GateIDCodemapCheck
	_, catalog, err := canonicalLocalWorkloadCatalog(tree)
	if err != nil {
		t.Fatal(err)
	}
	eligibleWorkload := localWorkloadPlanTestCatalogWorkload(t, catalog, eligibleID)
	eligibleWorkload.InputDigest = "sha256:" + strings.Repeat("b", 64)
	cacheRoot, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatalf("go env GOMODCACHE: %v", err)
	}
	receipt, err := gate.NewLocalExecutorSessionReceipt(context.Background(), repositoryRoot, tree, []gate.GateID{eligibleID}, gate.LocalExecutorDependencyInputs{GoModuleCache: strings.TrimSpace(string(cacheRoot))})
	if err != nil {
		t.Fatalf("NewLocalExecutorSessionReceipt() error = %v", err)
	}
	workloads := []gate.Workload{
		eligibleWorkload,
		{ID: string(gate.GateIDFrontendLint)},
		{ID: string(gate.GateIDReleaseLayeredCheck)},
		{ID: string(gate.GateIDLSPChangedDiagnostics)},
	}
	projections := []gate.LocalWorkloadPlanningProjection{
		{WorkloadID: eligibleID, PredictedDurationMS: 1, ResourceCPU: 1, ResourceMemoryGiB: 1},
		{WorkloadID: gate.GateIDFrontendLint, PredictedDurationMS: 1, ResourceCPU: 1, ResourceMemoryGiB: 1},
		{WorkloadID: gate.GateIDReleaseLayeredCheck, PredictedDurationMS: 1, ResourceCPU: 1, ResourceMemoryGiB: 1},
		{WorkloadID: gate.GateIDLSPChangedDiagnostics, PredictedDurationMS: 1, ResourceCPU: 1, ResourceMemoryGiB: 1},
	}
	items, proofs, err := buildLocalWorkloadPlanItems(gate.WorkloadCatalog{Workloads: workloads}, projections, receipt)
	if err != nil {
		t.Fatalf("buildLocalWorkloadPlanItems() error = %v", err)
	}
	if len(items) != len(projections) || len(proofs) != 1 {
		t.Fatalf("mixed plan = items=%#v proofs=%#v", items, proofs)
	}
	assertLocalWorkloadPlanIdentity(t, items[0], proofs[0], eligibleID, true)
	assertLocalWorkloadPlanItemsDirectRemote(t, items[1:], "mixed")
}

func TestLocalWorkloadPlanExecutionDigestRejectsCatalogDrift(t *testing.T) {
	workload := gate.Workload{ID: string(gate.GateIDFrontendLint), CommandDigest: strings.Repeat("a", 64)}
	if _, err := localWorkloadPlanExecutionDigest(workload); err == nil {
		t.Fatal("localWorkloadPlanExecutionDigest accepted a drifted catalog command")
	}
}

func TestLocalWorkloadPlanIdentityUsesLocalNamespaceKey(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	identity, err := localWorkloadPlanIdentity(gate.GateIDFrontendLint, digest, digest, digest)
	if err != nil {
		t.Fatal(err)
	}
	local := gate.NewWorkloadPassKey(gate.WorkloadPassNamespaceLocal, identity.IdentityDigest)
	remote := gate.NewWorkloadPassKey(gate.WorkloadPassNamespaceRemote, identity.IdentityDigest)
	if err := local.Validate(); err != nil {
		t.Fatal(err)
	}
	if local == remote || local.String() == remote.String() {
		t.Fatalf("local key %q aliases remote key %q", local, remote)
	}
}

func TestBuildLocalWorkloadPlanItemKnownUnmappedDoesNotReadReceiptEnvironment(t *testing.T) {
	workloadID := gate.GateIDLSPChangedDiagnostics
	item, proof, err := buildLocalWorkloadPlanItem(map[gate.GateID]gate.Workload{
		workloadID: {ID: string(workloadID)},
	}, gate.LocalWorkloadPlanningProjection{
		WorkloadID: workloadID, PredictedDurationMS: 1234, ResourceCPU: 2, ResourceMemoryGiB: 4,
	}, nil)
	if err != nil {
		t.Fatalf("buildLocalWorkloadPlanItem() error = %v", err)
	}
	if item.WorkloadID != workloadID || item.Resource != (gate.LocalWorkloadResource{DurationMS: 1234, CPU: 2, MemoryGiB: 4}) || item.LocalEligible {
		t.Fatalf("known-unmapped item = %#v, want direct remote-only schedule item", item)
	}
	if item.LocalKey != (gate.WorkloadPassKey{}) || item.LocalIdentity != (gate.WorkloadPassIdentity{}) || proof != (LocalWorkloadInputProof{}) {
		t.Fatalf("known-unmapped item/proof carries local PASS state: item=%#v proof=%#v", item, proof)
	}
}

func TestBuildLocalWorkloadPlanItemMappedIneligibleCarriesWorkloadIdentityAndKey(t *testing.T) {
	requireDarwinLocalWorkloadReceiptTest(t)
	repositoryRoot, tree := localWorkloadPlanTestRepositoryRootAndTree(t)
	_, catalog, err := canonicalLocalWorkloadCatalog(tree)
	if err != nil {
		t.Fatal(err)
	}
	workloadID := gate.GateIDWhitespaceCheck
	workload := localWorkloadPlanTestCatalogWorkload(t, catalog, workloadID)
	workload.InputDigest = "sha256:" + strings.Repeat("a", 64)
	receipt, err := gate.NewLocalExecutorSessionReceipt(context.Background(), repositoryRoot, tree, []gate.GateID{workloadID}, gate.LocalExecutorDependencyInputs{})
	if err != nil {
		t.Fatalf("NewLocalExecutorSessionReceipt() error = %v", err)
	}
	item, proof, err := buildLocalWorkloadPlanItem(map[gate.GateID]gate.Workload{workloadID: workload}, gate.LocalWorkloadPlanningProjection{
		WorkloadID: workloadID, PredictedDurationMS: 1234, ResourceCPU: 2, ResourceMemoryGiB: 4,
	}, receipt)
	if err != nil {
		t.Fatalf("buildLocalWorkloadPlanItem() error = %v", err)
	}
	if item.WorkloadID != workloadID || item.LocalIdentity.WorkloadID != workloadID || item.LocalEligible {
		t.Fatalf("mapped ineligible item = %#v, want workload ID-bound ineligible local identity", item)
	}
	if item.LocalKey.Namespace != gate.WorkloadPassNamespaceLocal || item.LocalKey.IdentityDigest != item.LocalIdentity.IdentityDigest || proof.WorkloadID != workloadID {
		t.Fatalf("mapped ineligible item/proof = %#v/%#v, want matching local namespace proof", item, proof)
	}
}

func TestBuildLocalWorkloadPlanItemEligibleCodemapCarriesHighTierLocalIdentity(t *testing.T) {
	requireDarwinLocalWorkloadReceiptTest(t)
	repositoryRoot, tree := localWorkloadPlanTestRepositoryRootAndTree(t)
	_, catalog, err := canonicalLocalWorkloadCatalog(tree)
	if err != nil {
		t.Fatal(err)
	}
	workloadID := gate.GateIDCodemapCheck
	workload := localWorkloadPlanTestCatalogWorkload(t, catalog, workloadID)
	workload.InputDigest = "sha256:" + strings.Repeat("b", 64)
	cacheRoot, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatalf("go env GOMODCACHE: %v", err)
	}
	receipt, err := gate.NewLocalExecutorSessionReceipt(context.Background(), repositoryRoot, tree, []gate.GateID{workloadID}, gate.LocalExecutorDependencyInputs{GoModuleCache: strings.TrimSpace(string(cacheRoot))})
	if err != nil {
		t.Fatalf("NewLocalExecutorSessionReceipt() error = %v", err)
	}
	item, proof, err := buildLocalWorkloadPlanItem(map[gate.GateID]gate.Workload{workloadID: workload}, gate.LocalWorkloadPlanningProjection{WorkloadID: workloadID, PredictedDurationMS: 80_001, ResourceCPU: 8, ResourceMemoryGiB: 16}, receipt)
	if err != nil {
		t.Fatalf("buildLocalWorkloadPlanItem() error = %v", err)
	}
	assertLocalWorkloadPlanIdentity(t, item, proof, workloadID, true)
	if item.Resource != (gate.LocalWorkloadResource{DurationMS: 80_001, CPU: 8, MemoryGiB: 16}) {
		t.Fatalf("eligible resource = %#v, want high-tier projection", item.Resource)
	}
}

func localWorkloadPlanTestRepositoryRootAndTree(t *testing.T) (string, string) {
	t.Helper()
	repositoryRoot, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	output, err := exec.Command("git", "-C", repositoryRoot, "rev-parse", "HEAD^{tree}").Output()
	if err != nil {
		t.Fatalf("read test tree: %v", err)
	}
	return repositoryRoot, strings.TrimSpace(string(output))
}

func localWorkloadPlanTestCatalogWorkload(t *testing.T, catalog gate.WorkloadCatalog, workloadID gate.GateID) gate.Workload {
	t.Helper()
	for _, workload := range catalog.Workloads {
		if gate.GateID(workload.ID) == workloadID {
			return workload
		}
	}
	t.Fatalf("canonical local catalog does not contain %q", workloadID)
	return gate.Workload{}
}

func assertLocalWorkloadPlanIdentity(t *testing.T, item gate.LocalWorkloadScheduleItem, proof LocalWorkloadInputProof, workloadID gate.GateID, eligible bool) {
	t.Helper()
	if item.LocalEligible != eligible || item.WorkloadID != workloadID || item.LocalIdentity.WorkloadID != workloadID {
		t.Fatalf("local item = %#v, want workload ID-bound local identity with eligible=%v", item, eligible)
	}
	if item.LocalKey.Namespace != gate.WorkloadPassNamespaceLocal || item.LocalKey.IdentityDigest != item.LocalIdentity.IdentityDigest || proof.WorkloadID != workloadID {
		t.Fatalf("local item/proof = %#v/%#v, want matching local namespace proof", item, proof)
	}
}

func assertLocalWorkloadPlanItemsDirectRemote(t *testing.T, items []gate.LocalWorkloadScheduleItem, planName string) {
	t.Helper()
	for _, item := range items {
		if item.LocalKey != (gate.WorkloadPassKey{}) || item.LocalIdentity != (gate.WorkloadPassIdentity{}) || item.LocalEligible {
			t.Fatalf("%s direct-remote item carries local state: %#v", planName, item)
		}
	}
}
