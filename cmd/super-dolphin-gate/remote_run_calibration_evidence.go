package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// acceptAndEmitRemoteCalibration 验证三次运行并将有完整证据的校准以 CAS 接受后输出。
func acceptAndEmitRemoteCalibration(
	stdout io.Writer,
	ledgerStore *gatecontract.DurationLedgerStore,
	commit string,
	inputs [3]remoteci.RunInput,
	results [3]remoteci.RunResult,
) error {
	if err := validateRemoteCalibrationRuns(inputs, results); err != nil {
		return err
	}
	catalogs, digests, err := remoteCalibrationCatalogs(inputs, results)
	if err != nil {
		return err
	}
	calibration := remoteDurationCalibration(commit, inputs, results[2], digests)
	passedWorkloads, err := remoteCalibrationPassedWorkloadSet(inputs, catalogs, results)
	if err != nil {
		return infrastructureError("verify remote calibration PASS coverage: %w", err)
	}
	snapshot, err := acceptRemoteDurationCalibrationWithPasses(
		ledgerStore,
		calibration,
		passedWorkloads,
		catalogs[:]...,
	)
	if err != nil {
		return infrastructureError("accept remote duration calibration: %w", err)
	}
	return emitRemoteCalibrationResult(stdout, snapshot, results)
}

// remoteCalibrationPassedWorkloadSet 把本轮整包 PASS 或完整顶层测试 PASS 投影回校准 catalog。
func remoteCalibrationPassedWorkloadSet(
	inputs [3]remoteci.RunInput,
	catalogs [3]gatecontract.WorkloadCatalog,
	results [3]remoteci.RunResult,
) (map[string]struct{}, error) {
	passed := make(map[string]struct{})
	for scenarioIndex, catalog := range catalogs {
		scenarioPassed, err := remoteCalibrationPassedCatalogWorkloadSet(
			inputs[scenarioIndex],
			catalog,
			results[scenarioIndex],
		)
		if err != nil {
			return nil, fmt.Errorf("calibration scenario %d: %w", scenarioIndex, err)
		}
		for key := range scenarioPassed {
			passed[key] = struct{}{}
		}
	}
	return passed, nil
}

// remoteCalibrationPassedCatalogWorkloadSet 汇集当前 catalog 中已由运行结果覆盖的 workload。
func remoteCalibrationPassedCatalogWorkloadSet(
	input remoteci.RunInput,
	catalog gatecontract.WorkloadCatalog,
	result remoteci.RunResult,
) (map[string]struct{}, error) {
	if result.Status != gatecontract.ResultStatusPassed {
		return map[string]struct{}{}, nil
	}
	executions := make(map[string]gatecontract.PlanGateExecution, len(result.GateExecutions))
	for _, execution := range result.GateExecutions {
		workloadID := string(execution.GateID)
		if _, duplicate := executions[workloadID]; duplicate {
			return nil, fmt.Errorf("workload %q is repeated", workloadID)
		}
		executions[workloadID] = execution
	}
	identities, err := canonicalCalibrationWorkloadIdentities(result)
	if err != nil {
		return nil, err
	}
	passed := make(map[string]struct{})
	for _, workload := range catalog.Workloads {
		if calibrationIdentityPassed(identities, workload) {
			passed[remoteCalibrationWorkloadKey(workload)] = struct{}{}
			continue
		}
		if calibrationExecutionPassed(executions[workload.ID], workload.ID) {
			passed[remoteCalibrationWorkloadKey(workload)] = struct{}{}
			continue
		}
		covered, err := calibrationPackageCoveredByTests(
			workload,
			input.Inventory,
			executions,
		)
		if err != nil {
			return nil, err
		}
		if covered {
			passed[remoteCalibrationWorkloadKey(workload)] = struct{}{}
		}
	}
	return passed, nil
}

