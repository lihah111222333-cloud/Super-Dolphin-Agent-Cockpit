package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	if err := validateRemoteBaselineSeedInput(input); err != nil {
		return eci.SeedRequest{}, err
	}
	resources, err := remoteBaselineSeedResources(config)
	if err != nil {
		return eci.SeedRequest{}, err
	}
	hasAccepted := accepted.Validate() == nil
	if accepted.SchemaVersion != 0 && !hasAccepted {
		return eci.SeedRequest{}, errors.New("previous baseline state is invalid; full Anchor rebuild is forbidden")
	}
	storageMode, err := remoteBaselineSeedStorageMode(hasAccepted, accepted, acceptedRecommendedSizeGiB, input, source, generation)
	if err != nil {
		return eci.SeedRequest{}, err
	}
	needsInternet := !hasAccepted
	request := eci.SeedRequest{
		ContainerGroupName: remoteBaselineResourceName(generation), ContainerName: "baseline-seed",
		Resources: resources, Command: []string{"/bin/sh"}, Args: []string{"/bootstrap/seed.sh"},
		AutoCreateEIP: needsInternet, EIPBandwidth: remoteBaselineSeedEIPBandwidth(needsInternet),
		Environment: remoteBaselineSeedEnvironment(input, source, generation),
		Tags:        map[string]string{"owner": "super-dolphin-ci", "generation": strconv.FormatUint(generation, 10)},
		Output:      remoteBaselineSeedVolume(config, remoteBaselineOutputPrefix(config, generation)),
		Input:       remoteBaselineSeedVolume(config, remoteBaselineInputPrefix(config, generation)),
		Script:      []byte(remoteBaselineSeedBootstrapScript),
	}
	resourceEnvironment, err := remoteBaselineSeedResourceEnvironment(resources)
	if err != nil {
		return eci.SeedRequest{}, err
	}
	maps.Copy(request.Environment, resourceEnvironment)
	request.Environment["BASELINE_TOOLCHAIN_CHANGED"] = strconv.FormatBool(hasAccepted && accepted.ToolchainDigest != input.Identity.ToolchainDigest)
	if hasAccepted {
		if err := bindRemoteBaselineSeedHistory(&request, config, accepted); err != nil {
			return eci.SeedRequest{}, err
		}
	}
	request.Environment["BASELINE_STORAGE_MODE"] = storageMode
	request.ClientToken, err = remoteBaselineSeedClientToken(request)
	if err != nil {
		return eci.SeedRequest{}, err
	}
	return request, nil
}

// remoteBaselineSeedDirectCacheLayers 将已验证直读层完整投影到 seed ECI 请求。
// accepted 已由调用方验证；顺序必须保留 newest-first，避免 cache proxy 以旧层遮蔽新层。
func remoteBaselineSeedDirectCacheLayers(accepted remoteci.BaselineState) []eci.DirectCacheLayer {
	if accepted.DirectCacheRef == nil {
		return nil
	}
	layers := accepted.DirectCacheRef.Layers
	result := make([]eci.DirectCacheLayer, len(layers))
	for index, layer := range layers {
		result[index] = eci.DirectCacheLayer{
			DataCacheID: layer.DataCacheID, DataCacheBucket: layer.DataCacheBucket, DataCachePath: layer.DataCachePath,
			SizeGiB: layer.SizeGiB, Generation: layer.Generation, SourceObjectPrefix: layer.SourceObjectPrefix,
			ManifestDigest: layer.ManifestDigest, TreeSHA256: layer.TreeSHA256, ParentChainSHA256: layer.ParentChainSHA256,
			RuntimeGoSHA256: layer.RuntimeGoSHA256, RuntimeDepsSHA256: layer.RuntimeDepsSHA256,
		}
	}
	return result
}

// remoteBaselineSeedDirectCacheEnvironment 为脚本声明有限且顺序敏感的 direct 层身份。
func remoteBaselineSeedDirectCacheEnvironment(layers []eci.DirectCacheLayer) map[string]string {
	environment := make(map[string]string, len(layers)+1)
	if len(layers) == 0 {
		return environment
	}
	environment["BASELINE_DIRECT_CACHE_LAYER_COUNT"] = strconv.Itoa(len(layers))
	for index, layer := range layers {
		environment[fmt.Sprintf("BASELINE_DIRECT_CACHE_LAYER_%d", index+1)] =
			strconv.FormatUint(layer.Generation, 10) + "|" + layer.ManifestDigest
	}
	return environment
}

// remoteBaselineSeedClientToken 将阿里云幂等键绑定到完整、规范化的 seed 请求。
func remoteBaselineSeedClientToken(request eci.SeedRequest) (string, error) {
	request.ClientToken = ""
	payload, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("encode baseline seed idempotence identity: %w", err)
	}
	return remoteBaselineClientToken("seed", request.Environment["BASELINE_GENERATION"], payload), nil
}

