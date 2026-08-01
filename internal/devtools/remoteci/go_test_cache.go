package remoteci

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	remoteGoTestInvocationFloorMS    int64 = 10_000
	remoteGoTestInvocationOverheadMS int64 = 3_000
)

type remoteWorkloadCacheSelection struct {
	catalog               gate.WorkloadCatalog
	workloads             []gate.Workload
	cacheEntries          []remoteWorkloadCacheEntry
	cached                map[string]gate.PlanGateExecution
	reused                []gate.GateID
	goTestEntriesByParent map[string][]remoteWorkloadCacheEntry
}

type remoteGoTestResumeSet struct {
	workloadsByParent         map[string][]gate.Workload
	entriesByParent           map[string][]remoteWorkloadCacheEntry
	estimatedDurationByParent map[string]int64
	excludedParents           map[string]struct{}
	forcedSplitParents        map[string]struct{}
	wholePackageRequired      map[string]struct{}
	inputDigests              map[string]string
	entries                   []remoteWorkloadCacheEntry
	durationIndex             gate.DurationSampleIndex
}

// splitRemoteGoTestCacheHits 将整包命中与顶层测试命中分开投影。
func splitRemoteGoTestCacheHits(
	workloads []gate.Workload,
	cached map[string]gate.PlanGateExecution,
) (map[string]gate.PlanGateExecution, map[string]gate.PlanGateExecution) {
	original := make(map[string]struct{}, len(workloads))
	for _, workload := range workloads {
		original[workload.ID] = struct{}{}
	}
	packageCached := make(map[string]gate.PlanGateExecution)
	testCached := make(map[string]gate.PlanGateExecution)
	for workloadID, execution := range cached {
		if _, ok := original[workloadID]; ok {
			packageCached[workloadID] = execution
			continue
		}
		testCached[workloadID] = execution
	}
	return packageCached, testCached
}

// enforceRemoteCalibrationEvidence 保证校准不会撤销已验证 PASS。
// 缺失时长仅影响估时完整度，不能把未变化目标重新送入 ECI。
func enforceRemoteCalibrationEvidence(
	workloads []gate.Workload,
	cached map[string]gate.PlanGateExecution,
	resume *remoteGoTestResumeSet,
	input RunInput,
) error {
	if !input.Calibration {
		return nil
	}
	_ = workloads
	_ = resume
	for workloadID, execution := range cached {
		if execution.GateID != gate.GateID(workloadID) ||
			execution.Status != gate.ResultStatusPassed ||
			execution.ExitCode != 0 {
			return fmt.Errorf(
				"calibration cache entry %q is not a verified PASS",
				workloadID,
			)
		}
	}
	return nil
}

// buildRemoteGoTestResumeSet 从精确测试清单构造可恢复的顶层测试 workload。
func buildRemoteGoTestResumeSet(
	workloads []gate.Workload,
	inventories map[string][]string,
	inputDigests map[string]string,
	input RunInput,
	prefix string,
) (remoteGoTestResumeSet, error) {
	durationIndex, err := gate.DurationSampleIndexFromSnapshot(
		input.LedgerSnapshot,
		remotePlanningContext(input),
	)
	if err != nil {
		return remoteGoTestResumeSet{}, fmt.Errorf("build remote Go test duration index: %w", err)
	}
	set := remoteGoTestResumeSet{
		workloadsByParent:         make(map[string][]gate.Workload),
		entriesByParent:           make(map[string][]remoteWorkloadCacheEntry),
		estimatedDurationByParent: make(map[string]int64),
		excludedParents:           make(map[string]struct{}),
		forcedSplitParents:        make(map[string]struct{}),
		wholePackageRequired:      make(map[string]struct{}),
		inputDigests:              make(map[string]string),
		durationIndex:             durationIndex,
	}
	for _, parentWorkload := range workloads {
		if err := appendRemoteGoTestResumeWorkload(&set, parentWorkload, inventories, inputDigests); err != nil {
			return remoteGoTestResumeSet{}, err
		}
	}
	testWorkloads := flattenRemoteGoTestResumeWorkloads(workloads, set.workloadsByParent)
	entries, err := remoteWorkloadCacheEntries(prefix, testWorkloads, set.inputDigests, input)
	if err != nil {
		return remoteGoTestResumeSet{}, err
	}
	set.entries = entries
	if err := indexRemoteGoTestResumeEntries(&set, entries); err != nil {
		return remoteGoTestResumeSet{}, err
	}
	return set, nil
}

