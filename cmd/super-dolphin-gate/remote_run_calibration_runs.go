package main

import (
	"encoding/hex"
	"errors"
	"io"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type remoteCalibrationIdentity struct {
	commit string
	tree   string
	base   string
}

// validateRemoteCalibrationOptions 限制校准只接收可复现身份、存储和分片容量参数。
func validateRemoteCalibrationOptions(options remoteRunOptions) error {
	if remoteCalibrationHasScenarioOptions(options) || remoteCalibrationHasSourceOptions(options) {
		return protocolError("remote calibrate accepts only run identity, storage, and shard-limit flags")
	}
	return nil
}

// remoteCalibrationHasScenarioOptions 判断校准是否混入运行场景或测试选择器。
func remoteCalibrationHasScenarioOptions(options remoteRunOptions) bool {
	return options.Base != "" || options.Profile != "" || options.Scenario != "" || len(options.Tests) != 0
}

// remoteCalibrationHasSourceOptions 判断校准是否混入推送或显式树 source 参数。
func remoteCalibrationHasSourceOptions(options remoteRunOptions) bool {
	return options.LocalRef != "" || options.RemoteRef != "" || options.ObservedRemote != "" ||
		options.UpdateKind != "" || options.Tree != "" || options.ParentCommit != ""
}

// resolveRemoteCalibrationIdentity 固定三次权威运行共享的提交、树和推送基线。
func resolveRemoteCalibrationIdentity(repositoryRoot string, revision string) (remoteCalibrationIdentity, error) {
	commit, err := remoteGitOutput(repositoryRoot, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	if err != nil {
		return remoteCalibrationIdentity{}, sourceError("resolve calibration commit: %v", err)
	}
	tree, err := remoteGitOutput(repositoryRoot, "rev-parse", "--verify", "--end-of-options", commit+"^{tree}")
	if err != nil {
		return remoteCalibrationIdentity{}, sourceError("resolve calibration tree: %v", err)
	}
	base, err := remoteGitOutput(repositoryRoot, "rev-parse", "--verify", "--end-of-options", commit+"^1^{commit}")
	if err != nil {
		return remoteCalibrationIdentity{}, sourceError("resolve calibration push base: %v", err)
	}
	return remoteCalibrationIdentity{commit: commit, tree: tree, base: base}, nil
}

// executeRemoteCalibrationRuns 保持首代 commit、push、release 的固定顺序和独立结果。
func executeRemoteCalibrationRuns(
	options remoteRunOptions,
	identity remoteCalibrationIdentity,
	ledgerStore *gatecontract.DurationLedgerStore,
	checkpoint *remoteci.CalibrationCheckpoint,
	stdout io.Writer,
) ([3]remoteci.RunInput, [3]remoteci.RunResult, error) {
	commitOptions, pushOptions, releaseOptions := remoteCalibrationRunOptions(options, identity.commit, identity.tree, identity.base)
	runOptions := [3]remoteRunOptions{commitOptions, pushOptions, releaseOptions}
	var inputs [3]remoteci.RunInput
	var results [3]remoteci.RunResult
	for index, current := range runOptions {
		input, result, ok, err := reusableRemoteCalibrationCheckpoint(ledgerStore, checkpoint, current.Scenario)
		if err != nil {
			return inputs, results, infrastructureError("validate remote calibration checkpoint: %v", err)
		}
		if ok {
			inputs[index], results[index] = input, result
			continue
		}
		result, input, runErr := executeRemoteRun(current)
		if err := checkpoint.Observe(current.Scenario, input, result, runErr == nil); err != nil {
			return inputs, results, infrastructureError("persist remote calibration checkpoint: %v", err)
		}
		if runErr != nil {
			return inputs, results, emitRemoteRunResult(stdout, result, runErr)
		}
		inputs[index] = input
		results[index] = result
	}
	return inputs, results, nil
}

// reusableRemoteCalibrationCheckpoint 只复用账本已覆盖当前场景全部 workload 的完成断点。
func reusableRemoteCalibrationCheckpoint(
	ledgerStore *gatecontract.DurationLedgerStore,
	checkpoint *remoteci.CalibrationCheckpoint,
	scenario string,
) (remoteci.RunInput, remoteci.RunResult, bool, error) {
	input, result, completed := checkpoint.Completed(scenario)
	if !completed {
		return remoteci.RunInput{}, remoteci.RunResult{}, false, nil
	}
	record, err := ledgerStore.LoadRemoteCIRun(result.JobID)
	if errors.Is(err, gatecontract.ErrRemoteCIRunNotFound) {
		if reopenErr := checkpoint.Reopen(scenario); reopenErr != nil {
			return remoteci.RunInput{}, remoteci.RunResult{}, false, reopenErr
		}
		return remoteci.RunInput{}, remoteci.RunResult{}, false, nil
	}
	if err != nil {
		return remoteci.RunInput{}, remoteci.RunResult{}, false, err
	}
	if !remoteCalibrationCheckpointRunMatches(input, result, record) {
		if err := checkpoint.Reopen(scenario); err != nil {
			return remoteci.RunInput{}, remoteci.RunResult{}, false, err
		}
		return remoteci.RunInput{}, remoteci.RunResult{}, false, nil
	}
	result = remoteRunResultFromLedgerRecord(record)
	complete, err := remoteCalibrationCheckpointEvidenceComplete(ledgerStore, input, result)
	if err != nil {
		return remoteci.RunInput{}, remoteci.RunResult{}, false, err
	}
	if !complete {
		if err := checkpoint.Reopen(scenario); err != nil {
			return remoteci.RunInput{}, remoteci.RunResult{}, false, err
		}
		return remoteci.RunInput{}, remoteci.RunResult{}, false, nil
	}
	return input, result, true, nil
}

// remoteCalibrationCheckpointEvidenceComplete 验证 checkpoint 对应 catalog 的全部 workload 都已有 PASS 证据。
func remoteCalibrationCheckpointEvidenceComplete(ledgerStore *gatecontract.DurationLedgerStore, input remoteci.RunInput, result remoteci.RunResult) (bool, error) {
	catalog, _, err := remoteCalibrationCatalog(input)
	if err != nil {
		return false, err
	}
	planning := gatecontract.PlanningContext{
		Platform: input.Platform, Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest,
		MaxShards: max(1, int(input.MaxShards)), TargetDurationMS: gatecontract.FullCITargetDurationMS,
	}
	snapshot, err := ledgerStore.LoadPlanning(planning)
	if err != nil {
		return false, err
	}
	index, err := gatecontract.DurationSampleIndexFromSnapshot(snapshot, planning)
	if err != nil {
		return false, err
	}
	passed, err := remoteCalibrationPassedCatalogWorkloadSet(input, catalog, result)
	if err != nil {
		return false, err
	}
	for _, workload := range catalog.Workloads {
		if _, ok := passed[remoteCalibrationWorkloadKey(workload)]; ok ||
			index.HasComparableSuccessfulDurationSample(workload) {
			continue
		}
		return false, nil
	}
	return true, nil
}

func remoteCalibrationCheckpointRunMatches(
	input remoteci.RunInput,
	checkpointResult remoteci.RunResult,
	record gatecontract.RemoteCIRunRecord,
) bool {
	return remoteCheckpointIdentityMatches(input, checkpointResult, record) &&
		remoteCheckpointExecutionMatches(input, checkpointResult, record) &&
		remoteCheckpointCompletionMatches(checkpointResult, record)
}

// remoteCheckpointIdentityMatches 验证账本记录与输入及 checkpoint 的不可变身份一致。
func remoteCheckpointIdentityMatches(input remoteci.RunInput, result remoteci.RunResult, record gatecontract.RemoteCIRunRecord) bool {
	return record.JobID == result.JobID && record.RequesterFingerprint == input.RequesterFingerprint &&
		record.RequesterFingerprint == result.RequesterFingerprint && record.Entrypoint == input.Entrypoint &&
		record.Entrypoint == result.Entrypoint && record.Profile == input.Profile && record.Profile == result.Profile
}

// remoteCheckpointExecutionMatches 验证 checkpoint 的计划、catalog、源树和镜像没有漂移。
func remoteCheckpointExecutionMatches(input remoteci.RunInput, result remoteci.RunResult, record gatecontract.RemoteCIRunRecord) bool {
	return record.PlanDigest == result.PlanDigest && record.CatalogDigest == result.CatalogDigest &&
		record.SourceTreeSHA == result.SourceTreeSHA && record.RunnerImage == input.RunnerImage &&
		record.CandidateCLIManifestSHA256 == result.CandidateCLIManifestSHA256 &&
		validRemoteCandidateCLIManifestSHA256(record.CandidateCLIManifestSHA256)
}

func validRemoteCandidateCLIManifestSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// remoteCheckpointCompletionMatches 仅接受干净且权威的成功记录。
func remoteCheckpointCompletionMatches(result remoteci.RunResult, record gatecontract.RemoteCIRunRecord) bool {
	return record.Status == gatecontract.ResultStatusPassed && record.Status == result.Status &&
		record.Authoritative && result.Authoritative && record.CleanupComplete && result.CleanupComplete && record.ErrorText == "" &&
		record.CandidateTestBinaryReceiptBindingDigest == result.CandidateTestBinaryReceiptBindingDigest
}

func remoteRunResultFromLedgerRecord(record gatecontract.RemoteCIRunRecord) remoteci.RunResult {
	shards := make([]remoteci.ShardResult, len(record.Shards))
	for index, shard := range record.Shards {
		shards[index] = remoteci.ShardResult{
			ShardIdentity:         shard.ShardIdentity,
			ContainerGroup:        shard.ContainerGroup,
			ContainerStatus:       shard.ContainerStatus,
			ExecutedWorkloads:     append([]gatecontract.GateID(nil), shard.Workloads...),
			MaterializationTiming: shard.MaterializationTiming,
		}
	}
	return remoteci.RunResult{
		SchemaVersion:                           remoteci.RunResultSchemaVersion,
		JobID:                                   record.JobID,
		RequesterFingerprint:                    record.RequesterFingerprint,
		Entrypoint:                              record.Entrypoint,
		Profile:                                 record.Profile,
		PlanDigest:                              record.PlanDigest,
		CatalogDigest:                           record.CatalogDigest,
		SourceTreeSHA:                           record.SourceTreeSHA,
		CandidateCLIManifestSHA256:              record.CandidateCLIManifestSHA256,
		CandidateTestBinaryReceiptBindingDigest: record.CandidateTestBinaryReceiptBindingDigest,
		RunnerImage:                             record.RunnerImage,
		Status:                                  record.Status,
		Authoritative:                           record.Authoritative,
		StartedAt:                               record.StartedAt,
		CompletedAt:                             record.CompletedAt,
		Shards:                                  shards,
		ReusedWorkloads:                         append([]gatecontract.GateID(nil), record.ReusedWorkloads...),
		CacheMissWorkloads:                      append([]gatecontract.GateID(nil), record.CacheMisses...),
		GateExecutions:                          append([]gatecontract.PlanGateExecution(nil), record.Executions...),
		WorkloadExecutions:                      append([]gatecontract.PlanGateExecution(nil), record.WorkloadExecutions...),
		PerformanceTimings:                      append([]gatecontract.RemoteCIPhaseTiming(nil), record.PhaseTimings...),
		OptimizationWarnings:                    append([]string(nil), record.Warnings...),
		CandidateTestBinaryBuilds:               remoteCandidateTestBinaryBuildsFromLedgerRecords(record.CandidateTestBinaryBuilds),
		CleanupComplete:                         record.CleanupComplete,
	}
}

func remoteCandidateTestBinaryBuildsFromLedgerRecords(records []gatecontract.CandidateTestBinaryBuildRecord) []remoteci.CandidateTestBinaryBuilderBuild {
	builds := make([]remoteci.CandidateTestBinaryBuilderBuild, 0, len(records))
	for _, record := range records {
		generations := make([]remoteci.CandidateTestBinaryCacheGenerationHit, 0, len(record.GOCacheBaselineHitRecords))
		for _, hit := range record.GOCacheBaselineHitRecords {
			generations = append(generations, remoteci.CandidateTestBinaryCacheGenerationHit{
				Generation: hit.Generation, Hits: hit.Hits, AnchorGeneration: hit.AnchorGeneration,
				AnchorManifestDigest: hit.AnchorManifestDigest, ManifestDigest: hit.ManifestDigest,
				CacheRootIdentity: hit.CacheRootIdentity,
			})
		}
		builds = append(builds, remoteci.CandidateTestBinaryBuilderBuild{
			Artifact: remoteci.CandidateTestBinaryArtifactRef{
				CandidateTree: record.CandidateTree, Package: record.Package, Mode: record.Mode,
				Platform: record.Platform, GoToolchain: record.GoToolchain, CGOEnabled: record.CGOEnabled,
				ToolchainSHA256: record.ToolchainSHA256, BuildFlags: append([]string(nil), record.BuildFlags...),
				CompileClosureSHA256: record.CompileClosureSHA256, ManifestSHA256: record.ManifestSHA256,
				BinarySHA256: strings.TrimPrefix(record.ArtifactSHA256, "sha256:"), BinarySize: record.BinarySize,
			},
			Metrics: remoteci.CandidateTestBinaryBuildMetrics{
				GoListWallMS: record.GoListWallMS, BuildWallMS: record.BuildWallMS,
				CompileActionMS: record.CompileActionMS, LinkActionMS: record.LinkActionMS,
				CompileCriticalWallMS: record.CompileCriticalWallMS, GOCachePrivateHits: record.GOCachePrivateHits, GOCachePrivateRootIdentity: record.GOCachePrivateRootIdentity,
				GOCacheBaselineHitsByGeneration: generations, GOCacheMisses: record.GOCacheMisses, GOCachePuts: record.GOCachePuts,
			},
		})
	}
	return builds
}

// acceptAndEmitRemoteCalibration 验证三次运行身份和 catalog 后用 CAS 接受并输出校准凭据。