// canonicalCalibrationWorkloadIdentities 复用唯一 result projector，确保
// direct/source/environment reuse 都以当前 consumer 身份计入校准正确性覆盖。
func canonicalCalibrationWorkloadIdentities(result remoteci.RunResult) ([]gatecontract.WorkloadPassIdentity, error) {
	canonicalResults, err := remoteci.CanonicalRemoteCIWorkloadResults(result)
	if err != nil {
		return nil, fmt.Errorf("project canonical calibration workload results: %w", err)
	}
	byWorkload := make(map[gatecontract.GateID]gatecontract.WorkloadPassIdentity, len(canonicalResults)+len(result.WorkloadPassIdentities))
	for _, identity := range result.WorkloadPassIdentities {
		byWorkload[identity.WorkloadID] = identity
	}
	for _, workloadResult := range canonicalResults {
		if existing, ok := byWorkload[workloadResult.Identity.WorkloadID]; ok && existing != workloadResult.Identity {
			return nil, fmt.Errorf("canonical calibration workload identity %q drifted", workloadResult.Identity.WorkloadID)
		}
		byWorkload[workloadResult.Identity.WorkloadID] = workloadResult.Identity
	}
	identities := make([]gatecontract.WorkloadPassIdentity, 0, len(byWorkload))
	for _, identity := range byWorkload {
		identities = append(identities, identity)
	}
	return identities, nil
}

// calibrationIdentityPassed 只把已由权威运行持久化的完整 workload PASS 身份投影为正确性覆盖。
func calibrationIdentityPassed(identities []gatecontract.WorkloadPassIdentity, workload gatecontract.Workload) bool {
	if !workload.Shardable || strings.TrimSpace(workload.InputDigest) == "" {
		return false
	}
	executionDigest := gatecontract.WorkloadPassExecutionDigest(workload)
	matched := false
	for _, identity := range identities {
		if identity.WorkloadID != gatecontract.GateID(workload.ID) {
			continue
		}
		// Identity.Validate 同时要求 InputDigest、EnvironmentDigest 和 content digest
		// 均为完整规范摘要；输入摘要还必须逐字匹配当前持久化 catalog。
		if matched || identity.Validate() != nil || identity.ExecutionDigest != executionDigest || identity.InputDigest != workload.InputDigest {
			return false
		}
		matched = true
	}
	return matched
}

// calibrationPackageCoveredByTests 判断包级 workload 是否被其所有精确测试成功覆盖。
func calibrationPackageCoveredByTests(
	workload gatecontract.Workload,
	inventory gatecontract.WorkloadInventory,
	executions map[string]gatecontract.PlanGateExecution,
) (bool, error) {
	parent, kind, packageTarget, targeted, err := gatecontract.ParseWorkloadID(workload.ID)
	if err != nil {
		return false, err
	}
	if !targeted || kind != gatecontract.WorkloadTargetGoPackage {
		return false, nil
	}
	tests := inventory.GoTests
	if parent == gatecontract.GateIDBackendTestGuardWithRace {
		tests = inventory.GoRaceTests
	}
	matched := 0
	for _, target := range tests {
		if target.Package != packageTarget {
			continue
		}
		matched++
		testWorkload, err := gatecontract.NewGoTestWorkload(
			parent,
			packageTarget,
			target.Name,
			1,
		)
		if err != nil {
			return false, err
		}
		if !calibrationExecutionPassed(executions[testWorkload.ID], testWorkload.ID) {
			return false, nil
		}
	}
	return matched > 0, nil
}

func calibrationExecutionPassed(
	execution gatecontract.PlanGateExecution,
	workloadID string,
) bool {
	return execution.GateID == gatecontract.GateID(workloadID) &&
		execution.Status == gatecontract.ResultStatusPassed &&
		execution.ExitCode == 0
}

func remoteCalibrationWorkloadKey(workload gatecontract.Workload) string {
	return workload.ID + "\x00" + workload.CommandDigest + "\x00" + workload.InputDigest
}

// validateRemoteCalibrationRuns 拒绝三种 source、运行时或权威性在首代校准中发生漂移。
func validateRemoteCalibrationRuns(inputs [3]remoteci.RunInput, results [3]remoteci.RunResult) error {
	commitInput, pushInput, releaseInput := inputs[0], inputs[1], inputs[2]
	if !remoteCalibrationInputSetMatches(commitInput, pushInput, releaseInput) {
		return infrastructureError("remote calibration commit, push, and release identities drifted")
	}
	if !remoteCalibrationForceMatches(inputs, results) || !remoteCalibrationResultsMatchInputs(inputs, results) {
		return infrastructureError("remote calibration commit, push, and release identities drifted")
	}
	if !remoteCalibrationResultsAuthoritativePassed(results) {
		return infrastructureError("remote calibration commit, push, and release identities drifted")
	}
	return nil
}

