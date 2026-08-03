package remoteci

import (
	"context"
	"fmt"
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type remoteAssets struct {
	materialization SourceMaterialization
	bundleKey       string
	bundleDigest    string
	bundleSize      int64
	manifestKey     string
	manifestDigest  string
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
		resources := gate.RemoteCIShardResources{ClassID: shard.ResourceClass, CPU: shard.Resources.CPU, MemoryGiB: shard.Resources.MemoryGiB}
		shards = append(shards, gate.RemoteCIShardRecord{ShardIdentity: shard.ShardIdentity, ContainerGroup: shard.ContainerGroup, ContainerStatus: containerStatus, Workloads: append([]gate.GateID(nil), shard.ExecutedWorkloads...), MaterializationTiming: shard.MaterializationTiming, Resources: resources})
	}
	return shards
}

// remotePlanningContext 统一缓存投影与最终 LPT 使用的环境和时限身份。
func remotePlanningContext(input RunInput) gate.PlanningContext {
	return gate.PlanningContext{Platform: input.Platform, Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest, TargetDurationMS: gate.FullCITargetDurationMS}
}

// prepareRemoteAssets 构建由远端 init 与执行分片共同消费的 canonical source bundle 资产。
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

// buildRemoteAssets 从唯一 SourceSpec 构建可复核 source.bundle 及其绑定对象键。
func buildRemoteAssets(ctx context.Context, input RunInput, jobID string, tempRoot string, sourcePrefix string) (remoteAssets, error) {
	materialization, err := MaterializeSource(ctx, input.RepositoryRoot, input.Source, tempRoot)
	if err != nil {
		return remoteAssets{}, fmt.Errorf("materialize remote CI source bundle: %w", err)
	}
	bundleInfo, err := os.Stat(materialization.BundlePath)
	if err != nil {
		return remoteAssets{}, fmt.Errorf("stat remote CI source bundle: %w", err)
	}
	if !bundleInfo.Mode().IsRegular() || bundleInfo.Size() <= 0 {
		return remoteAssets{}, fmt.Errorf("remote CI source bundle is not a non-empty regular file")
	}
	manifestDigest, err := fileDigest(materialization.ManifestPath)
	if err != nil {
		return remoteAssets{}, err
	}
	jobPrefix := sourcePrefix + jobID + "/"
	bundleDigest := materialization.Manifest.BundleDigest
	if len(bundleDigest) < len("sha256:") || bundleDigest[:len("sha256:")] != "sha256:" {
		return remoteAssets{}, fmt.Errorf("remote CI source bundle manifest digest is invalid")
	}
	return remoteAssets{
		materialization: materialization,
		bundleKey:       jobPrefix + bundleDigest[len("sha256:"):] + ".bundle",
		bundleDigest:    bundleDigest[len("sha256:"):],
		bundleSize:      bundleInfo.Size(),
		manifestKey:     jobPrefix + manifestDigest + ".manifest.json",
		manifestDigest:  manifestDigest,
	}, nil
}

// uploadSourceAssets 上传候选 source bundle 和 strict manifest，并立即纳入清理清单。
func (coordinator *Coordinator) uploadSourceAssets(ctx context.Context, assets remoteAssets, objectKeys *[]string) error {
	for _, item := range []struct{ path, key, label string }{
		{assets.materialization.BundlePath, assets.bundleKey, "source bundle"},
		{assets.materialization.ManifestPath, assets.manifestKey, "source manifest"},
	} {
		if err := coordinator.store.Create(ctx, item.path, item.key); err != nil {
			return fmt.Errorf("upload remote CI %s: %w", item.label, err)
		}
		*objectKeys = append(*objectKeys, item.key)
	}
	return nil
}
