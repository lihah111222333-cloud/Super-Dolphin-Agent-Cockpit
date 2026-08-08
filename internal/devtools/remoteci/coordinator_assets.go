package remoteci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

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
		shards = append(shards, gate.RemoteCIShardRecord{
			ShardIdentity: shard.ShardIdentity, ContainerGroup: shard.ContainerGroup, ContainerStatus: containerStatus,
			Workloads: append([]gate.GateID(nil), shard.ExecutedWorkloads...), MaterializationTiming: shard.MaterializationTiming,
			Resources: resources, TerminalEvidence: shard.TerminalEvidence.Clone(),
		})
	}
	return shards
}

// remotePlanningContext 统一缓存投影与最终 LPT 使用的环境和时限身份。
func remotePlanningContext(input RunInput) gate.PlanningContext {
	context := gate.PlanningContext{Platform: input.Platform, Runner: input.RunnerIdentityDigest, Toolchain: input.ToolchainDigest, Calibration: input.Calibration, TargetDurationMS: gate.FullCITargetDurationMS, AcceptedSnapshotID: input.ImageCacheSnapshotID}
	if input.Calibration {
		context.CalibrationResourceClassID = input.CalibrationResource.ID
		context.CalibrationResourceCPU = input.CalibrationResource.VCPU
		context.CalibrationResourceMemoryGiB = input.CalibrationResource.MemoryGiB
	}
	return context
}

// buildRemoteAssets 从唯一 SourceSpec 构建可复核 source.bundle 及其绑定对象键。
func buildRemoteAssets(ctx context.Context, input RunInput, jobID string, tempRoot string, sourcePrefix string) (remoteAssets, error) {
	baselineRoot, artifactRoot, err := prepareRemoteSourceRoots(tempRoot)
	if err != nil {
		return remoteAssets{}, err
	}
	materialization, err := materializeRemoteSource(ctx, input, baselineRoot, artifactRoot)
	if err != nil {
		return remoteAssets{}, err
	}
	bundleInfo, err := inspectRemoteSourceArtifacts(materialization)
	if err != nil {
		return remoteAssets{}, err
	}
	return finalizeRemoteAssets(materialization, bundleInfo, sourcePrefix, jobID)
}

// prepareRemoteSourceRoots 校验并创建 source baseline 与产物目录。
func prepareRemoteSourceRoots(tempRoot string) (string, string, error) {
	if err := validateCanonicalDirectory(tempRoot, true); err != nil {
		return "", "", fmt.Errorf("validate remote CI source staging root: %w", err)
	}
	baselineRoot := filepath.Join(tempRoot, "source-baseline.git")
	artifactRoot := filepath.Join(tempRoot, "source-artifacts")
	for _, root := range []string{baselineRoot, artifactRoot} {
		if err := os.Mkdir(root, privateSourceDirMode); err != nil {
			return "", "", fmt.Errorf("create remote CI source asset root %s: %w", filepath.Base(root), err)
		}
	}
	return baselineRoot, artifactRoot, nil
}

// materializeRemoteSource 构建、复核并清理本地 accepted baseline。
func materializeRemoteSource(ctx context.Context, input RunInput, baselineRoot string, artifactRoot string) (SourceMaterialization, error) {
	baseline, err := BuildSourceBaseline(ctx, input.RepositoryRoot, input.RunnerBaseTree, baselineRoot, input.Source.ObjectFormat)
	if err != nil {
		return SourceMaterialization{}, errors.Join(fmt.Errorf("build remote CI accepted source baseline: %w", err), cleanupRemoteSourceBaseline(baselineRoot))
	}
	materialization, err := MaterializeSource(ctx, input.RepositoryRoot, input.Source, artifactRoot, baseline)
	if err != nil {
		return SourceMaterialization{}, errors.Join(fmt.Errorf("materialize remote CI source bundle: %w", err), cleanupRemoteSourceBaseline(baselineRoot))
	}
	if err := validateRemoteSourceMaterialization(input, baseline, materialization); err != nil {
		return SourceMaterialization{}, errors.Join(err, cleanupRemoteSourceBaseline(baselineRoot))
	}
	if err := cleanupRemoteSourceBaseline(baselineRoot); err != nil {
		return SourceMaterialization{}, fmt.Errorf("cleanup local remote CI source baseline: %w", err)
	}
	return materialization, nil
}

