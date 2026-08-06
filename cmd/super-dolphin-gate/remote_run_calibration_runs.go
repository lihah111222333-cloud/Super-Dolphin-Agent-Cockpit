package main

import (
	"errors"
	"io"

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
	return options.Base != "" || options.Profile != "" || options.Scenario != "" || len(options.Tests) != 0 || options.WorkloadID != "" || options.CompletionReceiptPath != ""
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
	return executeRemoteCalibrationRunsWithExecutor(options, identity, ledgerStore, checkpoint, stdout, executeRemoteRun)
}

// executeRemoteCalibrationRunsWithExecutor 在每个场景终态后同步写入 SQLite checkpoint。
func executeRemoteCalibrationRunsWithExecutor(
	options remoteRunOptions,
	identity remoteCalibrationIdentity,
	ledgerStore *gatecontract.DurationLedgerStore,
	checkpoint *remoteci.CalibrationCheckpoint,
	stdout io.Writer,
	execute func(remoteRunOptions) (remoteci.RunResult, remoteci.RunInput, error),
) ([3]remoteci.RunInput, [3]remoteci.RunResult, error) {
	if ledgerStore == nil || checkpoint == nil || execute == nil {
		return [3]remoteci.RunInput{}, [3]remoteci.RunResult{}, infrastructureError("remote calibration ledger store, checkpoint, and executor are required")
	}
	commitOptions, pushOptions, releaseOptions := remoteCalibrationRunOptions(options, identity.commit, identity.tree, identity.base)
	runOptions := [3]remoteRunOptions{commitOptions, pushOptions, releaseOptions}
	var inputs [3]remoteci.RunInput
	var results [3]remoteci.RunResult
	for index, current := range runOptions {
		input, result, completed, err := reusableRemoteCalibrationCheckpoint(ledgerStore, checkpoint, current.Scenario)
		if err != nil {
			return inputs, results, infrastructureError("validate remote calibration checkpoint: %v", err)
		}
		if completed {
			inputs[index], results[index] = input, result
			continue
		}
		result, input, runErr := execute(current)
		if err := checkpoint.Observe(current.Scenario, input, result, runErr == nil); err != nil {
			checkpointErr := infrastructureError("persist remote calibration checkpoint: %v", err)
			if runErr != nil {
				return inputs, results, emitRemoteRunResult(stdout, ledgerStore, result, errors.Join(runErr, checkpointErr))
			}
			return inputs, results, checkpointErr
		}
		if runErr != nil {
			return inputs, results, emitRemoteRunResult(stdout, ledgerStore, result, runErr)
		}
		inputs[index] = input
		results[index] = result
	}
	return inputs, results, nil
}

// reusableRemoteCalibrationCheckpoint 只恢复已由同一 SQLite authority 完整回读的成功场景。
func reusableRemoteCalibrationCheckpoint(ledgerStore *gatecontract.DurationLedgerStore, checkpoint *remoteci.CalibrationCheckpoint, scenario string) (remoteci.RunInput, remoteci.RunResult, bool, error) {
	if ledgerStore == nil || checkpoint == nil {
		return remoteci.RunInput{}, remoteci.RunResult{}, false, errors.New("remote calibration ledger store and checkpoint are required")
	}
	input, result, completed := checkpoint.Completed(scenario)
	if !completed {
		return remoteci.RunInput{}, remoteci.RunResult{}, false, nil
	}
	record, found, err := remoteCalibrationCheckpointAuthorityRecord(ledgerStore, result.JobID)
	if err != nil {
		return remoteci.RunInput{}, remoteci.RunResult{}, false, err
	}
	if !found || !remoteCalibrationCheckpointRunMatches(input, result, record) {
		return reopenRemoteCalibrationCheckpoint(checkpoint, scenario)
	}
	result = remoteRunResultFromLedgerRecord(record)
	complete, err := remoteCalibrationCheckpointHasCompleteCatalog(input, result)
	if err != nil {
		return remoteci.RunInput{}, remoteci.RunResult{}, false, err
	}
	if !complete {
		return reopenRemoteCalibrationCheckpoint(checkpoint, scenario)
	}
	input.LedgerStore = ledgerStore
	return input, result, true, nil
}

// remoteCalibrationCheckpointAuthorityRecord 从唯一账本读取 checkpoint 绑定的运行记录。
func remoteCalibrationCheckpointAuthorityRecord(ledgerStore *gatecontract.DurationLedgerStore, jobID string) (gatecontract.RemoteCIRunRecord, bool, error) {
	record, err := ledgerStore.LoadRemoteCIRun(jobID)
	if errors.Is(err, gatecontract.ErrRemoteCIRunNotFound) {
		return gatecontract.RemoteCIRunRecord{}, false, nil
	}
	if err != nil {
		return gatecontract.RemoteCIRunRecord{}, false, err
	}
	return record, true, nil
}

