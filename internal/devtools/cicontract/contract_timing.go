package cicontract

import "errors"

// TimingPhases 返回权威耗时契约的稳定顺序。
func TimingPhases() []TimingPhase {
	return []TimingPhase{TimingECIWait, TimingSourceMaterialize, TimingCandidateCompile, TimingStartup, TimingTestBody, TimingTotal}
}

// CompileGroupTimingPhases 返回 compile group 专属的编译阶段；它不属于 workload
// startup/body/total，也不把 candidate Gate CLI 编译时间混入测试二进制编译。
func CompileGroupTimingPhases() []TimingPhase {
	return []TimingPhase{TimingTestBinaryCompile}
}

// ValidateTimingContract 锁定精确耗时字段、阶段与聚合语义的代码 owner。
func ValidateTimingContract() error {
	if TimingDurationColumn != "duration_ms" {
		return errors.New("remote CI timing duration column must be duration_ms")
	}
	wantPhases := [...]TimingPhase{TimingECIWait, TimingSourceMaterialize, TimingCandidateCompile, TimingStartup, TimingTestBody, TimingTotal}
	phases := TimingPhases()
	if len(phases) != len(wantPhases) {
		return errors.New("remote CI timing phases are incomplete")
	}
	for index := range wantPhases {
		if phases[index] != wantPhases[index] {
			return errors.New("remote CI timing phase order drifted")
		}
	}
	if TimingAggregationRaw != "raw" || TimingAggregationIntervalUnion != "interval_union" || TimingAggregationCriticalPath != "critical_path" {
		return errors.New("remote CI timing aggregation vocabulary drifted")
	}
	if len(CompileGroupTimingPhases()) != 1 || CompileGroupTimingPhases()[0] != TimingTestBinaryCompile {
		return errors.New("remote CI compile group timing phase drifted")
	}
	return nil
}

// ValidateSourceTransportContract 锁定 accepted baseline、thin bundle、strict
// manifest 和 shard 创建前的唯一源码传输边界。
func ValidateSourceTransportContract() error {
	if !validSourceTransportAssets() || !validSourceTransportProtocol() {
		return errors.New("remote CI incremental source transport contract drifted")
	}
	return nil
}

// validSourceTransportAssets 锁定 accepted baseline 与候选传输资产的规范路径和名称。
func validSourceTransportAssets() bool {
	return SourceBaselineRepositoryPath == "/opt/super-dolphin-gate/source-baseline.git" &&
		SourceBundleName == "source.bundle" &&
		SourceManifestName == "source-manifest.json" &&
		SourceBundleRef == "refs/source/materialized" &&
		SourceBaseRef == "refs/source/base"
}

// validSourceTransportProtocol 锁定 thin bundle 协议、单 prerequisite 与上传屏障。
func validSourceTransportProtocol() bool {
	return SourceManifestSchemaVersion == 3 &&
		SourceTransportKind == "git-bundle-thin" &&
		SourceBundleHeaderVersion == "v2" &&
		SourceBundlePrerequisiteCount == 1 &&
		SourceAssetsUploadBarrier == "source-bundle-and-strict-manifest-before-lpt-shards/v1"
}

// ValidateConcurrencyPolicy 锁定三层正常执行并发且不允许仓库内 admission 边界。
func ValidateConcurrencyPolicy() error {
	if GitHookInvocationConcurrencyPolicy != "unbounded_by_repository" || RemoteCIJobConcurrencyPolicy != "unbounded_by_repository" || ShardConcurrencyPolicy != "unbounded_by_repository" {
		return errors.New("remote CI hook, job, and shard concurrency must be unbounded by repository policy")
	}
	if GitIndexLockBoundary != "git_worktree_index_consistency_not_ci_admission" {
		return errors.New("remote CI Git index lock boundary drifted")
	}
	return nil
}

// RequiredChecks 返回每次远程 CI 都必须有执行 miss 或严格复用 hit 通过证据的稳定检查目录。
func RequiredChecks() []RequiredCheck {
	return []RequiredCheck{RequiredCheckGate, RequiredCheckNormal, RequiredCheckE2E, RequiredCheckRace, RequiredCheckFrontend, RequiredCheckDependency}
}
