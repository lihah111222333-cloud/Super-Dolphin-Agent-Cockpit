package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// buildRemoteBaselineSeedRequest 构造固定 generation 的 ECI seed 请求。
func buildRemoteBaselineSeedRequest(
	config remoteRunConfig,
	input remoteBaselineRefreshInput,
	source remoteBaselineSourceArtifact,
	accepted remoteci.BaselineState,
	acceptedRecommendedSizeGiB int,
	generation uint64,
) (eci.SeedRequest, error) {
	if !validRemoteManifestDigest(input.GateSourceDigest) {
		return eci.SeedRequest{}, errors.New("baseline gate source digest is invalid")
	}
	resources, err := remoteBaselineSeedResources(config)
	if err != nil {
		return eci.SeedRequest{}, err
	}
	hasAccepted := accepted.Validate() == nil
	appendDelta := remoteBaselineCapacityMatches(accepted, acceptedRecommendedSizeGiB) &&
		remoteBaselineSeedCanAppendDelta(accepted, input, source, generation)
	needsInternet := !hasAccepted || remoteBaselineSeedNeedsInternet(accepted, input)
	request := eci.SeedRequest{
		ContainerGroupName: remoteBaselineResourceName(generation), ContainerName: "baseline-seed",
		ClientToken: remoteBaselineClientToken(generation, input.Identity.MainTree),
		Resources:   resources, Command: []string{"/bin/sh"}, Args: []string{"/bootstrap/seed.sh"},
		AutoCreateEIP: needsInternet, EIPBandwidth: remoteBaselineSeedEIPBandwidth(needsInternet),
		Environment: remoteBaselineSeedEnvironment(input, source, generation),
		Tags:        map[string]string{"owner": "super-dolphin-ci", "generation": strconv.FormatUint(generation, 10)},
		Output:      remoteBaselineSeedVolume(config, remoteBaselineOutputPrefix(config, generation)),
		Input:       remoteBaselineSeedVolume(config, remoteBaselineInputPrefix(config, generation)),
		Script:      []byte(remoteBaselineSeedBootstrapScript),
	}
	if hasAccepted {
		anchor := accepted.CurrentAnchorRef()
		request.DataCacheBucket = anchor.DataCacheBucket
		request.PreviousDataCachePath = anchor.DataCachePath
		request.Environment["BASELINE_ANCHOR_MANIFEST_DIGEST"] = anchor.ManifestDigest
		deltas := accepted.DeltaRefs()
		if len(deltas) > remoteBaselineDeltaLimit {
			return eci.SeedRequest{}, fmt.Errorf("accepted baseline has more than %d Delta layers", remoteBaselineDeltaLimit)
		}
		if len(deltas) != 0 {
			request.BaselineLayers = remoteBaselineSeedVolume(config, strings.TrimSuffix(config.OSS.BaselinePrefix, "/"))
		}
		maps.Copy(request.Environment, remoteBaselineSeedDeltaEnvironment(deltas))
	}
	if appendDelta {
		request.Environment["BASELINE_STORAGE_MODE"] = remoteci.BaselineStorageModeDelta
	} else {
		request.Environment["BASELINE_STORAGE_MODE"] = remoteci.BaselineStorageModeAnchor
	}
	return request, nil
}

// remoteBaselineSeedNeedsInternet 仅在没有可复用依赖或不可变运行时身份变化时申请临时 EIP。
func remoteBaselineSeedNeedsInternet(accepted remoteci.BaselineState, input remoteBaselineRefreshInput) bool {
	return accepted.SchemaVersion == 0 || accepted.Platform != input.Identity.Platform ||
		accepted.ToolchainDigest != input.Identity.ToolchainDigest || accepted.RuntimeImage != input.Identity.RuntimeImage ||
		input.RuntimeDependencyDigest == "" || input.AcceptedRuntimeDependencyDigest == "" ||
		input.RuntimeDependencyDigest != input.AcceptedRuntimeDependencyDigest
}

// remoteBaselineSeedCanAppendDelta 只允许经过连续后代验证的兼容 Anchor 追加有限 Delta。
func remoteBaselineSeedCanAppendDelta(accepted remoteci.BaselineState, input remoteBaselineRefreshInput, source remoteBaselineSourceArtifact, generation uint64) bool {
	identity := input.Identity
	if !remoteBaselineSeedHasAppendableDelta(accepted, input, source, generation) {
		return false
	}
	return remoteBaselineSeedIdentityMatches(accepted, identity)
}