// remoteBaselineSeedNeedsInternet 仅在没有可复用依赖或不可变运行时身份变化时申请临时 EIP。
func remoteBaselineSeedNeedsInternet(accepted remoteci.BaselineState, _ remoteBaselineRefreshInput) bool {
	return accepted.SchemaVersion == 0
}

// remoteBaselineForcesRuntimeRefresh 禁止历史依赖合同复用当前 seed 的完整 runtime 层。
func remoteBaselineForcesRuntimeRefresh(input remoteBaselineRefreshInput) bool {
	return input.RuntimeDependencySchemaVersion != remoteci.RuntimeDependencySchemaVersion
}

// remoteBaselineSeedSourceExtendsAccepted 验证 Delta source manifest 的连续提交与树身份。
func remoteBaselineSeedSourceExtendsAccepted(manifest remoteBaselineSourceManifest, accepted remoteci.BaselineState, identity remoteci.BaselineIdentity) bool {
	return manifest.BaseCommit == accepted.MainCommit && manifest.BaseTree == accepted.MainTree &&
		manifest.TargetCommit == identity.MainCommit && manifest.TargetTree == identity.MainTree
}

// remoteBaselineSeedIdentityMatches 验证 Delta 沿用 Anchor 所需的平台和基础镜像身份；工具链由 runtime-go Delta 更新。
func remoteBaselineSeedIdentityMatches(accepted remoteci.BaselineState, identity remoteci.BaselineIdentity) bool {
	return accepted.Platform == identity.Platform && accepted.RuntimeImage == identity.RuntimeImage
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
		"BASELINE_MANIFEST_SCHEMA_VERSION":         strconv.FormatUint(uint64(remoteci.BaselineManifestSchemaVersion), 10),
		"BASELINE_MANIFEST_MIN_COMPATIBLE_VERSION": strconv.FormatUint(uint64(remoteci.BaselineManifestMinimumCompatibleVersion), 10),
		"BASELINE_GENERATION":                      strconv.FormatUint(generation, 10), "BASELINE_MAIN_COMMIT": input.Identity.MainCommit,
		"BASELINE_MAIN_TREE": input.Identity.MainTree, "BASELINE_PLATFORM": input.Identity.Platform,
		"BASELINE_POLICY_DIGEST": input.Identity.PolicyDigest, "BASELINE_TOOLCHAIN_DIGEST": input.Identity.ToolchainDigest,
		"BASELINE_GATE_SOURCE_SHA256": input.GateSourceDigest,
		"BASELINE_GO_TOOLCHAIN":       input.GoToolchain,
		"BASELINE_RUNTIME_IMAGE":      input.Identity.RuntimeImage, "BASELINE_SOURCE_MODE": string(source.Manifest.Mode),
		"BASELINE_SOURCE_BASE_COMMIT": source.Manifest.BaseCommit, "BASELINE_SOURCE_BASE_TREE": source.Manifest.BaseTree,
		"BASELINE_SOURCE_BUNDLE_SHA256":               source.Manifest.BundleSHA256,
		"BASELINE_SOURCE_BUNDLE_SIZE":                 strconv.FormatInt(source.Manifest.BundleSize, 10),
		"BASELINE_SOURCE_MANIFEST_SHA256":             source.ManifestSHA256,
		"BASELINE_SEED_SCRIPT_SHA256":                 digestBytes([]byte(remoteBaselineSeedScript)),
		"BASELINE_SEED_SCRIPT_SIZE":                   strconv.Itoa(len(remoteBaselineSeedScript)),
		"BASELINE_FORCE_RUNTIME_REFRESH":              strconv.FormatBool(remoteBaselineForcesRuntimeRefresh(input)),
		"BASELINE_RUNTIME_DEPENDENCY_DIGEST":          input.RuntimeDependencyDigest,
		"BASELINE_ACCEPTED_RUNTIME_DEPENDENCY_DIGEST": input.AcceptedRuntimeDependencyDigest,
		"BASELINE_STORAGE_MODE":                       remoteci.BaselineStorageModeAnchor,
		"BASELINE_SQRUFF_SHA256":                      input.SqruffSHA256,
	}
}

// remoteBaselineSeedVolume 构造受限的 OSS 挂载描述。
func remoteBaselineSeedVolume(config remoteRunConfig, prefix string) eci.OSSVolume {
	return eci.OSSVolume{Bucket: config.OSS.Bucket, Endpoint: strings.TrimPrefix(config.OSS.InternalEndpoint, "https://"),
		Path: "/" + strings.TrimSuffix(prefix, "/"), RoleName: config.WorkerRoleName}
}

