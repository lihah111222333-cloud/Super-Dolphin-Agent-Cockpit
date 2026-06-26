package team

import (
	"context"
	"path/filepath"
	"strings"
)

// teamSyncBatch 是单次远端 push 请求的上传和删除集合。
type teamSyncBatch struct {
	Uploads map[string]string
	Deletes []string
}

// preparedTeamSyncPush 是持锁准备好的推送计划。
// localTree 用于最终确认远端状态是否覆盖当前本地快照，filtered 记录密钥扫描后的可推送文件。
type preparedTeamSyncPush struct {
	localChecksums map[string]string
	localTree      string
	filtered       TeamMemPrePushScanResult
	batches        []teamSyncBatch
}

// pushLocked 在持有 s.mu 时把本地团队记忆变更推送到远端。
// 该流程会先执行密钥过滤，再按远端容量限制分批推送；冲突时最多 pull 后重试一次。
func (s *TeamSyncService) pushLocked(ctx context.Context, trigger TeamSyncTrigger, retried bool) (TeamSyncPushResult, error) {
	result := TeamSyncPushResult{}
	plan, ok, err := s.preparePushLocked()
	if err != nil || !ok {
		return result, err
	}
	result.Skipped = plan.filtered.Skipped
	if len(plan.batches) == 0 {
		result.Failed = skippedFailures(plan.filtered.Skipped)
		return result, nil
	}
	for _, batch := range plan.batches {
		response, err := s.pushBatchLocked(ctx, batch)
		if err != nil {
			return result, err
		}
		stop, final, err := s.handlePushBatchResponseLocked(ctx, trigger, retried, plan.filtered, result, response)
		if err != nil || stop {
			return final, err
		}
		result = final
	}
	return s.finalizePushLocked(ctx, trigger, result, plan)
}

// buildTeamSyncBatches 根据远端容量上限把上传和删除操作拆成请求批次。
func buildTeamSyncBatches(uploads map[string]string, deletes []string, limit int) []teamSyncBatch {
	if len(uploads) == 0 && len(deletes) == 0 {
		return nil
	}
	if limit <= 0 || len(uploads)+len(deletes) <= limit {
		return []teamSyncBatch{{Uploads: cloneStringMap(uploads), Deletes: append([]string(nil), deletes...)}}
	}
	return buildLimitedTeamSyncBatches(uploads, deletes, limit)
}

// buildLimitedTeamSyncBatches 按稳定路径顺序拆分 push 批次。
// 先上传后删除，保证同一批内的计数不超过远端声明的 max entries。
func buildLimitedTeamSyncBatches(uploads map[string]string, deletes []string, limit int) []teamSyncBatch {
	uploadKeys := sortedMapKeys(uploads)
	deleteKeys := append([]string(nil), deletes...)
	var batches []teamSyncBatch
	current := teamSyncBatch{Uploads: map[string]string{}}
	count := 0
	flush := func() {
		if len(current.Uploads) == 0 && len(current.Deletes) == 0 {
			return
		}
		if len(current.Uploads) == 0 {
			current.Uploads = nil
		}
		batches = append(batches, current)
		current = teamSyncBatch{Uploads: map[string]string{}}
		count = 0
	}
	appendTeamSyncUploads(&current, uploads, uploadKeys, limit, &count, flush)
	appendTeamSyncDeletes(&current, deleteKeys, limit, &count, flush)
	flush()
	return batches
}

// preparePushLocked 扫描本地文件并构建 push 计划。
// 本地 checksum 未变化时返回 ok=false；命中密钥的文件会从 uploads 中移除但记录到 Skipped。
func (s *TeamSyncService) preparePushLocked() (preparedTeamSyncPush, bool, error) {
	if !s.runtimeReadyLocked() {
		return preparedTeamSyncPush{}, false, nil
	}
	local, err := scanTeamMarkdownFiles(s.root)
	if err != nil {
		return preparedTeamSyncPush{}, false, err
	}
	localChecksums := localChecksumMap(local)
	localTree := checksumTree(localChecksums)
	if localTree == strings.TrimSpace(s.state.LastKnownChecksum) {
		return preparedTeamSyncPush{}, false, nil
	}
	uploads, deletes := diffServerChecksums(local, s.state.ServerChecksums)
	filtered := s.guard.FilterPushFiles(uploads)
	return preparedTeamSyncPush{
		localChecksums: localChecksums,
		localTree:      localTree,
		filtered:       filtered,
		batches:        buildTeamSyncBatches(filtered.Allowed, deletes, s.state.ServerMaxEntries),
	}, true, nil
}

// pushBatchLocked 发送单个 push 批次，并带上当前远端 ETag 做并发保护。
func (s *TeamSyncService) pushBatchLocked(ctx context.Context, batch teamSyncBatch) (TeamSyncPushResponse, error) {
	return s.remote.PushFiles(ctx, TeamSyncPushRequest{
		RepoSlug:      s.repoSlug,
		IfMatch:       strings.TrimSpace(s.state.ServerETag),
		Uploads:       batch.Uploads,
		Deletes:       batch.Deletes,
		BaseChecksums: cloneChecksumMap(s.state.ServerChecksums),
	})
}

// handlePushBatchResponseLocked 合并单个远端 push 响应。
// 远端冲突会进入 pull-and-retry，NotFound 不清空本地，只把远端缺失记为失败让调用方可见。
func (s *TeamSyncService) handlePushBatchResponseLocked(
	ctx context.Context,
	trigger TeamSyncTrigger,
	retried bool,
	filtered TeamMemPrePushScanResult,
	result TeamSyncPushResult,
	response TeamSyncPushResponse,
) (bool, TeamSyncPushResult, error) {
	stop, updated, err := s.applyPushBatchLimitsLocked(result, response)
	if err != nil || stop {
		return stop, updated, err
	}
	if response.Conflict {
		return s.handlePushConflictLocked(ctx, trigger, retried, filtered, updated, response)
	}
	if response.NotFound {
		updated.Failed = mergeFailures(updated.Failed, map[string]string{"remote": "not_found"})
		return true, updated, nil
	}
	applyPushResponse(&s.state, response)
	updated.Applied = updated.Applied || len(response.Applied) > 0 || len(response.Deleted) > 0
	updated.Failed = mergeFailures(updated.Failed, response.Failed)
	return false, updated, nil
}