// remoteCalibrationInputSetMatches 核对三种入口是否共享同一 source 与运行时身份。
func remoteCalibrationInputSetMatches(commitInput remoteci.RunInput, pushInput remoteci.RunInput, releaseInput remoteci.RunInput) bool {
	return remoteCalibrationSourcesMatch(commitInput, pushInput, releaseInput) &&
		remoteCalibrationRuntimeMatches(commitInput, pushInput, releaseInput)
}

// remoteCalibrationResultsAuthoritativePassed 要求三次结果均为权威 PASS。
func remoteCalibrationResultsAuthoritativePassed(results [3]remoteci.RunResult) bool {
	for _, result := range results {
		if !result.Authoritative || result.Status != gatecontract.ResultStatusPassed {
			return false
		}
	}
	return true
}

// remoteCalibrationResultsMatchInputs 要求每次权威结果精确回显其运行输入身份。
func remoteCalibrationResultsMatchInputs(inputs [3]remoteci.RunInput, results [3]remoteci.RunResult) bool {
	for index, input := range inputs {
		if !remoteCalibrationResultMatchesInput(input, results[index]) {
			return false
		}
	}
	return true
}

// remoteCalibrationResultMatchesInput 核对单次结果的 source 和运行时回显。
func remoteCalibrationResultMatchesInput(input remoteci.RunInput, result remoteci.RunResult) bool {
	return remoteCalibrationResultSourceMatchesInput(input, result) &&
		remoteCalibrationResultRuntimeMatchesInput(input, result)
}

// remoteCalibrationResultSourceMatchesInput 核对入口、profile 与 exact source 身份。
func remoteCalibrationResultSourceMatchesInput(input remoteci.RunInput, result remoteci.RunResult) bool {
	return result.AgentTokenDigest == input.AgentTokenDigest &&
		result.Entrypoint == input.Entrypoint &&
		result.Profile == input.Profile &&
		result.SourceTreeSHA == input.Tree &&
		result.SourceTreeSHA == input.Source.SourceTreeSHA
}

// remoteCalibrationResultRuntimeMatchesInput 核对 accepted generation、snapshot 与 runner 身份。
func remoteCalibrationResultRuntimeMatchesInput(input remoteci.RunInput, result remoteci.RunResult) bool {
	return result.AcceptedGeneration == input.AcceptedGeneration &&
		result.ImageCacheSnapshotID == input.ImageCacheSnapshotID &&
		result.CandidateGateSourceSHA256 == input.CandidateGateSourceSHA256 &&
		result.CandidateGateToolchainSHA256 == input.CandidateGateToolchainSHA256 &&
		result.RunnerImage == input.RunnerImage
}

// remoteCalibrationForceMatches 要求三次校准运行和各自输入保持同一显式 force 语义。
func remoteCalibrationForceMatches(inputs [3]remoteci.RunInput, results [3]remoteci.RunResult) bool {
	for index := range inputs {
		if results[index].Force != inputs[index].Force {
			return false
		}
		if index > 0 && (inputs[index].Force != inputs[0].Force || results[index].Force != results[0].Force) {
			return false
		}
	}
	return true
}

// remoteCalibrationSourcesMatch 核对首代三次运行分别使用树、范围和提交 source。
func remoteCalibrationSourcesMatch(commitInput remoteci.RunInput, pushInput remoteci.RunInput, releaseInput remoteci.RunInput) bool {
	return commitInput.Source.Kind == gatecontract.SourceKindTree &&
		pushInput.Source.Kind == gatecontract.SourceKindRange &&
		releaseInput.Source.Kind == gatecontract.SourceKindCommit &&
		commitInput.Tree == pushInput.Tree && commitInput.Tree == releaseInput.Tree
}

