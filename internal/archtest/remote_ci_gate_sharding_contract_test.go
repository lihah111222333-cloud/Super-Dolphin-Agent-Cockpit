package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestRemoteCIGateWorkloadShardingContract 锁定规范 workload 目录、仅 MISS 规划器、
// 无上限 ECI 分片与 typed release owner 之间的唯一执行边界。
func TestRemoteCIGateWorkloadShardingContract(t *testing.T) {
	root := findRepoRoot(t)
	t.Run("release parents have shardable canonical workloads", func(t *testing.T) {
		assertReleaseParentsHaveShardableWorkloads(t)
	})
	t.Run("expanded catalog enters the workload planner", func(t *testing.T) {
		assertExpandedCatalogPlannerBoundary(t, root)
	})
	t.Run("remote fanout is unlimited and release owner is typed", func(t *testing.T) {
		assertRemoteFanoutAndReleaseOwnerBoundary(t, root)
	})
	t.Run("parent proof is bounded", func(t *testing.T) {
		assertRemoteParentProofBoundary(t, root)
	})
}

// assertReleaseParentsHaveShardableWorkloads 要求除 owner 外的每个 release 前序门禁
// 都在权威目录中提供至少一个可执行分片，owner 本身只能保留为 typed metadata。
func assertReleaseParentsHaveShardableWorkloads(t *testing.T) {
	t.Helper()
	source := gate.SourceSpec{
		Kind:          gate.SourceKindTree,
		ObjectFormat:  gate.GitObjectFormatSHA1,
		Tree:          &gate.TreeSource{SHA: strings.Repeat("a", 40), ParentCommitSHA: strings.Repeat("b", 40)},
		SourceTreeSHA: strings.Repeat("a", 40),
	}
	plan, err := gate.BuildGatePlan(gate.ProfileRelease, source)
	if err != nil {
		t.Fatalf("BuildGatePlan(release): %v", err)
	}
	catalog, err := gate.BuildExpandedWorkloadCatalog(plan, gate.DefaultWorkloadBootstrapPolicy(), gate.WorkloadInventory{
		GoPackages:  []string{"./internal/archtest"},
		GoTests:     []gate.GoTestTarget{{Package: "./internal/archtest", Name: "TestCommon"}},
		GoRaceTests: []gate.GoTestTarget{{Package: "./internal/archtest", Name: "TestRace"}},
	})
	if err != nil {
		t.Fatalf("BuildExpandedWorkloadCatalog(release): %v", err)
	}

	shardableParents := make(map[gate.GateID]bool)
	for _, workload := range catalog.Workloads {
		parent, err := gate.WorkloadParentGateID(workload.ID)
		if err != nil {
			t.Fatalf("WorkloadParentGateID(%q): %v", workload.ID, err)
		}
		if parent == gate.GateIDReleaseLayeredCheck {
			if workload.Shardable {
				t.Fatalf("release owner workload %q is executable/shardable", workload.ID)
			}
			continue
		}
		if workload.Shardable {
			shardableParents[parent] = true
		}
	}
	assertEveryReleaseParentIsShardable(t, plan, shardableParents)
}

// assertEveryReleaseParentIsShardable 校验所有 release 前序门禁都已进入可分片目录。
func assertEveryReleaseParentIsShardable(t *testing.T, plan gate.GatePlan, shardableParents map[gate.GateID]bool) {
	t.Helper()
	for _, spec := range plan.Gates {
		if spec.ID == gate.GateIDReleaseLayeredCheck {
			continue
		}
		if !shardableParents[spec.ID] {
			t.Fatalf("release prerequisite parent %q has no shardable canonical workload", spec.ID)
		}
	}
}

