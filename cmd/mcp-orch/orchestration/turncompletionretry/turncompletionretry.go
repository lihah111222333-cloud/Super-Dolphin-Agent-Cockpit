package turncompletionretry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

// turn completion retry 内部 wakeup 的固定 kind 和目标。
const (
	WakeupKind    = "turn_complete_retry"
	TargetAgentID = "mcp-orch"
)

// Enqueuer 是 turn.completed subscriber 入队内部修复 wakeup 所需的窄接口。
// 真正重放“完成节点”由 dispatcher 调 Complete 做。
type Enqueuer interface {
	EnqueueWakeup(context.Context, taskdag.EnqueueWakeupInput) (int64, error)
}

// CompleteOutcome 是内部 completion retry 的处理结果枚举。
type CompleteOutcome string

// CompleteOutcome 结果常量。
const (
	CompleteSucceeded       CompleteOutcome = "succeeded"
	CompleteAlreadyTerminal CompleteOutcome = "already_terminal"
	CompleteRetry           CompleteOutcome = "retry"
	CompleteInvalid         CompleteOutcome = "invalid"
)

// CompleteResult 返回 completion retry 重放结果和需要继续 retry 的错误。
type CompleteResult struct {
	Outcome CompleteOutcome
	Result  *taskdag.CompleteNodeWithDownstreamResult
	Err     error
}

// Enqueue 把 turn.completed 的 result 存成内部 wakeup。
// idempotency_key 按 dag/run/node 固定，重复投递只会有一条修复任务。
func Enqueue(ctx context.Context, flow any, node *taskdag.Node, result json.RawMessage) error {
	enqueuer, ok := flow.(Enqueuer)
	if !ok {
		return errors.New("taskdag wakeup enqueue port not wired")
	}
	runID := nodeRunID(node)
	if runID <= 0 {
		return errors.New("runtime run_id required for turn.completed completion retry")
	}
	if len(result) == 0 {
		return errors.New("turn.completed completion retry result is empty")
	}
	if !json.Valid(result) {
		return errors.New("turn.completed completion retry result is not valid JSON")
	}
	_, err := enqueuer.EnqueueWakeup(ctx, taskdag.EnqueueWakeupInput{
		DagKey: node.DagKey, NodeKey: node.NodeKey, RunID: runID, WakeupKind: WakeupKind,
		TargetAgentID: TargetAgentID, PromptPayload: append(json.RawMessage(nil), result...),
		IdempotencyKey: IdempotencyKey(node.DagKey, node.NodeKey, runID),
	})
	return err
}

// IsWakeup 判断 wakeup 是否为 turn.completed completion retry 内部任务。
func IsWakeup(w *taskdag.Wakeup) bool {
	return w != nil && strings.TrimSpace(w.WakeupKind) == WakeupKind
}

// CompleteInput 从内部 wakeup 还原 CompleteNodeInput。
// dag/node/run_id 都必须在，避免把旧结果写到模板或别的 run。
func CompleteInput(w *taskdag.Wakeup) (taskdag.CompleteNodeInput, error) {
	if w == nil {
		return taskdag.CompleteNodeInput{}, errors.New("nil wakeup")
	}
	runID := wakeupRunID(w)
	dagKey, nodeKey := strings.TrimSpace(w.DagKey), strings.TrimSpace(w.NodeKey)
	if runID <= 0 {
		return taskdag.CompleteNodeInput{}, fmt.Errorf("wakeup %d missing runtime run_id", w.ID)
	}
	if dagKey == "" || nodeKey == "" {
		return taskdag.CompleteNodeInput{}, fmt.Errorf("wakeup %d missing dag_key/node_key", w.ID)
	}
	if len(w.PromptPayload) == 0 || !json.Valid(w.PromptPayload) {
		return taskdag.CompleteNodeInput{}, fmt.Errorf("wakeup %d invalid turn.completed completion retry payload", w.ID)
	}
	return taskdag.CompleteNodeInput{Status: "done", Result: append(json.RawMessage(nil), w.PromptPayload...), DagKey: dagKey, NodeKey: nodeKey, RunID: runID}, nil
}

// Complete 由 dispatcher 调用，用于重放“完成节点 + 调度下游”。
// 节点已终态就当成功收掉 wakeup；其它错误继续 retry。
func Complete(ctx context.Context, store any, w *taskdag.Wakeup) CompleteResult {
	input, err := CompleteInput(w)
	if err != nil {
		return CompleteResult{Outcome: CompleteInvalid, Err: err}
	}
	flow, ok := store.(taskdag.NodeFlowStore)
	if !ok {
		return CompleteResult{Outcome: CompleteInvalid, Err: errors.New("store missing NodeFlowStore for turn.completed completion retry")}
	}
	res, err := flow.CompleteNodeAndScheduleDownstream(ctx, input)
	switch {
	case err == nil:
		return CompleteResult{Outcome: CompleteSucceeded, Result: res}
	case errors.Is(err, sql.ErrNoRows) || platformdb.IsNotFound(err):
		return CompleteResult{Outcome: CompleteAlreadyTerminal}
	default:
		return CompleteResult{Outcome: CompleteRetry, Err: err}
	}
}

// IdempotencyKey 生成重试任务的幂等键。
func IdempotencyKey(dagKey, nodeKey string, runID int64) string {
	return "dag/" + dagKey + "/run/" + strconv.FormatInt(runID, 10) + "/" + nodeKey + "/turn-complete-retry"
}

// nodeRunID 从 runtime node 中读取 run_id，缺失时返回 0 让调用方 fail-fast。
func nodeRunID(node *taskdag.Node) int64 {
	if node == nil || node.RunID == nil {
		return 0
	}
	return *node.RunID
}

// wakeupRunID 从内部 wakeup 中读取 run_id，缺失时返回 0 让调用方 fail-fast。
func wakeupRunID(w *taskdag.Wakeup) int64 {
	if w == nil || w.RunID == nil {
		return 0
	}
	return *w.RunID
}