// remoteCalibrationRuntimeMatches 核对三次运行共用 platform、runner manifest 与 toolchain。
func remoteCalibrationRuntimeMatches(commitInput remoteci.RunInput, pushInput remoteci.RunInput, releaseInput remoteci.RunInput) bool {
	return commitInput.Platform == pushInput.Platform && commitInput.Platform == releaseInput.Platform &&
		commitInput.RunnerIdentityDigest == pushInput.RunnerIdentityDigest &&
		commitInput.RunnerIdentityDigest == releaseInput.RunnerIdentityDigest &&
		commitInput.ToolchainDigest == pushInput.ToolchainDigest &&
		commitInput.ToolchainDigest == releaseInput.ToolchainDigest &&
		commitInput.ImageCacheSnapshotID != "" && commitInput.ImageCacheSnapshotID == pushInput.ImageCacheSnapshotID &&
		commitInput.ImageCacheSnapshotID == releaseInput.ImageCacheSnapshotID &&
		remoteCalibrationResourcesMatch(commitInput, pushInput, releaseInput)
}

// remoteCalibrationResourcesMatch 严格核对三次校准运行的完整资源身份。
func remoteCalibrationResourcesMatch(commitInput remoteci.RunInput, pushInput remoteci.RunInput, releaseInput remoteci.RunInput) bool {
	return commitInput.CalibrationResource == pushInput.CalibrationResource &&
		commitInput.CalibrationResource == releaseInput.CalibrationResource
}

// remoteCalibrationCatalogs 从 SQLite 回读三次运行实际使用且已绑定精确输入摘要的 catalog。
func remoteCalibrationCatalogs(inputs [3]remoteci.RunInput, results [3]remoteci.RunResult) ([3]gatecontract.WorkloadCatalog, [3]string, error) {
	var catalogs [3]gatecontract.WorkloadCatalog
	var digests [3]string
	for index, input := range inputs {
		result := results[index]
		plan, catalog, err := remoteRunContractPlanAndCatalog(input, result)
		if err != nil {
			return catalogs, digests, infrastructureError("load exact remote calibration catalog: %v", err)
		}
		if err := validateRemoteRunResultPlanBinding(plan, catalog, result); err != nil {
			return catalogs, digests, infrastructureError("validate exact remote calibration result binding: %w", err)
		}
		if err := validateRemoteCalibrationCatalogInputDigests(catalog); err != nil {
			return catalogs, digests, err
		}
		catalogs[index], digests[index] = catalog, results[index].CatalogDigest
	}
	return catalogs, digests, nil
}

// validateRemoteCalibrationCatalogInputDigests 禁止用空摘要 wildcard 接受其它源码或测试输入的耗时样本。
func validateRemoteCalibrationCatalogInputDigests(catalog gatecontract.WorkloadCatalog) error {
	for _, workload := range catalog.Workloads {
		if workload.Shardable && strings.TrimSpace(workload.InputDigest) == "" {
			return fmt.Errorf("%w: workload %q has no exact production input digest", errRemoteCalibrationSamplesIncomplete, workload.ID)
		}
	}
	return nil
}

// remoteDurationCalibration 汇集已验证的三份 catalog digest 和共享运行身份。
func remoteDurationCalibration(
	commit string,
	inputs [3]remoteci.RunInput,
	releaseResult remoteci.RunResult,
	digests [3]string,
) gatecontract.DurationCalibration {
	return remoteDurationCalibrationFromInputs(
		commit,
		inputs,
		digests,
		releaseResult.CompletedAt.UTC(),
	)
}

func remoteDurationCalibrationFromInputs(
	commit string,
	inputs [3]remoteci.RunInput,
	digests [3]string,
	completedAt time.Time,
) gatecontract.DurationCalibration {
	return gatecontract.DurationCalibration{
		SchemaVersion: gatecontract.DurationCalibrationSchemaVersion,
		Commit:        commit, Tree: inputs[0].Tree, Platform: inputs[0].Platform,
		Runner: inputs[0].RunnerIdentityDigest, Toolchain: inputs[0].ToolchainDigest,
		CalibrationResourceClassID: inputs[0].CalibrationResource.ID,
		CalibrationResourceCPU:     float64(inputs[0].CalibrationResource.VCPU), CalibrationResourceMemoryGiB: float64(inputs[0].CalibrationResource.MemoryGiB),
		CommitEntrypoint: inputs[0].Entrypoint, PushEntrypoint: inputs[1].Entrypoint,
		ReleaseEntrypoint: inputs[2].Entrypoint, CommitCatalogDigest: digests[0],
		PushCatalogDigest: digests[1], ReleaseCatalogDigest: digests[2],
		AcceptedSnapshotID: inputs[0].ImageCacheSnapshotID,
		CompletedAt:        completedAt.UTC(),
	}
}

