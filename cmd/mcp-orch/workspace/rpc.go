package workspace

import (
	"context"
	"errors"
	"strings"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// NewWorkspaceHandlers 注册 workspace JSON-RPC handlers。
// 所有 handler 都走 StrictHandler 和 typedRPCAdapter，先校验参数再进服务层。
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

// handleCreateRun 处理 workspace/run/create RPC。
// typedRPCAdapter 会先校验 source_root 等必填参数，避免空请求进入服务层并产生半初始化 run。
func handleCreateRun(svc Service) func(context.Context, createRunParams) (runResult, error) {
	return typedRPCAdapter(func(ctx context.Context, p createRunParams) (runResult, error) {
		run, err := svc.CreateRun(ctx, p)
		if err != nil {
			return runResult{}, err
		}
		return runResult{Run: run}, nil
	}, validateCreateRunParams)
}

// handleGetRun 处理 workspace/run/get RPC。
// run_key 为空会在适配层 fail-fast，服务层只接收已定位的读取请求。
func handleGetRun(svc Service) func(context.Context, runKeyParams) (runResult, error) {
	return typedRPCAdapter(func(ctx context.Context, p runKeyParams) (runResult, error) {
		run, err := svc.GetRun(ctx, p.RunKey)
		if err != nil {
			return runResult{}, err
		}
		return runResult{Run: run}, nil
	}, validateRunKeyParams)
}

// handleListRuns 处理 workspace/run/list RPC。
// 列表路径允许空过滤条件，limit 归一化由服务层统一处理。
func handleListRuns(svc Service) func(context.Context, listRunsParams) (runsResult, error) {
	return typedRPCAdapter(func(ctx context.Context, p listRunsParams) (runsResult, error) {
		runs, err := svc.ListRuns(ctx, p.Status, p.DagKey, p.Limit)
		if err != nil {
			return runsResult{}, err
		}
		return runsResult{Runs: runs}, nil
	})
}

// handleMergeRun 处理 workspace/run/merge RPC。
// merge 参数先转为服务层请求，冲突检测、dry-run 和删除保护都集中在 Service。
func handleMergeRun(svc Service) func(context.Context, mergeRunParams) (mergeResult, error) {
	return typedRPCAdapter(func(ctx context.Context, p mergeRunParams) (mergeResult, error) {
		result, err := svc.MergeRun(ctx, mergeRunRequestFromParams(p))
		if err != nil {
			return mergeResult{}, err
		}
		return mergeResult{Result: result}, nil
	}, validateMergeRunParams)
}

// mergeRunRequestFromParams 将 RPC 参数转换为服务层 merge 请求。
func mergeRunRequestFromParams(p mergeRunParams) MergeRunRequest {
	return MergeRunRequest(p)
}

// handleAbortRun 处理 workspace/run/abort RPC。
// abort 后回读 run，确保响应反映持久化后的状态。
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

// handleListRunFiles 处理 workspace/run/files/list RPC。
// state 只是过滤条件，run_key 必须先通过校验，避免跨 run 泄漏文件状态。
func handleListRunFiles(svc Service) func(context.Context, listRunFilesParams) (runFilesResult, error) {
	return typedRPCAdapter(func(ctx context.Context, p listRunFilesParams) (runFilesResult, error) {
		files, err := svc.ListRunFiles(ctx, p.RunKey, p.State)
		if err != nil {
			return runFilesResult{}, err
		}
		return runFilesResult{Files: files}, nil
	}, validateListRunFilesParams)
}

// handleGetRunFile 处理 workspace/run/file/get RPC。
// run_key 和 path 都是必填边界，路径合法性仍由服务层/工厂统一判定。
func handleGetRunFile(svc Service) func(context.Context, runFileParams) (runFileResult, error) {
	return typedRPCAdapter(func(ctx context.Context, p runFileParams) (runFileResult, error) {
		file, err := svc.GetRunFile(ctx, p.RunKey, p.Path)
		if err != nil {
			return runFileResult{}, err
		}
		return runFileResult{File: file}, nil
	}, validateRunFileParams)
}

// required 校验字符串参数非空。
// 校验发生在 RPC 入口，确保服务层不需要重复处理空定位符。
func required(value, field string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New(field + " is required")
	}
	return nil
}

// required2 校验两个字符串参数都非空。
// 失败时保留第一个缺失字段名，方便 RPC 调用方直接修正参数。
func required2(left, leftName, right, rightName string) error {
	if err := required(left, leftName); err != nil {
		return err
	}
	return required(right, rightName)
}
