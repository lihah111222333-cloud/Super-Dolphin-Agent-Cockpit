package workspace

import (
	"context"
	"errors"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/workspace"
)

// dryRunMerge 在不写 source 文件的情况下评估 merge 结果。
// 它会短暂进入 merging 再恢复 active，保证和真实 merge 共用状态栅栏。
func (s *service) dryRunMerge(
	ctx context.Context,
	run *Run,
	req MergeRunRequest,
	updatedBy string,
) (*MergeRunResult, error) {
	mergingRun, err := s.transitionMergeRun(ctx, run, statusActive, statusMerging, req, updatedBy, nil, "")
	if err != nil {
		return nil, err
	}
	result, dryRunErr := s.buildDryRunResult(ctx, run, req)
	message := ""
	if dryRunErr != nil {
		message = dryRunErr.Error()
	}
	activeRun, restoreErr := s.transitionMergeRun(ctx, mergingRun, statusMerging, statusActive, req, updatedBy, result, message)
	if restoreErr != nil {
		if dryRunErr != nil {
			return nil, errors.Join(dryRunErr, restoreErr)
		}
		return nil, restoreErr
	}
	if dryRunErr != nil {
		return nil, dryRunErr
	}
	result.Status = activeRun.Status
	return result, nil
}

// buildDryRunResult 读取 run 文件并构造 dry-run 结果。
func (s *service) buildDryRunResult(
	ctx context.Context,
	run *Run,
	req MergeRunRequest,
) (*MergeRunResult, error) {
	files, err := s.store.ListFiles(ctx, storeworkspace.ListFilesFilter{
		RunKey: run.RunKey,
		Limit:  mergeListLimit,
	})
	if err != nil {
		return nil, err
	}
	result, _, err := s.buildMergePlan(run, files, req)
	if err != nil {
		return nil, err
	}
	result.DryRun = req.DryRun
	result.Status = run.Status
	return result, nil
}