// inspectRemoteSourceArtifacts 确认 bundle 与 manifest 都是非空普通文件。
func inspectRemoteSourceArtifacts(materialization SourceMaterialization) (os.FileInfo, error) {
	bundleInfo, err := os.Stat(materialization.BundlePath)
	if err != nil {
		return nil, fmt.Errorf("stat remote CI source bundle: %w", err)
	}
	manifestInfo, err := os.Stat(materialization.ManifestPath)
	if err != nil {
		return nil, fmt.Errorf("stat remote CI source manifest: %w", err)
	}
	if !bundleInfo.Mode().IsRegular() || bundleInfo.Size() <= 0 {
		return nil, fmt.Errorf("remote CI source bundle is not a non-empty regular file")
	}
	if !manifestInfo.Mode().IsRegular() || manifestInfo.Size() <= 0 {
		return nil, fmt.Errorf("remote CI source manifest is not a non-empty regular file")
	}
	return bundleInfo, nil
}

// finalizeRemoteAssets 计算内容摘要并构造按 job 隔离的 OSS 对象键。
func finalizeRemoteAssets(materialization SourceMaterialization, bundleInfo os.FileInfo, sourcePrefix string, jobID string) (remoteAssets, error) {
	manifestDigest, err := fileDigest(materialization.ManifestPath)
	if err != nil {
		return remoteAssets{}, fmt.Errorf("digest remote CI source manifest: %w", err)
	}
	bundleDigest := materialization.Manifest.BundleDigest
	if len(bundleDigest) < len("sha256:") || bundleDigest[:len("sha256:")] != "sha256:" {
		return remoteAssets{}, fmt.Errorf("remote CI source bundle manifest digest is invalid")
	}
	jobPrefix := sourcePrefix + jobID + "/"
	return remoteAssets{
		materialization: materialization,
		bundleKey:       jobPrefix + bundleDigest[len("sha256:"):] + ".bundle",
		bundleDigest:    bundleDigest[len("sha256:"):],
		bundleSize:      bundleInfo.Size(),
		manifestKey:     jobPrefix + manifestDigest + ".manifest.json",
		manifestDigest:  manifestDigest,
	}, nil
}

// validateRemoteSourceMaterialization 在返回上传键前绑定候选与 accepted baseline 身份。
func validateRemoteSourceMaterialization(input RunInput, baseline SourceBaseline, materialization SourceMaterialization) error {
	manifest := materialization.Manifest
	if err := validateRemoteSourceManifestInput(input, baseline, manifest); err != nil {
		return err
	}
	if err := validateRemoteSourceCommits(input, baseline, manifest); err != nil {
		return err
	}
	if materialization.BundlePath == "" || materialization.ManifestPath == "" {
		return fmt.Errorf("remote CI source materialization must return both bundle and manifest paths")
	}
	return nil
}

// validateRemoteSourceManifestInput 校验 manifest 与本次输入及 accepted baseline 一致。
func validateRemoteSourceManifestInput(input RunInput, baseline SourceBaseline, manifest SourceMaterializationManifest) error {
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("validate remote CI source manifest: %w", err)
	}
	if !reflect.DeepEqual(manifest.Source, input.Source) {
		return fmt.Errorf("remote CI source manifest source drifted from input")
	}
	if manifest.SourceTreeSHA != input.Tree || manifest.SourceTreeSHA != input.Source.SourceTreeSHA {
		return fmt.Errorf("remote CI source manifest tree %q does not match input tree %q", manifest.SourceTreeSHA, input.Tree)
	}
	if manifest.ObjectFormat != input.Source.ObjectFormat || baseline.ObjectFormat != input.Source.ObjectFormat {
		return fmt.Errorf("remote CI source manifest object format drifted from input")
	}
	if manifest.BaselineTreeSHA != input.RunnerBaseTree || manifest.BaselineTreeSHA != baseline.TreeSHA {
		return fmt.Errorf("remote CI source manifest baseline tree %q does not match input runner base tree %q", manifest.BaselineTreeSHA, input.RunnerBaseTree)
	}
	return nil
}

