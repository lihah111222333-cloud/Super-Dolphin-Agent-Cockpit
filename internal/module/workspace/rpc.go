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
		"workspace/run/create":        rpc.StrictHandler(handleCreateRun(svc)),
		"workspace/run/get":           rpc.StrictHandler(handleGetRun(svc)),
		"workspace/run/list":          rpc.StrictHandler(handleListRuns(svc)),
		"workspace/run/status/update": rpc.StrictHandler(handleUpdateRunStatus(svc)),
		"workspace/run/merge":         rpc.StrictHandler(handleMergeRun(svc)),
		"workspace/run/abort":         rpc.StrictHandler(handleAbortRun(svc)),
		"workspace/run/files/list":    rpc.StrictHandler(handleListRunFiles(svc)),
		"workspace/run/file/get":      rpc.StrictHandler(handleGetRunFile(svc)),
	}}
}

func handleCreateRun(svc Service) func(context.Context, createRunParams) (runResult, error) {
	return func(ctx context.Context, p createRunParams) (runResult, error) {
		if err := required(p.SourceRoot, "sourceRoot"); err != nil {
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
		if err := required(p.RunKey, "runKey"); err != nil {
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

func handleUpdateRunStatus(svc Service) func(context.Context, updateRunStatusParams) (runResult, error) {
	return func(ctx context.Context, p updateRunStatusParams) (runResult, error) {
		if err := required2(p.RunKey, "runKey", p.Status, "status"); err != nil {
			return runResult{}, err
		}
		run, err := svc.UpdateRunStatus(ctx, p.RunKey, p.Status)
		if err != nil {
			return runResult{}, err
		}
		return runResult{Run: run}, nil
	}
}

func handleMergeRun(svc Service) func(context.Context, runKeyParams) (runResult, error) {
	return func(ctx context.Context, p runKeyParams) (runResult, error) {
		if err := required(p.RunKey, "runKey"); err != nil {
			return runResult{}, err
		}
		run, err := svc.MergeRun(ctx, p.RunKey)
		if err != nil {
			return runResult{}, err
		}
		return runResult{Run: run}, nil
	}
}

func handleAbortRun(svc Service) func(context.Context, runKeyParams) (runResult, error) {
	return func(ctx context.Context, p runKeyParams) (runResult, error) {
		if err := required(p.RunKey, "runKey"); err != nil {
			return runResult{}, err
		}
		if err := svc.AbortRun(ctx, p.RunKey); err != nil {
			return runResult{}, err
		}
		run, err := svc.GetRun(ctx, p.RunKey)
		if err != nil {
			return runResult{}, err
		}
		return runResult{Run: run}, nil
	}
}

func handleListRunFiles(svc Service) func(context.Context, runKeyParams) (runFilesResult, error) {
	return func(ctx context.Context, p runKeyParams) (runFilesResult, error) {
		if err := required(p.RunKey, "runKey"); err != nil {
			return runFilesResult{}, err
		}
		files, err := svc.ListRunFiles(ctx, p.RunKey)
		if err != nil {
			return runFilesResult{}, err
		}
		return runFilesResult{Files: files}, nil
	}
}

func handleGetRunFile(svc Service) func(context.Context, runFileParams) (runFileResult, error) {
	return func(ctx context.Context, p runFileParams) (runFileResult, error) {
		if err := required2(p.RunKey, "runKey", p.Path, "path"); err != nil {
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