// appendRemoteGoTestResumeWorkload 决定单个父 workload 是否应进入测试级恢复集合。
func appendRemoteGoTestResumeWorkload(set *remoteGoTestResumeSet, workload gate.Workload, inventories map[string][]string, inputDigests map[string]string) error {
	_, kind, _, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil {
		return err
	}
	if targeted && kind == gate.WorkloadTargetGoPackage {
		if _, available := inventories[workload.ID]; !available {
			set.excludedParents[workload.ID] = struct{}{}
			return nil
		}
		if remoteGoTestHasOverTargetFailure(workload, set.durationIndex) {
			set.forcedSplitParents[workload.ID] = struct{}{}
		}
	}
	return appendRemoteGoTestResumeParent(set, workload, inventories, inputDigests)
}

// remoteGoTestHasOverTargetFailure 用同环境失败耗时触发测试级拆分，但不污染成功样本估算。
func remoteGoTestHasOverTargetFailure(
	workload gate.Workload,
	index gate.DurationSampleIndex,
) bool {
	return index.HasFailureExceedingDuration(workload, gate.FullCITargetDurationMS)
}

// appendRemoteGoTestResumeParent 展开一个整包 workload 的全部顶层测试。
func appendRemoteGoTestResumeParent(
	set *remoteGoTestResumeSet,
	parentWorkload gate.Workload,
	inventories map[string][]string,
	inputDigests map[string]string,
) error {
	names, ok := inventories[parentWorkload.ID]
	if !ok || len(names) == 0 {
		return nil
	}
	parent, packageTarget, err := remoteGoTestResumeParentTarget(parentWorkload)
	if err != nil {
		return err
	}
	if _, ok := inputDigests[parentWorkload.ID]; !ok {
		return fmt.Errorf("Go test inventory parent %q has no input digest", parentWorkload.ID)
	}
	parentEstimate, err := set.durationIndex.EstimateWorkloadDurationMS(parentWorkload)
	if err != nil {
		return fmt.Errorf("estimate Go test inventory parent %q: %w", parentWorkload.ID, err)
	}
	set.estimatedDurationByParent[parentWorkload.ID] = parentEstimate
	for _, name := range names {
		if err := appendRemoteGoTestResumeTarget(set, parentWorkload, parent, packageTarget, name, len(names), parentEstimate, inputDigests); err != nil {
			return err
		}
	}
	return nil
}

func remoteGoTestResumeParentTarget(workload gate.Workload) (gate.GateID, string, error) {
	parent, kind, packageTarget, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil {
		return "", "", err
	}
	if !targeted || kind != gate.WorkloadTargetGoPackage {
		return "", "", fmt.Errorf("Go test inventory parent %q is not a package workload", workload.ID)
	}
	return parent, packageTarget, nil
}

func appendRemoteGoTestResumeTarget(set *remoteGoTestResumeSet, parentWorkload gate.Workload, parent gate.GateID, packageTarget, name string, count int, parentEstimate int64, inputDigests map[string]string) error {
	estimate := remoteGoTestBootstrapEstimateMS(parentWorkload, name, count, parentEstimate, set.durationIndex)
	workload, err := gate.NewGoTestWorkload(parent, packageTarget, name, estimate)
	if err != nil {
		return err
	}
	testInputDigest, ok := inputDigests[workload.ID]
	if !ok || testInputDigest == "" {
		return fmt.Errorf("Go test inventory target %q has no independent input digest", workload.ID)
	}
	set.workloadsByParent[parentWorkload.ID] = append(set.workloadsByParent[parentWorkload.ID], workload)
	set.inputDigests[workload.ID] = testInputDigest
	return nil
}