// validateRemoteSourceCommits 校验 deterministic baseline 与 transport commit 身份。
func validateRemoteSourceCommits(input RunInput, baseline SourceBaseline, manifest SourceMaterializationManifest) error {
	expectedBaselineCommit, err := DeterministicSourceBaselineCommitSHA(input.RunnerBaseTree, input.Source.ObjectFormat)
	if err != nil {
		return fmt.Errorf("derive deterministic remote CI source baseline commit: %w", err)
	}
	if baseline.CommitSHA != expectedBaselineCommit || manifest.BaselineCommitSHA != expectedBaselineCommit {
		return fmt.Errorf("remote CI source manifest baseline commit %q does not match deterministic input baseline %q", manifest.BaselineCommitSHA, expectedBaselineCommit)
	}
	if !validOID(manifest.SyntheticBaseTreeSHA, input.Source.ObjectFormat) || !validOID(manifest.SyntheticBaseCommitSHA, input.Source.ObjectFormat) {
		return errors.New("remote CI source manifest synthetic base identity is invalid")
	}
	expectedSyntheticBaseCommit, err := DeterministicSourceSyntheticBaseCommitSHA(manifest.SyntheticBaseTreeSHA, expectedBaselineCommit, input.Source.ObjectFormat)
	if err != nil {
		return fmt.Errorf("derive deterministic remote CI source synthetic base commit: %w", err)
	}
	if manifest.SyntheticBaseCommitSHA != expectedSyntheticBaseCommit {
		return fmt.Errorf("remote CI source manifest synthetic base commit %q does not match deterministic input synthetic base %q", manifest.SyntheticBaseCommitSHA, expectedSyntheticBaseCommit)
	}
	expectedTransportCommit, err := DeterministicSourceTransportCommitSHA(input.Source.SourceTreeSHA, expectedSyntheticBaseCommit, input.Source.ObjectFormat)
	if err != nil {
		return fmt.Errorf("derive deterministic remote CI source transport commit: %w", err)
	}
	if manifest.TransportCommitSHA != expectedTransportCommit {
		return fmt.Errorf("remote CI source manifest transport commit %q does not match deterministic input transport %q", manifest.TransportCommitSHA, expectedTransportCommit)
	}
	return nil
}

// cleanupRemoteSourceBaseline 清理 MaterializeSource 完成复核后的本地只读 baseline 副本。
func cleanupRemoteSourceBaseline(root string) error {
	if _, err := os.Lstat(root); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("lstat local source baseline: %w", err)
	}
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("local source baseline contains unsupported symlink %q", path)
		}
		if entry.IsDir() {
			return os.Chmod(path, privateSourceDirMode)
		}
		return os.Chmod(path, 0o600)
	})
	return errors.Join(walkErr, os.RemoveAll(root))
}

// uploadSourceAssets 上传候选 source bundle 和 strict manifest，并立即纳入清理清单。
func (coordinator *Coordinator) uploadSourceAssets(ctx context.Context, assets remoteAssets, objectKeys *[]string) error {
	for _, item := range []struct{ path, key, label string }{
		{assets.materialization.BundlePath, assets.bundleKey, "source bundle"},
		{assets.materialization.ManifestPath, assets.manifestKey, "source manifest"},
	} {
		// Create 是 create-only，但 ACK/409 可能在服务端已经落对象后才返回；
		// 先登记键，调用失败也必须让 job-prefix cleanup 覆盖这个副作用。
		*objectKeys = append(*objectKeys, item.key)
		if err := coordinator.store.Create(ctx, item.path, item.key); err != nil {
			return fmt.Errorf("upload remote CI %s: %w", item.label, err)
		}
	}
	return nil
}
