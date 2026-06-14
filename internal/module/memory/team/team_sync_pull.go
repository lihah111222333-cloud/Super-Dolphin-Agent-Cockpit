package team

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// pullLocked 处理pulllocked。
func (s *TeamSyncService) pullLocked(ctx context.Context, trigger TeamSyncTrigger) (TeamSyncPullResult, error) {
	result := TeamSyncPullResult{}
	if !s.runtimeReadyLocked() {
		return result, nil
	}
	hashes, err := s.remote.PullHashes(ctx, s.repoSlug, s.state.ServerETag)
	if err != nil {
		return result, err
	}
	if hashes.NotFound {
		return s.handlePullNotFoundLocked(ctx, trigger)
	}
	if hashes.NotModified {
		return s.handlePullNotModifiedLocked(hashes.ETag, nil)
	}
	if checksumMapsEqual(hashes.Checksums, s.state.ServerChecksums) {
		return s.handlePullNotModifiedLocked(hashes.ETag, hashes.Checksums)
	}
	remote, err := s.remote.PullFiles(ctx, s.repoSlug, "")
	if err != nil {
		return result, err
	}
	if remote.NotFound {
		return s.handlePullNotFoundLocked(ctx, trigger)
	}
	return s.applyPulledFilesLocked(ctx, trigger, hashes, remote)
}

func (s *TeamSyncService) handlePullNotFoundLocked(ctx context.Context, trigger TeamSyncTrigger) (TeamSyncPullResult, error) {
	result := TeamSyncPullResult{NotFound: true}
	changed, err := s.clearLocalTeamRootLocked()
	if err != nil {
		return result, err
	}
	s.state = SyncState{}
	if err := s.persistStateLocked(); err != nil {
		return result, err
	}
	result.Cleared = changed
	result.Applied = changed
	if changed {
		s.invalidateLocked(ctx, trigger)
	}
	return result, nil
}

func (s *TeamSyncService) handlePullNotModifiedLocked(etag string, checksums map[string]string) (TeamSyncPullResult, error) {
	if len(checksums) > 0 {
		s.state.ServerChecksums = cloneChecksumMap(checksums)
	}
	s.state.ServerETag = firstNonEmptyTeamString(etag, s.state.ServerETag)
	if err := s.refreshLocalChecksumLocked(); err != nil {
		return TeamSyncPullResult{}, err
	}
	return TeamSyncPullResult{NotModified: true}, nil
}

func (s *TeamSyncService) applyPulledFilesLocked(
	ctx context.Context,
	trigger TeamSyncTrigger,
	hashes TeamSyncHashesResponse,
	remote TeamSyncPullResponse,
) (TeamSyncPullResult, error) {
	paths, local, err := s.applyRemoteFilesLocked(remote.Files)
	if err != nil {
		return TeamSyncPullResult{}, err
	}
	checksums := normalizeRemoteChecksums(remote.Checksums, remote.Files)
	if len(checksums) == 0 {
		checksums = localChecksumMap(local)
	}
	s.state.ServerChecksums = checksums
	s.state.ServerETag = firstNonEmptyTeamString(remote.ETag, hashes.ETag, s.state.ServerETag)
	s.state.LastKnownChecksum = checksumTree(localChecksumMap(local))
	if err := s.persistStateLocked(); err != nil {
		return TeamSyncPullResult{}, err
	}
	result := TeamSyncPullResult{Paths: paths, Applied: len(paths) > 0, NotModified: len(paths) == 0}
	if result.Applied {
		s.invalidateLocked(ctx, trigger)
	}
	return result, nil
}

// invalidateLocked reuses the same PromptAssemblyService.Invalidate primitive that
// thread RunPostCompactCleanup currently delegates to for Phase I-1 cleanup.
// invalidateLocked 处理invalidatelocked。
func (s *TeamSyncService) invalidateLocked(ctx context.Context, trigger TeamSyncTrigger) {
	if s == nil || s.invalidator == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	if err := s.invalidator.Invalidate(ctx, contract.InvalidateMemoryWrite); err != nil && s.logger != nil {
		s.logger.Warn("team sync invalidate failed", "trigger", trigger, "error", err)
	}
}

func (s *TeamSyncService) syncedChecksum() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.state.LastKnownChecksum)
}

