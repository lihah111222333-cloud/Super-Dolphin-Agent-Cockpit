package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	dashboardListDAGsSnapshotAllQuery = `
SELECT id, dag_key, version, title, description, status, created_by, metadata,
       trigger, cron_expr, next_run_at, started_at, finished_at, created_at, updated_at
FROM task_dags
ORDER BY updated_at DESC, id DESC
LIMIT $1`
	dashboardListDAGsSnapshotStatusQuery = `
SELECT id, dag_key, version, title, description, status, created_by, metadata,
       trigger, cron_expr, next_run_at, started_at, finished_at, created_at, updated_at
FROM task_dags
WHERE status = $1
ORDER BY updated_at DESC, id DESC
LIMIT $2`
	dashboardListDAGsSnapshotKeywordQuery = `
SELECT id, dag_key, version, title, description, status, created_by, metadata,
       trigger, cron_expr, next_run_at, started_at, finished_at, created_at, updated_at
FROM task_dags
WHERE LOWER(dag_key) LIKE '%' || LOWER($1) || '%'
   OR LOWER(title) LIKE '%' || LOWER($1) || '%'
   OR LOWER(description) LIKE '%' || LOWER($1) || '%'
ORDER BY updated_at DESC, id DESC
LIMIT $2`
	dashboardListDAGsSnapshotStatusKeywordQuery = `
SELECT id, dag_key, version, title, description, status, created_by, metadata,
       trigger, cron_expr, next_run_at, started_at, finished_at, created_at, updated_at
FROM task_dags
WHERE status = $1
  AND (LOWER(dag_key) LIKE '%' || LOWER($2) || '%'
    OR LOWER(title) LIKE '%' || LOWER($2) || '%'
    OR LOWER(description) LIKE '%' || LOWER($2) || '%')
ORDER BY updated_at DESC, id DESC
LIMIT $3`
	dashboardGetDAGSnapshotQuery = `
SELECT id, dag_key, version, title, description, status, created_by, metadata,
       trigger, cron_expr, next_run_at, started_at, finished_at, created_at, updated_at
FROM task_dags
WHERE dag_key = $1
LIMIT 1`
	dashboardListTemplateNodesSnapshotQuery = `
SELECT id, dag_key, node_key, title, node_type, assigned_to, depends_on, status,
       command_ref, config, result, run_id, started_at, finished_at, created_at,
       updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id
FROM task_dag_nodes
WHERE dag_key = $1 AND run_id IS NULL
ORDER BY id ASC`
	dashboardListRunNodesSnapshotQuery = `
SELECT id, dag_key, node_key, title, node_type, assigned_to, depends_on, status,
       command_ref, config, result, run_id, started_at, finished_at, created_at,
       updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id
FROM task_dag_nodes
WHERE dag_key = $1 AND run_id = $2
ORDER BY id ASC`
	dashboardListRunsSnapshotAllQuery = `
SELECT id, run_key, dag_key, dag_version_snapshot, trigger_source, status,
       started_at, finished_at, budget_used, budget_limit, metadata,
       created_at, updated_at
FROM task_dag_runs
WHERE dag_key = $1
ORDER BY started_at DESC, id DESC
LIMIT $2`
	dashboardListRunsSnapshotStatusQuery = `
SELECT id, run_key, dag_key, dag_version_snapshot, trigger_source, status,
       started_at, finished_at, budget_used, budget_limit, metadata,
       created_at, updated_at
FROM task_dag_runs
WHERE dag_key = $1 AND status = $2
ORDER BY started_at DESC, id DESC
LIMIT $3`
	dashboardListLatestRunsByDAGSnapshotQueryTemplate = `
SELECT id, run_key, dag_key, dag_version_snapshot, trigger_source, status,
       started_at, finished_at, budget_used, budget_limit, metadata,
       created_at, updated_at
FROM (
    SELECT id, run_key, dag_key, dag_version_snapshot, trigger_source, status,
           started_at, finished_at, budget_used, budget_limit, metadata,
           created_at, updated_at,
           ROW_NUMBER() OVER (PARTITION BY dag_key ORDER BY started_at DESC, id DESC) AS run_rank
    FROM task_dag_runs
    WHERE dag_key IN (%s)
) latest_runs
WHERE run_rank = 1
LIMIT $%d`
	dashboardGetRunSnapshotQuery = `
SELECT id, run_key, dag_key, dag_version_snapshot, trigger_source, status,
       started_at, finished_at, events, budget_used, budget_limit, metadata,
       created_at, updated_at
FROM task_dag_runs
WHERE run_key = $1
LIMIT 1`
)

