package remoteci

import (
	"context"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci/source"
)

type remoteAssets struct {
	artifact       source.Artifact
	manifestDigest string
	patchKey       string
	manifestKey    string
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
		resources := gate.RemoteCIShardResources{}
		if shard.ResourceClass != "" {
			resources = gate.RemoteCIShardResources{ClassID: shard.ResourceClass, CPU: shard.Resources.CPU, MemoryGiB: shard.Resources.MemoryGiB}
		}
		shards = append(shards, gate.RemoteCIShardRecord{ShardIdentity: shard.ShardIdentity, ContainerGroup: shard.ContainerGroup, ContainerStatus: containerStatus, Workloads: append([]gate.GateID(nil), shard.ExecutedWorkloads...), MaterializationTiming: shard.MaterializationTiming, Resources: resources})
	}
	return shards
}

// remotePlanningContext 统一缓存投影与最终 LPT 使用的环境和时限身份。
func remotePlanningContext(input RunInput) gate.PlanningContext {
	return gate.PlanningContext{Platform: input.Platform, Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest, TargetDurationMS: gate.FullCITargetDurationMS}
}

// prepareRemoteAssets 构建将由远端 builder 与执行分片共同消费的源和候选 CLI 资产。
func (coordinator *Coordinator) prepareRemoteAssets(
	ctx context.Context,
	input RunInput,
	jobID, tempRoot string,
) (remoteAssets, error) {
	assets, err := buildRemoteAssets(ctx, input, jobID, tempRoot, coordinator.config.SourcePrefix)
	if err != nil {
		return remoteAssets{}, err
	}
	return assets, nil
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

// uploadSourceAssets 上传候选源码差分，并立即纳入清理清单。
func (coordinator *Coordinator) uploadSourceAssets(ctx context.Context, assets remoteAssets, objectKeys *[]string) error {
	for _, item := range []struct{ path, key, label string }{
		{assets.artifact.PatchPath, assets.patchKey, "source delta"},
		{assets.artifact.ManifestPath, assets.manifestKey, "source manifest"},
	} {
		if err := coordinator.store.Create(ctx, item.path, item.key); err != nil {
			return fmt.Errorf("upload remote CI %s: %w", item.label, err)
		}
		*objectKeys = append(*objectKeys, item.key)
	}
	return nil
}
