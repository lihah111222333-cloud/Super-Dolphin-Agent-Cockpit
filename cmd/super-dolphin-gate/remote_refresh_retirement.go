package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/datacache"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// cleanupRetiredRemoteBaseline 删除已退役 Anchor DataCache 和全部 OSS generation。
func cleanupRetiredRemoteBaseline(
	ctx context.Context,
	client remoteBaselineDataCacheClient,
	store remoteBaselineOSSStore,
	statePath string,
	state *remoteci.BaselineState,
) error {
	if !remoteBaselineRetirementDependenciesComplete(ctx, client, store, state) {
		return errors.New("retired remote baseline cleanup dependencies are incomplete")
	}
	if !state.HasRetiredReferences() {
		return nil
	}
	if err := state.Validate(); err != nil {
		return fmt.Errorf("validate retired baseline journal: %w", err)
	}
	prefixes, err := collectRetiredRemoteBaselinePrefixes(ctx, client, *state)
	if err != nil {
		return err
	}
	for _, prefix := range prefixes {
		if err := store.DeletePrefix(ctx, prefix); err != nil {
			return fmt.Errorf("delete retired OSS generation %q: %w", prefix, err)
		}
	}
	state.RetiredAnchor = nil
	state.RetiredDirectCacheRef = nil
	state.RetiredDeltas = nil
	if err := writeRemoteBaselineState(statePath, *state); err != nil {
		return fmt.Errorf("persist retired baseline cleanup: %w", err)
	}
	return nil
}

// remoteBaselineRetirementDependenciesComplete 验证退役资源清理所需的全部依赖。
func remoteBaselineRetirementDependenciesComplete(
	ctx context.Context,
	client remoteBaselineDataCacheClient,
	store remoteBaselineOSSStore,
	state *remoteci.BaselineState,
) bool {
	return ctx != nil && client != nil && store != nil && state != nil
}

// collectRetiredRemoteBaselinePrefixes 删除退役 Anchor，并汇集全部退役 generation 前缀。
func collectRetiredRemoteBaselinePrefixes(
	ctx context.Context,
	client remoteBaselineDataCacheClient,
	state remoteci.BaselineState,
) ([]string, error) {
	prefixes := make([]string, 0, 1+len(state.RetiredDeltas))
	if state.RetiredAnchor != nil {
		if err := removeRetiredRemoteDataCache(ctx, client, *state.RetiredAnchor); err != nil {
			return nil, err
		}
		prefixes = append(prefixes, state.RetiredAnchor.SourceObjectPrefix)
	}
	if state.RetiredDirectCacheRef != nil {
		retired := state.RetiredDirectCacheRef
		if err := removeRetiredRemoteDirectDataCache(ctx, client, *retired); err != nil {
			return nil, err
		}
		prefixes = append(prefixes, retired.SourceObjectPrefix)
	}
	for _, delta := range state.RetiredDeltas {
		prefixes = append(prefixes, delta.SourceObjectPrefix)
	}
	return uniqueRemoteBaselinePrefixes(prefixes), nil
}

// removeRetiredRemoteDirectDataCache 在删除前额外绑定直读缓存的规范资源名。
func removeRetiredRemoteDirectDataCache(ctx context.Context, client remoteBaselineDataCacheClient, retired remoteci.DirectCacheRef) error {
	caches, err := client.Describe(ctx, retired.DataCacheID)
	if err != nil {
		return fmt.Errorf("describe retired direct DataCache: %w", err)
	}
	if len(caches) > 1 {
		return errors.New("retired direct DataCache identity is ambiguous")
	}
	if len(caches) == 0 {
		return nil
	}
	expectedName := remoteBaselineResourceName(retired.Generation) + "-direct-cache"
	if caches[0].Name != expectedName {
		return errors.New("retired direct DataCache resource name drifted")
	}
	return removeRetiredRemoteDataCache(ctx, client, remoteci.BaselineCacheRef{DataCacheID: retired.DataCacheID, DataCacheBucket: retired.DataCacheBucket, DataCachePath: retired.DataCachePath})
}

func uniqueRemoteBaselinePrefixes(prefixes []string) []string {
	seen := make(map[string]struct{}, len(prefixes))
	unique := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		if _, exists := seen[prefix]; exists {
			continue
		}
		seen[prefix] = struct{}{}
		unique = append(unique, prefix)
	}
	return unique
}

