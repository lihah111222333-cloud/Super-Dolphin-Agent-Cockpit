package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// HandleGetRun 处理get运行记录。
func HandleGetRun(svc contract.OrchestrationService) ToolHandler {
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

func translateGetRunError(runKey string, err error) error {
	if errors.Is(err, orchestration.ErrRunNotFound) {
		return fmt.Errorf(
			"run 不存在：run_key=%s。请检查传入的 run_key 是否正确，或先调 task_start_dag 启动 run (run_key=%s, please verify the run_key or call task_start_dag first): %w",
			runKey, runKey, err,
		)
	}
	return err
}

func translateTerminateDAGError(req contract.TerminateDAGRequest, err error) error {
	if errors.Is(err, orchestration.ErrRunNotFound) {
		return fmt.Errorf(
			"无法停止：run 不存在或已不可访问 (dag_key=%s, run_key=%s); cannot stop missing run (dag_key=%s, run_key=%s): %w",
			req.DagKey, req.RunKey, req.DagKey, req.RunKey, err,
		)
	}
	return err
}

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
