package remoteci

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

type missReuseLPTFixture struct {
	plan     gate.GatePlan
	catalog  gate.WorkloadCatalog
	snapshot gate.DurationLedgerSnapshot
	context  gate.PlanningContext
	input    RunInput
	allIDs   []gate.GateID
}

// TestRemoteWorkloadMissesRejectReuseOverlapBeforeRemoteSideEffects 守护 reused 与 miss 交集在 splitter 前失败。
func TestRemoteWorkloadMissesRejectReuseOverlapBeforeRemoteSideEffects(t *testing.T) {
	objectKeys := []string{"sentinel-object"}
	createdGroups := []string{"sentinel-group"}
	_, err := (&Coordinator{}).runRemoteWorkloadMisses(
		context.Background(),
		RunInput{},
		gate.GatePlan{},
		gate.WorkloadCatalog{},
		[]gate.GateID{"workload-a"},
		map[string]gate.WorkloadPassEvidence{"workload-a": {}},
		"job-test",
		"/unused",
		&objectKeys,
		&createdGroups,
		RunResult{},
	)
	if err == nil || !strings.Contains(err.Error(), "both reused and a miss") {
		t.Fatalf("runRemoteWorkloadMisses() error = %v, want reuse/miss overlap rejection", err)
	}
	if !slices.Equal(objectKeys, []string{"sentinel-object"}) || !slices.Equal(createdGroups, []string{"sentinel-group"}) {
		t.Fatalf("overlap rejection changed remote side-effect tracking: objectKeys=%v createdGroups=%v", objectKeys, createdGroups)
	}
}

// TestMissReuseReplansLPTWithoutFullPlanHoles 验证部分复用只对 miss 重新执行 LPT 分片。
func TestMissReuseReplansLPTWithoutFullPlanHoles(t *testing.T) {
	fixture := newMissReuseLPTFixture(t)
	fullPlan := mustBuildMissReusePlan(t, fixture, fixture.allIDs)
	misses := classifyMissReuseFixture(t, fullPlan, fixture.allIDs)
	missPlan := mustBuildMissReusePlan(t, fixture, misses)
	set, err := buildRemoteExecutionShardSetForWorkloads(fixture.plan, fixture.catalog, misses, fixture.input)
	if err != nil {
		t.Fatalf("buildRemoteExecutionShardSetForWorkloads() error = %v", err)
	}
	assertMissReusePlan(t, set, missPlan, misses)
	assertFilteredFullPlanHasHoles(t, fullPlan, misses, len(set.WorkloadPlan.Shards))
}