// emitRemoteCalibrationResult 输出 CAS 已接受的校准状态和三次权威工作负载标识。
func emitRemoteCalibrationResult(
	stdout io.Writer,
	snapshot gatecontract.DurationLedgerSnapshot,
	results [3]remoteci.RunResult,
) error {
	accepted := snapshot.Ledger.Calibration
	result := remoteCalibrationResult{
		SchemaVersion: remoteCalibrationResultSchemaVersion, Force: results[0].Force, Commit: accepted.Commit, Tree: accepted.Tree,
		RunnerManifestDigest: accepted.Runner, CommitJobID: results[0].JobID, PushJobID: results[1].JobID,
		ReleaseJobID: results[2].JobID, LedgerGeneration: snapshot.Generation, WorkloadCount: accepted.WorkloadCount,
		RacePackageCount: accepted.RacePackageCount, CompletedAt: accepted.CompletedAt,
	}
	result.CalibrationResourceClassID = accepted.CalibrationResourceClassID
	result.CalibrationResourceCPU = accepted.CalibrationResourceCPU
	result.CalibrationResourceMemoryGiB = accepted.CalibrationResourceMemoryGiB
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return infrastructureError("encode remote calibration result: %v", err)
	}
	return nil
}

// remoteCalibrationRunOptions 生成首代 commit、push、release 的固定入口与 source 身份。
func remoteCalibrationRunOptions(options remoteRunOptions, commit string, tree string, base string) (remoteRunOptions, remoteRunOptions, remoteRunOptions) {
	commitOptions := options
	commitOptions.Commit, commitOptions.Tree, commitOptions.ParentCommit = "", tree, base
	commitOptions.Scenario = "commit"
	commitOptions.Entrypoint = string(gatecontract.CIEntrypointGitPreCommit)
	commitOptions.Calibration = true

	pushOptions := options
	pushOptions.Commit, pushOptions.Tree, pushOptions.ParentCommit, pushOptions.Base = commit, "", "", base
	pushOptions.Scenario = "push"
	pushOptions.Entrypoint = string(gatecontract.CIEntrypointGitPrePush)
	pushOptions.LocalRef, pushOptions.RemoteRef, pushOptions.ObservedRemote = "refs/heads/main", "refs/heads/main", base
	pushOptions.UpdateKind = string(gatecontract.UpdateKindFastForward)
	pushOptions.Calibration = true

	releaseOptions := options
	releaseOptions.Commit, releaseOptions.Tree, releaseOptions.ParentCommit, releaseOptions.Base = commit, "", "", ""
	releaseOptions.Scenario = "full"
	releaseOptions.Entrypoint = string(gatecontract.CIEntrypointRelease)
	releaseOptions.LocalRef, releaseOptions.RemoteRef, releaseOptions.ObservedRemote, releaseOptions.UpdateKind = "", "", "", ""
	releaseOptions.Calibration = true
	return commitOptions, pushOptions, releaseOptions
}

