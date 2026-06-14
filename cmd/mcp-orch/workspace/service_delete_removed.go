package workspace

import (
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

// collectRemovedWorkspaceFiles 收集removed工作区文件。
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
		inspected, err := inspectRunFile(run, file.RelativePath)
		if err != nil {
			return nil, err
		}
		if inspected.WorkspaceExists {
			continue
		}
		removed[inspected.RelativePath] = removedWorkspaceFile{
			RelativePath:       inspected.RelativePath,
			SourceSHA256Before: inspected.SourceSHA256,
		}
	}
	return removed, nil
}