// flattenRemoteGoTestResumeWorkloads 保持父 workload 顺序展开顶层测试。
func flattenRemoteGoTestResumeWorkloads(
	parents []gate.Workload,
	byParent map[string][]gate.Workload,
) []gate.Workload {
	var workloads []gate.Workload
	for _, parent := range parents {
		workloads = append(workloads, byParent[parent.ID]...)
	}
	return workloads
}

// indexRemoteGoTestResumeEntries 按整包父目标索引测试级缓存身份。
func indexRemoteGoTestResumeEntries(
	set *remoteGoTestResumeSet,
	entries []remoteWorkloadCacheEntry,
) error {
	for _, entry := range entries {
		parent, _, err := remoteGoTestCacheParent(entry.workloadID)
		if err != nil {
			return err
		}
		set.entriesByParent[parent] = append(set.entriesByParent[parent], entry)
	}
	return nil
}

// remoteGoTestBootstrapEstimateMS 优先复用同 runner 的测试级成功耗时。
func remoteGoTestBootstrapEstimateMS(
	parent gate.Workload,
	name string,
	count int,
	parentEstimate int64,
	index gate.DurationSampleIndex,
) int64 {
	if durationMS, ok := index.GoTestDurationMS(parent, name); ok {
		return remoteGoTestInvocationEstimateMS(durationMS)
	}
	return max(remoteGoTestInvocationFloorMS, parentEstimate/int64(count))
}

// remoteGoTestInvocationEstimateMS 为测试本体耗时预留独立 go test 进程开销。
func remoteGoTestInvocationEstimateMS(testDurationMS int64) int64 {
	const maximumInt64 = int64(^uint64(0) >> 1)
	if testDurationMS > maximumInt64-remoteGoTestInvocationOverheadMS {
		return maximumInt64
	}
	return max(remoteGoTestInvocationFloorMS, testDurationMS+remoteGoTestInvocationOverheadMS)
}

// remoteGoTestCacheParent 还原一个顶层测试缓存项的整包父 workload。
func remoteGoTestCacheParent(workloadID string) (string, string, error) {
	parent, kind, target, targeted, err := gate.ParseWorkloadID(workloadID)
	if err != nil {
		return "", "", err
	}
	if !targeted || kind != gate.WorkloadTargetGoTest {
		return "", "", fmt.Errorf("workload %q is not a Go test cache target", workloadID)
	}
	testTarget, err := gate.ParseGoTestTarget(target)
	if err != nil {
		return "", "", err
	}
	parentID, err := remotePackageWorkloadID(parent, testTarget.Package)
	if err != nil {
		return "", "", err
	}
	return parentID, testTarget.Name, nil
}

// remoteRequiredGoTestCacheEntries 仅返回父包尚未通过的测试级缓存身份。
// 整包 PASS 已经覆盖包内全部测试，不能再读取或迁移其逐测试标记。
func remoteRequiredGoTestCacheEntries(
	entries []remoteWorkloadCacheEntry,
	cached map[string]gate.PlanGateExecution,
) ([]remoteWorkloadCacheEntry, error) {
	misses := make([]remoteWorkloadCacheEntry, 0, len(entries))
	for _, entry := range entries {
		if _, ok := cached[entry.workloadID]; ok {
			continue
		}
		parentID, _, err := remoteGoTestCacheParent(entry.workloadID)
		if err != nil {
			return nil, err
		}
		if _, ok := cached[parentID]; ok {
			continue
		}
		misses = append(misses, entry)
	}
	return misses, nil
}

func remotePackageWorkloadID(parent gate.GateID, packageTarget string) (string, error) {
	workload, err := gate.NewGoPackageWorkload(parent, packageTarget, 1)
	if err != nil {
		return "", err
	}
	return workload.ID, nil
}