func (s *service) hasDAGSnapshotQueries() bool {
	return s != nil && s.dbQueries != nil
}

func (s *service) listDAGsFromSnapshot(ctx context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
	query, args := dashboardListDAGsSnapshotQuery(filter)
	rows, err := s.dbQueries.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return dashboardDAGSummariesFromRows(rows)
}

func dashboardListDAGsSnapshotQuery(filter contract.ListDAGsFilter) (string, []any) {
	status := strings.TrimSpace(filter.Status)
	keyword := strings.TrimSpace(filter.Keyword)
	if status != "" && keyword != "" {
		return dashboardListDAGsSnapshotStatusKeywordQuery, []any{status, keyword, filter.Limit}
	}
	if status != "" {
		return dashboardListDAGsSnapshotStatusQuery, []any{status, filter.Limit}
	}
	if keyword != "" {
		return dashboardListDAGsSnapshotKeywordQuery, []any{keyword, filter.Limit}
	}
	return dashboardListDAGsSnapshotAllQuery, []any{filter.Limit}
}

// getDAGDetailFromSnapshot 从 task_dags 快照表读取 DAG 模板和节点。
// 缺失 dagKey 或行映射失败都直接返回错误，避免前端展示半截 DAG。
func (s *service) getDAGDetailFromSnapshot(ctx context.Context, dagKey string) (*contract.DAGDetail, error) {
	rows, err := s.dbQueries.Query(ctx, dashboardGetDAGSnapshotQuery, dagKey)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("dashboard: dag %q not found", dagKey)
	}
	dag, err := dashboardDAGSummaryFromRow(rows[0])
	if err != nil {
		return nil, err
	}
	nodeRows, err := s.dbQueries.Query(ctx, dashboardListTemplateNodesSnapshotQuery, dagKey)
	if err != nil {
		return nil, err
	}
	nodes, err := dashboardDAGNodesFromRows(nodeRows)
	if err != nil {
		return nil, err
	}
	return &contract.DAGDetail{DAG: dag, Nodes: nodes}, nil
}

func (s *service) listDAGRunsFromSnapshot(ctx context.Context, dagKey, status string, limit int32) ([]contract.Run, error) {
	query, args := dashboardListRunsSnapshotQuery(dagKey, status, limit)
	rows, err := s.dbQueries.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return dashboardRunsFromRows(rows)
}

func dashboardListRunsSnapshotQuery(dagKey, status string, limit int32) (string, []any) {
	status = strings.TrimSpace(status)
	if status != "" {
		return dashboardListRunsSnapshotStatusQuery, []any{dagKey, status, limit}
	}
	return dashboardListRunsSnapshotAllQuery, []any{dagKey, limit}
}

// listLatestDAGRunsByDAGFromSnapshot 为一组 DAG 读取各自最新 run 快照。
// 查询结果按 SQL 排序去重，调用方依赖空 map 表示没有可展示的历史运行。
func (s *service) listLatestDAGRunsByDAGFromSnapshot(ctx context.Context, dagKeys []string) (map[string]contract.Run, error) {
	if len(dagKeys) == 0 {
		return map[string]contract.Run{}, nil
	}
	query, args := dashboardLatestRunsByDAGQuery(dagKeys)
	rows, err := s.dbQueries.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	runs, err := dashboardRunsFromRows(rows)
	if err != nil {
		return nil, err
	}
	out := make(map[string]contract.Run, len(runs))
	for _, run := range runs {
		if _, exists := out[run.DagKey]; !exists {
			out[run.DagKey] = run
		}
	}
	return out, nil
}

