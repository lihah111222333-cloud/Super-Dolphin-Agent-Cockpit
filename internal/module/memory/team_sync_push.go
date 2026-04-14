package memory

import (
	"context"
	"path/filepath"
	"strings"
)

type teamSyncBatch struct {
	Uploads map[string]string
	Deletes []string
}

func (s *TeamSyncService) pushLocked(ctx context.Context, trigger TeamSyncTrigger, retried bool) (TeamSyncPushResult, error) {
	result := TeamSyncPushResult{}
	if !s.runtimeReadyLocked() {
		return result, nil
	}
	local, err := scanTeamMarkdownFiles(s.root)
	if err != nil {
		return result, err
	}
	localChecksums := localChecksumMap(local)
	localTree := checksumTree(localChecksums)
	if localTree == strings.TrimSpace(s.state.LastKnownChecksum) {
		return result, nil
	}
	uploads, deletes := diffServerChecksums(local, s.state.ServerChecksums)
	filtered := s.guard.FilterPushFiles(uploads)
	result.Skipped = filtered.Skipped
	batches := buildTeamSyncBatches(filtered.Allowed, deletes, s.state.ServerMaxEntries)
	if len(batches) == 0 {
		result.Failed = skippedFailures(filtered.Skipped)
		return result, nil
	}
	for _, batch := range batches {
		response, err := s.remote.PushFiles(ctx, TeamSyncPushRequest{
			RepoSlug:      s.repoSlug,
			IfMatch:       strings.TrimSpace(s.state.ServerETag),
			Uploads:       batch.Uploads,
			Deletes:       batch.Deletes,
			BaseChecksums: cloneChecksumMap(s.state.ServerChecksums),
		})
		if err != nil {
			return result, err
		}
		if response.MaxEntries > 0 {
			s.state.ServerMaxEntries = response.MaxEntries
			if err := s.persistStateLocked(); err != nil {
				return result, err
			}
			result.LearnedMaxEntries = true
			if len(response.Applied) == 0 && len(response.Deleted) == 0 && len(response.Failed) == 0 && !response.Conflict {
				break
			}
		}
		if response.Conflict {
			if retried {
				result.Failed = mergeFailures(result.Failed, mergeFailures(response.Failed, skippedFailures(filtered.Skipped)))
				return result, nil
			}
			if _, err := s.pullLocked(ctx, TeamSyncTriggerConflict); err != nil {
				return result, err
			}
			retry, err := s.pushLocked(ctx, trigger, true)
			if err != nil {
				return retry, err
			}
			retry.Retried = true
			return retry, nil
		}
		if response.NotFound {
			result.Failed = mergeFailures(result.Failed, map[string]string{"remote": "not_found"})
			break
		}
		applyPushResponse(&s.state, response)
		result.Applied = result.Applied || len(response.Applied) > 0 || len(response.Deleted) > 0
		result.Failed = mergeFailures(result.Failed, response.Failed)
	}
	if checksumMapsEqual(localChecksums, s.state.ServerChecksums) {
		s.state.LastKnownChecksum = localTree
	}
	if err := s.persistStateLocked(); err != nil {
		return result, err
	}
	result.Failed = mergeFailures(result.Failed, skippedFailures(filtered.Skipped))
	if result.Applied {
		s.invalidateLocked(ctx, trigger)
	}
	return result, nil
}

func buildTeamSyncBatches(uploads map[string]string, deletes []string, limit int) []teamSyncBatch {
	if len(uploads) == 0 && len(deletes) == 0 {
		return nil
	}
	if limit <= 0 || len(uploads)+len(deletes) <= limit {
		return []teamSyncBatch{{Uploads: cloneStringMap(uploads), Deletes: append([]string(nil), deletes...)}}
	}
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
	for _, key := range uploadKeys {
		if count == limit {
			flush()
		}
		current.Uploads[key] = uploads[key]
		count++
	}
	for _, key := range deleteKeys {
		if count == limit {
			flush()
		}
		current.Deletes = append(current.Deletes, key)
		count++
	}
	flush()
	return batches
}

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