// remoteBaselineSeedHasAppendableDelta 验证 Delta 追加所需的状态、源工件和依赖连续性。
func remoteBaselineSeedHasAppendableDelta(accepted remoteci.BaselineState, input remoteBaselineRefreshInput, source remoteBaselineSourceArtifact, generation uint64) bool {
	if accepted.Validate() != nil || source.Manifest.Mode != remoteBaselineSourceDelta {
		return false
	}
	if generation <= accepted.Generation || len(accepted.DeltaRefs()) >= remoteBaselineDeltaLimit {
		return false
	}
	if !remoteBaselineSeedSourceExtendsAccepted(source.Manifest, accepted, input.Identity) {
		return false
	}
	return input.RuntimeDependencyDigest != "" && input.AcceptedRuntimeDependencyDigest != "" &&
		input.RuntimeDependencyDigest == input.AcceptedRuntimeDependencyDigest
}

// remoteBaselineSeedSourceExtendsAccepted 验证 Delta source manifest 的连续提交与树身份。
func remoteBaselineSeedSourceExtendsAccepted(manifest remoteBaselineSourceManifest, accepted remoteci.BaselineState, identity remoteci.BaselineIdentity) bool {
	return manifest.BaseCommit == accepted.MainCommit && manifest.BaseTree == accepted.MainTree &&
		manifest.TargetCommit == identity.MainCommit && manifest.TargetTree == identity.MainTree
}

// remoteBaselineSeedIdentityMatches 验证 Delta 沿用 Anchor 所需的平台、工具链和镜像身份。
func remoteBaselineSeedIdentityMatches(accepted remoteci.BaselineState, identity remoteci.BaselineIdentity) bool {
	return accepted.Platform == identity.Platform && accepted.ToolchainDigest == identity.ToolchainDigest &&
		accepted.RuntimeImage == identity.RuntimeImage
}

// remoteBaselineSeedDeltaEnvironment 把有限 Delta 链分散到独立环境槽，避免单值长度随链增长。
func remoteBaselineSeedDeltaEnvironment(deltas []remoteci.BaselineDeltaRef) map[string]string {
	environment := make(map[string]string, len(deltas))
	for index, delta := range deltas {
		name := fmt.Sprintf("BASELINE_DELTA_MANIFEST_%d", index+1)
		environment[name] = strconv.FormatUint(delta.Generation, 10) + "@" + delta.ManifestDigest
	}
	return environment
}

// remoteBaselineSeedEIPBandwidth 返回满足外部依赖下载的最小带宽。
func remoteBaselineSeedEIPBandwidth(needsInternet bool) int {
	if needsInternet {
		return 100
	}
	return 0
}

// remoteBaselineSeedEnvironment 生成脚本验证所需的全部不可变输入。
func remoteBaselineSeedEnvironment(input remoteBaselineRefreshInput, source remoteBaselineSourceArtifact, generation uint64) map[string]string {
	return map[string]string{
		"BASELINE_MANIFEST_SCHEMA_VERSION": strconv.FormatUint(uint64(remoteci.BaselineManifestSchemaVersion), 10),
		"BASELINE_GENERATION":              strconv.FormatUint(generation, 10), "BASELINE_MAIN_COMMIT": input.Identity.MainCommit,
		"BASELINE_MAIN_TREE": input.Identity.MainTree, "BASELINE_PLATFORM": input.Identity.Platform,
		"BASELINE_POLICY_DIGEST": input.Identity.PolicyDigest, "BASELINE_TOOLCHAIN_DIGEST": input.Identity.ToolchainDigest,
		"BASELINE_GATE_SOURCE_SHA256": input.GateSourceDigest,
		"BASELINE_RUNTIME_IMAGE":      input.Identity.RuntimeImage, "BASELINE_SOURCE_MODE": string(source.Manifest.Mode),
		"BASELINE_SOURCE_BASE_COMMIT": source.Manifest.BaseCommit, "BASELINE_SOURCE_BASE_TREE": source.Manifest.BaseTree,
		"BASELINE_SOURCE_BUNDLE_SHA256":   source.Manifest.BundleSHA256,
		"BASELINE_SOURCE_BUNDLE_SIZE":     strconv.FormatInt(source.Manifest.BundleSize, 10),
		"BASELINE_SOURCE_MANIFEST_SHA256": source.ManifestSHA256,
		"BASELINE_SEED_SCRIPT_SHA256":     digestBytes([]byte(remoteBaselineSeedScript)),
		"BASELINE_SEED_SCRIPT_SIZE":       strconv.Itoa(len(remoteBaselineSeedScript)),
		"BASELINE_STORAGE_MODE":           remoteci.BaselineStorageModeAnchor,
		"BASELINE_SQRUFF_SHA256":          input.SqruffSHA256,
	}
}