func (s *TeamSyncService) scanCurrentLocalChecksum(root string) (string, error) {
	local, err := scanTeamMarkdownFiles(root)
	if err != nil {
		return "", err
	}
	return checksumTree(localChecksumMap(local)), nil
}

func (s *TeamSyncService) suppressWatcherWrites(paths []string) {
	if s == nil || s.watcher == nil || len(paths) == 0 {
		return
	}
	s.watcher.Suppress(paths...)
}

func (s *TeamSyncService) applyRemoteFilesLocked(remoteFiles map[string]TeamSyncFile) ([]string, map[string]teamSyncLocalFile, error) {
	current, err := scanTeamMarkdownFiles(s.root)
	if err != nil {
		return nil, nil, err
	}
	normalizedRemote := normalizeRemoteTeamSyncFiles(remoteFiles)
	changed := map[string]struct{}{}
	suppress, err := s.syncRemoteTeamFilesLocked(current, normalizedRemote, changed)
	if err != nil {
		return nil, nil, err
	}
	s.suppressWatcherWrites(suppress)
	if err := pruneEmptyTeamDirs(s.root); err != nil {
		return nil, nil, err
	}
	updated, err := scanTeamMarkdownFiles(s.root)
	if err != nil {
		return nil, nil, err
	}
	return sortedStringSet(changed), updated, nil
}

// normalizeRemoteTeamSyncFiles 规范化remoteteamsync文件。
func normalizeRemoteTeamSyncFiles(remoteFiles map[string]TeamSyncFile) map[string]TeamSyncFile {
	if len(remoteFiles) == 0 {
		return nil
	}
	normalized := make(map[string]TeamSyncFile, len(remoteFiles))
	for rel, file := range remoteFiles {
		rel = strings.TrimSpace(filepath.ToSlash(rel))
		if rel == "" || shouldIgnoreTeamSyncPath(rel) {
			continue
		}
		normalized[rel] = file
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func (s *TeamSyncService) syncRemoteTeamFilesLocked(
	current map[string]teamSyncLocalFile,
	remoteFiles map[string]TeamSyncFile,
	changed map[string]struct{},
) ([]string, error) {
	suppress, err := s.writeRemoteTeamFilesLocked(current, remoteFiles, changed)
	if err != nil {
		return nil, err
	}
	removed, err := s.removeMissingRemoteTeamFilesLocked(current, remoteFiles, changed)
	if err != nil {
		return nil, err
	}
	return append(suppress, removed...), nil
}

// writeRemoteTeamFilesLocked 写入remoteteam文件locked。
func (s *TeamSyncService) writeRemoteTeamFilesLocked(
	current map[string]teamSyncLocalFile,
	remoteFiles map[string]TeamSyncFile,
	changed map[string]struct{},
) ([]string, error) {
	suppress := make([]string, 0, len(remoteFiles))
	for rel, file := range remoteFiles {
		normalized, target, err := teamSyncTargetPath(s.root, rel)
		if err != nil {
			return nil, err
		}
		suppress = append(suppress, target)
		if existing, ok := current[normalized]; ok && existing.Content == file.Content {
			continue
		}
		if err := writeTeamSyncFile(target, file.Content); err != nil {
			return nil, err
		}
		changed[normalized] = struct{}{}
	}
	return suppress, nil
}

func (s *TeamSyncService) removeMissingRemoteTeamFilesLocked(
	current map[string]teamSyncLocalFile,
	remoteFiles map[string]TeamSyncFile,
	changed map[string]struct{},
) ([]string, error) {
	suppress := make([]string, 0, len(current))
	for rel, file := range current {
		if _, ok := remoteFiles[rel]; ok {
			continue
		}
		suppress = append(suppress, file.Path)
		if err := os.Remove(file.Path); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		changed[rel] = struct{}{}
	}
	return suppress, nil
}

// clearLocalTeamRootLocked 清理localteam根目录locked。
func (s *TeamSyncService) clearLocalTeamRootLocked() (bool, error) {
	files, err := scanTeamMarkdownFiles(s.root)
	if err != nil {
		return false, err
	}
	if len(files) == 0 {
		return false, nil
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	s.suppressWatcherWrites(paths)
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return false, err
		}
	}
	if err := pruneEmptyTeamDirs(s.root); err != nil {
		return false, err
	}
	return true, nil
}