// projectRemoteGoTestCacheSelection 将测试级命中投影为只执行 miss 的有效目录。
func projectRemoteGoTestCacheSelection(
	catalog gate.WorkloadCatalog,
	packageCached map[string]gate.PlanGateExecution,
	testCached map[string]gate.PlanGateExecution,
	resume remoteGoTestResumeSet,
	inputDigests map[string]string,
	input RunInput,
	prefix string,
) (remoteWorkloadCacheSelection, error) {
	effective := gate.WorkloadCatalog{
		Version: catalog.Version, Authoritative: catalog.Authoritative,
		Workloads: make([]gate.Workload, 0, len(catalog.Workloads)),
	}
	cached := make(map[string]gate.PlanGateExecution, len(packageCached)+len(testCached))
	effectiveInputs := make(map[string]string)
	for _, workload := range catalog.Workloads {
		appendProjectedRemoteGoTestWorkload(
			&effective,
			cached,
			effectiveInputs,
			workload,
			packageCached,
			testCached,
			resume,
			inputDigests,
		)
	}
	if err := gate.ValidateWorkloadCatalog(effective); err != nil {
		return remoteWorkloadCacheSelection{}, err
	}
	workloads := remoteShardableWorkloads(effective)
	entries, err := remoteWorkloadCacheEntries(prefix, workloads, effectiveInputs, input)
	if err != nil {
		return remoteWorkloadCacheSelection{}, err
	}
	reused, err := remoteCachedWorkloadIDs(workloads, cached)
	if err != nil {
		return remoteWorkloadCacheSelection{}, err
	}
	return remoteWorkloadCacheSelection{
		catalog: effective, workloads: workloads, cacheEntries: entries,
		cached: cached, reused: reused, goTestEntriesByParent: resume.entriesByParent,
	}, nil
}

// appendProjectedRemoteGoTestWorkload 选择整包执行或测试级恢复投影。
func appendProjectedRemoteGoTestWorkload(
	effective *gate.WorkloadCatalog,
	cached map[string]gate.PlanGateExecution,
	effectiveInputs map[string]string,
	workload gate.Workload,
	packageCached map[string]gate.PlanGateExecution,
	testCached map[string]gate.PlanGateExecution,
	resume remoteGoTestResumeSet,
	inputDigests map[string]string,
) {
	if _, excluded := resume.excludedParents[workload.ID]; excluded {
		return
	}
	testWorkloads := resume.workloadsByParent[workload.ID]
	_, packageHit := packageCached[workload.ID]
	testCacheHits := remoteGoTestCacheHitCount(testWorkloads, testCached)
	overBudget := resume.estimatedDurationByParent[workload.ID] > gate.FullCITargetDurationMS
	_, forcedSplit := resume.forcedSplitParents[workload.ID]
	_, wholePackageRequired := resume.wholePackageRequired[workload.ID]
	if shouldProjectRemoteGoTests(
		workload.Shardable, packageHit, wholePackageRequired, len(testWorkloads) > 0,
		testCacheHits > 0, overBudget, forcedSplit,
	) {
		effective.Workloads = append(effective.Workloads, testWorkloads...)
		appendProjectedRemoteGoTests(cached, effectiveInputs, testWorkloads, testCached, resume.inputDigests)
		return
	}
	effective.Workloads = append(effective.Workloads, workload)
	if !workload.Shardable {
		return
	}
	effectiveInputs[workload.ID] = inputDigests[workload.ID]
	if execution, ok := packageCached[workload.ID]; ok {
		cached[workload.ID] = execution
	}
}

// shouldProjectRemoteGoTests 隔离测试级恢复条件，保持目录投影逻辑可审计。
func shouldProjectRemoteGoTests(
	shardable bool,
	packageHit bool,
	wholePackageRequired bool,
	hasTests bool,
	hasTestCacheHit bool,
	overBudget bool,
	forcedSplit bool,
) bool {
	if !shardable || packageHit || wholePackageRequired || !hasTests {
		return false
	}
	return hasTestCacheHit || overBudget || forcedSplit
}

