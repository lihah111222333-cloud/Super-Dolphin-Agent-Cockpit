package remotecicli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterocinode"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteruntime"
)

func TestRuntimeProbeTransportBindsExactEndpointAndContextBody(t *testing.T) {
	topology := executeTopologyFixture()
	config := executeRuntimeFixture("/tmp/remote-ci-topology.json", topology)
	fake := newAdapterRuntimeFake(config)
	target := adapterTargetFacts()
	response := heteroci.ProbeResponse{SchemaVersion: heteroci.ProbeResponseSchemaVersion, OwnerEpoch: 1, CapacityBasis: heteroci.SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations, NodeID: remoteruntime.Ubuntu01NodeID, Endpoint: topology.Nodes[0].Endpoint, Platform: topology.Nodes[0].Platform, ProbeIdentity: topology.Nodes[0].ProbeIdentity, ProbeBootID: "boot-01", Status: heteroci.NodeStatusLive, HeartbeatUnixMilli: 1, Total: heteroci.ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 1}, Available: heteroci.ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 1}}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	fake.probeResult = remoteruntime.NodeResult{NodeID: topology.Nodes[0].NodeID, Identity: config.Nodes[0].ProbeIdentity, Raw: raw}
	transport, err := NewRuntimeProbeTransport(fake, target, topology)
	if err != nil {
		t.Fatalf("NewRuntimeProbeTransport() error = %v", err)
	}
	body, err := transport.Probe(context.Background(), topology.Nodes[0].Endpoint, []byte("probe"))
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	read, err := readAdapterProbeBody(context.Background(), body)
	if err != nil || !bytes.Equal(read, raw) {
		t.Fatalf("probe body = %q/%v, want %q", read, err, raw)
	}
	if err := body.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := transport.Probe(context.Background(), "ssh://unknown.invalid", []byte("probe")); !errors.Is(err, ErrRuntimeAdapter) {
		t.Fatalf("unknown endpoint error = %v", err)
	}
	if fake.probeNode != topology.Nodes[0].NodeID || fake.probeTarget != target {
		t.Fatalf("probe binding = node %q target %#v", fake.probeNode, fake.probeTarget)
	}
}

func TestExactExecutorBinderFourNodesCarriesTargetPlatformCGOAndDigests(t *testing.T) {
	portable := adapterPortableRun(t)
	expanded := append([]remoteportable.PortableManifestWorkload(nil), portable.Manifest.Workloads...)
	portable.Manifest.Workloads = nil
	portable.Manifest.CapacityPolicyDigest = mustAdapterCapacityDigest(t, portable.Manifest)
	var digestErr error
	portable.ManifestDigest, digestErr = remoteportable.PortableManifestContentDigest(portable.Manifest)
	if digestErr != nil {
		t.Fatal(digestErr)
	}
	topology := executeTopologyFixture()
	config := executeRuntimeFixture("/tmp/remote-ci-topology.json", topology)
	config.Nodes[2].CGOEnabled, config.Nodes[3].CGOEnabled = "1", "1"
	fake := newAdapterRuntimeFake(config)
	binder, err := NewExactExecutorBinder(fake, portable, expanded, adapterDigest("module"), newAdapterReceiptFactory())
	if err != nil {
		t.Fatalf("NewExactExecutorBinder() error = %v", err)
	}
	plan, batches := adapterBindingPlan(t, config, expanded)
	plan.ManifestDigest = portable.ManifestDigest
	plan.BindingDigest = mustAdapterBindingDigest(t, plan)
	bound, err := binder.Bind(plan)
	if err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	assertAdapterFourNodeExecution(t, bound, batches)
	assertAdapterRuntimeFacts(t, fake)
	assertAdapterRuntimeRequests(t, fake)
}

