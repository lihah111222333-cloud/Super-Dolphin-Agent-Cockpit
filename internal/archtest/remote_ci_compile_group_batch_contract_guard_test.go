package archtest

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCICompileGroupBatchContract 锁定 MISS 内一次编译、按包安全批处理的唯一执行语义。
func TestRemoteCICompileGroupBatchContract(t *testing.T) {
	root := findRepoRoot(t)
	assertRemoteCICompileGroupBatchSchema(t, root)
	assertRemoteCICompileGroupHelperBoundary(t, root)
	assertRemoteCICompileGroupBatchExecution(t, root)
	assertRemoteCICompileGroupBatchWarning(t, root)
}

func assertRemoteCICompileGroupBatchSchema(t *testing.T, root string) {
	t.Helper()
	source := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/compile_group.go"))
	for _, marker := range []string{
		"CompileGroupSchemaVersion uint32 = 2",
		"SelectorEstimates",
		"BatchPlanDigest",
		"BatchPlanWarning",
		"validateArchtestCompileGroupShape",
		"CompileGroupMaxSelectors",
		"validateCompileGroupSafetyBatch",
		"validateCompileGroupSelectorIdentitySafety",
		"IsCanonicalGoTestHelper",
	} {
		if !strings.Contains(source, marker) {
			t.Errorf("compile group schema is missing marker %q", marker)
		}
	}
}

func assertRemoteCICompileGroupHelperBoundary(t *testing.T, root string) {
	t.Helper()
	inventory := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/go_test_inventory.go"))
	helper := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/provider/codexapp/transport_local_test.go"))
	for _, marker := range []string{"remoteGoTestDirectiveAllows", "super-dolphin-ci: helper"} {
		if !strings.Contains(inventory+helper, marker) {
			t.Errorf("remote Go helper inventory boundary is missing marker %q", marker)
		}
	}
}

func assertRemoteCICompileGroupBatchExecution(t *testing.T, root string) {
	t.Helper()
	compileSource := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/executor_compile_group.go"))
	batchSource := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/executor_compile_group_batch.go"))
	environmentSource := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/executor_compile_group_batch_environment.go"))
	contractSource := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/cicontract/contract.go"))
	plannerSource := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_compile_planning.go"))
	reportSource := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/executor_plan_report_log_budget.go"))
	planSource := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/executor_plan.go"))
	workerSource := strings.Join([]string{compileSource, batchSource, environmentSource, plannerSource, reportSource, planSource, contractSource}, "\n")
	assertCompileGroupPlannerBatchPolicy(t, root, plannerSource)
	for _, marker := range []string{
		"compiledGroupArtifact",
		"executeCompileGroupBatchWaves",
		"safego.Go",
		"HOME",
		"GOTMPDIR",
		"XDG_CACHE_HOME",
		"artifact.candidateCacheRoot",
		"artifact.baselineCacheSeedRoot",
		"GoBuildCacheProxyMetricsPathForInvocation",
		"AtomicArchtestPackageTarget",
		"GOMEMLIMIT",
		"3GiB",
		"executorPlanSuccessfulSelectorLogBytes",
		"fullFailureLogAvailable",
		"consumeTopLevelLifecycleEvent",
		"bodyStarted",
		"normalizeCompileGroupReportLogs",
		"validateCompileGroupReportLogBudget",
		"executorPlanCompileGroupLogPolicyVersion",
		"appendExecutionProfileDigest",
		"appendCompileGroupLogBudgetDigest",
		"executorPlanReportMaxOutputBytes",
	} {
		if !strings.Contains(workerSource, marker) {
			t.Errorf("compile group worker is missing marker %q", marker)
		}
	}
	for _, marker := range []string{"HOME", "GOTMPDIR", "XDG_CACHE_HOME", "isAtomicGoPackageTarget", "GOMEMLIMIT", "3GiB"} {
		if !strings.Contains(environmentSource, marker) {
			t.Errorf("compile group batch environment is missing marker %q", marker)
		}
	}
	if !strings.Contains(contractSource, "CompileGroupMaxSelectors = 64") {
		t.Fatal("cicontract compile-group selector budget owner drifted")
	}
	if strings.Contains(batchSource, "executeCompiledSelectorBatch(") || strings.Contains(batchSource, "compileGroupBatchCommandArgv(") {
		t.Fatal("compile group worker retains a retired non-batch compatibility entrypoint")
	}
}

