package workspace

import (
	"context"
	"errors"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/sidecar/orch/store/workspace"
)

// dryRunMerge 处理dry运行记录merge。
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