func assertAdapterFourNodeExecution(t *testing.T, bound map[heterociworkflow.ExecutorKey]heterociworkflow.BatchExecutor, batches map[heterociworkflow.ExecutorKey]heterociworkflow.ExecutionBatch) {
	t.Helper()
	for key, batch := range batches {
		executor := bound[key]
		if executor == nil {
			t.Fatalf("missing executor for %v", key)
		}
		result, err := executor.ExecuteBatch(context.Background(), batch)
		if err != nil {
			t.Fatalf("ExecuteBatch(%s) error = %v", key.NodeID, err)
		}
		if len(result.Outcomes) != 1 || result.Outcomes[0].State != heteroci.LeaseStateSucceeded {
			t.Fatalf("batch result for %s = %#v", key.NodeID, result)
		}
	}
}

func assertAdapterRuntimeFacts(t *testing.T, fake *adapterRuntimeFake) {
	t.Helper()
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	if len(fake.runs) != 4 {
		t.Fatalf("runtime calls = %d, want 4", len(fake.runs))
	}
	for _, run := range fake.runs {
		wantOS, wantCGO := "linux", "0"
		if strings.HasPrefix(run.nodeID, "Windows-") {
			wantOS, wantCGO = "windows", "1"
		}
		if run.facts.GOOS != wantOS || run.facts.GOARCH != "amd64" || run.facts.CGOEnabled != wantCGO || run.facts.ModuleIdentity != adapterDigest("module") || run.facts.WorkloadIdentity == "" {
			t.Fatalf("runtime facts for %s = %#v", run.nodeID, run.facts)
		}
	}
}

func assertAdapterRuntimeRequests(t *testing.T, fake *adapterRuntimeFake) {
	t.Helper()
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	for _, run := range fake.runs {
		if run.target != adapterTargetFacts() || len(run.request.Workloads) != 1 || !strings.HasPrefix(run.request.Workloads[0].CommandDigest, "sha256:") || !strings.HasPrefix(run.request.Workloads[0].ExecutionDigest, "sha256:") {
			t.Fatalf("runtime binding for %s = target %#v request %#v", run.nodeID, run.target, run.request)
		}
	}
}

func TestExactExecutorBinderAcceptsManualCatalogExpandedSuperset(t *testing.T) {
	portable := adapterPortableRun(t)
	expanded := append([]remoteportable.PortableManifestWorkload(nil), portable.Manifest.Workloads...)
	extra := expanded[len(expanded)-1]
	extra.UnitID = "windows/unit-03"
	extra.ExecutionDigest = mustAdapterWorkloadDigest(t, extra)
	expanded = append(expanded, extra)
	topology := executeTopologyFixture()
	config := executeRuntimeFixture("/tmp/remote-ci-topology.json", topology)
	if _, err := NewExactExecutorBinder(newAdapterRuntimeFake(config), portable, expanded, adapterDigest("module"), newAdapterReceiptFactory()); err != nil {
		t.Fatalf("manual+catalog expanded superset error = %v", err)
	}
}

func TestExactExecutorBinderRejectsCatalogOnlyWithoutExpandedWorkloads(t *testing.T) {
	portable := adapterPortableRun(t)
	portable.Manifest.Workloads = nil
	topology := executeTopologyFixture()
	config := executeRuntimeFixture("/tmp/remote-ci-topology.json", topology)
	_, err := NewExactExecutorBinder(newAdapterRuntimeFake(config), portable, nil, adapterDigest("module"), newAdapterReceiptFactory())
	if !errors.Is(err, ErrRuntimeAdapter) {
		t.Fatalf("missing expanded workloads error = %v", err)
	}
}

func TestExactExecutorBinderRejectsMissingCleanupWithoutRetry(t *testing.T) {
	portable := adapterPortableRun(t)
	topology := executeTopologyFixture()
	config := executeRuntimeFixture("/tmp/remote-ci-topology.json", topology)
	fake := newAdapterRuntimeFake(config)
	fake.cleanup = false
	binder, err := NewExactExecutorBinder(fake, portable, portable.Manifest.Workloads, adapterDigest("module"), newAdapterReceiptFactory())
	if err != nil {
		t.Fatalf("NewExactExecutorBinder() error = %v", err)
	}
	plan, batches := adapterBindingPlan(t, config, portable.Manifest.Workloads)
	plan.ManifestDigest = portable.ManifestDigest
	plan.BindingDigest = mustAdapterBindingDigest(t, plan)
	bound, err := binder.Bind(plan)
	if err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	key, batch := firstAdapterBatch(batches)
	if _, err := bound[key].ExecuteBatch(context.Background(), batch); !errors.Is(err, ErrRuntimeAdapterCleanup) {
		t.Fatalf("cleanup error = %v", err)
	}
	fake.mutex.Lock()
	runs := len(fake.runs)
	fake.mutex.Unlock()
	if runs != 1 {
		t.Fatalf("runtime calls = %d, want exactly one", runs)
	}
}

