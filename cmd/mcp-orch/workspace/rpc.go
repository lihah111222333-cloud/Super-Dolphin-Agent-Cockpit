package workspace

import (
	"context"
	"errors"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// NewWorkspaceHandlers 创建工作区处理器。
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
	return typedRPCAdapter(func(ctx context.Context, p createRunParams) (runResult, error) {
		run, err := svc.CreateRun(ctx, p)
		if err != nil {
			return runResult{}, err
		}
		return runResult{Run: run}, nil
	}, validateCreateRunParams)
}

func handleGetRun(svc Service) func(context.Context, runKeyParams) (runResult, error) {
	return typedRPCAdapter(func(ctx context.Context, p runKeyParams) (runResult, error) {
		run, err := svc.GetRun(ctx, p.RunKey)
		if err != nil {
			return runResult{}, err
		}
		return runResult{Run: run}, nil
	}, validateRunKeyParams)
}

func handleListRuns(svc Service) func(context.Context, listRunsParams) (runsResult, error) {
	return typedRPCAdapter(func(ctx context.Context, p listRunsParams) (runsResult, error) {
		runs, err := svc.ListRuns(ctx, p.Status, p.DagKey, p.Limit)
		if err != nil {
			return runsResult{}, err
		}
		return runsResult{Runs: runs}, nil
	})
}

func handleMergeRun(svc Service) func(context.Context, mergeRunParams) (mergeResult, error) {
	return typedRPCAdapter(func(ctx context.Context, p mergeRunParams) (mergeResult, error) {
		result, err := svc.MergeRun(ctx, mergeRunRequestFromParams(p))
		if err != nil {
			return mergeResult{}, err
		}
		return mergeResult{Result: result}, nil
	}, validateMergeRunParams)
}

func mergeRunRequestFromParams(p mergeRunParams) MergeRunRequest {
	return MergeRunRequest(p)
}

func handleAbortRun(svc Service) func(context.Context, abortRunParams) (runResult, error) {
	return typedRPCAdapter(func(ctx context.Context, p abortRunParams) (runResult, error) {
		if err := svc.AbortRun(ctx, p.RunKey, p.UpdatedBy, p.Reason); err != nil {
			return runResult{}, err
		}
		run, err := svc.GetRun(ctx, p.RunKey)
		if err != nil {
			return runResult{}, err
		}
		return runResult{Run: run}, nil
	}, validateAbortRunParams)
}

func handleListRunFiles(svc Service) func(context.Context, listRunFilesParams) (runFilesResult, error) {
	return typedRPCAdapter(func(ctx context.Context, p listRunFilesParams) (runFilesResult, error) {
		files, err := svc.ListRunFiles(ctx, p.RunKey, p.State)
		if err != nil {
			return runFilesResult{}, err
		}
		return runFilesResult{Files: files}, nil
	}, validateListRunFilesParams)
}

func handleGetRunFile(svc Service) func(context.Context, runFileParams) (runFileResult, error) {
	return typedRPCAdapter(func(ctx context.Context, p runFileParams) (runFileResult, error) {
		file, err := svc.GetRunFile(ctx, p.RunKey, p.Path)
		if err != nil {
			return runFileResult{}, err
		}
		return runFileResult{File: file}, nil
	}, validateRunFileParams)
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