type remoteBaselineSeedRuntime interface {
	DescribeContainerGroups(context.Context, ...string) ([]eci.ContainerGroup, error)
	DescribeContainerLog(context.Context, string, string) (string, error)
}

// waitRemoteBaselineSeed 轮询 seed ECI、转发运行中进度并校验完成标记。
func waitRemoteBaselineSeed(ctx context.Context, runtime remoteBaselineSeedRuntime, groupID, containerName string, generation uint64, identity remoteci.BaselineIdentity) error {
	return waitRemoteBaselineSeedWithWriter(ctx, runtime, groupID, containerName, generation, identity, os.Stderr)
}

func waitRemoteBaselineSeedWithWriter(
	ctx context.Context,
	runtime remoteBaselineSeedRuntime,
	groupID, containerName string,
	generation uint64,
	identity remoteci.BaselineIdentity,
	stderr io.Writer,
) error {
	return waitRemoteBaselineSeedWithWriterAndInterval(ctx, runtime, groupID, containerName, generation, identity, stderr, remoteBaselinePollInterval)
}

func waitRemoteBaselineSeedWithWriterAndInterval(
	ctx context.Context,
	runtime remoteBaselineSeedRuntime,
	groupID, containerName string,
	generation uint64,
	identity remoteci.BaselineIdentity,
	stderr io.Writer,
	pollInterval time.Duration,
) error {
	timer := time.NewTicker(pollInterval)
	defer timer.Stop()
	forwarded := make(map[string]struct{})
	for {
		groups, err := runtime.DescribeContainerGroups(ctx, groupID)
		if err != nil {
			return err
		}
		if len(groups) != 1 || groups[0].ID != groupID {
			return errors.New("baseline seed ECI identity is missing")
		}
		if groups[0].Status == "Running" {
			log, err := runtime.DescribeContainerLog(ctx, groupID, containerName)
			if err != nil {
				return fmt.Errorf("read running baseline seed log: %w", err)
			}
			if err := forwardRemoteBaselineSeedLiveLog(stderr, log, forwarded); err != nil {
				return fmt.Errorf("forward running baseline seed log: %w", err)
			}
		}
		if err := validateRemoteBaselineSeedStatus(ctx, runtime, groupID, containerName, generation, identity, groups[0]); err != nil {
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

func forwardRemoteBaselineSeedLiveLog(stderr io.Writer, log string, forwarded map[string]struct{}) error {
	for line := range strings.SplitSeq(log, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !isRemoteBaselineSeedLiveProgressLine(line) {
			continue
		}
		if _, alreadyForwarded := forwarded[line]; alreadyForwarded {
			continue
		}
		if _, err := fmt.Fprintln(stderr, line); err != nil {
			return err
		}
		forwarded[line] = struct{}{}
	}
	return nil
}

func isRemoteBaselineSeedLiveProgressLine(line string) bool {
	for _, prefix := range []string{
		"seed stage ",
		"seed progress: ",
		"go cache compile ",
		"go build cache ",
		"go module cache ",
		"runtime dependency cache ",
		"[baseline-seed] ",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// validateRemoteBaselineSeedStatus 校验 seed 的终态日志与成功标记。
func validateRemoteBaselineSeedStatus(ctx context.Context, runtime remoteBaselineSeedRuntime, groupID, containerName string, generation uint64, identity remoteci.BaselineIdentity, group eci.ContainerGroup) error {
	if group.Status == "Succeeded" {
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
	if group.Status != "Failed" {
		return nil
	}
	log, err := runtime.DescribeContainerLog(ctx, groupID, containerName)
	if err != nil {
		return errors.Join(errors.New("baseline seed ECI failed"), err)
	}
	return fmt.Errorf("baseline seed ECI failed: %s; %s", remoteBaselineSeedFailureEvidence(group, containerName), strings.TrimSpace(log))
}

// remoteBaselineSeedFailureEvidence 保留 seed 容器退出码和平台事件，避免成功校验行掩盖真实失败。
func remoteBaselineSeedFailureEvidence(group eci.ContainerGroup, containerName string) string {
	for _, container := range group.Containers {
		if container.Name == containerName {
			exitCode := "missing"
			if container.CurrentState.ExitCode != nil {
				exitCode = strconv.FormatInt(*container.CurrentState.ExitCode, 10)
			}
			return fmt.Sprintf("container=%s state=%s exit_code=%s reason=%q message=%q", container.Name, container.CurrentState.State, exitCode, container.CurrentState.Reason, container.CurrentState.Message)
		}
	}
	if len(group.Events) != 0 {
		event := group.Events[len(group.Events)-1]
		return fmt.Sprintf("event_type=%q reason=%q message=%q count=%d", event.Type, event.Reason, event.Message, event.Count)
	}
	return "terminal container evidence is missing"
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