func TestSucceededExecuteTerminalShardsUseFourBoundNodes(t *testing.T) {
	portable := adapterPortableRun(t)
	topology := executeTopologyFixture()
	config := executeRuntimeFixture("/tmp/remote-ci-topology.json", topology)
	plan, batches := adapterBindingPlan(t, config, portable.Manifest.Workloads)
	leases := make([]heteroci.RoutingLease, 0, len(batches))
	units := make([]heteroci.ExecutionUnit, 0, len(batches))
	unitResults := make([]heterociworkflow.UnitResult, 0, len(batches))
	var fence heteroci.OwnerFence
	for _, batch := range batches {
		lease := batch.Started[0].Lease
		lease.State, lease.StartedAtUnixMilli, lease.FinishedAtUnixMilli = heteroci.LeaseStateSucceeded, 2_000, 3_000
		leases = append(leases, lease)
		units = append(units, batch.Units[0])
		unitResults = append(unitResults, heterociworkflow.UnitResult{UnitID: lease.UnitID, State: heteroci.LeaseStateSucceeded})
		fence = batch.Started[0].Fence
	}
	snapshot := heterociworkflow.PreparedRunSnapshot{ExternalOperationID: plan.ExternalOperationID, Fence: fence, Units: units, BindingPlan: plan, BindingDigest: plan.BindingDigest}
	resultFence := fence
	resultFence.Revision++
	result := heterociworkflow.RunResult{Authority: heteroci.AuthorityLocalNonAuthoritative, Status: heterociworkflow.ResultStatusNotVerified, Fence: resultFence, BindingPlan: plan, ExecutorBindingDigest: plan.BindingDigest, Leases: leases, Units: unitResults}
	shards, startedAt, finishedAt, err := succeededExecuteTerminalShards(snapshot, result)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 4 || startedAt.UnixMilli() != 2_000 || finishedAt.UnixMilli() != 3_000 {
		t.Fatalf("terminal shards/times = %d %v %v", len(shards), startedAt, finishedAt)
	}
	for index, shard := range shards {
		if shard.NodeID != plan.Bindings[index].NodeID || shard.CleanupState != heterociexec.PostgreSQLReceiptCleanupVerified || shard.Status != heterociexec.PostgreSQLReceiptStatusSucceeded {
			t.Fatalf("terminal shard %d = %#v", index, shard)
		}
	}
}

func TestExecuteWritersAreBoundedAndRejectShortWrite(t *testing.T) {
	var output bytes.Buffer
	if err := writeBoundedExecuteJSON(&output, []byte(`{"ok":true}`), 64); err != nil || output.String() != "{\"ok\":true}\n" {
		t.Fatalf("bounded output = %q, %v", output.String(), err)
	}
	if err := writeBoundedExecuteJSON(shortExecuteWriter{}, []byte(`{}`), 64); !errors.Is(err, ErrExecuteComposition) {
		t.Fatalf("short write error = %v", err)
	}
	if err := writeBoundedExecuteJSON(&output, []byte(`{"oversize":true}`), 2); !errors.Is(err, ErrExecuteComposition) {
		t.Fatalf("oversize error = %v", err)
	}
}

type shortExecuteWriter struct{}

func (shortExecuteWriter) Write([]byte) (int, error) { return 0, nil }

