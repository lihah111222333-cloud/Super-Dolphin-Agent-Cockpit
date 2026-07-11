package team

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// pullLocked 在持有 s.mu 时执行一次远端拉取。
// 先用 hash/ETag 快速判断是否需要下载文件，NotFound 会清空本地同步状态并触发 prompt 失效。
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

// handlePullNotFoundLocked 收口远端 NotFound：清空本地团队记忆文件和同步状态，避免继续沿用旧 ETag/checksum。
// 它不能改 watcher、session 绑定等运行态；只有实际删除文件时才标记 Applied/Cleared 并触发 prompt 失效。
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

// handlePullNotModifiedLocked 刷新远端 ETag 和本地 checksum，但不改动文件内容。
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

// applyPulledFilesLocked 把远端文件集应用到本地目录并更新同步状态。
// 只有文件实际变化时才标记 Applied 并通知 prompt 失效。
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

// invalidateLocked 在团队记忆内容变化后通知 prompt 组装缓存失效。
// 使用 WithoutCancel 脱离原请求取消，确保同步完成后的缓存失效仍能落地；失败只记录日志。
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

// syncedChecksum 返回最近一次持久化的本地 checksum。
func (s *TeamSyncService) syncedChecksum() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.state.LastKnownChecksum)
}

// scanCurrentLocalChecksum 重新扫描 root 下团队记忆文件并计算 checksum。
func (s *TeamSyncService) scanCurrentLocalChecksum(root string) (string, error) {
	local, err := scanTeamMarkdownFiles(root)
	if err != nil {
		return "", err
	}
	return checksumTree(localChecksumMap(local)), nil
}

// suppressWatcherWrites 暂时屏蔽当前 watcher 对指定路径的本地写事件。
// 远端拉取写盘会触发 fsnotify，这里避免把同步自身的写入误判为用户本地修改。
func (s *TeamSyncService) suppressWatcherWrites(paths []string) {
	if s == nil || s.watcher == nil || len(paths) == 0 {
		return
	}
	s.watcher.Suppress(paths...)
}

// applyRemoteFilesLocked 将远端文件快照同步到本地，并返回变化后的本地扫描结果。
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

// normalizeRemoteTeamSyncFiles 规范化远端文件路径，并跳过内部状态文件。
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

// syncRemoteTeamFilesLocked 先写入远端存在的文件，再删除远端已不存在的本地文件。
// 返回的 suppress 路径用于让 watcher 忽略这次同步造成的文件事件。
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

// writeRemoteTeamFilesLocked 写入远端文件快照中新增或内容变化的文件。
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

// removeMissingRemoteTeamFilesLocked 删除远端快照中已不存在的本地团队记忆文件。
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

// clearLocalTeamRootLocked 清空本地团队记忆 Markdown 文件。
// 删除前会 suppress watcher，避免远端 NotFound 清理动作被重新推回远端。
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