// assertExpandedCatalogPlannerBoundary 要求展开后的原子目标进入 WorkloadExecutionPlan，
// 且远程分片构造器只能消费调用方冻结的 MISS executionIDs。
func assertExpandedCatalogPlannerBoundary(t *testing.T, root string) {
	t.Helper()
	planningPath := filepath.Join(root, "internal/devtools/gate/workload_planning.go")
	planning := readRemoteCIContractGuardFile(t, planningPath)
	assertPlannerCanonicalMarkersPresent(t, planning)
	assertRetiredRemoteCIEntrypointsAbsent(t, root)
	if strings.Contains(remoteCIFunctionSource(t, planningPath, "BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs"), "allShardableWorkloadIDs(catalog)") {
		t.Fatal("MISS-specific workload planner silently expands back to the full catalog")
	}

	coordinatorPath := filepath.Join(root, "internal/devtools/remoteci/coordinator.go")
	coordinator := remoteCIFunctionSource(t, coordinatorPath, "buildRemoteExecutionShardSetForWorkloads")
	for _, marker := range []string{
		"executionIDs",
		"BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs(",
		"BuildContainerShardSetFromWorkloadPlan(",
	} {
		if !strings.Contains(coordinator, marker) {
			t.Fatalf("remote MISS shard builder is missing marker %q", marker)
		}
	}
	if strings.Contains(readRemoteCIContractGuardFile(t, coordinatorPath), "func buildRemoteExecutionShardSet(") {
		t.Fatal("remote CI retains a full-catalog shard planner entrypoint")
	}

	preparedPath := filepath.Join(root, "internal/devtools/remoteci/prepared_run.go")
	runPrepared := remoteCIFunctionSource(t, preparedPath, "RunPrepared")
	for _, forbidden := range []string{
		"PlanLPT(",
		"BuildWorkloadExecutionPlan",
		"buildRemoteExecutionShardSet(",
	} {
		if strings.Contains(runPrepared, forbidden) {
			t.Fatalf("RunPrepared performs planning/execution itself via %q", forbidden)
		}
	}
	missPath := remoteCIFunctionSource(t, preparedPath, "executePreparedWorkloadMisses")
	assertRemoteCIMarkerOrder(t, missPath, "if prepared.allReused", "createRemoteTempRoot(", "runRemoteWorkloadMisses(")
	if !strings.Contains(missPath, "prepared.reuse.cacheMisses") {
		t.Fatal("remote shard planner input is not the frozen MISS set")
	}
}

func assertPlannerCanonicalMarkersPresent(t *testing.T, planning string) {
	t.Helper()
	for _, marker := range []string{
		"BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs(",
		"executionIDs []GateID",
		"workloadExecutionCatalog(",
		"planLPTWithIndex(",
	} {
		if !strings.Contains(planning, marker) {
			t.Fatalf("workload planner is missing canonical MISS/expanded marker %q", marker)
		}
	}
}

type retiredRemoteCIEntrypoint struct {
	packagePath string
	receiver    string
	name        string
}

// assertRetiredRemoteCIEntrypointsAbsent 扫描对应包的全部生产 Go 文件，禁止旧入口换文件复活。
func assertRetiredRemoteCIEntrypointsAbsent(t *testing.T, root string) {
	t.Helper()
	for _, retired := range []retiredRemoteCIEntrypoint{
		{packagePath: "internal/devtools/gate", name: "PlanLPT"},
		{packagePath: "internal/devtools/gate", name: "BuildWorkloadExecutionPlan"},
		{packagePath: "internal/devtools/remoteci", receiver: "Coordinator", name: "Run"},
	} {
		matches, err := findRetiredRemoteCIEntrypoints(root, retired)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range matches {
			t.Errorf("retired remote CI entrypoint %s revived at %s", retired.name, match)
		}
	}
}

func findRetiredRemoteCIEntrypoints(root string, retired retiredRemoteCIEntrypoint) ([]string, error) {
	directory := filepath.Join(root, filepath.FromSlash(retired.packagePath))
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read remote CI package %s: %w", retired.packagePath, err)
	}
	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(directory, name)
		fileSet := token.NewFileSet()
		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return nil, fmt.Errorf("parse remote CI production file %s: %w", path, parseErr)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && isRetiredRemoteCIEntrypoint(function, retired) {
				position := fileSet.Position(function.Pos())
				matches = append(matches, fmt.Sprintf("%s:%d", filepath.ToSlash(position.Filename), position.Line))
			}
		}
	}
	return matches, nil
}

