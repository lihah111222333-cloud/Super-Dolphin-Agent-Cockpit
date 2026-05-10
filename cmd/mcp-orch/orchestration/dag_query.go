package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// =====================================================
// DAG v2 T3.1: task_get_run service 实现
// DAG v2 T3.2: task_list_runs service 实现
// DAG v2 T3.1/T3.2: task_get_run / task_list_runs service implementation
// =====================================================
//
// service.GetRun 接通 RunStore.GetRun(run_key)，把存储域 Run 转换为
// contract.Run DTO 返回给 MCP 调用方。节点信息不内联（用户决策 A2）：
// 调用方需另查 task_get_dag 拿 DAG 模板 + 节点。
//
// service.ListRuns 接通 RunStore.ListRuns(filter)，列出指定 DAG 的最近 run。
// dag_key 必填、status 透传、limit 默认 50。
//
// service.GetRun wires through RunStore.GetRun(run_key) and converts the
// storage-domain Run into a contract.Run DTO for MCP callers. Nodes are
// intentionally not inlined (user decision A2): callers fetch the DAG
// template + nodes via task_get_dag.
//
// service.ListRuns wires through RunStore.ListRuns(filter), listing recent
// runs for a DAG. dag_key required; status passed through; limit defaults to 50.

// ErrRunNotFound 表示 GetRun 调用时 run_key 不存在。
// 与 ErrDAGNotFound 对齐：service 层 sentinel + tool 层中英双语转译。
//
// ErrRunNotFound is returned by GetRun when the supplied run_key does not
// exist. Mirrors ErrDAGNotFound's pattern: service-layer sentinel +
// bilingual translation at the tool layer.
var ErrRunNotFound = errors.New("orchestration: run_key not found")

// GetRun 按 run_key 取一条 task_dag_runs。
//
// 防御性检查与 ListRuns / StartDAG 对齐：service 自身或 runStore 未注入时统一
// 返 ErrRunStoreUnset（同一个 sentinel，调用方 errors.Is 判断更省事）。运行时
// 调用路径上 ProvideService 的 setter 注入会保证两者非 nil（fx 提供路线 N 后已守护）。
//
// 错误转译：runStore 返回的域错误若 IsNotFound 命中 → 包装 ErrRunNotFound
// sentinel；其他错误透传并附 run_key 上下文。
//
// GetRun fetches a single task_dag_runs row by run_key.
//
// Defensive checks align with ListRuns / StartDAG: if the service itself or
// runStore is unset, we return the same ErrRunStoreUnset sentinel so callers
// have a single errors.Is target. In the production path ProvideService's
// setter injection guarantees both are non-nil (route N's fx wiring guards
// that already).
//
// Error translation: when runStore returns a domain error matched by
// IsNotFound we wrap ErrRunNotFound; other errors are passed through with
// the run_key for context.
func (s *service) GetRun(ctx context.Context, req contract.GetRunRequest) (contract.GetRunResponse, error) {
	if s == nil || s.runStore == nil {
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

// ListRuns 列出指定 DAG 的最近 run（T3.2）。
//   - dag_key 必填；空字符串报错。
//   - status 可选（store 层 ListTaskDagRunsByKey 把空串视为不过滤；
//     合法状态枚举由 migration 0080 task_dag_runs.status CHECK 锁全集，
//     service 不重复校验，错误 status 由 DB 直接拒绝）。
//   - limit 走 shared.ClampLimit(val, 1, 200, 50)：<1 → 默认 50，>200 → cap 200。
//     M2 阶段 50 够用，200 是防呆上限（调用方传 99999999 不会透到 SQL 层）。
//     store_run.go:46 还会用 limit<=0 → 50 兜底，为例外路径多一道保险。
//   - runStore == nil 防御与 StartDAG 一致：返 ErrRunStoreUnset，避免裸构造
//     测试路径走到 nil pointer。
//
// ListRuns lists recent runs for a DAG (T3.2).
//   - dag_key required; empty string returns an error.
//   - status optional; the store treats empty as no filter and migration 0080
//     CHECK constrains the legal status set, so the service does not re-validate.
//   - limit goes through shared.ClampLimit(val, 1, 200, 50): <1 → default 50,
//     >200 → capped to 200. M2 callers rarely need more, and 200 keeps a stray
//     99999999 from reaching SQL. store_run.go:46 still defaults limit<=0 to 50.
//   - runStore == nil defense matches StartDAG: returns ErrRunStoreUnset.
func (s *service) ListRuns(ctx context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
	if s == nil || s.runStore == nil {
		return contract.ListRunsResponse{}, ErrRunStoreUnset
	}
	dagKey := strings.TrimSpace(req.DagKey)
	if dagKey == "" {
		return contract.ListRunsResponse{}, fmt.Errorf("orchestration: ListRuns: dag_key required")
	}
	filter := taskdag.ListRunsFilter{
		DagKey: dagKey,
		Status: strings.TrimSpace(req.Status),
		Limit:  int32(shared.ClampLimit(int(req.Limit), 1, 200, 50)),
	}
	rows, err := s.runStore.ListRuns(ctx, filter)
	if err != nil {
		return contract.ListRunsResponse{}, fmt.Errorf("orchestration: ListRuns(%q): %w", dagKey, err)
	}
	return contract.ListRunsResponse{Runs: mapRuns(rows)}, nil
}

// mapRuns 把 store 层 taskdag.Run slice 转为 contract.Run slice，
// 复用 dagRunDTO 做单行转换以保持与 GetRun 路径一致的防御拷贝语义。
//
// mapRuns converts a slice of taskdag.Run into contract.Run, reusing
// dagRunDTO for per-row conversion so defensive-copy semantics stay aligned
// with the GetRun path.
func mapRuns(items []taskdag.Run) []contract.Run {
	mapped := make([]contract.Run, 0, len(items))
	for _, item := range items {
		mapped = append(mapped, dagRunDTO(item))
	}
	return mapped
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
		FinishedAt:         shared.CloneTime(row.FinishedAt),
		Events:             append([]byte(nil), row.Events...),
		BudgetUsed:         row.BudgetUsed,
		BudgetLimit:        cloneInt64(row.BudgetLimit),
		Metadata:           append([]byte(nil), row.Metadata...),
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}
