package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// HandleGetRun 读取单次 DAG 运行快照。
// run_key 可以来自 pos；找不到 run 时会转成双语错误，方便终端和聊天层直接展示。
func HandleGetRun(svc contract.DAGRuntime) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in GetRunInput) (any, error) {
		runKey, err := resolveRunKeyInput(in.RunKey, in.Pos)
		if err != nil {
			return nil, err
		}
		resp, err := svc.GetRun(ctx, contract.GetRunRequest{RunKey: runKey})
		if err != nil {
			return nil, translateGetRunError(runKey, err)
		}
		return resp, nil
	})
}

// translateGetRunError 将服务层哨兵错误转成用户可执行的提示。
func translateGetRunError(runKey string, err error) error {
	if errors.Is(err, orchestration.ErrRunNotFound) {
		return fmt.Errorf(
			"run 不存在：run_key=%s。请检查传入的 run_key 是否正确，或先调 task_start_dag 启动 run (run_key=%s, please verify the run_key or call task_start_dag first): %w",
			runKey, runKey, err,
		)
	}
	return err
}

// translateTerminateDAGError 保留 dag/run 双栅栏信息。
func translateTerminateDAGError(req contract.TerminateDAGRequest, err error) error {
	if errors.Is(err, orchestration.ErrRunNotFound) {
		return fmt.Errorf(
			"无法停止：run 不存在或已不可访问 (dag_key=%s, run_key=%s); cannot stop missing run (dag_key=%s, run_key=%s): %w",
			req.DagKey, req.RunKey, req.DagKey, req.RunKey, err,
		)
	}
	return err
}

// translateDeleteDAGError 将删除前置条件失败解释为可操作消息。
func translateDeleteDAGError(dagKey string, err error) error {
	if errors.Is(err, orchestration.ErrDAGAlreadyRunning) {
		return fmt.Errorf(
			"无法删除：任务流程仍有运行中的 run，请先停止或等待完成 (dag_key=%s); cannot delete DAG with an active run (dag_key=%s): %w",
			dagKey, dagKey, err,
		)
	}
	if errors.Is(err, orchestration.ErrDAGNotFound) {
		return fmt.Errorf(
			"无法删除：DAG 不存在 (dag_key=%s); cannot delete missing DAG (dag_key=%s): %w",
			dagKey, dagKey, err,
		)
	}
	return err
}

// translateStartDAGError 将启动阶段的幂等和并发冲突展开给调用方。
func translateStartDAGError(dagKey string, err error) error {
	var exhausted *orchestration.IdempotencyKeyExhaustedError
	if errors.As(err, &exhausted) {
		return fmt.Errorf(
			"幂等键已耗尽：上次 run 已失败/取消，请换新 idempotency_key 重试 (run_key=%s, status=%s); idempotency key exhausted: previous run is failed/cancelled, retry with a new idempotency_key (run_key=%s, status=%s): %w",
			exhausted.RunKey, exhausted.Status,
			exhausted.RunKey, exhausted.Status,
			err,
		)
	}
	if errors.Is(err, orchestration.ErrDAGAlreadyRunning) {
		return fmt.Errorf(
			"DAG 已有在跑 run，拒绝并发启动 (dag_key=%s); dag already has an active run, refusing concurrent start (dag_key=%s): %w",
			dagKey, dagKey, err,
		)
	}
	if errors.Is(err, orchestration.ErrDAGNotFound) {
		return fmt.Errorf(
			"DAG 不存在：dag_key=%s。请先调 task_create_dag 创建后再启动 (dag_key=%s, please call task_create_dag first): %w",
			dagKey, dagKey, err,
		)
	}
	return err
}