func isRetiredRemoteCIEntrypoint(function *ast.FuncDecl, retired retiredRemoteCIEntrypoint) bool {
	if function.Name.Name != retired.name {
		return false
	}
	if retired.receiver == "" {
		return function.Recv == nil
	}
	if function.Recv == nil || len(function.Recv.List) != 1 {
		return false
	}
	return remoteCIReceiverTypeName(function.Recv.List[0].Type) == retired.receiver
}

func remoteCIReceiverTypeName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return remoteCIReceiverTypeName(typed.X)
	case *ast.IndexExpr:
		return remoteCIReceiverTypeName(typed.X)
	case *ast.IndexListExpr:
		return remoteCIReceiverTypeName(typed.X)
	case *ast.ParenExpr:
		return remoteCIReceiverTypeName(typed.X)
	default:
		return ""
	}
}

// assertRemoteFanoutAndReleaseOwnerBoundary 禁止 ECI 创建链引入全局并发上限，
// 并禁止 release owner 退化成第二 worker 命令或规划器入口。
func assertRemoteFanoutAndReleaseOwnerBoundary(t *testing.T, root string) {
	t.Helper()
	assertRemoteContainerFanout(t, root)
	assertWorkerRejectsReleaseOwner(t, root)
	assertTypedReleaseOwner(t, root)
	assertRemoteFinalizersDoNotRunWorkloads(t, root)
}

// assertRemoteContainerFanout 锁定 coordinator ECI 创建链的无上限 fanout。
func assertRemoteContainerFanout(t *testing.T, root string) {
	t.Helper()
	coordinatorPath := filepath.Join(root, "internal/devtools/remoteci/coordinator.go")
	createGroups := remoteCIFunctionSource(t, coordinatorPath, "uploadAndCreateRemoteGroups")
	if !strings.Contains(createGroups, ".Go(") || !strings.Contains(createGroups, "uploadAndCreateRemoteGroup(") {
		t.Fatal("remote ECI create fanout is not one worker per planned shard")
	}
	assertNoRemoteConcurrencyLimit(t, "uploadAndCreateRemoteGroups", createGroups)
}

// assertWorkerRejectsReleaseOwner 要求 worker 规范集合与 shard 身份都排除 release owner。
func assertWorkerRejectsReleaseOwner(t *testing.T, root string) {
	t.Helper()
	containerPath := filepath.Join(root, "internal/devtools/gate/container_shards.go")
	executorPath := filepath.Join(root, "internal/devtools/gate/executor_plan.go")
	requiredWorkerGates := remoteCIFunctionSource(t, executorPath, "requiredContainerShardGateIDs")
	if !strings.Contains(requiredWorkerGates, "if profile == ProfileRelease") ||
		!strings.Contains(requiredWorkerGates, "ids = ids[:len(ids)-1]") {
		t.Fatal("release owner is not removed from the worker required-gate set")
	}
	validateWorkerGates := remoteCIFunctionSource(t, executorPath, "validateContainerShardGateIDs")
	for _, marker := range []string{"workloadParentGateID", "requiredSet[parent]"} {
		if !strings.Contains(validateWorkerGates, marker) {
			t.Fatalf("worker shard validation does not bind atomic workload %q", marker)
		}
	}
	validateShard := remoteCIFunctionSource(t, containerPath, "validateContainerShard")
	if !strings.Contains(validateShard, "GateIDReleaseLayeredCheck") {
		t.Fatal("ContainerShard validation does not reject release owner gate IDs")
	}
	prerequisites := remoteCIFunctionSource(t, containerPath, "containerShardPrerequisites")
	if !strings.Contains(prerequisites, "release") || !strings.Contains(prerequisites, "GateIDReleaseLayeredCheck") {
		t.Fatal("container shard prerequisite path does not prove release owner exclusion")
	}
}

