package workspace

import (
	"context"
	"errors"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/workspace"
)

func (s *service) dryRunMerge(
	ctx context.Context,
	run *Run,
	req MergeRunRequest,
	updatedBy string,
) (*MergeRunResult, error) {
	mergingRun, err := s.transitionRunStatus(ctx, storeworkspace.TransitionRunStatusInput{
		RunKey:     run.RunKey,
		FromStatus: statusActive,
		Status:     statusMerging,
		UpdatedBy:  updatedBy,
		Metadata:   mergeMetadata(nil, req, ""),
	})
	if err != nil {
		return nil, err
	}
	s.emitRunStatusChanged(run.Status, mergingRun)
	result, dryRunErr := s.buildDryRunResult(ctx, run, req)
	message := ""
	if dryRunErr != nil {
		message = dryRunErr.Error()
	}
	activeRun, restoreErr := s.transitionRunStatus(ctx, storeworkspace.TransitionRunStatusInput{
		RunKey:     run.RunKey,
		FromStatus: statusMerging,
		Status:     statusActive,
		UpdatedBy:  updatedBy,
		Metadata:   mergeMetadata(result, req, message),
	})
	if restoreErr != nil {
		if dryRunErr != nil {
			return nil, errors.Join(dryRunErr, restoreErr)
		}
		return nil, restoreErr
	}
	s.emitRunStatusChanged(mergingRun.Status, activeRun)
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