func dashboardLatestRunsByDAGQuery(dagKeys []string) (string, []any) {
	placeholders := make([]string, len(dagKeys))
	args := make([]any, 0, len(dagKeys)+1)
	for index, dagKey := range dagKeys {
		placeholders[index] = fmt.Sprintf("$%d", index+1)
		args = append(args, dagKey)
	}
	limitPlaceholder := len(dagKeys) + 1
	args = append(args, int32(len(dagKeys)))
	return fmt.Sprintf(dashboardListLatestRunsByDAGSnapshotQueryTemplate, strings.Join(placeholders, ", "), limitPlaceholder), args
}

// getDAGRunFromSnapshot 从 run 快照读取一次运行及其节点状态。
// runKey 不存在时返回明确错误，节点读取失败也不返回不完整响应。
func (s *service) getDAGRunFromSnapshot(ctx context.Context, runKey string) (contract.GetRunResponse, error) {
	rows, err := s.dbQueries.Query(ctx, dashboardGetRunSnapshotQuery, runKey)
	if err != nil {
		return contract.GetRunResponse{}, err
	}
	if len(rows) == 0 {
		return contract.GetRunResponse{}, fmt.Errorf("dashboard: dag run %q not found", runKey)
	}
	run, err := dashboardRunFromRow(rows[0])
	if err != nil {
		return contract.GetRunResponse{}, err
	}
	nodeRows, err := s.dbQueries.Query(ctx, dashboardListRunNodesSnapshotQuery, run.DagKey, run.ID)
	if err != nil {
		return contract.GetRunResponse{}, err
	}
	nodes, err := dashboardDAGNodesFromRows(nodeRows)
	if err != nil {
		return contract.GetRunResponse{}, err
	}
	return contract.GetRunResponse{Run: run, Nodes: nodes}, nil
}

func dashboardDAGSummariesFromRows(rows []map[string]any) ([]contract.DAGSummary, error) {
	out := make([]contract.DAGSummary, 0, len(rows))
	for index, row := range rows {
		item, err := dashboardDAGSummaryFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("dashboard: map dag row %d: %w", index, err)
		}
		out = append(out, item)
	}
	return out, nil
}

// dashboardDAGSummaryFromRow 将一行 task_dags 查询结果转换为 dashboard DAG 摘要。
// 必填主键和版本字段缺失时立即报错，调度开关由触发器、cron 表达式和 next_run_at 共同决定。
func dashboardDAGSummaryFromRow(row map[string]any) (contract.DAGSummary, error) {
	id, err := dashboardRowInt64(row, "id", true)
	if err != nil {
		return contract.DAGSummary{}, err
	}
	dagKey, err := dashboardRequiredRowString(row, "dag_key")
	if err != nil {
		return contract.DAGSummary{}, err
	}
	version, err := dashboardRowInt64(row, "version", true)
	if err != nil {
		return contract.DAGSummary{}, err
	}
	times, err := dashboardDAGSummaryTimesFromRow(row)
	if err != nil {
		return contract.DAGSummary{}, err
	}
	scheduleEnabled := strings.TrimSpace(dashboardString(row, "trigger")) == "scheduled" &&
		strings.TrimSpace(dashboardString(row, "cron_expr")) != "" &&
		times.nextRunAt != nil
	return contract.DAGSummary{
		ID:              id,
		DagKey:          dagKey,
		Version:         version,
		Title:           dashboardString(row, "title"),
		Description:     dashboardString(row, "description"),
		Status:          dashboardString(row, "status"),
		CreatedBy:       dashboardString(row, "created_by"),
		Metadata:        dashboardJSON(row, "metadata"),
		Trigger:         dashboardString(row, "trigger"),
		CronExpr:        dashboardString(row, "cron_expr"),
		NextRunAt:       times.nextRunAt,
		ScheduleEnabled: scheduleEnabled,
		StartedAt:       times.startedAt,
		FinishedAt:      times.finishedAt,
		CreatedAt:       times.createdAt,
		UpdatedAt:       times.updatedAt,
	}, nil
}