// prepareRemoteCalibrationLedger 创建空账本或确认可恢复的未接受校准状态。
func prepareRemoteCalibrationLedger(path string) (*gatecontract.DurationLedgerStore, error) {
	store, err := gatecontract.NewDurationLedgerStore(path)
	if err != nil {
		return nil, err
	}
	snapshot, err := store.LoadMetadata()
	if err == nil {
		if snapshot.Ledger.Calibration != nil {
			return nil, errors.New("remote duration calibration is already complete")
		}
		return store, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if _, err := store.CompareAndSwap(0, gatecontract.NewDurationLedger()); err != nil {
		return nil, err
	}
	return store, nil
}

// acceptRemoteDurationCalibration 用 CAS 接受每个 catalog workload 都有成功样本的首代校准。
func acceptRemoteDurationCalibration(
	store *gatecontract.DurationLedgerStore,
	calibration gatecontract.DurationCalibration,
	catalogs ...gatecontract.WorkloadCatalog,
) (gatecontract.DurationLedgerSnapshot, error) {
	return acceptRemoteDurationCalibrationWithPasses(store, calibration, nil, catalogs...)
}

// acceptRemoteDurationCalibrationWithPasses 在比较样本完整时以 CAS 写入校准元数据。
func acceptRemoteDurationCalibrationWithPasses(
	store *gatecontract.DurationLedgerStore,
	calibration gatecontract.DurationCalibration,
	passedWorkloads map[string]struct{},
	catalogs ...gatecontract.WorkloadCatalog,
) (gatecontract.DurationLedgerSnapshot, error) {
	for attempt := range 16 {
		snapshot, err := store.LoadPlanning(remoteCalibrationPlanningContext(calibration))
		if err != nil {
			return gatecontract.DurationLedgerSnapshot{}, err
		}
		// 三次运行 acceptance 必须从 SQLite 证明可比较的 calibration sample 与 accepted shard overhead；
		// correctness PASS 本身不是耗时证据。
		workloadCount, racePackageCount, err := verifyRemoteCalibrationAcceptanceEvidence(
			snapshot,
			calibration,
			passedWorkloads,
			catalogs...,
		)
		if err != nil {
			return gatecontract.DurationLedgerSnapshot{}, err
		}
		calibration.WorkloadCount, calibration.RacePackageCount = workloadCount, racePackageCount
		if snapshot.Ledger.Calibration != nil {
			if equivalentRemoteDurationCalibration(*snapshot.Ledger.Calibration, calibration) {
				return snapshot, nil
			}
			return gatecontract.DurationLedgerSnapshot{}, errors.New("remote duration calibration was completed concurrently")
		}
		updated, err := store.CompareAndSwapCalibration(
			snapshot.Generation,
			&calibration,
		)
		if err == nil {
			return updated, nil
		}
		if !errors.Is(err, gatecontract.ErrDurationLedgerConflict) && !errors.Is(err, gatecontract.ErrDurationLedgerBusy) {
			return gatecontract.DurationLedgerSnapshot{}, err
		}
		time.Sleep(time.Duration(attempt+1) * 10 * time.Millisecond)
	}
	return gatecontract.DurationLedgerSnapshot{}, errors.New("accept duration calibration exceeded retry limit")
}

// verifyRemoteCalibrationAcceptanceEvidence 在三次运行 acceptance 中只接受
// SQLite 的 calibration-mode 样本；同时要求 normal planning 所需的 accepted overhead。
func verifyRemoteCalibrationAcceptanceEvidence(
	snapshot gatecontract.DurationLedgerSnapshot,
	calibration gatecontract.DurationCalibration,
	passedWorkloads map[string]struct{},
	catalogs ...gatecontract.WorkloadCatalog,
) (int, int, error) {
	for _, catalog := range catalogs {
		if err := validateRemoteCalibrationCatalogInputDigests(catalog); err != nil {
			return 0, 0, err
		}
	}
	if passedWorkloads != nil {
		if workloadID := remoteCalibrationMissingPassedWorkload(passedWorkloads, catalogs...); workloadID != "" {
			return 0, 0, fmt.Errorf("%w: workload %q has no successful calibration run coverage", errRemoteCalibrationSamplesIncomplete, workloadID)
		}
	}
	workloadCount, racePackageCount, err := verifyRemoteCalibrationIndexedEvidence(snapshot, calibration, passedWorkloads, catalogs...)
	if err != nil {
		return 0, 0, err
	}
	if snapshot.Ledger.ShardOverhead == nil {
		return 0, 0, fmt.Errorf("%w: accepted shard overhead is incomplete", errRemoteCalibrationSamplesIncomplete)
	}
	return workloadCount, racePackageCount, nil
}

// remoteCalibrationMissingPassedWorkload 返回当前 calibration catalog 中缺少运行覆盖的 workload。
func remoteCalibrationMissingPassedWorkload(passed map[string]struct{}, catalogs ...gatecontract.WorkloadCatalog) string {
	for _, catalog := range catalogs {
		for _, workload := range catalog.Workloads {
			if !workload.Shardable {
				continue
			}
			if _, ok := passed[remoteCalibrationWorkloadKey(workload)]; !ok {
				return workload.ID
			}
		}
	}
	return ""
}

// equivalentRemoteDurationCalibration 要求全部验收字段相同；完成时间有意排除，因为等价 agent
// 可能在不同时间完成却生成同一已接受校准快照。
func equivalentRemoteDurationCalibration(accepted, candidate gatecontract.DurationCalibration) bool {
	accepted.CompletedAt = time.Time{}
	candidate.CompletedAt = time.Time{}
	return accepted == candidate
}

// verifyRemoteCalibrationSamples 要求每个 workload 和每个 Go race 包都有同身份成功样本。
func verifyRemoteCalibrationIndexedEvidence(
	snapshot gatecontract.DurationLedgerSnapshot,
	calibration gatecontract.DurationCalibration,
	passedWorkloads map[string]struct{},
	catalogs ...gatecontract.WorkloadCatalog,
) (int, int, error) {
	index, err := gatecontract.DurationSampleIndexFromSnapshot(
		snapshot,
		remoteCalibrationPlanningContext(calibration),
	)
	if err != nil {
		return 0, 0, err
	}
	return verifyRemoteCalibrationEvidence(index, passedWorkloads, catalogs...)
}

// verifyRemoteCalibrationEvidence 验证每个 catalog workload 都有可比较的成功样本。
type remoteCalibrationWorkloadIdentity struct {
	id, digest, inputDigest string
	kind                    gatecontract.WorkloadKind
	shardable               bool
}

// verifyRemoteCalibrationEvidence 在有本轮 coverage 时要求当前 correctness PASS，
// 并允许跨 input 保守上界；无本轮 coverage 时仍要求 exact-input 成功样本。
func verifyRemoteCalibrationEvidence(
	index gatecontract.DurationSampleIndex,
	passedWorkloads map[string]struct{},
	catalogs ...gatecontract.WorkloadCatalog,
) (int, int, error) {
	expected := make(map[string]remoteCalibrationWorkloadIdentity)
	for _, catalog := range catalogs {
		if err := validateRemoteCalibrationCatalogInputDigests(catalog); err != nil {
			return 0, 0, err
		}
		for _, workload := range catalog.Workloads {
			key := remoteCalibrationWorkloadKey(workload)
			identity := remoteCalibrationWorkloadIdentity{
				id: workload.ID, digest: workload.CommandDigest, inputDigest: workload.InputDigest,
				kind: workload.Kind, shardable: workload.Shardable,
			}
			expected[key] = identity
		}
	}
	runnableRacePackages := make(map[string]struct{})
	for key, workload := range expected {
		packageTarget, runnable, err := remoteCalibrationRunnableRacePackageTarget(workload)
		if err != nil {
			return 0, 0, err
		}
		if runnable {
			runnableRacePackages[packageTarget] = struct{}{}
		}
		if !workload.shardable {
			continue
		}
		if !remoteCalibrationWorkloadHasPass(index, passedWorkloads, key, workload) {
			return 0, 0, fmt.Errorf(
				"%w: workload %q has no successful calibration duration evidence",
				errRemoteCalibrationSamplesIncomplete,
				workload.id,
			)
		}
	}
	if remoteCalibrationCatalogIncomplete(expected, len(runnableRacePackages)) {
		return 0, 0, fmt.Errorf("%w: workload catalog is incomplete", errRemoteCalibrationSamplesIncomplete)
	}
	return len(expected), len(runnableRacePackages), nil
}

// remoteCalibrationRunnableRacePackageTarget 返回 catalog 中仍有可执行 race selector 的唯一包目标。
// race catalog 已在 gate 层过滤 normal-only 静态目标；此处只从实际 workload 重新计算去重后的 runnable 包数。
func remoteCalibrationRunnableRacePackageTarget(workload remoteCalibrationWorkloadIdentity) (string, bool, error) {
	parent, kind, target, targeted, err := gatecontract.ParseWorkloadID(workload.id)
	if err != nil {
		return "", false, err
	}
	if parent != gatecontract.GateIDBackendTestGuardWithRace || !targeted {
		return "", false, nil
	}
	switch kind {
	case gatecontract.WorkloadTargetGoPackage:
		return target, true, nil
	case gatecontract.WorkloadTargetGoTest:
		testTarget, err := gatecontract.ParseGoTestTarget(target)
		if err != nil {
			return "", false, err
		}
		return testTarget.Package, true, nil
	default:
		return "", false, nil
	}
}

// remoteCalibrationWorkloadHasPass 在有本轮 coverage 时允许跨 input 保守上界；
// 仅从历史样本恢复校准时仍要求 exact-input 样本，禁止历史上界替代当前 PASS。
func remoteCalibrationWorkloadHasPass(index gatecontract.DurationSampleIndex, passed map[string]struct{}, key string, workload remoteCalibrationWorkloadIdentity) bool {
	candidate := gatecontract.Workload{
		ID: workload.id, Kind: workload.kind, CommandDigest: workload.digest, InputDigest: workload.inputDigest,
	}
	if passed == nil {
		return index.HasComparableSuccessfulDurationSample(candidate)
	}
	if _, ok := passed[key]; !ok {
		return false
	}
	return index.HasSuccessfulCalibrationDurationEvidence(candidate)
}

// remoteCalibrationCatalogIncomplete 拒绝空 catalog 或缺少 runnable race 包的校准范围。
func remoteCalibrationCatalogIncomplete(expected map[string]remoteCalibrationWorkloadIdentity, runnableRacePackages int) bool {
	return len(expected) == 0 || runnableRacePackages == 0
}

func remoteCalibrationPlanningContext(
	calibration gatecontract.DurationCalibration,
) gatecontract.PlanningContext {
	return gatecontract.PlanningContext{
		Platform:                     calibration.Platform,
		Runner:                       calibration.Runner,
		Toolchain:                    calibration.Toolchain,
		Calibration:                  true,
		CalibrationResourceClassID:   calibration.CalibrationResourceClassID,
		CalibrationResourceCPU:       calibration.CalibrationResourceCPU,
		CalibrationResourceMemoryGiB: calibration.CalibrationResourceMemoryGiB,
		TargetDurationMS:             gatecontract.FullCITargetDurationMS,
		AcceptedSnapshotID:           calibration.AcceptedSnapshotID,
	}
}

// validateRemoteCloudIdentity 校验执行远程 ECI 所需的账号和网络字段。
func validateRemoteCloudIdentity(config remoteRunConfig) error {
	for _, value := range []string{config.AliyunCLI, config.CredentialProfile, config.RegionID, config.SecurityGroupID, config.WorkerRoleName} {
		if strings.TrimSpace(value) == "" {
			return errors.New("remote CI Aliyun identity and network settings are incomplete")
		}
	}
	if err := cicontract.ValidateECIMultiZoneScheduling(cicontract.ECIMultiZoneScheduleStrategy, config.VSwitches); err != nil {
		return err
	}
	if path.Base(config.AliyunCLI) != "aliyun" {
		return errors.New("remote CI cloud client must be the Alibaba Cloud aliyun CLI")
	}
	return nil
}

// validateRemoteStorageConfig 校验 source 与 baseline 对象存储前缀安全且不相同。
func validateRemoteStorageConfig(config remoteRunConfig) error {
	if strings.TrimSpace(config.OSS.Bucket) == "" || strings.TrimSpace(config.OSS.Endpoint) == "" || strings.TrimSpace(config.OSS.InternalEndpoint) == "" ||
		!validRemoteOSSPrefix(config.OSS.SourcePrefix) {
		return errors.New("remote CI OSS settings are incomplete")
	}
	return nil
}

// validRemoteOSSPrefix 只接受规范化、以斜杠结尾的相对 OSS 对象前缀。
func validRemoteOSSPrefix(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.HasPrefix(value, "/") ||
		!strings.HasSuffix(value, "/") || strings.ContainsAny(value, "\\\x00\r\n") {
		return false
	}
	withoutSlash := strings.TrimSuffix(value, "/")
	for segment := range strings.SplitSeq(withoutSlash, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return path.Clean(withoutSlash) == withoutSlash
}

// validateRemoteShardCapacity 校验可配置 ECI 资源档位。
func validateRemoteShardCapacity(config remoteRunConfig) error {
	if err := config.Capacity.ResourcePolicy.Validate(); err != nil {
		return fmt.Errorf("remote CI resource policy: %w", err)
	}
	if _, err := config.Capacity.ResourcePolicy.ResolveCalibrationClass(); err != nil {
		return fmt.Errorf("remote CI calibration resource class: %w", err)
	}
	return nil
}