// remoteGoTestCacheHitCount 统计一个整包目标下可复用的顶层测试数。
func remoteGoTestCacheHitCount(
	workloads []gate.Workload,
	cached map[string]gate.PlanGateExecution,
) int {
	count := 0
	for _, workload := range workloads {
		if _, ok := cached[workload.ID]; ok {
			count++
		}
	}
	return count
}

// appendProjectedRemoteGoTests 复制测试级目录、输入摘要和已有执行结果。
func appendProjectedRemoteGoTests(
	cached map[string]gate.PlanGateExecution,
	effectiveInputs map[string]string,
	workloads []gate.Workload,
	testCached map[string]gate.PlanGateExecution,
	inputDigests map[string]string,
) {
	for _, workload := range workloads {
		effectiveInputs[workload.ID] = inputDigests[workload.ID]
		if execution, ok := testCached[workload.ID]; ok {
			cached[workload.ID] = execution
		}
	}
}

// storePassedGoTestCache 将失败包里已经终态通过的顶层测试发布为独立不可变标记。
func (coordinator *Coordinator) storePassedGoTestCache(
	ctx context.Context,
	tempRoot string,
	entriesByParent map[string][]remoteWorkloadCacheEntry,
	executions map[string]gate.PlanGateExecution,
	ledgerStore *gate.DurationLedgerStore,
) error {
	var cacheErr error
	for _, parentID := range sortedRemoteGoTestCacheParents(entriesByParent) {
		entries := entriesByParent[parentID]
		execution, ok := executions[parentID]
		if !ok || !failedGoPackageHasReusablePass(execution) {
			continue
		}
		passed, err := passedGoTestCacheExecutions(entries, execution.TestTimings)
		if err != nil {
			cacheErr = errors.Join(cacheErr, err)
			continue
		}
		if len(passed) == len(entries) {
			// A failed process after every top-level event can still fail in TestMain or cleanup.
			continue
		}
		cacheErr = errors.Join(
			cacheErr,
			coordinator.storePassedWorkloadCache(
				ctx,
				tempRoot,
				entries,
				passed,
				ledgerStore,
			),
		)
	}
	return cacheErr
}

// failedGoPackageHasReusablePass 同时覆盖断言失败、超时和测试进程中断后的部分成功。
func failedGoPackageHasReusablePass(execution gate.PlanGateExecution) bool {
	if execution.Status != gate.ResultStatusFailed {
		return false
	}
	for _, timing := range execution.TestTimings {
		if !strings.Contains(timing.Name, "/") && timing.Status == gate.GoTestStatusPass {
			return true
		}
	}
	return false
}

// passedGoTestCacheExecutions 只把精确清单中明确通过的顶层测试转换为 PASS 候选。
func passedGoTestCacheExecutions(
	entries []remoteWorkloadCacheEntry,
	timings []gate.GoTestTiming,
) (map[string]gate.PlanGateExecution, error) {
	entriesByName := make(map[string]remoteWorkloadCacheEntry, len(entries))
	for _, entry := range entries {
		_, name, err := remoteGoTestCacheParent(entry.workloadID)
		if err != nil {
			return nil, err
		}
		entriesByName[name] = entry
	}
	passed := make(map[string]gate.PlanGateExecution)
	for _, timing := range timings {
		if strings.Contains(timing.Name, "/") || timing.Status != gate.GoTestStatusPass {
			continue
		}
		entry, ok := entriesByName[timing.Name]
		if !ok {
			return nil, fmt.Errorf("reported top-level Go test %q is absent from the exact Git tree inventory", timing.Name)
		}
		passed[entry.workloadID] = gate.PlanGateExecution{
			GateID: gate.GateID(entry.workloadID), Status: gate.ResultStatusPassed, ExitCode: 0,
			TestTimings: []gate.GoTestTiming{timing},
		}
	}
	return passed, nil
}

func sortedRemoteGoTestCacheParents(entries map[string][]remoteWorkloadCacheEntry) []string {
	parents := make([]string, 0, len(entries))
	for parent := range entries {
		parents = append(parents, parent)
	}
	sort.Strings(parents)
	return parents
}
