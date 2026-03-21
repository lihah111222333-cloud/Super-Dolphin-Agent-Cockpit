package workspace

import (
	"fmt"
	"io/fs"
	"path/filepath"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/store/workspace"
)

type removedWorkspaceFile struct {
	RelativePath    string
	WorkspaceSHA256 string
}

func (s *service) buildMergePlan(
	run *Run,
	files []RunFile,
	req MergeRunRequest,
) (*MergeRunResult, []storeworkspace.WorkspaceRunFile, error) {
	removed, err := s.collectRemovedWorkspaceFiles(run, files, req.DeleteRemoved)
	if err != nil {
		return nil, nil, err
	}
	result, updates := s.planMerge(run, files, removed, req.DryRun)
	return result, updates, nil
}

func (s *service) collectRemovedWorkspaceFiles(
	run *Run,
	files []RunFile,
	enabled bool,
) (map[string]removedWorkspaceFile, error) {
	if !enabled || len(files) == 0 {
		return nil, nil
	}
	tracked := trackedRunFilePaths(files)
	removed := make(map[string]removedWorkspaceFile, len(tracked))
	err := filepath.WalkDir(run.WorkspacePath, func(path string, entry fs.DirEntry, walkErr error) error {
		item, ok, err := classifyRemovedWorkspaceFile(run, tracked, path, entry, walkErr)
		if err != nil || !ok {
			return err
		}
		removed[item.RelativePath] = item
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk workspace for removed files: %w", err)
	}
	return removed, nil
}

func trackedRunFilePaths(files []RunFile) map[string]struct{} {
	paths := make(map[string]struct{}, len(files))
	for _, file := range files {
		paths[file.RelativePath] = struct{}{}
	}
	return paths
}

func classifyRemovedWorkspaceFile(
	run *Run,
	tracked map[string]struct{},
	path string,
	entry fs.DirEntry,
	walkErr error,
) (removedWorkspaceFile, bool, error) {
	if walkErr != nil {
		return removedWorkspaceFile{}, false, walkErr
	}
	if entry.IsDir() || !entry.Type().IsRegular() {
		return removedWorkspaceFile{}, false, nil
	}
	rel, err := filepath.Rel(run.WorkspacePath, path)
	if err != nil {
		return removedWorkspaceFile{}, false, err
	}
	rel, err = normalizeRelativePath(rel)
	if err != nil {
		return removedWorkspaceFile{}, false, err
	}
	if _, ok := tracked[rel]; !ok {
		return removedWorkspaceFile{}, false, nil
	}
	sourceHash, err := hashFileIfExists(filepath.Join(run.SourceRoot, rel))
	if err != nil || sourceHash != "" {
		return removedWorkspaceFile{}, false, err
	}
	workspaceHash, err := hashFile(path)
	if err != nil {
		return removedWorkspaceFile{}, false, err
	}
	return removedWorkspaceFile{RelativePath: rel, WorkspaceSHA256: workspaceHash}, true, nil
}
