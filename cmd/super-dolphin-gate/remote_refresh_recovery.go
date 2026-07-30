package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// cleanupStaleRemoteBaselineCandidate 回收同一未接受 generation 的 DataCache 与 OSS 残留。
func cleanupStaleRemoteBaselineCandidate(
	ctx context.Context,
	session remoteBaselineRefreshSession,
	stage remoteBaselineArtifactStage,
) error {
	if err := validateRemoteBaselineCandidateIdentity(session, stage); err != nil {
		return err
	}
	cachePath := remoteBaselineCachePath(session.config, stage.generation)
	caches, err := session.cache.FindByPath(
		ctx,
		session.config.DataCache.Bucket,
		cachePath,
		remoteBaselineCandidateTags(stage.generation),
	)
	if err != nil {
		return fmt.Errorf("find stale candidate DataCache: %w", err)
	}
	if len(caches) > 1 {
		return errors.New("stale candidate DataCache identity is ambiguous")
	}
	if len(caches) == 1 {
		cache := caches[0]
		if cache.Name != remoteBaselineResourceName(stage.generation) ||
			cache.Bucket != session.config.DataCache.Bucket ||
			cache.Path != cachePath {
			return errors.New("stale candidate DataCache identity drifted")
		}
		candidate := remoteci.BaselineCacheRef{
			Generation:      stage.generation,
			DataCacheID:     cache.ID,
			DataCacheBucket: cache.Bucket,
			DataCachePath:   cache.Path,
		}
		if err := removeRetiredRemoteDataCache(ctx, session.cache, candidate); err != nil {
			return fmt.Errorf("delete stale candidate DataCache: %w", err)
		}
	}
	if err := session.store.DeletePrefix(ctx, stage.generationPrefix); err != nil {
		return fmt.Errorf("delete stale candidate OSS generation: %w", err)
	}
	return nil
}

// validateRemoteBaselineCandidateIdentity 限制清理目标为下一代规范缓存与对象前缀。
func validateRemoteBaselineCandidateIdentity(
	session remoteBaselineRefreshSession,
	stage remoteBaselineArtifactStage,
) error {
	generation, err := nextRemoteBaselineGeneration(session.accepted, session.legacy)
	if err != nil {
		return err
	}
	if stage.generation != generation ||
		stage.generationPrefix != remoteBaselineSourcePrefix(session.config, generation) ||
		stage.inputPrefix != remoteBaselineInputPrefix(session.config, generation) ||
		stage.outputPrefix != remoteBaselineOutputPrefix(session.config, generation) {
		return errors.New("stale candidate baseline identity is not the next canonical generation")
	}
	return nil
}

// remoteBaselineCandidateTags 返回跨源码版本稳定的未接受 generation 查询标签。
func remoteBaselineCandidateTags(generation uint64) map[string]string {
	return map[string]string{
		"owner":      "super-dolphin-ci",
		"generation": strconv.FormatUint(generation, 10),
	}
}