func TestRunPreparedExecuteFailureClosesWithoutTerminal(t *testing.T) {
	closed := false
	components := executeComponents{
		owner: &executeOwnerStub{}, receipts: &executeReceiptAuthorityStub{}, probe: &executeProbeStub{}, binder: &executeBinderStub{},
		close: func() error { closed = true; return nil },
	}
	dependencies := executeCommandDependencies{
		clock: func() time.Time { return time.UnixMilli(10_000).UTC() },
		components: func(context.Context, executePreparedInputs, func() time.Time) (executeComponents, error) {
			return components, nil
		},
		run: func(context.Context, heterociworkflow.Dependencies, heterociworkflow.RunRequest) (heterociworkflow.RunResult, error) {
			return heterociworkflow.RunResult{}, errors.New("workflow failed")
		},
	}
	prepared := executePreparedInputs{Portable: remoteportable.BoundPortableRun{Manifest: remoteportable.PortableManifest{LogPolicy: remoteportable.PortableManifestLogPolicy{MaxResultBytes: 1024, MaxEventBytes: 1024}}}}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	err := runPreparedExecute(context.Background(), prepared, stdout, stderr, dependencies)
	if err == nil || !closed || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("failure close/output = err=%v closed=%t stdout=%q stderr=%q", err, closed, stdout, stderr)
	}
}

type executeOwnerStub struct{ heterociworkflow.DurableOwner }
type executeReceiptAuthorityStub struct{ executeReceiptAuthority }
type executeProbeStub struct{ heteroci.ProbeTransport }
type executeBinderStub struct {
	heterociworkflow.ExecutorBinder
}

func readAdapterProbeBody(ctx context.Context, body heteroci.ProbeResponseBody) ([]byte, error) {
	var output bytes.Buffer
	buffer := make([]byte, 128)
	for {
		read, err := body.Read(ctx, buffer)
		if read > 0 {
			_, _ = output.Write(buffer[:read])
		}
		if errors.Is(err, io.EOF) {
			return output.Bytes(), nil
		}
		if err != nil {
			return nil, err
		}
	}
}

type adapterRun struct {
	target  remoteruntime.TargetFacts
	facts   remoteruntime.ExecutionFacts
	nodeID  string
	request heterocinode.BatchRequest
}

type adapterRuntimeFake struct {
	mutex       sync.Mutex
	config      remoteruntime.RuntimeConfig
	probeResult remoteruntime.NodeResult
	probeNode   string
	probeTarget remoteruntime.TargetFacts
	runs        []adapterRun
	cleanup     bool
}

func newAdapterRuntimeFake(config remoteruntime.RuntimeConfig) *adapterRuntimeFake {
	return &adapterRuntimeFake{config: config, cleanup: true}
}

func (fake *adapterRuntimeFake) Config() remoteruntime.RuntimeConfig { return fake.config }

func (fake *adapterRuntimeFake) Probe(_ context.Context, target remoteruntime.TargetFacts, nodeID string, _ []byte) (remoteruntime.NodeResult, error) {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()
	fake.probeNode, fake.probeTarget = nodeID, target
	return fake.probeResult, nil
}

func (fake *adapterRuntimeFake) Run(_ context.Context, target remoteruntime.TargetFacts, facts remoteruntime.ExecutionFacts, nodeID string, request []byte) (remoteruntime.NodeResult, error) {
	decoded, err := heterocinode.DecodeBatchRequest(request)
	if err != nil {
		return remoteruntime.NodeResult{}, err
	}
	fake.mutex.Lock()
	fake.runs = append(fake.runs, adapterRun{target: target, facts: facts, nodeID: nodeID, request: decoded})
	fake.mutex.Unlock()
	result := heterocinode.BatchResult{SchemaVersion: heterocinode.BatchResultSchemaVersion, Authority: heterocinode.BatchAuthorityLocalNonAuthoritative, Status: heterocinode.BatchStatusNotVerified, Outcome: heterocinode.BatchOutcomeSuccess}
	for _, workload := range decoded.Workloads {
		result.Outcomes = append(result.Outcomes, heterocinode.BatchOutcome{UnitID: workload.UnitID, Status: heterocinode.BatchOutcomeSuccess, ExitCode: 0})
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return remoteruntime.NodeResult{}, err
	}
	for _, node := range fake.config.Nodes {
		if node.NodeID == nodeID {
			return remoteruntime.NodeResult{NodeID: nodeID, Identity: node.RunnerIdentity, Raw: raw, CleanupVerified: fake.cleanup}, nil
		}
	}
	return remoteruntime.NodeResult{}, errors.New("fake node is missing")
}

