package main

import (
	"errors"
	"fmt"
	goversion "go/version"
	"log/slog"
	"maps"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// validateRemoteBaselineSeedInput 验证 seed 构造前不可缺失的候选身份。
func validateRemoteBaselineSeedInput(input remoteBaselineRefreshInput) error {
	if !validRemoteManifestDigest(input.GateSourceDigest) {
		return errors.New("baseline gate source digest is invalid")
	}
	if !goversion.IsValid(input.GoToolchain) {
		return errors.New("baseline Go toolchain is invalid")
	}
	if !remoteci.SupportsBaselineRuntimeDependencySchema(input.RuntimeDependencySchemaVersion) {
		return errors.New("baseline runtime dependency schema is invalid")
	}
	return nil
}

// remoteBaselineSeedResourceEnvironment 生成受整型资源约束的 Go 构建参数。
func remoteBaselineSeedResourceEnvironment(resources eci.Resources) (map[string]string, error) {
	seedCPU := int(resources.CPU)
	seedMemoryGiB := int(resources.MemoryGiB)
	if seedCPU < 1 || float64(seedCPU) != resources.CPU || seedMemoryGiB < 4 || float64(seedMemoryGiB) != resources.MemoryGiB {
		return nil, errors.New("baseline seed resources must use whole CPU and GiB values")
	}
	return map[string]string{
		"BASELINE_SEED_GO_PARALLELISM":  strconv.Itoa(seedCPU),
		"BASELINE_SEED_GO_MEMORY_LIMIT": strconv.Itoa(seedMemoryGiB*3/4) + "GiB",
	}, nil
}

// bindRemoteBaselineSeedHistory 绑定压实或追加所需的完整已接受层身份。
func bindRemoteBaselineSeedHistory(request *eci.SeedRequest, config remoteRunConfig, accepted remoteci.BaselineState) error {
	anchor := accepted.CurrentAnchorRef()
	request.DataCacheBucket = anchor.DataCacheBucket
	request.PreviousDataCachePath = anchor.DataCachePath
	request.Environment["BASELINE_ANCHOR_MANIFEST_DIGEST"] = anchor.ManifestDigest
	directLayers := remoteBaselineSeedDirectCacheLayers(accepted)
	request.DirectCacheLayers = directLayers
	maps.Copy(request.Environment, remoteBaselineSeedDirectCacheEnvironment(directLayers))
	deltas := accepted.DeltaRefs()
	if len(deltas) > remoteBaselineDeltaLimit {
		return fmt.Errorf("accepted baseline has more than %d Delta layers", remoteBaselineDeltaLimit)
	}
	if len(deltas) != 0 {
		request.BaselineLayers = remoteBaselineSeedVolume(config, strings.TrimSuffix(config.OSS.BaselinePrefix, "/"))
	}
	maps.Copy(request.Environment, remoteBaselineSeedDeltaEnvironment(deltas))
	return nil
}

// remoteBaselineIncrementalRefreshRejection 返回禁止复用已接受层的首个原因。
func remoteBaselineIncrementalRefreshRejection(session remoteBaselineRefreshSession) string {
	switch {
	case !remoteBaselineCapacityMatches(session.accepted, session.acceptedRecommendedSizeGiB):
		return "capacity changed"
	case remoteBaselineForcesRuntimeRefresh(session.input):
		return "runtime dependency schema changed"
	case session.input.RuntimeDependencyDigest == "" || session.input.AcceptedRuntimeDependencyDigest == "":
		return "runtime dependency identity is missing"
	case !remoteBaselineSeedIdentityMatches(session.accepted, session.input.Identity):
		return "platform or runtime image changed"
	default:
		return ""
	}
}

// logRemoteBaselineIncrementalRefresh 明确区分普通 Delta 与离线压实。
func logRemoteBaselineIncrementalRefresh(session remoteBaselineRefreshSession) {
	if len(session.accepted.DeltaRefs()) >= remoteBaselineDeltaLimit {
		slog.Info("remote baseline refresh uses incremental compaction", "parent_generation", session.accepted.Generation, "existing_deltas", len(session.accepted.DeltaRefs()), "network_required", false)
		return
	}
	slog.Info("remote baseline refresh uses incremental delta", "parent_generation", session.accepted.Generation, "existing_deltas", len(session.accepted.DeltaRefs()), "toolchain_changed", session.accepted.ToolchainDigest != session.input.Identity.ToolchainDigest, "runtime_dependency_digest", session.input.RuntimeDependencyDigest, "accepted_runtime_dependency_digest", session.input.AcceptedRuntimeDependencyDigest)
}

// remoteBaselineCanReuse 只允许完整历史、身份和容量均匹配的基线跳过刷新。
func remoteBaselineCanReuse(accepted remoteci.BaselineState, identity remoteci.BaselineIdentity, recommendedSizeGiB int) bool {
	return accepted.SourceHistoryVersion == remoteci.BaselineSourceHistorySchemaVersion &&
		accepted.Matches(identity) && remoteBaselineCapacityMatches(accepted, recommendedSizeGiB)
}

// remoteBaselineSeedStorageMode 只允许首次 Anchor、兼容 Delta 或满链增量压实。
func remoteBaselineSeedStorageMode(hasAccepted bool, accepted remoteci.BaselineState, acceptedRecommendedSizeGiB int, input remoteBaselineRefreshInput, source remoteBaselineSourceArtifact, generation uint64) (string, error) {
	if !hasAccepted {
		return remoteci.BaselineStorageModeAnchor, nil
	}
	materialize := remoteBaselineCapacityMatches(accepted, acceptedRecommendedSizeGiB) &&
		remoteBaselineSeedCanMaterializePrevious(accepted, input, source, generation)
	if !materialize {
		return "", errors.New("accepted baseline exists but this refresh cannot be represented as a Delta; full Anchor rebuild is forbidden")
	}
	if len(accepted.DeltaRefs()) >= remoteBaselineDeltaLimit {
		return remoteci.BaselineStorageModeAnchor, nil
	}
	return remoteci.BaselineStorageModeDelta, nil
}

// remoteBaselineSeedCanAppendDelta 只允许经过连续后代验证的兼容 Anchor 追加有限 Delta。
func remoteBaselineSeedCanAppendDelta(accepted remoteci.BaselineState, input remoteBaselineRefreshInput, source remoteBaselineSourceArtifact, generation uint64) bool {
	return len(accepted.DeltaRefs()) < remoteBaselineDeltaLimit && remoteBaselineSeedCanMaterializePrevious(accepted, input, source, generation)
}

// remoteBaselineSeedCanMaterializePrevious 只允许从完整、连续且兼容的已接受链离线物化下一代。
func remoteBaselineSeedCanMaterializePrevious(accepted remoteci.BaselineState, input remoteBaselineRefreshInput, source remoteBaselineSourceArtifact, generation uint64) bool {
	return remoteBaselineSeedHasMaterializableHistory(accepted, input, source, generation) && remoteBaselineSeedIdentityMatches(accepted, input.Identity)
}

// remoteBaselineSeedHasMaterializableHistory 验证增量追加与压实共同依赖的历史连续性。
func remoteBaselineSeedHasMaterializableHistory(accepted remoteci.BaselineState, input remoteBaselineRefreshInput, source remoteBaselineSourceArtifact, generation uint64) bool {
	return accepted.Validate() == nil && source.Manifest.Mode == remoteBaselineSourceDelta &&
		!remoteBaselineForcesRuntimeRefresh(input) && generation > accepted.Generation &&
		remoteBaselineSeedSourceExtendsAccepted(source.Manifest, accepted, input.Identity) &&
		validRemoteManifestDigest(input.RuntimeDependencyDigest) && validRemoteManifestDigest(input.AcceptedRuntimeDependencyDigest)
}
