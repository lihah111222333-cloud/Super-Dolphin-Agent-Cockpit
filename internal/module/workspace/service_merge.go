package workspace

import (
	"context"
	"errors"

	storeworkspace "github.com/anthropic-ai/super-agent-v3/internal/store/workspace"
)

func (s *service) executeMerge(
	ctx context.Context,
	run *Run,
	req MergeRunRequest,
	updatedBy string,
) (*MergeRunResult, error) {
	emptyResult, _ := s.planMerge(run, nil)
	files, err := s.store.ListFiles(ctx, storeworkspace.ListFilesFilter{
		RunKey: run.RunKey,
		Limit:  mergeListLimit,
	})
	if err != nil {
		return nil, s.failMergeRun(ctx, run, req, emptyResult, nil, updatedBy, err)
	}
	result, updates := s.planMerge(run, files)
	result.DryRun = req.DryRun
	if req.DeleteRemoved {
		// TODO(p2-r2): restore full V2 deleteRemoved semantics once merge walks the workspace tree again.
	}
	if err := s.applyFileUpdates(ctx, updates); err != nil {
		return nil, s.failMergeRun(ctx, run, req, result, files, updatedBy, err)
	}
	if result.Conflicts > 0 || result.Errors > 0 {
		failedRun, err := s.transitionMergeFailed(ctx, run, req, result, updatedBy, "")
		if err != nil {
			return nil, err
		}
		result.Status = failedRun.Status
		s.emitRunStatusChanged(run.Status, failedRun)
		s.emitRunMergeErrorEvent(failedRun, result, updatedBy, "")
		return result, nil
	}
	mergedRun, err := s.transitionRunStatus(ctx, storeworkspace.TransitionRunStatusInput{
		RunKey:     run.RunKey,
		FromStatus: statusMerging,
		Status:     statusMerged,
		UpdatedBy:  updatedBy,
		Metadata:   mergeMetadata(result, req, ""),
	})
	if err != nil {
		return nil, s.failMergeRun(ctx, run, req, result, files, updatedBy, err)
	}
	result.Status = mergedRun.Status
	s.emitRunStatusChanged(run.Status, mergedRun)
	s.emitRunMergedEvent(mergedRun, result.Merged)
	return result, nil
}

func (s *service) transitionMergeFailed(
	ctx context.Context,
	run *Run,
	req MergeRunRequest,
	result *MergeRunResult,
	updatedBy, message string,
) (*Run, error) {
	failedRun, err := s.transitionRunStatus(ctx, storeworkspace.TransitionRunStatusInput{
		RunKey:     run.RunKey,
		FromStatus: statusMerging,
		Status:     statusFailed,
		UpdatedBy:  updatedBy,
		Metadata:   mergeMetadata(result, req, message),
	})
	if err != nil {
		s.emitRunMergeErrorEvent(run, result, updatedBy, message)
		return nil, err
	}
	return failedRun, nil
}

func (s *service) failMergeRun(
	ctx context.Context,
	run *Run,
	req MergeRunRequest,
	result *MergeRunResult,
	files []RunFile,
	updatedBy string,
	cause error,
) error {
	mergeErr := s.rollbackMergeState(ctx, files, cause)
	failedRun, err := s.transitionMergeFailed(ctx, run, req, result, updatedBy, mergeErr.Error())
	if err != nil {
		return errors.Join(mergeErr, err)
	}
	result.Status = failedRun.Status
	s.emitRunStatusChanged(run.Status, failedRun)
	s.emitRunMergeErrorEvent(failedRun, result, updatedBy, mergeErr.Error())
	return mergeErr
}