func dashboardDAGNodesFromRows(rows []map[string]any) ([]contract.DAGNode, error) {
	out := make([]contract.DAGNode, 0, len(rows))
	for index, row := range rows {
		item, err := dashboardDAGNodeFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("dashboard: map dag node row %d: %w", index, err)
		}
		out = append(out, item)
	}
	return out, nil
}

// dashboardDAGNodeFromRow 将 task_dag_nodes 行转换为前端可读的 DAG 节点。
// 依赖列表和时间字段解析失败会阻断整条响应，避免节点拓扑被错误展示。
func dashboardDAGNodeFromRow(row map[string]any) (contract.DAGNode, error) {
	id, err := dashboardRowInt64(row, "id", true)
	if err != nil {
		return contract.DAGNode{}, err
	}
	dagKey, err := dashboardRequiredRowString(row, "dag_key")
	if err != nil {
		return contract.DAGNode{}, err
	}
	nodeKey, err := dashboardRequiredRowString(row, "node_key")
	if err != nil {
		return contract.DAGNode{}, err
	}
	dependsOn, err := dashboardStringSlice(row, "depends_on")
	if err != nil {
		return contract.DAGNode{}, err
	}
	times, err := dashboardDAGNodeTimesFromRow(row)
	if err != nil {
		return contract.DAGNode{}, err
	}
	activeWakeupID, err := dashboardOptionalInt64Ptr(row, "active_wakeup_id")
	if err != nil {
		return contract.DAGNode{}, err
	}
	return contract.DAGNode{
		ID:               id,
		DagKey:           dagKey,
		NodeKey:          nodeKey,
		Title:            dashboardString(row, "title"),
		NodeType:         dashboardString(row, "node_type"),
		AssignedTo:       dashboardString(row, "assigned_to"),
		DependsOn:        dependsOn,
		Status:           dashboardString(row, "status"),
		CommandRef:       dashboardString(row, "command_ref"),
		Config:           dashboardJSON(row, "config"),
		Result:           dashboardJSON(row, "result"),
		StartedAt:        times.startedAt,
		FinishedAt:       times.finishedAt,
		CreatedAt:        times.createdAt,
		UpdatedAt:        times.updatedAt,
		ActiveTurnID:     dashboardStringPtr(row, "active_turn_id"),
		ActiveWakeupID:   activeWakeupID,
		LastEventAt:      times.lastEventAt,
		SpawningThreadID: dashboardStringPtr(row, "spawning_thread_id"),
	}, nil
}

func dashboardRunsFromRows(rows []map[string]any) ([]contract.Run, error) {
	out := make([]contract.Run, 0, len(rows))
	for index, row := range rows {
		item, err := dashboardRunFromRow(row)
		if err != nil {
			return nil, fmt.Errorf("dashboard: map dag run row %d: %w", index, err)
		}
		out = append(out, item)
	}
	return out, nil
}

// dashboardRunFromRow 将 task_dag_runs 行转换为运行快照。
// events 和 metadata 允许按展示层 fallback 规范修正格式，核心 ID、版本和预算字段仍保持 fail-fast。
func dashboardRunFromRow(row map[string]any) (contract.Run, error) {
	id, err := dashboardRowInt64(row, "id", true)
	if err != nil {
		return contract.Run{}, err
	}
	version, err := dashboardRowInt64(row, "dag_version_snapshot", true)
	if err != nil {
		return contract.Run{}, err
	}
	budgetUsed, err := dashboardRowInt64(row, "budget_used", true)
	if err != nil {
		return contract.Run{}, err
	}
	runKey, err := dashboardRequiredRowString(row, "run_key")
	if err != nil {
		return contract.Run{}, err
	}
	dagKey, err := dashboardRequiredRowString(row, "dag_key")
	if err != nil {
		return contract.Run{}, err
	}
	times, err := dashboardRunTimesFromRow(row)
	if err != nil {
		return contract.Run{}, err
	}
	budgetLimit, err := dashboardOptionalInt64Ptr(row, "budget_limit")
	if err != nil {
		return contract.Run{}, err
	}
	return contract.Run{
		ID:                 id,
		RunKey:             runKey,
		DagKey:             dagKey,
		DagVersionSnapshot: version,
		TriggerSource:      dashboardString(row, "trigger_source"),
		Status:             dashboardString(row, "status"),
		StartedAt:          times.startedAt,
		FinishedAt:         times.finishedAt,
		Events:             dashboardJSONOrDefault(row, "events", json.RawMessage("[]")),
		BudgetUsed:         budgetUsed,
		BudgetLimit:        budgetLimit,
		Metadata:           dashboardJSONOrDefault(row, "metadata", json.RawMessage("{}")),
		CreatedAt:          times.createdAt,
		UpdatedAt:          times.updatedAt,
	}, nil
}

