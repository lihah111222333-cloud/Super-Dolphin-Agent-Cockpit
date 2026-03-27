package workspace

import (
	"fmt"
	"path/filepath"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/workspace"
)

type removedWorkspaceFile struct {
	RelativePath       string
	SourceSHA256Before string
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
	removed := make(map[string]removedWorkspaceFile, len(files))
	for _, file := range files {
		rel, err := normalizeRelativePath(file.RelativePath)
		if err != nil {
			return nil, err
		}
		workspaceHash, err := hashFileIfExists(filepath.Join(run.WorkspacePath, rel))
		if err != nil {
			return nil, fmt.Errorf("hash workspace file %q: %w", rel, err)
		}
		if workspaceHash != "" {
			continue
		}
		sourceHash, err := hashFileIfExists(filepath.Join(run.SourceRoot, rel))
		if err != nil {
			return nil, fmt.Errorf("hash source file %q: %w", rel, err)
		}
		removed[file.RelativePath] = removedWorkspaceFile{
			RelativePath:       rel,
			SourceSHA256Before: sourceHash,
		}
	}
	return removed, nil
}