type adapterReceiptStore struct {
	mutex   sync.Mutex
	claims  map[string]bool
	results map[string]heterociworkflow.BatchResult
}

func newAdapterReceiptFactory() ReceiptStoreFactory {
	return func(remoteruntime.NodeBinding) (heterociexec.ReceiptStore, error) {
		return &adapterReceiptStore{claims: map[string]bool{}, results: map[string]heterociworkflow.BatchResult{}}, nil
	}
}

func (store *adapterReceiptStore) key(batch heterociworkflow.ExecutionBatch) string {
	return string(batch.BatchID) + ":" + batch.MaterialDigest
}
func (store *adapterReceiptStore) Claim(_ context.Context, batch heterociworkflow.ExecutionBatch, _ string) (bool, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	key := store.key(batch)
	if store.claims[key] {
		return false, nil
	}
	store.claims[key] = true
	return true, nil
}
func (store *adapterReceiptStore) Read(_ context.Context, batch heterociworkflow.ExecutionBatch, _ string) (heterociworkflow.BatchResult, bool, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	result, ok := store.results[store.key(batch)]
	return result, ok, nil
}
func (store *adapterReceiptStore) WriteIfAbsent(_ context.Context, result heterociworkflow.BatchResult) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.results[string(result.BatchID)+":"+result.MaterialDigest] = result
	return nil
}

func adapterPortableRun(t *testing.T) remoteportable.BoundPortableRun {
	t.Helper()
	digest := adapterDigest("manifest")
	workloads := []remoteportable.PortableManifestWorkload{}
	for _, item := range []struct{ id, platform, runtime string }{{"linux/unit-01", string(remoteportable.RepositoryWorkloadPlatformLinuxAMD64), string(remoteportable.RepositoryWorkloadRuntimeUbuntuContainer)}, {"linux/unit-02", string(remoteportable.RepositoryWorkloadPlatformLinuxAMD64), string(remoteportable.RepositoryWorkloadRuntimeUbuntuContainer)}, {"windows/unit-01", string(remoteportable.RepositoryWorkloadPlatformWindowsAMD64), string(remoteportable.RepositoryWorkloadRuntimeWindowsNative)}, {"windows/unit-02", string(remoteportable.RepositoryWorkloadPlatformWindowsAMD64), string(remoteportable.RepositoryWorkloadRuntimeWindowsNative)}} {
		workload := remoteportable.PortableManifestWorkload{UnitID: item.id, CommandDigest: adapterDigest(item.id + "-command"), TargetPlatform: item.platform, RuntimeCapability: item.runtime, CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, Kind: remoteportable.RepositoryWorkloadKindGoTest, ModuleRoot: ".", Package: "./example", Name: "TestExample", ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)}
		workload.ExecutionDigest = mustAdapterWorkloadDigest(t, workload)
		workloads = append(workloads, workload)
	}
	manifest := remoteportable.PortableManifest{SchemaVersion: remoteportable.PortableManifestSchemaVersion, ContractID: remoteportable.PortableManifestContractID, Engine: remoteportable.PortableManifestEngine{RequiredProtocolABIDigest: digest, RequiredToolchainDigest: digest, RequiredCapabilities: []string{"go"}}, Target: remoteportable.PortableManifestTarget{RepositoryID: "fixture", ObjectFormat: "sha1", WorktreeMount: "worktree"}, Workloads: workloads, CatalogPolicies: []remoteportable.PortableManifestCatalogPolicy{{Platform: string(remoteportable.RepositoryWorkloadPlatformLinuxAMD64), Runtime: string(remoteportable.RepositoryWorkloadRuntimeUbuntuContainer), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)}, {Platform: string(remoteportable.RepositoryWorkloadPlatformWindowsAMD64), Runtime: string(remoteportable.RepositoryWorkloadRuntimeWindowsNative), CPUMilli: 1_000, MemoryBytes: 1 << 30, HistoricalEstimateMS: 1_000, ExecutionPolicy: string(remoteportable.RepositoryWorkloadExecutionPolicyNormal)}}, Profile: remoteportable.ProfileLocalFast, RequiredNodeCapabilities: []string{"go1.26.5"}, LogPolicy: remoteportable.PortableManifestLogPolicy{ResultSchema: "remote-ci-result/v1", EventSchema: "remote-ci-event/v1", MaxResultBytes: 1 << 20, MaxEventBytes: 64 << 10}, Authority: remoteportable.PortableManifestAuthority{ContractID: remoteportable.PortableManifestContractID, PostgreSQLIdentity: "remote-ci-postgresql-primary/v1"}, Modules: []remoteportable.RepositoryWorkloadModule{{Root: ".", ModulePath: "example.invalid/fixture"}}, ExcludedNestedModules: []string{}}
	manifest.CapacityPolicyDigest = mustAdapterCapacityDigest(t, manifest)
	if err := manifest.Validate(); err != nil {
		t.Fatalf("portable manifest fixture: %v", err)
	}
	manifestDigest, err := remoteportable.PortableManifestContentDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return remoteportable.BoundPortableRun{Manifest: manifest, ManifestDigest: manifestDigest, ManifestBlobSHA256: digest, RepositoryID: "fixture", ObjectFormat: "sha1", CommitSHA: strings.Repeat("a", 40), TreeSHA: strings.Repeat("b", 40), ManifestPath: "ci/workloads.json", BindingSHA256: digest}
}

