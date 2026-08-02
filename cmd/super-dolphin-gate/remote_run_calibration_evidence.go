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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
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
	catalogs, digests, err := remoteCalibrationCatalogs(inputs)
	if err != nil {
		return err
	}
	calibration := remoteDurationCalibration(commit, inputs, results[2], digests)
	passedWorkloads, err := remoteCalibrationPassedWorkloadSet(inputs, catalogs, results)
	if err != nil {
		return infrastructureError("verify remote calibration PASS coverage: %v", err)
	}
	snapshot, err := acceptRemoteDurationCalibrationWithPasses(
		ledgerStore,
		calibration,
		passedWorkloads,
		catalogs[:]...,
	)
	if err != nil {
		return infrastructureError("accept remote duration calibration: %v", err)
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
	reused := make(map[string]struct{}, len(result.ReusedWorkloads))
	for _, workloadID := range result.ReusedWorkloads {
		reused[string(workloadID)] = struct{}{}
	}
	passed := make(map[string]struct{})
	for _, workload := range catalog.Workloads {
		if _, reusedWorkload := reused[workload.ID]; reusedWorkload {
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
	return workload.ID + "\x00" + workload.CommandDigest
}

// validateRemoteCalibrationRuns 拒绝三种 source、运行时或权威性在首代校准中发生漂移。
func validateRemoteCalibrationRuns(inputs [3]remoteci.RunInput, results [3]remoteci.RunResult) error {
	commitInput, pushInput, releaseInput := inputs[0], inputs[1], inputs[2]
	if !remoteCalibrationSourcesMatch(commitInput, pushInput, releaseInput) ||
		!remoteCalibrationRuntimeMatches(commitInput, pushInput, releaseInput) ||
		!results[0].Authoritative || !results[1].Authoritative || !results[2].Authoritative ||
		results[0].Status != gatecontract.ResultStatusPassed ||
		results[1].Status != gatecontract.ResultStatusPassed ||
		results[2].Status != gatecontract.ResultStatusPassed {
		return infrastructureError("remote calibration commit, push, and release identities drifted")
	}
	return nil
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
		commitInput.ToolchainDigest == releaseInput.ToolchainDigest
}

// remoteCalibrationCatalogs 构造并返回 commit、push、release 三份独立 catalog digest。
func remoteCalibrationCatalogs(inputs [3]remoteci.RunInput) ([3]gatecontract.WorkloadCatalog, [3]string, error) {
	var catalogs [3]gatecontract.WorkloadCatalog
	var digests [3]string
	for index, input := range inputs {
		catalog, digest, err := remoteCalibrationCatalog(input)
		if err != nil {
			return catalogs, digests, infrastructureError("build remote calibration catalog: %v", err)
		}
		catalogs[index], digests[index] = catalog, digest
	}
	return catalogs, digests, nil
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
		CommitEntrypoint: inputs[0].Entrypoint, PushEntrypoint: inputs[1].Entrypoint,
		ReleaseEntrypoint: inputs[2].Entrypoint, CommitCatalogDigest: digests[0],
		PushCatalogDigest: digests[1], ReleaseCatalogDigest: digests[2],
		CompletedAt: completedAt.UTC(),
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
		SchemaVersion: remoteCalibrationResultSchemaVersion, Commit: accepted.Commit, Tree: accepted.Tree,
		RunnerManifestDigest: accepted.Runner, CommitJobID: results[0].JobID, PushJobID: results[1].JobID,
		ReleaseJobID: results[2].JobID, LedgerGeneration: snapshot.Generation, WorkloadCount: accepted.WorkloadCount,
		RacePackageCount: accepted.RacePackageCount, CompletedAt: accepted.CompletedAt,
	}
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

// remoteCalibrationCatalog 从固定运行输入生成可比较的 workload catalog 与 digest。
func remoteCalibrationCatalog(input remoteci.RunInput) (gatecontract.WorkloadCatalog, string, error) {
	plan, err := gatecontract.BuildGatePlan(input.Profile, input.Source)
	if err != nil {
		return gatecontract.WorkloadCatalog{}, "", err
	}
	catalog, err := gatecontract.BuildCalibrationWorkloadCatalog(plan, gatecontract.DefaultWorkloadBootstrapPolicy(), input.Inventory)
	if err != nil {
		return gatecontract.WorkloadCatalog{}, "", err
	}
	digest, err := gatecontract.WorkloadCatalogDigest(catalog)
	if err != nil {
		return gatecontract.WorkloadCatalog{}, "", err
	}
	return catalog, digest, nil
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
		if snapshot.Ledger.Calibration != nil {
			return gatecontract.DurationLedgerSnapshot{}, errors.New("remote duration calibration was completed concurrently")
		}
		workloadCount, racePackageCount, err := verifyRemoteCalibrationIndexedEvidence(
			snapshot,
			calibration,
			passedWorkloads,
			catalogs...,
		)
		if err != nil {
			return gatecontract.DurationLedgerSnapshot{}, err
		}
		calibration.WorkloadCount, calibration.RacePackageCount = workloadCount, racePackageCount
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
	id, digest string
	kind       gatecontract.WorkloadKind
	shardable  bool
}

// verifyRemoteCalibrationEvidence 验证每个 catalog workload 都有可比较的成功样本。
func verifyRemoteCalibrationEvidence(
	index gatecontract.DurationSampleIndex,
	passedWorkloads map[string]struct{},
	catalogs ...gatecontract.WorkloadCatalog,
) (int, int, error) {
	expected := make(map[string]remoteCalibrationWorkloadIdentity)
	for _, catalog := range catalogs {
		for _, workload := range catalog.Workloads {
			expected[workload.ID+"\x00"+workload.CommandDigest] = remoteCalibrationWorkloadIdentity{
				id: workload.ID, digest: workload.CommandDigest, kind: workload.Kind, shardable: workload.Shardable,
			}
		}
	}
	racePackages := 0
	for key, workload := range expected {
		parent, err := gatecontract.WorkloadParentGateID(workload.id)
		if err != nil {
			return 0, 0, err
		}
		if remoteCalibrationRacePackage(parent, workload.kind) {
			racePackages++
		}
		if !workload.shardable {
			continue
		}
		if !remoteCalibrationWorkloadHasPass(index, passedWorkloads, key, workload) {
			return 0, 0, fmt.Errorf(
				"%w: workload %q has no comparable successful duration sample",
				errRemoteCalibrationSamplesIncomplete,
				workload.id,
			)
		}
	}
	if remoteCalibrationCatalogIncomplete(expected, racePackages) {
		return 0, 0, fmt.Errorf("%w: workload catalog is incomplete", errRemoteCalibrationSamplesIncomplete)
	}
	return len(expected), racePackages, nil
}

// remoteCalibrationRacePackage 识别必须拥有独立证据的 Go race workload。
func remoteCalibrationRacePackage(parent gatecontract.GateID, kind gatecontract.WorkloadKind) bool {
	return parent == gatecontract.GateIDBackendTestGuardWithRace && kind == gatecontract.WorkloadKindGoTest
}

// remoteCalibrationWorkloadHasPass 优先使用本轮通过集合，再回退到可比较账本样本。
func remoteCalibrationWorkloadHasPass(index gatecontract.DurationSampleIndex, passed map[string]struct{}, key string, workload remoteCalibrationWorkloadIdentity) bool {
	if _, ok := passed[key]; ok {
		return true
	}
	return index.HasComparableSuccessfulDurationSample(gatecontract.Workload{ID: workload.id, Kind: workload.kind, CommandDigest: workload.digest})
}

// remoteCalibrationCatalogIncomplete 拒绝空 catalog 或缺少 race 包的校准范围。
func remoteCalibrationCatalogIncomplete(expected map[string]remoteCalibrationWorkloadIdentity, racePackages int) bool {
	return len(expected) == 0 || racePackages == 0
}

func remoteCalibrationPlanningContext(
	calibration gatecontract.DurationCalibration,
) gatecontract.PlanningContext {
	return gatecontract.PlanningContext{
		Platform:         calibration.Platform,
		Runner:           calibration.Runner,
		Toolchain:        calibration.Toolchain,
		TargetDurationMS: gatecontract.FullCITargetDurationMS,
	}
}

// validateRemoteCloudIdentity 校验执行远程 ECI 所需的账号和网络字段。
func validateRemoteCloudIdentity(config remoteRunConfig) error {
	for _, value := range []string{config.AliyunCLI, config.CredentialProfile, config.RegionID, config.VSwitchID, config.SecurityGroupID, config.WorkerRoleName} {
		if strings.TrimSpace(value) == "" {
			return errors.New("remote CI Aliyun identity and network settings are incomplete")
		}
	}
	return nil
}

// validateRemoteStorageConfig 校验 source 与 baseline 对象存储前缀安全且不相同。
func validateRemoteStorageConfig(config remoteRunConfig) error {
	if strings.TrimSpace(config.OSS.Bucket) == "" || strings.TrimSpace(config.OSS.Endpoint) == "" || strings.TrimSpace(config.OSS.InternalEndpoint) == "" ||
		!validRemoteOSSPrefix(config.OSS.SourcePrefix) || !validRemoteOSSPrefix(config.OSS.BaselinePrefix) ||
		config.OSS.SourcePrefix == config.OSS.BaselinePrefix {
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
	_, err := remoteBaselineSeedResources(config)
	return err
}

// remoteBaselineSeedResources resolves the explicit resource class used to refresh the shared baseline.
func remoteBaselineSeedResources(config remoteRunConfig) (eci.Resources, error) {
	class, err := config.Capacity.ResourcePolicy.ResolveClass(config.Capacity.SeedClass)
	if err != nil {
		return eci.Resources{}, fmt.Errorf("remote CI seed resource class: %w", err)
	}
	return eci.Resources{CPU: class.VCPU, MemoryGiB: class.MemoryGiB}, nil
}