// assertTypedReleaseOwner 要求 coordinator 只生成 typed release 证明，且唯一 SQLite
// finalizer 只接收当前 RunResult 的 CheckReceiptRecord。
func assertTypedReleaseOwner(t *testing.T, root string) {
	t.Helper()
	appendOwner := remoteCIFunctionSource(t, filepath.Join(root, "internal/devtools/remoteci/coordinator_results.go"), "appendRemoteOwnerAttestation")
	for _, marker := range []string{"remoteCatalogHasReleaseOwner", "gate.ExecuteReleaseLayerAttestation"} {
		if !strings.Contains(appendOwner, marker) {
			t.Fatalf("release owner aggregation is missing typed marker %q", marker)
		}
	}
	for _, forbidden := range []string{
		"runRemoteWorkloadMisses(",
		"PlanLPT(",
		"BuildWorkloadExecutionPlan",
		"exec.Command(",
		"os/exec",
	} {
		if strings.Contains(appendOwner, forbidden) {
			t.Fatalf("release owner aggregation reruns executable work via %q", forbidden)
		}
	}

	finalizerPath := filepath.Join(root, "cmd/super-dolphin-gate/remote_run.go")
	for _, check := range []struct {
		functionName string
		marker       string
	}{
		{functionName: "finalizeRemoteRunReceiptAuthority", marker: "FinalizeRemoteCIRunAuthorityWithSamples("},
		{functionName: "finalizeRemoteRunReceiptAuthorityWithShardOverhead", marker: "FinalizeRemoteCIRunAuthorityWithShardOverhead("},
	} {
		finalizer := remoteCIFunctionSource(t, finalizerPath, check.functionName)
		if !strings.Contains(finalizer, check.marker) {
			t.Fatalf("remote finalizer %s does not commit typed SQLite CheckReceiptRecord authority via %q", check.functionName, check.marker)
		}
	}
}

// assertRemoteFinalizersDoNotRunWorkloads 禁止唯一 SQLite finalizer 重跑任何 workload。
func assertRemoteFinalizersDoNotRunWorkloads(t *testing.T, root string) {
	t.Helper()
	finalizerPath := filepath.Join(root, "cmd/super-dolphin-gate/remote_run.go")
	for _, functionName := range []string{
		"finalizeRemoteRunEvidence",
		"finalizeRemoteRunReceiptAuthority",
		"finalizeRemoteRunReceiptAuthorityWithShardOverhead",
	} {
		body := remoteCIFunctionSource(t, finalizerPath, functionName)
		for _, forbidden := range []string{
			"runRemoteWorkloadMisses(",
			"PlanLPT(",
			"BuildWorkloadExecutionPlan",
			"ExecutePlanExecutor(",
			"exec.Command(",
		} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("remote finalizer %s reruns gate work via %q", functionName, forbidden)
			}
		}
	}
}

// assertNoRemoteConcurrencyLimit 拒绝远程分片创建函数引入仓库级 admission cap。
func assertNoRemoteConcurrencyLimit(t *testing.T, functionName, source string) {
	t.Helper()
	for _, forbidden := range []string{
		"SetLimit(",
		"TryGo(",
		"semaphore",
		"max_shards",
		"max_concurrency",
		"laneCount",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("%s introduces a remote shard admission limit via %q", functionName, forbidden)
		}
	}
}

// assertRemoteParentProofBoundary 要求父门禁只输出固定大小的确定性摘要，
// 禁止按每个原子子 workload 追加无界日志。
func assertRemoteParentProofBoundary(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "internal/devtools/remoteci/coordinator_results.go")
	parent := remoteCIFunctionSource(t, path, "AggregateCatalogWorkloads")
	for _, marker := range []string{"len(parents)", "aggregateWorkloadGate(", "executions = append(executions, aggregate)"} {
		if !strings.Contains(parent, marker) {
			t.Fatalf("parent aggregation is missing bounded marker %q", marker)
		}
	}
	proof := remoteCIFunctionSource(t, path, "aggregateWorkloadGate")
	for _, forbidden := range []string{
		"workload=%s",
		"fmt.Fprintf(",
		"result.Log = append(result.Log",
	} {
		if strings.Contains(proof, forbidden) {
			t.Fatalf("parent proof emits unbounded per-child output via %q", forbidden)
		}
	}
	if !strings.Contains(proof, "sha256.Sum256") {
		t.Fatal("parent proof does not derive a deterministic digest")
	}
}