func adapterBindingPlan(t *testing.T, config remoteruntime.RuntimeConfig, workloads []remoteportable.PortableManifestWorkload) (heterociworkflow.ExecutorBindingPlan, map[heterociworkflow.ExecutorKey]heterociworkflow.ExecutionBatch) {
	t.Helper()
	identity, err := heteroci.DeriveTopologyRunIdentity("fixture", strings.Repeat("b", 40), strings.Repeat("r", 64))
	if err != nil {
		t.Fatal(err)
	}
	operation, err := heteroci.ParseOperationID(strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	plan := heterociworkflow.ExecutorBindingPlan{SchemaVersion: heterociworkflow.ExecutorBindingPlanSchemaVersion, ExternalOperationID: operation, DurableRunID: identity.DurableRunID, RequestDigest: adapterDigest("request"), ManifestDigest: adapterDigest("manifest"), ExternalBindingDigest: adapterDigest("binding"), ManifestBlobSHA256: adapterDigest("blob"), RuntimePolicyDigest: adapterDigest("runtime"), TopologyManifestDigest: adapterDigest("topology"), PlanDigest: adapterDigest("plan"), AuthorityGeneration: 1}
	batches := map[heterociworkflow.ExecutorKey]heterociworkflow.ExecutionBatch{}
	for index, node := range config.Nodes {
		key := heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: node.NodeID}
		workload := workloads[index]
		batchID, err := heterociworkflow.DeriveBatchID(string(identity.DurableRunID), key, 0, []string{workload.UnitID})
		if err != nil {
			t.Fatal(err)
		}
		bindingNodeDigest, err := heterociworkflow.DeriveExecutorBindingNodeIDDigest(node.NodeID, key.Kind)
		if err != nil {
			t.Fatal(err)
		}
		plan.Bindings = append(plan.Bindings, heterociworkflow.ExecutorBinding{NodeID: node.NodeID, NodeIDDigest: bindingNodeDigest, ExecutorKind: key.Kind, BatchIDs: []heterociworkflow.BatchID{batchID}, ShardDigest: adapterDigest(node.NodeID + "-shard"), BatchSetDigest: adapterDigest(node.NodeID + "-batch"), UnitSetDigest: adapterDigest(node.NodeID + "-unit"), NodeFactsDigest: adapterDigest(node.NodeID + "-facts")})
		batches[key] = adapterExecutionBatch(t, node, workload, string(identity.DurableRunID), batchID)
	}
	plan.BindingDigest, err = heterociworkflow.DeriveExecutorBindingAggregateDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	return plan, batches
}

