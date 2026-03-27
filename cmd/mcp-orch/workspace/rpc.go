package workspace

import (
	"context"
	"errors"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func NewWorkspaceHandlers(svc Service) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"workspace/run/create":     rpc.StrictHandler(handleCreateRun(svc)),
		"workspace/run/get":        rpc.StrictHandler(handleGetRun(svc)),
		"workspace/run/list":       rpc.StrictHandler(handleListRuns(svc)),
		"workspace/run/merge":      rpc.StrictHandler(handleMergeRun(svc)),
		"workspace/run/abort":      rpc.StrictHandler(handleAbortRun(svc)),
		"workspace/run/files/list": rpc.StrictHandler(handleListRunFiles(svc)),
		"workspace/run/file/get":   rpc.StrictHandler(handleGetRunFile(svc)),
	}}
}

func handleCreateRun(svc Service) func(context.Context, createRunParams) (runResult, error) {
	return func(ctx context.Context, p createRunParams) (runResult, error) {
		if err := required(p.SourceRoot, "source_root"); err != nil {
			return runResult{}, err
		}
		run, err := svc.CreateRun(ctx, p)
		if err != nil {
			return runResult{}, err
		}
		return runResult{Run: run}, nil
	}
}

func handleGetRun(svc Service) func(context.Context, runKeyParams) (runResult, error) {
	return func(ctx context.Context, p runKeyParams) (runResult, error) {
		if err := required(p.RunKey, "run_key"); err != nil {
			return runResult{}, err
		}
		run, err := svc.GetRun(ctx, p.RunKey)
		if err != nil {
			return runResult{}, err
		}
		return runResult{Run: run}, nil
	}
}

func handleListRuns(svc Service) func(context.Context, listRunsParams) (runsResult, error) {
	return func(ctx context.Context, p listRunsParams) (runsResult, error) {
		runs, err := svc.ListRuns(ctx, p.Status, p.DagKey, p.Limit)
		if err != nil {
			return runsResult{}, err
		}
		return runsResult{Runs: runs}, nil
	}
}

func handleMergeRun(svc Service) func(context.Context, mergeRunParams) (mergeResult, error) {
	return func(ctx context.Context, p mergeRunParams) (mergeResult, error) {
		if err := required(p.RunKey, "run_key"); err != nil {
			return mergeResult{}, err
		}
		result, err := svc.MergeRun(ctx, mergeRunRequestFromParams(p))
		if err != nil {
			return mergeResult{}, err
		}
		return mergeResult{Result: result}, nil
	}
}

func mergeRunRequestFromParams(p mergeRunParams) MergeRunRequest {
	return MergeRunRequest{
		RunKey:        p.RunKey,
		UpdatedBy:     p.UpdatedBy,
		DryRun:        p.DryRun,
		DeleteRemoved: p.DeleteRemoved,
	}
}

func handleAbortRun(svc Service) func(context.Context, abortRunParams) (runResult, error) {
	return func(ctx context.Context, p abortRunParams) (runResult, error) {
		if err := required(p.RunKey, "run_key"); err != nil {
			return runResult{}, err
		}
		if err := svc.AbortRun(ctx, p.RunKey, p.UpdatedBy, p.Reason); err != nil {
			return runResult{}, err
		}
		run, err := svc.GetRun(ctx, p.RunKey)
		if err != nil {
			return runResult{}, err
		}
		return runResult{Run: run}, nil
	}
}

func handleListRunFiles(svc Service) func(context.Context, listRunFilesParams) (runFilesResult, error) {
	return func(ctx context.Context, p listRunFilesParams) (runFilesResult, error) {
		if err := required(p.RunKey, "run_key"); err != nil {
			return runFilesResult{}, err
		}
		files, err := svc.ListRunFiles(ctx, p.RunKey, p.State)
		if err != nil {
			return runFilesResult{}, err
		}
		return runFilesResult{Files: files}, nil
	}
}

func handleGetRunFile(svc Service) func(context.Context, runFileParams) (runFileResult, error) {
	return func(ctx context.Context, p runFileParams) (runFileResult, error) {
		if err := required2(p.RunKey, "run_key", p.Path, "path"); err != nil {
			return runFileResult{}, err
		}
		file, err := svc.GetRunFile(ctx, p.RunKey, p.Path)
		if err != nil {
			return runFileResult{}, err
		}
		return runFileResult{File: file}, nil
	}
}

func required(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New(field + " is required")
	}
	return nil
}

func required2(left, leftName, right, rightName string) error {
	if err := required(left, leftName); err != nil {
		return err
	}
	return required(right, rightName)
}
