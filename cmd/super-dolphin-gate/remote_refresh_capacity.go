package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const remoteDataCacheGiB int64 = 1 << 30

// remoteBaselineCapacityMatches 判断 accepted Anchor 是否已经达到本次计算出的目标容量。
func remoteBaselineCapacityMatches(accepted remoteci.BaselineState, recommendedSizeGiB int) bool {
	return accepted.SchemaVersion != 0 && recommendedSizeGiB > 0 &&
		accepted.DataCacheSizeGiB == recommendedSizeGiB
}

// remoteBaselineRecommendedSizeGiB 按签名产物实际大小预留 5 GiB，并受仓外上限约束。
func remoteBaselineRecommendedSizeGiB(config remoteRunConfig, manifest remoteci.BaselineManifest) (int, error) {
	payloadBytes, err := remoteBaselinePayloadBytes(manifest)
	if err != nil {
		return 0, err
	}
	payloadGiB := payloadBytes / remoteDataCacheGiB
	if payloadBytes%remoteDataCacheGiB != 0 {
		payloadGiB++
	}
	recommended := payloadGiB + remoteDataCacheFreeReserveGiB
	recommended = max(recommended, remoteDataCacheMinimumSizeGiB)
	if recommended > int64(config.DataCache.MaxSizeGiB) {
		return 0, fmt.Errorf(
			"remote baseline requires %d GiB including %d GiB reserve, exceeding configured maximum %d GiB",
			recommended,
			remoteDataCacheFreeReserveGiB,
			config.DataCache.MaxSizeGiB,
		)
	}
	return int(recommended), nil
}

// remoteBaselinePayloadBytes 汇总 manifest 绑定的全部 DataCache 输入对象大小。
func remoteBaselinePayloadBytes(manifest remoteci.BaselineManifest) (int64, error) {
	if err := manifest.Validate(); err != nil {
		return 0, fmt.Errorf("validate remote baseline manifest for capacity: %w", err)
	}
	total := int64(0)
	add := func(size int64) error {
		const maxInt64 = int64(^uint64(0) >> 1)
		if size <= 0 || total > maxInt64-size {
			return errors.New("remote baseline payload size overflows int64")
		}
		total += size
		return nil
	}
	if err := add(manifest.GateBinarySize); err != nil {
		return 0, err
	}
	if err := add(manifest.CABundleSize); err != nil {
		return 0, err
	}
	if len(manifest.Layers) == 0 {
		if err := add(manifest.ArchiveSize); err != nil {
			return 0, err
		}
		return total, nil
	}
	for _, layer := range manifest.Layers {
		if err := add(layer.Size); err != nil {
			return 0, err
		}
	}
	return total, nil
}

// loadAcceptedRemoteBaselineRecommendedSize 下载并验签 accepted Anchor manifest 后计算目标容量。
func loadAcceptedRemoteBaselineRecommendedSize(
	ctx context.Context,
	config remoteRunConfig,
	store remoteBaselineOSSStore,
	accepted remoteci.BaselineState,
) (int, error) {
	if accepted.SchemaVersion == 0 {
		return 0, nil
	}
	anchor := accepted.CurrentAnchorRef()
	tempRoot, err := os.MkdirTemp("", "super-dolphin-accepted-baseline-manifest-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tempRoot)

	manifestPath := filepath.Join(tempRoot, "baseline-manifest.json")
	manifestKey := strings.TrimSuffix(anchor.SourceObjectPrefix, "/") + "/output/baseline-manifest.json"
	if err := store.Download(ctx, manifestKey, manifestPath); err != nil {
		return 0, fmt.Errorf("download accepted Anchor manifest %q: %w", manifestKey, err)
	}
	data, err := readRemoteBaselineManifestBytes(manifestPath)
	if err != nil {
		return 0, err
	}
	if digest := remoteci.BaselineManifestDigest(data); digest != anchor.ManifestDigest {
		return 0, errors.New("accepted Anchor manifest digest drifted")
	}
	manifest, err := remoteci.DecodeBaselineManifest(data)
	if err != nil {
		return 0, err
	}
	if !remoteBaselineAnchorManifestMatchesReference(anchor, manifest, accepted.Platform, accepted.RuntimeImage) {
		return 0, errors.New("accepted Anchor manifest identity drifted")
	}
	return remoteBaselineRecommendedSizeGiB(config, manifest)
}

// remoteBaselineAnchorManifestMatchesReference 只按 Anchor 自身引用和跨层不变量复验历史 manifest。
func remoteBaselineAnchorManifestMatchesReference(anchor remoteci.BaselineCacheRef, manifest remoteci.BaselineManifest, platform, runtimeImage string) bool {
	return manifest.StorageMode == remoteci.BaselineStorageModeAnchor &&
		manifest.Generation == anchor.Generation &&
		manifest.MainCommit == anchor.MainCommit &&
		manifest.MainTree == anchor.MainTree &&
		manifest.Platform == platform &&
		manifest.RuntimeImage == runtimeImage
}