// remoteBaselineSeedVolume 构造受限的 OSS 挂载描述。
func remoteBaselineSeedVolume(config remoteRunConfig, prefix string) eci.OSSVolume {
	return eci.OSSVolume{Bucket: config.OSS.Bucket, Endpoint: strings.TrimPrefix(config.OSS.InternalEndpoint, "https://"),
		Path: "/" + strings.TrimSuffix(prefix, "/"), RoleName: config.WorkerRoleName}
}

// waitRemoteBaselineSeed 轮询 seed ECI 并校验完成标记。
func waitRemoteBaselineSeed(ctx context.Context, runtime *eci.Client, groupID, containerName string, generation uint64, identity remoteci.BaselineIdentity) error {
	timer := time.NewTicker(remoteBaselinePollInterval)
	defer timer.Stop()
	for {
		groups, err := runtime.DescribeContainerGroups(ctx, groupID)
		if err != nil {
			return err
		}
		if len(groups) != 1 || groups[0].ID != groupID {
			return errors.New("baseline seed ECI identity is missing")
		}
		if err := validateRemoteBaselineSeedStatus(ctx, runtime, groupID, containerName, generation, identity, groups[0].Status); err != nil {
			return err
		}
		if groups[0].Status == "Succeeded" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// validateRemoteBaselineSeedStatus 校验 seed 的终态日志与成功标记。
func validateRemoteBaselineSeedStatus(ctx context.Context, runtime *eci.Client, groupID, containerName string, generation uint64, identity remoteci.BaselineIdentity, status string) error {
	if status == "Succeeded" {
		log, err := runtime.DescribeContainerLog(ctx, groupID, containerName)
		if err != nil {
			return err
		}
		marker := fmt.Sprintf("SUPER_DOLPHIN_BASELINE_READY generation=%d commit=%s tree=%s", generation, identity.MainCommit, identity.MainTree)
		if !strings.Contains(log, marker) {
			return errors.New("baseline seed completion marker is missing")
		}
		return nil
	}
	if status != "Failed" {
		return nil
	}
	log, err := runtime.DescribeContainerLog(ctx, groupID, containerName)
	if err != nil {
		return errors.Join(errors.New("baseline seed ECI failed"), err)
	}
	return fmt.Errorf("baseline seed ECI failed: %s", strings.TrimSpace(log))
}

// downloadRemoteBaselineManifest 下载并校验新 generation 的 manifest。
func downloadRemoteBaselineManifest(ctx context.Context, config remoteRunConfig, generation uint64, identity remoteci.BaselineIdentity, gateSourceDigest string) (remoteci.BaselineManifest, string, error) {
	store, err := newRemoteBaselineOSSStore(config)
	if err != nil {
		return remoteci.BaselineManifest{}, "", err
	}
	tempRoot, err := os.MkdirTemp("", "super-dolphin-baseline-manifest-*")
	if err != nil {
		return remoteci.BaselineManifest{}, "", err
	}
	defer os.RemoveAll(tempRoot)
	path := filepath.Join(tempRoot, "baseline-manifest.json")
	if err := store.Download(ctx, remoteBaselineOutputPrefix(config, generation)+"baseline-manifest.json", path); err != nil {
		return remoteci.BaselineManifest{}, "", err
	}
	data, err := readRemoteBaselineManifestBytes(path)
	if err != nil {
		return remoteci.BaselineManifest{}, "", err
	}
	manifest, err := remoteci.DecodeBaselineManifest(data)
	if err != nil {
		return remoteci.BaselineManifest{}, "", err
	}
	if !manifest.Matches(generation, identity) || manifest.GateSourceSHA256 != gateSourceDigest {
		return remoteci.BaselineManifest{}, "", errors.New("remote baseline manifest does not match refresh input")
	}
	return manifest, remoteci.BaselineManifestDigest(data), nil
}

// readRemoteBaselineManifestBytes 读取受大小限制的 manifest 文件。
func readRemoteBaselineManifestBytes(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() <= 0 || info.Size() > remoteBaselineManifestMaxBytes {
		return nil, errors.New("remote baseline manifest size is invalid")
	}
	return os.ReadFile(path)
}

// remoteBaselinePolicyDigest 将注册表、seed 脚本与运行时依赖闭包绑定为基线策略摘要。
func remoteBaselinePolicyDigest(registryDigest string, runtimeDependencyDigest string) string {
	return "sha256:" + digestBytes([]byte(registryDigest+"\n"+runtimeDependencyDigest+"\n"+remoteBaselineSeedBootstrapScript+"\n"+remoteBaselineSeedScript+"\n"))
}
