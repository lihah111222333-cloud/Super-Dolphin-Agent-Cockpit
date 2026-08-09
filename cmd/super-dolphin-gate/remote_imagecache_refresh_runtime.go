package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/oss"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const (
	remoteImageCacheRefreshReceiptSuffix = "baseline-refresh/receipts/current.json"
	remoteImageCacheRefreshReceiptLimit  = 64 << 10
	remoteImageCacheRefreshLookupTimeout = time.Minute
)

type remoteImageCacheRuntime struct {
	Image      string
	SnapshotID string
	CacheOnly  bool
}

// loadRemoteImageCacheRuntime 读取 OSS 刷新回执并实时确认 Ready cache 后返回本次运行物料。
func loadRemoteImageCacheRuntime(config remoteRunConfig, state remoteci.BaselineState) (remoteImageCacheRuntime, error) {
	ctx, cancel := gateprivate.WithTimeout(context.Background(), remoteImageCacheRefreshLookupTimeout)
	defer cancel()
	client, err := oss.NewCLI(oss.Config{
		Binary: config.AliyunCLI, Bucket: config.OSS.Bucket, Endpoint: config.OSS.Endpoint,
		Profile: config.CredentialProfile, Prefix: config.OSS.SourcePrefix,
	})
	if err != nil {
		return remoteImageCacheRuntime{}, fmt.Errorf("open remote ImageCache refresh receipt store: %w", err)
	}
	key := config.OSS.SourcePrefix + remoteImageCacheRefreshReceiptSuffix
	payload, err := client.Read(ctx, key, remoteImageCacheRefreshReceiptLimit)
	if err != nil {
		return remoteImageCacheRuntime{}, fmt.Errorf("read remote ImageCache refresh receipt: %w", err)
	}
	receipt, err := cicontract.DecodeImageCacheRefreshReceipt(payload, time.Now().UTC())
	if err != nil {
		return remoteImageCacheRuntime{}, err
	}
	if err := validateRemoteImageCacheRefreshBinding(config, state, receipt); err != nil {
		return remoteImageCacheRuntime{}, err
	}
	verifier, err := newRemoteGenerationOneVerifier(config)
	if err != nil {
		return remoteImageCacheRuntime{}, fmt.Errorf("open remote ImageCache refresh verifier: %w", err)
	}
	cache, err := verifier.DescribeImageCache(ctx, receipt.ImageCacheID)
	if err != nil {
		return remoteImageCacheRuntime{}, fmt.Errorf("describe remote ImageCache refresh runtime: %w", err)
	}
	if cache.RegionID != config.RegionID {
		return remoteImageCacheRuntime{}, errors.New("remote ImageCache refresh runtime region drifted")
	}
	if err := eci.ValidateReadyImageCache(cache, receipt.ImageCacheID, receipt.ImageCacheName, receipt.Image); err != nil {
		return remoteImageCacheRuntime{}, err
	}
	if cache.SnapshotID != receipt.ImageCacheSnapshotID {
		return remoteImageCacheRuntime{}, errors.New("remote ImageCache refresh runtime snapshot drifted")
	}
	return remoteImageCacheRuntime{Image: receipt.Image, SnapshotID: receipt.ImageCacheSnapshotID, CacheOnly: true}, nil
}

// validateRemoteImageCacheRefreshBinding 证明刷新层来自已接受 OCI 基线且不能改写 correctness authority。
func validateRemoteImageCacheRefreshBinding(config remoteRunConfig, state remoteci.BaselineState, receipt cicontract.ImageCacheRefreshReceipt) error {
	if receipt.RegionID != config.RegionID || receipt.OCIBaseImage != state.RuntimeImage {
		return errors.New("remote ImageCache refresh receipt is not based on the accepted runtime image and region")
	}
	if receipt.Image == state.RuntimeImage || receipt.ImageCacheSnapshotID == state.ImageCacheSnapshotID {
		return errors.New("remote ImageCache refresh receipt does not identify a successor cache layer")
	}
	if strings.TrimSpace(receipt.BaseSnapshotID) == "" || strings.TrimSpace(receipt.BaseImage) == "" {
		return errors.New("remote ImageCache refresh receipt base identity is incomplete")
	}
	return nil
}

// applyRemoteImageCacheRuntime 只替换执行加速物料，保留 accepted generation、runner identity 与 SQLite 证据语义。
func applyRemoteImageCacheRuntime(input *remoteci.RunInput, runtime remoteImageCacheRuntime) error {
	if input == nil || strings.TrimSpace(runtime.Image) == "" || strings.TrimSpace(runtime.SnapshotID) == "" || !runtime.CacheOnly {
		return errors.New("remote ImageCache refresh runtime is incomplete")
	}
	input.ExecutionRunnerImage = runtime.Image
	input.ExecutionImageCacheSnapshotID = runtime.SnapshotID
	input.ImageCacheOnly = true
	return nil
}