func adapterExecutionBatch(t *testing.T, node remoteruntime.NodeBinding, workload remoteportable.PortableManifestWorkload, runID string, batchID heterociworkflow.BatchID) heterociworkflow.ExecutionBatch {
	t.Helper()
	key := heterociworkflow.ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: node.NodeID}
	unit := heteroci.ExecutionUnit{UnitID: workload.UnitID, TargetPlatform: heteroci.Platform(workload.TargetPlatform), RequiredRuntime: heteroci.RuntimeKind(workload.RuntimeCapability), EstimatedDurationMS: workload.HistoricalEstimateMS, CPUMilli: workload.CPUMilli, MemoryBytes: workload.MemoryBytes}
	assignment := heteroci.RouteAssignment{UnitID: unit.UnitID, Executor: key.Kind, NodeID: node.NodeID, Endpoint: node.Endpoint, ProbeIdentity: node.ProbeIdentity, ProbeBootID: "boot-" + node.NodeID}
	started := heteroci.StartedRoutingLease{Fence: heteroci.OwnerFence{Scope: heteroci.RoutingOwnerScope(adapterDigest("scope")), OwnerID: heteroci.RoutingOwnerID(strings.Repeat("c", 64)), OwnerEpoch: 1, Revision: 1}, Lease: heteroci.RoutingLease{SchemaVersion: heteroci.RoutingLeaseSchemaVersion, Authority: heteroci.AuthorityLocalNonAuthoritative, RunID: runID, DecisionDigest: adapterDigest("decision"), UnitID: unit.UnitID, Executor: key.Kind, NodeID: node.NodeID, Endpoint: node.Endpoint, ProbeIdentity: node.ProbeIdentity, ProbeBootID: assignment.ProbeBootID, State: heteroci.LeaseStateStarted}, Occupancy: heteroci.StartedRoutingOccupancy{OwnerEpoch: 1, RunID: runID, UnitID: unit.UnitID, DecisionDigest: adapterDigest("decision")}}
	batch := heterociworkflow.ExecutionBatch{BatchID: batchID, RunID: runID, Key: key, Wave: 0, Units: []heteroci.ExecutionUnit{unit}, Assignments: []heteroci.RouteAssignment{assignment}, Started: []heteroci.StartedRoutingLease{started}}
	material, err := heterociworkflow.DeriveExecutionMaterialDigest(batch)
	if err != nil {
		t.Fatalf("material digest: %v", err)
	}
	batch.MaterialDigest = material
	return batch
}

func firstAdapterBatch(batches map[heterociworkflow.ExecutorKey]heterociworkflow.ExecutionBatch) (heterociworkflow.ExecutorKey, heterociworkflow.ExecutionBatch) {
	for key, batch := range batches {
		return key, batch
	}
	return heterociworkflow.ExecutorKey{}, heterociworkflow.ExecutionBatch{}
}

func mustAdapterBindingDigest(t *testing.T, plan heterociworkflow.ExecutorBindingPlan) string {
	t.Helper()
	digest, err := heterociworkflow.DeriveExecutorBindingAggregateDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func adapterTargetFacts() remoteruntime.TargetFacts {
	return remoteruntime.TargetFacts{RepositoryID: "fixture", ObjectFormat: "sha1", CommitSHA: strings.Repeat("a", 40), TreeSHA: strings.Repeat("b", 40)}
}

func mustAdapterWorkloadDigest(t *testing.T, workload remoteportable.PortableManifestWorkload) string {
	t.Helper()
	digest, err := remoteportable.DerivePortableWorkloadExecutionDigest(workload)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func mustAdapterCapacityDigest(t *testing.T, manifest remoteportable.PortableManifest) string {
	t.Helper()
	digest, err := remoteportable.DerivePortableCapacityPolicyDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func adapterDigest(string) string {
	return "sha256:" + strings.Repeat("a", 64)
}
