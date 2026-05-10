package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

// =====================================================
// DAG v2 T3.1: task_get_run service 实现
// DAG v2 T3.1: task_get_run service implementation
// =====================================================
//
// service.GetRun 接通 RunStore.GetRun(run_key)，把存储域 Run 转换为
// contract.Run DTO 返回给 MCP 调用方。节点信息不内联（用户决策 A2）：
// 调用方需另查 task_get_dag 拿 DAG 模板 + 节点。
//
// service.GetRun wires through RunStore.GetRun(run_key) and converts the
// storage-domain Run into a contract.Run DTO for MCP callers. Nodes are
// intentionally not inlined (user decision A2): callers fetch the DAG
// template + nodes via task_get_dag.

// ErrRunNotFound 表示 GetRun 调用时 run_key 不存在。
// 与 ErrDAGNotFound 对齐：service 层 sentinel + tool 层中英双语转译。
//
// ErrRunNotFound is returned by GetRun when the supplied run_key does not
// exist. Mirrors ErrDAGNotFound's pattern: service-layer sentinel +
// bilingual translation at the tool layer.
var ErrRunNotFound = errors.New("orchestration: run_key not found")

// GetRun 按 run_key 取一条 task_dag_runs。
//
// 防御性检查与 StartDAG 保持一致：service 自身或 runStore 未注入时返
// ErrRunStoreUnset / ErrLifecycleNotImplemented；运行时调用路径上
// ProvideService 的 setter 注入会保证两者非 nil（fx 提供路线 N 后已守护）。
//
// 错误转译：runStore 返回的域错误若 IsNotFound 命中 → 包装 ErrRunNotFound
// sentinel；其他错误透传并附 run_key 上下文。
//
// GetRun fetches a single task_dag_runs row by run_key.
//
// Defensive checks mirror StartDAG: if the service or runStore is unset, we
// return ErrLifecycleNotImplemented / ErrRunStoreUnset respectively. In the
// production path ProvideService's setter injection guarantees both are
// non-nil (route N's fx wiring guards that already).
//
// Error translation: when runStore returns a domain error matched by
// IsNotFound we wrap ErrRunNotFound; other errors are passed through with
// the run_key for context.
func (s *service) GetRun(ctx context.Context, req contract.GetRunRequest) (contract.GetRunResponse, error) {
	if s == nil {
		return contract.GetRunResponse{}, ErrLifecycleNotImplemented
	}
	if s.runStore == nil {
		return contract.GetRunResponse{}, ErrRunStoreUnset
	}
	runKey := strings.TrimSpace(req.RunKey)
	if runKey == "" {
		return contract.GetRunResponse{}, fmt.Errorf("orchestration: GetRun: run_key required")
	}
	run, err := s.runStore.GetRun(ctx, runKey)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return contract.GetRunResponse{}, fmt.Errorf("%w: %s", ErrRunNotFound, runKey)
		}
		return contract.GetRunResponse{}, fmt.Errorf("orchestration: GetRun(%q): %w", runKey, err)
	}
	if run == nil {
		// 防御：实现规范 GetRun 在未命中时返回 IsNotFound 域错误而非 (nil, nil)，
		// 此分支只为兜底未来实现退化（例如新 store 后端忘记包错误）。
		// Defensive: the GetRun contract returns an IsNotFound error on miss
		// rather than (nil, nil); this branch only guards against future store
		// backends that forget to wrap the error.
		return contract.GetRunResponse{}, fmt.Errorf("%w: %s", ErrRunNotFound, runKey)
	}
	return contract.GetRunResponse{Run: dagRunDTO(*run)}, nil
}

// dagRunDTO 把存储层 taskdag.Run 转成 contract.Run DTO（值拷贝 + RawMessage
// defensive copy，防上层修改 events/metadata 渗回 store 缓存）。
//
// dagRunDTO converts a storage-layer taskdag.Run into the contract.Run DTO
// (value copy + defensive RawMessage copy so callers cannot mutate
// events/metadata back into a store-side cache).
func dagRunDTO(row taskdag.Run) contract.Run {
	return contract.Run{
		ID:                 row.ID,
		RunKey:             row.RunKey,
		DagKey:             row.DagKey,
		DagVersionSnapshot: row.DagVersionSnapshot,
		TriggerSource:      row.TriggerSource,
		Status:             row.Status,
		StartedAt:          row.StartedAt,
		FinishedAt:         row.FinishedAt,
		Events:             append([]byte(nil), row.Events...),
		BudgetUsed:         row.BudgetUsed,
		BudgetLimit:        row.BudgetLimit,
		Metadata:           append([]byte(nil), row.Metadata...),
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}
