package remoteci

import (
	"context"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci/source"
)

type remoteAssets struct {
	artifact              source.Artifact
	manifestDigest        string
	patchKey              string
	manifestKey           string
	candidateCLI          CandidateCLIArtifactRef
	candidatePath         string
	candidateManifestPath string
	candidateBinaryKey    string
}

// remoteCIShardRecords 仅持久化已观测到的 ECI 分片，并保留尚未取得终态的已创建分片。
func remoteCIShardRecords(shardResults []ShardResult) []gate.RemoteCIShardRecord {
	shards := make([]gate.RemoteCIShardRecord, 0, len(shardResults))
	for _, shard := range shardResults {
		if shard.ShardIdentity == "" && shard.ContainerGroup == "" &&
			shard.ContainerStatus == "" && len(shard.ExecutedWorkloads) == 0 {
			continue
		}
		containerStatus := shard.ContainerStatus
		if containerStatus == "" {
			containerStatus = "Unknown"
		}
		shards = append(shards, gate.RemoteCIShardRecord{ShardIdentity: shard.ShardIdentity, ContainerGroup: shard.ContainerGroup, ContainerStatus: containerStatus, Workloads: append([]gate.GateID(nil), shard.ExecutedWorkloads...)})
	}
	return shards
}

// lookupPassedWorkloads 计算 workload 指纹并读取当前环境下可复用的通过标记。
func (coordinator *Coordinator) lookupPassedWorkloads(ctx context.Context, input RunInput, catalog gate.WorkloadCatalog, trace *remoteRunPerformanceTrace) (remoteWorkloadCacheSelection, error) {
	return lookupPassedWorkloads(ctx, coordinator.store, coordinator.config.WorkloadCachePrefix, coordinator.now, input, catalog, trace)
}

// remotePlanningContext 统一缓存投影与最终 LPT 使用的环境和时限身份。
func remotePlanningContext(input RunInput) gate.PlanningContext {
	return gate.PlanningContext{Platform: input.Platform, Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest, MaxShards: int(input.MaxShards), TargetDurationMS: gate.FullCITargetDurationMS}
}

// prepareRemoteShardRequests 构建候选源和 CLI 资产，并生成消费二者的分片请求。
func (coordinator *Coordinator) prepareRemoteShardRequests(
	ctx context.Context,
	input RunInput,
	jobID, tempRoot string,
	shards []gate.ContainerShard,
	counts remoteCIPhaseCounts,
	trace *remoteRunPerformanceTrace,
) (remoteAssets, []ShardRequest, []string, error) {
	sourceBuildSpan := trace.start("source.build", counts)
	assets, err := buildRemoteAssets(ctx, input, jobID, tempRoot, coordinator.config.SourcePrefix)
	trace.finish(sourceBuildSpan, err, counts)
	if err != nil {
		return remoteAssets{}, nil, nil, err
	}
	candidateBuildSpan := trace.start("candidate_cli.build", counts)
	assets.candidateCLI, assets.candidatePath, assets.candidateManifestPath, assets.candidateBinaryKey, err = buildRemoteCandidateCLIArtifact(ctx, coordinator.config.CandidateCLIBuilder, input, jobID, tempRoot, coordinator.config.SourcePrefix)
	trace.finish(candidateBuildSpan, err, counts)
	if err != nil {
		return remoteAssets{}, nil, nil, err
	}
	requestBuildSpan := trace.start("request.build", counts)
	requests, keys, err := buildShardRequestsWithCandidate(coordinator.config.SourcePrefix, jobID, shards, assets.artifact, assets.patchKey, assets.manifestKey, assets.manifestDigest, assets.candidateCLI, input)
	trace.finish(requestBuildSpan, err, counts)
	if err != nil {
		return remoteAssets{}, nil, nil, err
	}
	return assets, requests, keys, nil
}

// buildRemoteAssets 从精确树构建源差分及其绑定对象键。
func buildRemoteAssets(ctx context.Context, input RunInput, jobID string, tempRoot string, sourcePrefix string) (remoteAssets, error) {
	artifact, err := source.Build(ctx, input.RepositoryRoot, source.SourceSpec{BaseCommit: input.RunnerBaseCommit, BaseTree: input.RunnerBaseTree, TargetCommit: input.Commit, TargetTree: input.Tree}, tempRoot)
	if err != nil {
		return remoteAssets{}, fmt.Errorf("build remote CI source delta: %w", err)
	}
	digest, err := fileDigest(artifact.ManifestPath)
	if err != nil {
		return remoteAssets{}, err
	}
	jobPrefix := sourcePrefix + jobID + "/"
	return remoteAssets{artifact: artifact, manifestDigest: digest, patchKey: jobPrefix + artifact.Manifest.PatchSHA256 + ".patch", manifestKey: jobPrefix + digest + ".manifest.json"}, nil
}

// uploadSourceAssets 上传差分和候选 CLI，并立即纳入清理清单。
func (coordinator *Coordinator) uploadSourceAssets(ctx context.Context, assets remoteAssets, objectKeys *[]string) error {
	for _, item := range []struct{ path, key, label string }{
		{assets.artifact.PatchPath, assets.patchKey, "source delta"},
		{assets.artifact.ManifestPath, assets.manifestKey, "source manifest"},
		{assets.candidatePath, assets.candidateBinaryKey, "candidate CLI binary"},
		{assets.candidateManifestPath, assets.candidateCLI.ManifestKey, "candidate CLI manifest"},
	} {
		if err := coordinator.store.Upload(ctx, item.path, item.key); err != nil {
			return fmt.Errorf("upload remote CI %s: %w", item.label, err)
		}
		*objectKeys = append(*objectKeys, item.key)
	}
	return nil
}