func newMissReuseLPTFixture(t *testing.T) missReuseLPTFixture {
	t.Helper()
	runnerDigest := "sha256:" + strings.Repeat("a", 64)
	toolchainDigest := "sha256:" + strings.Repeat("b", 64)
	inputDigest := "sha256:" + strings.Repeat("c", 64)
	treeSHA := strings.Repeat("d", 40)
	plan, err := gate.BuildGatePlan(gate.ProfileRelease, gate.SourceSpec{
		Kind: gate.SourceKindTree, ObjectFormat: gate.GitObjectFormatSHA1,
		Tree: &gate.TreeSource{SHA: treeSHA, ParentCommitSHA: strings.Repeat("e", 40)}, SourceTreeSHA: treeSHA,
	})
	if err != nil {
		t.Fatalf("BuildGatePlan() error = %v", err)
	}
	goTests := make([]gate.GoTestTarget, 6)
	for index := range goTests {
		goTests[index] = gate.GoTestTarget{Package: fmt.Sprintf("./fixture/pkg%d", index), Name: fmt.Sprintf("TestFixture%d", index)}
	}
	catalog, err := gate.BuildSelectedTestWorkloadCatalog(plan, gate.WorkloadInventory{GoTests: goTests})
	if err != nil {
		t.Fatalf("BuildSelectedTestWorkloadCatalog() error = %v", err)
	}
	for index := range catalog.Workloads {
		// Production Prepare binds every exact selector to its own source-tree
		// input digest before planning; keep this fixture on that same identity.
		catalog.Workloads[index].InputDigest = inputDigest
	}
	ledger := gate.DurationLedger{Version: 1}
	durations := []int64{60_000, 60_000, 40_000, 40_000, 40_000, 40_000}
	for index, workload := range catalog.Workloads {
		ledger.Samples = append(ledger.Samples, gate.DurationSample{
			Bucket: gate.DurationBucket{WorkloadID: workload.ID, CommandDigest: workload.CommandDigest, InputDigest: inputDigest,
				Platform: "linux/amd64", Runner: runnerDigest, Toolchain: toolchainDigest,
				ExecutionMode: gate.DurationExecutionModeCalibration, ResourceClassID: "calibration",
				ResourceCPU: cicontract.CalibrationResourceCPU, ResourceMemoryGiB: cicontract.CalibrationResourceMemoryGiB},
			Succeeded: true, DurationMS: durations[index],
		})
	}
	appendMissReuseSelectorTimingSamples(t, &ledger, catalog, inputDigest, runnerDigest, toolchainDigest, durations)
	snapshot := gate.DurationLedgerSnapshot{Generation: 1, Ledger: ledger}
	context := gate.PlanningContext{Platform: "linux/amd64", Runner: runnerDigest, Toolchain: toolchainDigest,
		Calibration: true, CalibrationResourceClassID: "calibration", CalibrationResourceCPU: cicontract.CalibrationResourceCPU,
		CalibrationResourceMemoryGiB: cicontract.CalibrationResourceMemoryGiB,
		TargetDurationMS:             gate.FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-miss-replan"}
	allIDs := make([]gate.GateID, 0, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		allIDs = append(allIDs, gate.GateID(workload.ID))
	}
	compileInputs := missReuseCompileInputs(t, catalog, inputDigest)
	return missReuseLPTFixture{plan: plan, catalog: catalog, snapshot: snapshot, context: context, allIDs: allIDs,
		input: RunInput{LedgerSnapshot: snapshot, Calibration: true, Platform: context.Platform,
			ToolchainDigest: toolchainDigest, RunnerIdentityDigest: runnerDigest,
			RunnerConfigDigest: "sha256:" + strings.Repeat("f", 64), ImageCacheSnapshotID: context.AcceptedSnapshotID,
			CalibrationResource:        shardresource.Class{ID: "calibration", VCPU: cicontract.CalibrationResourceCPU, MemoryGiB: cicontract.CalibrationResourceMemoryGiB},
			WorkloadCompileGroupInputs: compileInputs}}
}

// appendMissReuseSelectorTimingSamples 为每个精确 Go selector 补齐父包与测试体的权威校准样本。
// 仅有 workload 总时长样本时 compile-aware planner 会合法使用 1s bootstrap，无法证明多分片；
// 这里按真实 package+selector identity 写入同一 calibration 资源桶，避免 fixture 失去覆盖。
func appendMissReuseSelectorTimingSamples(t *testing.T, ledger *gate.DurationLedger, catalog gate.WorkloadCatalog, inputDigest, runnerDigest, toolchainDigest string, durations []int64) {
	t.Helper()
	if len(catalog.Workloads) != len(durations) {
		t.Fatalf("selector timing durations=%d, catalog workloads=%d", len(durations), len(catalog.Workloads))
	}
	for index, workload := range catalog.Workloads {
		_, _, payload, targeted, err := gate.ParseWorkloadID(workload.ID)
		if err != nil || !targeted {
			t.Fatalf("ParseWorkloadID(%q) = targeted=%t err=%v", workload.ID, targeted, err)
		}
		target, err := gate.ParseGoTestTarget(payload)
		if err != nil {
			t.Fatalf("ParseGoTestTarget(%q): %v", workload.ID, err)
		}
		parent, err := gate.NewGoPackageWorkload(gate.GateIDBackendTestWithGuard, target.Package, durations[index])
		if err != nil {
			t.Fatalf("NewGoPackageWorkload(%q): %v", target.Package, err)
		}
		bucket := func(workloadID, commandDigest string) gate.DurationBucket {
			return gate.DurationBucket{
				WorkloadID: workloadID, CommandDigest: commandDigest, InputDigest: inputDigest,
				Platform: "linux/amd64", Runner: runnerDigest, Toolchain: toolchainDigest,
				ExecutionMode: gate.DurationExecutionModeCalibration, ResourceClassID: "calibration",
				ResourceCPU: cicontract.CalibrationResourceCPU, ResourceMemoryGiB: cicontract.CalibrationResourceMemoryGiB,
			}
		}
		ledger.Samples = append(ledger.Samples, gate.DurationSample{
			Bucket: bucket(parent.ID, parent.CommandDigest), Succeeded: true, DurationMS: durations[index],
		})
		targetSample := gate.DurationSample{
			Bucket: gate.DurationBucket{
				WorkloadID:    gate.GoTestDurationWorkloadID(parent.ID, target.Name),
				CommandDigest: gate.GoTestDurationCommandDigest(parent.CommandDigest, target.Name), InputDigest: inputDigest,
				Platform: "linux/amd64", Runner: runnerDigest, Toolchain: toolchainDigest,
				ExecutionMode: gate.DurationExecutionModeCalibration, ResourceClassID: "calibration",
				ResourceCPU: cicontract.CalibrationResourceCPU, ResourceMemoryGiB: cicontract.CalibrationResourceMemoryGiB,
			},
			Succeeded: true, DurationMS: durations[index], TargetKind: gate.WorkloadKindGoTest,
			ParentWorkloadID: parent.ID, ParentCommandDigest: parent.CommandDigest, TargetName: target.Name, TargetStatus: gate.GoTestStatusPass,
		}
		ledger.Samples = append(ledger.Samples, targetSample)
	}
}

func missReuseCompileInputs(t *testing.T, catalog gate.WorkloadCatalog, inputDigest string) map[string]gate.CompileGroupInput {
	t.Helper()
	compileInputs := make(map[string]gate.CompileGroupInput)
	for _, workload := range catalog.Workloads {
		workloadID := gate.GateID(workload.ID)
		if !gate.CompileGroupWorkloadSupported(workloadID) {
			continue
		}
		_, _, payload, targeted, err := gate.ParseWorkloadID(workload.ID)
		if err != nil || !targeted {
			t.Fatalf("ParseWorkloadID(%q) = targeted=%t err=%v", workload.ID, targeted, err)
		}
		target, err := gate.ParseGoTestTarget(payload)
		if err != nil {
			t.Fatalf("ParseGoTestTarget(%q): %v", workload.ID, err)
		}
		semantic, err := gate.CompileGroupSemanticKeyForWorkloadID(workloadID)
		if err != nil {
			t.Fatalf("CompileGroupSemanticKeyForWorkloadID(%q): %v", workload.ID, err)
		}
		compileInputs[workload.ID] = gate.CompileGroupInput{
			PackageTarget: target.Package, SemanticKey: semantic,
			SharedInputDigest: inputDigest, ProfileDigest: "sha256:" + strings.Repeat("f", 64),
		}
	}
	return compileInputs
}

func mustBuildMissReusePlan(t *testing.T, fixture missReuseLPTFixture, ids []gate.GateID) gate.WorkloadExecutionPlan {
	t.Helper()
	compileInputs, _, err := remoteCompileGroupInputsForExecution(ids, fixture.input.WorkloadCompileGroupInputs)
	if err != nil {
		t.Fatalf("remoteCompileGroupInputsForExecution() error = %v", err)
	}
	plan, err := gate.BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs(fixture.plan, fixture.catalog, fixture.snapshot, fixture.context, ids, compileInputs)
	if err != nil {
		t.Fatalf("BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs() error = %v", err)
	}
	if len(plan.Shards) < 2 {
		t.Fatalf("workload plan shards = %d, want at least two to expose filtered holes", len(plan.Shards))
	}
	return plan
}

func classifyMissReuseFixture(t *testing.T, fullPlan gate.WorkloadExecutionPlan, allIDs []gate.GateID) []gate.GateID {
	t.Helper()
	keep := make(map[gate.GateID]struct{})
	for _, planned := range fullPlan.Shards[0].Workloads {
		keep[gate.GateID(planned.Workload.ID)] = struct{}{}
	}
	identities := make([]gate.WorkloadPassIdentity, 0, len(allIDs))
	reused := make(map[string]gate.WorkloadPassEvidence)
	for _, id := range allIDs {
		identity := gate.WorkloadPassIdentity{WorkloadID: id}
		identities = append(identities, identity)
		if _, ok := keep[id]; ok {
			reused[string(id)] = gate.WorkloadPassEvidence{Identity: identity}
		}
	}
	_, misses, err := classifyRemoteWorkloadPassesStrict(identities, reused)
	if err != nil {
		t.Fatalf("classifyRemoteWorkloadPassesStrict() error = %v", err)
	}
	if len(misses) == 0 || len(misses) == len(allIDs) {
		t.Fatalf("classified misses = %d, want a non-empty partial reuse set", len(misses))
	}
	return misses
}

func assertMissReusePlan(t *testing.T, set gate.ContainerShardSet, want gate.WorkloadExecutionPlan, misses []gate.GateID) {
	t.Helper()
	if !slices.Equal(set.WorkloadPlan.ExecutionWorkloadIDs, misses) {
		t.Fatalf("execution workload IDs = %v, want exact misses %v", set.WorkloadPlan.ExecutionWorkloadIDs, misses)
	}
	if !reflect.DeepEqual(set.WorkloadPlan, want) {
		t.Fatalf("miss workload plan differs from direct miss-only plan:\nset=%#v\nwant=%#v", set.WorkloadPlan, want)
	}
	for index, shard := range set.WorkloadPlan.Shards {
		if len(shard.Workloads) == 0 || len(set.Shards[index].GateIDs) == 0 {
			t.Fatalf("miss shard %d is empty: workload=%#v container=%#v", index, shard, set.Shards[index])
		}
	}
}

func assertFilteredFullPlanHasHoles(t *testing.T, fullPlan gate.WorkloadExecutionPlan, misses []gate.GateID, missShardCount int) {
	t.Helper()
	missSet := make(map[gate.GateID]struct{}, len(misses))
	for _, id := range misses {
		missSet[id] = struct{}{}
	}
	holes := 0
	for _, shard := range fullPlan.Shards {
		if !slices.ContainsFunc(shard.Workloads, func(planned gate.PlannedWorkload) bool {
			_, ok := missSet[gate.GateID(planned.Workload.ID)]
			return ok
		}) {
			holes++
		}
	}
	if holes == 0 {
		t.Fatal("full-plan filtering produced no empty shard; fixture no longer covers hole regression")
	}
	if missShardCount == len(fullPlan.Shards) {
		t.Fatalf("miss-only shard count = %d, full=%d; want a compact replan instead of preserving filtered holes", missShardCount, len(fullPlan.Shards))
	}
}