// applyPushBatchLimitsLocked 学习远端返回的批次容量上限。
// 如果响应只携带上限而没有实际应用结果，当前 push 会停止，下一次按新上限重新分批。
func (s *TeamSyncService) applyPushBatchLimitsLocked(
	result TeamSyncPushResult,
	response TeamSyncPushResponse,
) (bool, TeamSyncPushResult, error) {
	if response.MaxEntries <= 0 {
		return false, result, nil
	}
	s.state.ServerMaxEntries = response.MaxEntries
	if err := s.persistStateLocked(); err != nil {
		return false, result, err
	}
	result.LearnedMaxEntries = true
	if len(response.Applied) == 0 && len(response.Deleted) == 0 && len(response.Failed) == 0 && !response.Conflict {
		return true, result, nil
	}
	return false, result, nil
}

// handlePushConflictLocked 处理远端 ETag 冲突。
// 首次冲突先拉取远端再重试一次；已重试仍冲突时合并远端失败和本地跳过文件后停止。
func (s *TeamSyncService) handlePushConflictLocked(
	ctx context.Context,
	trigger TeamSyncTrigger,
	retried bool,
	filtered TeamMemPrePushScanResult,
	result TeamSyncPushResult,
	response TeamSyncPushResponse,
) (bool, TeamSyncPushResult, error) {
	if retried {
		result.Failed = mergeFailures(result.Failed, mergeFailures(response.Failed, skippedFailures(filtered.Skipped)))
		return true, result, nil
	}
	if _, err := s.pullLocked(ctx, TeamSyncTriggerConflict); err != nil {
		return true, result, err
	}
	retry, err := s.pushLocked(ctx, trigger, true)
	if err != nil {
		return true, retry, err
	}
	retry.Retried = true
	return true, retry, nil
}

// finalizePushLocked 持久化 push 后的同步状态，并在远端接受变更时触发 prompt 失效。
func (s *TeamSyncService) finalizePushLocked(
	ctx context.Context,
	trigger TeamSyncTrigger,
	result TeamSyncPushResult,
	plan preparedTeamSyncPush,
) (TeamSyncPushResult, error) {
	if checksumMapsEqual(plan.localChecksums, s.state.ServerChecksums) {
		s.state.LastKnownChecksum = plan.localTree
	}
	if err := s.persistStateLocked(); err != nil {
		return result, err
	}
	result.Failed = mergeFailures(result.Failed, skippedFailures(plan.filtered.Skipped))
	if result.Applied {
		s.invalidateLocked(ctx, trigger)
	}
	return result, nil
}

// appendTeamSyncUploads 将上传项追加到当前批次，到达限制时调用 flush。
func appendTeamSyncUploads(
	current *teamSyncBatch,
	uploads map[string]string,
	keys []string,
	limit int,
	count *int,
	flush func(),
) {
	for _, key := range keys {
		if *count == limit {
			flush()
		}
		current.Uploads[key] = uploads[key]
		*count = *count + 1
	}
}

// appendTeamSyncDeletes 将删除项追加到当前批次，到达限制时调用 flush。
func appendTeamSyncDeletes(
	current *teamSyncBatch,
	keys []string,
	limit int,
	count *int,
	flush func(),
) {
	for _, key := range keys {
		if *count == limit {
			flush()
		}
		current.Deletes = append(current.Deletes, key)
		*count = *count + 1
	}
}

// applyPushResponse 把远端 push 响应写回本地 SyncState。
// 只接受非空 path/checksum；远端删除会移除对应 checksum，空 checksum map 会被收敛为 nil。
func applyPushResponse(state *SyncState, response TeamSyncPushResponse) {
	if state == nil {
		return
	}
	if state.ServerChecksums == nil {
		state.ServerChecksums = map[string]string{}
	}
	for path, checksum := range response.Applied {
		path = strings.TrimSpace(filepath.ToSlash(path))
		checksum = strings.TrimSpace(checksum)
		if path == "" || checksum == "" {
			continue
		}
		state.ServerChecksums[path] = checksum
	}
	for _, path := range response.Deleted {
		delete(state.ServerChecksums, strings.TrimSpace(filepath.ToSlash(path)))
	}
	if len(state.ServerChecksums) == 0 {
		state.ServerChecksums = nil
	}
	if etag := strings.TrimSpace(response.ETag); etag != "" {
		state.ServerETag = etag
	}
}

// skippedFailures 把密钥扫描跳过的文件转换为失败 map，供 UI/RPC 展示。
func skippedFailures(skipped []TeamMemSkippedFile) map[string]string {
	if len(skipped) == 0 {
		return nil
	}
	failed := make(map[string]string, len(skipped))
	for _, item := range skipped {
		failed[item.Path] = ErrTeamMemSecretDetected.Error()
	}
	return failed
}

// mergeFailures 合并两个文件失败 map，右侧同名路径覆盖左侧。
func mergeFailures(left, right map[string]string) map[string]string {
	if len(left) == 0 && len(right) == 0 {
		return nil
	}
	merged := cloneStringMap(left)
	if merged == nil {
		merged = map[string]string{}
	}
	for key, value := range right {
		merged[key] = value
	}
	return merged
}
