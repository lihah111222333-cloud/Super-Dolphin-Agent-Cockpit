package team

import (
	"context"
	"path/filepath"
	"strings"
)

type teamSyncBatch struct {
	Uploads map[string]string
	Deletes []string
}

type preparedTeamSyncPush struct {
	localChecksums map[string]string
	localTree      string
	filtered       TeamMemPrePushScanResult
	batches        []teamSyncBatch
}

// pushLocked 处理pushlocked。
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

func buildTeamSyncBatches(uploads map[string]string, deletes []string, limit int) []teamSyncBatch {
	if len(uploads) == 0 && len(deletes) == 0 {
		return nil
	}
	if limit <= 0 || len(uploads)+len(deletes) <= limit {
		return []teamSyncBatch{{Uploads: cloneStringMap(uploads), Deletes: append([]string(nil), deletes...)}}
	}
	return buildLimitedTeamSyncBatches(uploads, deletes, limit)
}

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

func (s *TeamSyncService) pushBatchLocked(ctx context.Context, batch teamSyncBatch) (TeamSyncPushResponse, error) {
	return s.remote.PushFiles(ctx, TeamSyncPushRequest{
		RepoSlug:      s.repoSlug,
		IfMatch:       strings.TrimSpace(s.state.ServerETag),
		Uploads:       batch.Uploads,
		Deletes:       batch.Deletes,
		BaseChecksums: cloneChecksumMap(s.state.ServerChecksums),
	})
}

// handlePushBatchResponseLocked 处理pushbatch响应locked。
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

// applyPushBatchLimitsLocked 应用pushbatchlimitslocked。
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

// applyPushResponse 应用push响应。
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