// remoteCalibrationCheckpointHasCompleteCatalog 验证权威运行覆盖当前 catalog 的全部 workload。
func remoteCalibrationCheckpointHasCompleteCatalog(input remoteci.RunInput, result remoteci.RunResult) (bool, error) {
	catalog, _, err := remoteCalibrationCatalog(input)
	if err != nil {
		return false, err
	}
	passed, err := remoteCalibrationPassedCatalogWorkloadSet(input, catalog, result)
	if err != nil {
		return false, err
	}
	for _, workload := range catalog.Workloads {
		if _, ok := passed[remoteCalibrationWorkloadKey(workload)]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func reopenRemoteCalibrationCheckpoint(checkpoint *remoteci.CalibrationCheckpoint, scenario string) (remoteci.RunInput, remoteci.RunResult, bool, error) {
	if err := checkpoint.Reopen(scenario); err != nil {
		return remoteci.RunInput{}, remoteci.RunResult{}, false, err
	}
	return remoteci.RunInput{}, remoteci.RunResult{}, false, nil
}

// remoteCalibrationCheckpointRunMatches 确认 checkpoint、权威账本记录和运行输入属于同一次成功校准。
func remoteCalibrationCheckpointRunMatches(input remoteci.RunInput, result remoteci.RunResult, record gatecontract.RemoteCIRunRecord) bool {
	return remoteCalibrationCheckpointRunIdentityMatches(input, result, record) &&
		remoteCalibrationCheckpointExecutionMatches(input, result, record) &&
		remoteCalibrationCheckpointSourceMatches(input, result, record) &&
		remoteCalibrationCheckpointPassed(record)
}

// remoteCalibrationCheckpointRunIdentityMatches 验证 job 与 accepted generation 的稳定身份。
func remoteCalibrationCheckpointRunIdentityMatches(input remoteci.RunInput, result remoteci.RunResult, record gatecontract.RemoteCIRunRecord) bool {
	return record.JobID == result.JobID &&
		record.AcceptedGeneration == input.AcceptedGeneration &&
		record.AcceptedGeneration == result.AcceptedGeneration
}

// remoteCalibrationCheckpointExecutionMatches 验证同一执行入口、计划、catalog 和 snapshot。
func remoteCalibrationCheckpointExecutionMatches(input remoteci.RunInput, result remoteci.RunResult, record gatecontract.RemoteCIRunRecord) bool {
	return record.Entrypoint == input.Entrypoint &&
		record.Entrypoint == result.Entrypoint &&
		record.Profile == input.Profile &&
		record.Profile == result.Profile &&
		record.PlanDigest == result.PlanDigest &&
		record.CatalogDigest == result.CatalogDigest &&
		record.ImageCacheSnapshotID == input.ImageCacheSnapshotID &&
		record.ImageCacheSnapshotID == result.ImageCacheSnapshotID
}

// remoteCalibrationCheckpointSourceMatches 验证同一 source tree 与候选 Gate 编译身份。
func remoteCalibrationCheckpointSourceMatches(input remoteci.RunInput, result remoteci.RunResult, record gatecontract.RemoteCIRunRecord) bool {
	return record.SourceTreeSHA == input.Tree &&
		record.SourceTreeSHA == result.SourceTreeSHA &&
		record.CandidateGateSourceSHA256 == input.CandidateGateSourceSHA256 &&
		record.CandidateGateSourceSHA256 == result.CandidateGateSourceSHA256 &&
		record.CandidateGateToolchainSHA256 == input.CandidateGateToolchainSHA256 &&
		record.CandidateGateToolchainSHA256 == result.CandidateGateToolchainSHA256
}

// remoteCalibrationCheckpointPassed 验证账本记录保持完整的权威成功终态。
func remoteCalibrationCheckpointPassed(record gatecontract.RemoteCIRunRecord) bool {
	return record.Status == gatecontract.ResultStatusPassed &&
		record.Authoritative &&
		record.CleanupComplete &&
		record.ErrorText == ""
}

func remoteRunResultFromLedgerRecord(record gatecontract.RemoteCIRunRecord) remoteci.RunResult {
	return remoteci.RunResult{
		SchemaVersion:                remoteci.RunResultSchemaVersion,
		AcceptedGeneration:           record.AcceptedGeneration,
		ImageCacheSnapshotID:         record.ImageCacheSnapshotID,
		JobID:                        record.JobID,
		AgentTokenDigest:             record.AgentTokenDigest,
		Entrypoint:                   record.Entrypoint,
		Profile:                      record.Profile,
		PlanDigest:                   record.PlanDigest,
		CatalogDigest:                record.CatalogDigest,
		SourceTreeSHA:                record.SourceTreeSHA,
		CandidateGateSourceSHA256:    record.CandidateGateSourceSHA256,
		CandidateGateToolchainSHA256: record.CandidateGateToolchainSHA256,
		RunnerImage:                  record.RunnerImage,
		Status:                       record.Status,
		Authoritative:                record.Authoritative,
		StartedAt:                    record.StartedAt,
		CompletedAt:                  record.CompletedAt,
		GateExecutions:               append(append([]gatecontract.PlanGateExecution(nil), record.Executions...), record.WorkloadExecutions...),
		WorkloadExecutions:           append([]gatecontract.PlanGateExecution(nil), record.WorkloadExecutions...),
		OptimizationWarnings:         append([]string(nil), record.Warnings...),
		TimingWarnings:               append([]gatecontract.RemoteCITimingWarning(nil), record.TimingWarnings...),
		CleanupComplete:              record.CleanupComplete,
	}
}

// acceptAndEmitRemoteCalibration 验证三次运行身份和 catalog 后用 CAS 接受并输出校准凭据。