func dashboardRequiredRowString(row map[string]any, key string) (string, error) {
	value := strings.TrimSpace(dashboardString(row, key))
	if value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func dashboardString(row map[string]any, key string) string {
	value := row[key]
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func dashboardStringPtr(row map[string]any, key string) *string {
	value := strings.TrimSpace(dashboardString(row, key))
	if value == "" {
		return nil
	}
	return &value
}

func dashboardJSON(row map[string]any, key string) json.RawMessage {
	return dashboardJSONOrDefault(row, key, nil)
}

// dashboardJSONOrDefault 规范化 dashboard 快照中的 JSON 列。
// fallback 只用于展示层兜住空值或历史脏数据，调用方仍需对必填业务字段单独校验。
func dashboardJSONOrDefault(row map[string]any, key string, fallback json.RawMessage) json.RawMessage {
	value, ok := row[key]
	if !ok || value == nil {
		return append(json.RawMessage(nil), fallback...)
	}
	switch typed := value.(type) {
	case json.RawMessage:
		return dashboardValidJSONOrFallback(typed, fallback)
	case []byte:
		return dashboardValidJSONOrFallback(json.RawMessage(typed), fallback)
	case string:
		return dashboardValidJSONOrFallback(json.RawMessage(strings.TrimSpace(typed)), fallback)
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return append(json.RawMessage(nil), fallback...)
		}
		return dashboardValidJSONOrFallback(raw, fallback)
	}
}

func dashboardValidJSONOrFallback(raw json.RawMessage, fallback json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return append(json.RawMessage(nil), fallback...)
	}
	if json.Valid(json.RawMessage(trimmed)) {
		return append(json.RawMessage(nil), trimmed...)
	}
	quoted, err := json.Marshal(trimmed)
	if err != nil {
		return append(json.RawMessage(nil), fallback...)
	}
	return quoted
}

func dashboardStringSlice(row map[string]any, key string) ([]string, error) {
	raw := dashboardJSONOrDefault(row, key, json.RawMessage("[]"))
	if len(raw) == 0 {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	if values == nil {
		values = []string{}
	}
	return values, nil
}

func dashboardRowInt64(row map[string]any, key string, required bool) (int64, error) {
	value, ok := row[key]
	if !ok || value == nil {
		if required {
			return 0, fmt.Errorf("%s is required", key)
		}
		return 0, nil
	}
	return dashboardInt64Value(key, value)
}

// dashboardInt64Value 将数据库驱动可能返回的数字形态统一为 int64。
// 浮点小数、不可解析字符串或未知类型会返回错误，避免静默截断计数和 ID。
func dashboardInt64Value(key string, value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case int64:
		return typed, nil
	case float64:
		if math.Trunc(typed) != typed {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return int64(typed), nil
	case *int64:
		if typed == nil {
			return 0, nil
		}
		return *typed, nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", key, err)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("%s has unsupported type %T", key, value)
	}
}

func dashboardOptionalInt64Ptr(row map[string]any, key string) (*int64, error) {
	if value, ok := row[key]; !ok || value == nil {
		return nil, nil
	}
	value, err := dashboardRowInt64(row, key, false)
	if err != nil {
		return nil, err
	}
	return &value, nil
}