// removeRetiredRemoteDataCache 删除仍存在的退役 DataCache 并等待其消失。
func removeRetiredRemoteDataCache(ctx context.Context, client remoteBaselineDataCacheClient, retired remoteci.BaselineCacheRef) error {
	caches, err := client.Describe(ctx, retired.DataCacheID)
	if err != nil {
		return fmt.Errorf("describe retired DataCache: %w", err)
	}
	if len(caches) > 1 {
		return errors.New("retired DataCache identity is ambiguous")
	}
	if len(caches) == 0 {
		return nil
	}
	cache := caches[0]
	if cache.ID != retired.DataCacheID || cache.Bucket != retired.DataCacheBucket || cache.Path != retired.DataCachePath {
		return errors.New("retired DataCache identity drifted")
	}
	if cache.Status != datacache.StatusDeleting {
		if err := client.Delete(ctx, retired.DataCacheID, retired.DataCacheBucket, retired.DataCachePath); err != nil {
			return fmt.Errorf("delete retired DataCache: %w", err)
		}
	}
	return waitRemoteDataCacheDeleted(ctx, client, retired)
}

// waitRemoteDataCacheDeleted 等待已退役 DataCache 完全消失。
func waitRemoteDataCacheDeleted(
	ctx context.Context,
	client remoteBaselineDataCacheClient,
	retired remoteci.BaselineCacheRef,
) error {
	timer := time.NewTicker(remoteBaselinePollInterval)
	defer timer.Stop()
	for {
		caches, err := client.Describe(ctx, retired.DataCacheID)
		if err != nil {
			return fmt.Errorf("describe deleting DataCache: %w", err)
		}
		if len(caches) == 0 {
			return nil
		}
		if len(caches) != 1 ||
			caches[0].ID != retired.DataCacheID ||
			caches[0].Bucket != retired.DataCacheBucket ||
			caches[0].Path != retired.DataCachePath {
			return errors.New("deleting DataCache identity drifted")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// waitRemoteDataCache 等待请求的 DataCache 到达可用终态。
func waitRemoteDataCache(
	ctx context.Context,
	client remoteBaselineDataCacheClient,
	cacheID string,
	expectedPath string,
	expectedBucket string,
) (datacache.DataCache, error) {
	timer := time.NewTicker(remoteBaselinePollInterval)
	defer timer.Stop()
	for {
		cache, ready, err := inspectRemoteDataCache(ctx, client, cacheID, expectedPath, expectedBucket)
		if err != nil {
			return datacache.DataCache{}, err
		}
		if ready {
			return cache, nil
		}
		select {
		case <-ctx.Done():
			return datacache.DataCache{}, ctx.Err()
		case <-timer.C:
		}
	}
}

// inspectRemoteDataCache 验证等待中的 DataCache 身份并识别终态。
func inspectRemoteDataCache(ctx context.Context, client remoteBaselineDataCacheClient, cacheID, expectedPath, expectedBucket string) (datacache.DataCache, bool, error) {
	caches, err := client.Describe(ctx, cacheID)
	if err != nil {
		return datacache.DataCache{}, false, err
	}
	if len(caches) != 1 || caches[0].ID != cacheID || caches[0].Path != expectedPath || caches[0].Bucket != expectedBucket {
		return datacache.DataCache{}, false, errors.New("DataCache identity does not match refresh request")
	}
	cache := caches[0]
	if cache.Status == datacache.StatusAvailable {
		return cache, true, nil
	}
	if cache.Status == datacache.StatusFailed || cache.Status == datacache.StatusUpdateFailed {
		return datacache.DataCache{}, false, fmt.Errorf("DataCache %s ended with status %s", cacheID, cache.Status)
	}
	return cache, false, nil
}

func verifyAvailableDataCache(
	ctx context.Context,
	client remoteBaselineDataCacheClient,
	state remoteci.BaselineState,
) error {
	anchor := state.CurrentAnchorRef()
	cache, err := waitRemoteDataCache(
		ctx,
		client,
		anchor.DataCacheID,
		anchor.DataCachePath,
		anchor.DataCacheBucket,
	)
	if err != nil {
		return err
	}
	if cache.SizeGiB != anchor.SizeGiB {
		return errors.New("accepted DataCache size drifted")
	}
	return nil
}
