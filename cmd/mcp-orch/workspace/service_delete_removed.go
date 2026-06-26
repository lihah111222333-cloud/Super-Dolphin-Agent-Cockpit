package workspace

import (
	storeworkspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/workspace"
)

// removedWorkspaceFile 保存 workspace 中已删除文件的源端快照。
type removedWorkspaceFile struct {
	RelativePath       string
	SourceSHA256Before string
}

// buildMergePlan 构建本次 merge 的文件计划。
// DeleteRemoved 会先收集 workspace 缺失文件，再统一交给 planMerge 判定冲突。
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

// collectRemovedWorkspaceFiles 收集 workspace 中已删除且允许同步删除的文件。
// 未开启 delete_removed 时返回 nil，避免误把缺失文件当删除意图。
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