// assertCompileGroupPlannerBatchPolicy 锁定 workload 驱动批次与 archtest 有界
// 单进程语义。资源 vCPU 只属于 ECI execution identity，不能重新成为 batch
// ceiling；更大的 archtest selector 集合必须在 bucket 层拆成独立 group/shard。
func assertCompileGroupPlannerBatchPolicy(t *testing.T, root, plannerSource string) {
	t.Helper()
	plannerPath := filepath.Join(root, "internal/devtools/gate/workload_compile_planning.go")
	assertCompileGroupCapacityPolicy(t, plannerPath)
	assertArchtestCompileGroupPartitionPolicy(t, plannerPath, plannerSource)
	assertArchtestCompileGroupValidationPolicy(t, root)
}

func assertCompileGroupCapacityPolicy(t *testing.T, plannerPath string) {
	t.Helper()
	capacity := remoteCIFunctionSource(t, plannerPath, "compileGroupBatchCapacity")
	for _, marker := range []string{
		"validateCompileGroupResourceClass(resourceClassID)",
		"return selectorCount, nil",
	} {
		if !strings.Contains(capacity, marker) {
			t.Fatalf("compile-group capacity is missing workload-driven marker %q", marker)
		}
	}
	for _, forbidden := range []string{
		"compileGroupResourceVCPUs(",
		"capacity > selectorCount",
	} {
		if strings.Contains(capacity, forbidden) {
			t.Fatalf("compile-group capacity retains resource batch ceiling %q", forbidden)
		}
	}
}

func assertArchtestCompileGroupPartitionPolicy(t *testing.T, plannerPath, plannerSource string) {
	t.Helper()
	partition := remoteCIFunctionSource(t, plannerPath, "splitCompilePlanningPartitions")
	for _, marker := range []string{"CompileGroupMaxSelectors", "isAtomicGoPackageTarget", "partitionBodies := make([]int64, groupCount)", "appendArchtestCompilePlanningSelector", "sortArchtestCompilePlanningPartition"} {
		if !strings.Contains(partition, marker) {
			t.Fatalf("archtest planner is missing bounded-partition marker %q", marker)
		}
	}
	if strings.Contains(plannerSource, "serialCompileGroupBatches") {
		t.Fatal("compile-group planner retains a retired serial archtest batch path")
	}
	archtest := remoteCIFunctionSource(t, plannerPath, "chooseCompileGroupArchtestBatch")
	for _, marker := range []string{"lptCompileGroupBatches(selectors, 1)", "archtest_group_batch_limit=1"} {
		if !strings.Contains(archtest, marker) {
			t.Fatalf("archtest planner is missing per-group single-batch marker %q", marker)
		}
	}
	if strings.Contains(plannerSource, "chooseCompileGroupSingleBatch") || strings.Contains(plannerSource, "at_max_batches=1") {
		t.Fatal("compile-group planner retains retired all-selector archtest markers")
	}
}

func assertArchtestCompileGroupValidationPolicy(t *testing.T, root string) {
	t.Helper()
	archtestValidation := remoteCIFunctionSource(t, filepath.Join(root, "internal/devtools/gate/compile_group.go"), "validateArchtestCompileGroupShape")
	for _, marker := range []string{"CompileGroupMaxSelectors", "len(group.BatchPlan) != 1", "batch.Wave != 0", "batch.Exclusive"} {
		if !strings.Contains(archtestValidation, marker) {
			t.Fatalf("compile-group validator is missing archtest shape marker %q", marker)
		}
	}
	if strings.Contains(readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/compile_group.go")), "validateAtomicArchtestBatchPlan") {
		t.Fatal("compile-group validator retains retired atomic archtest function")
	}
	if !strings.Contains(archtestValidation, "exceeds selector bound") {
		t.Fatal("compile-group validator does not enforce the bounded archtest selector group")
	}
}

func assertRemoteCICompileGroupBatchWarning(t *testing.T, root string) {
	t.Helper()
	planner := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/gate/workload_compile_planning.go"))
	coordinator := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/coordinator.go"))
	warnings := readRemoteCIContractGuardFile(t, filepath.Join(root, "internal/devtools/remoteci/coordinator_compile_group_warnings.go"))
	for _, marker := range []string{
		"CompileGroupBatchTargetMS",
		"compileGroupPlanWarnings(prepared.set.WorkloadPlan.CompileGroups)",
		"compile_group_plan group_id=",
	} {
		if !strings.Contains(planner+coordinator+warnings, marker) {
			t.Errorf("compile group warning projection is missing marker %q", marker)
		}
	}
}
