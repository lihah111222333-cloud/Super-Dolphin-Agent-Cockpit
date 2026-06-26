package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeevents"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/taskupdatelease"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const defaultDAGStatus = "draft"

// createDAGParams 是 task_create_dag 工具层的 JSON 入参，保留旧 camelCase 兼容。
type createDAGParams struct {
	DagKey      string                `json:"dag_key"`
	Title       string                `json:"title"`
	Description string                `json:"description,omitempty"`
	CreatedBy   string                `json:"created_by,omitempty"`
	Metadata    json.RawMessage       `json:"metadata,omitempty"`
	Nodes       []createDAGNodeParams `json:"nodes,omitempty"`
}

// UnmarshalJSON 解析 JSON 输入，并兼容旧字段或字符串写法。
func (p *createDAGParams) UnmarshalJSON(data []byte) error {
	type current createDAGParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		DagKey    string `json:"dagKey"`
		CreatedBy string `json:"createdBy"`
	}) error {
		*p = createDAGParams(*raw)
		if strings.TrimSpace(p.DagKey) == "" {
			p.DagKey = strings.TrimSpace(legacy.DagKey)
		}
		if strings.TrimSpace(p.CreatedBy) == "" {
			p.CreatedBy = strings.TrimSpace(legacy.CreatedBy)
		}
		return nil
	})
}

// createDAGNodeParams 是 task_create_dag 内单个节点的 JSON 入参。
type createDAGNodeParams struct {
	NodeKey    string          `json:"node_key"`
	Title      string          `json:"title"`
	NodeType   string          `json:"node_type,omitempty"`
	AssignedTo string          `json:"assigned_to,omitempty"`
	DependsOn  []string        `json:"depends_on,omitempty"`
	CommandRef string          `json:"command_ref,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
}

// UnmarshalJSON 解析 JSON 输入，并兼容旧字段或字符串写法。
func (p *createDAGNodeParams) UnmarshalJSON(data []byte) error {
	type current createDAGNodeParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		NodeKey    string   `json:"nodeKey"`
		NodeType   string   `json:"nodeType"`
		AssignedTo string   `json:"assignedTo"`
		DependsOn  []string `json:"dependsOn"`
		CommandRef string   `json:"commandRef"`
	}) error {
		*p = createDAGNodeParams(*raw)
		if strings.TrimSpace(p.NodeKey) == "" {
			p.NodeKey = strings.TrimSpace(legacy.NodeKey)
		}
		if strings.TrimSpace(p.NodeType) == "" {
			p.NodeType = strings.TrimSpace(legacy.NodeType)
		}
		if strings.TrimSpace(p.AssignedTo) == "" {
			p.AssignedTo = strings.TrimSpace(legacy.AssignedTo)
		}
		if len(p.DependsOn) == 0 && len(legacy.DependsOn) > 0 {
			p.DependsOn = append([]string(nil), legacy.DependsOn...)
		}
		if strings.TrimSpace(p.CommandRef) == "" {
			p.CommandRef = strings.TrimSpace(legacy.CommandRef)
		}
		return nil
	})
}

// listDAGsParams 是 task_list_dags 的筛选入参。
type listDAGsParams struct {
	Status  string `json:"status,omitempty"`
	Keyword string `json:"keyword,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// updateNodeParams 是 task_update_node 的 JSON 入参，嵌入 dag/node 定位字段。
type updateNodeParams struct {
	dagNodeParams
	RunID  int64           `json:"run_id"`
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
}

// UnmarshalJSON 解析 JSON 输入，并兼容旧字段或字符串写法。
func (p *updateNodeParams) UnmarshalJSON(data []byte) error {
	type wire struct {
		DagKey  string          `json:"dag_key"`
		NodeKey string          `json:"node_key"`
		RunID   int64           `json:"run_id"`
		Status  string          `json:"status"`
		Result  json.RawMessage `json:"result,omitempty"`
	}
	return decodeLegacyAlias(data, new(wire), func(raw *wire, legacy *struct {
		DagKey  string `json:"dagKey"`
		NodeKey string `json:"nodeKey"`
		RunID   int64  `json:"runId"`
	}) error {
		*p = updateNodeParams{
			dagNodeParams: dagNodeParams{DagKey: raw.DagKey, NodeKey: raw.NodeKey},
			RunID:         raw.RunID,
			Status:        raw.Status,
			Result:        append(json.RawMessage(nil), raw.Result...),
		}
		if strings.TrimSpace(p.DagKey) == "" {
			p.DagKey = strings.TrimSpace(legacy.DagKey)
		}
		if strings.TrimSpace(p.NodeKey) == "" {
			p.NodeKey = strings.TrimSpace(legacy.NodeKey)
		}
		if p.RunID == 0 {
			p.RunID = legacy.RunID
		}
		return nil
	})
}

// CreateDAG 创建 DAG 记录、节点和初始调度状态。
func (s *service) CreateDAG(ctx context.Context, req CreateDAGRequest) (DAGDetail, error) {
	if err := nodeexec.ValidateCreateDAGNodes(req.Nodes); err != nil {
		return DAGDetail{}, fmt.Errorf("orchestration: create_dag invalid request: nodes topology invalid: %w", err)
	}
	var detail DAGDetail
	err := s.withDAGStore(func(store taskdag.OrchestrationStore) error {
		return store.WithTx(ctx, func(txStore taskdag.DAGMutationStore) error {
			dag, dagErr := upsertDAG(ctx, txStore, req)
			if dagErr != nil {
				return dagErr
			}
			if nodeErr := upsertDAGNodes(ctx, txStore, dag.DagKey, req.Nodes); nodeErr != nil {
				return nodeErr
			}
			loaded, loadErr := loadDAGDetail(ctx, txStore, dag.DagKey)
			if loadErr != nil {
				return loadErr
			}
			detail = loaded
			return nil
		})
	})
	if err != nil {
		return DAGDetail{}, err
	}
	return detail, nil
}

// GetDAG 读取 DAG 明细并补齐节点、边和运行状态。
func (s *service) GetDAG(ctx context.Context, dagKey string) (DAGDetail, error) {
	var detail DAGDetail
	err := s.withDAGStore(func(store taskdag.OrchestrationStore) error {
		loaded, loadErr := loadDAGDetail(ctx, store, dagKey)
		if loadErr != nil {
			return loadErr
		}
		detail = loaded
		return nil
	})
	return detail, err
}

// ListDAGs 按查询条件列出 DAG 摘要。
func (s *service) ListDAGs(ctx context.Context, filter ListDAGsFilter) ([]DAGSummary, error) {
	var summaries []DAGSummary
	err := s.withDAGStore(func(store taskdag.OrchestrationStore) error {
		dags, listErr := store.ListDAGs(ctx, taskdag.ListDAGsFilter{
			Status:  strings.TrimSpace(filter.Status),
			Keyword: strings.TrimSpace(filter.Keyword),
			Limit:   int32(shared.ClampLimit(filter.Limit, 1, 0, 50)),
		})
		if listErr != nil {
			return listErr
		}
		summaries = mapDAGSummaries(dags)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return summaries, nil
}

// task_update_node 先按当前 run 的节点状态校验，再决定怎么写。
// done 要唤醒下游，failed 要处理失败级联；别绕过这些流程直接改 status。
func (s *service) UpdateNodeStatus(ctx context.Context, req UpdateNodeStatusRequest) (DAGNode, error) {
	input, err := nodeStatusUpdateFromRequest(req)
	if err != nil {
		return DAGNode{}, err
	}
	var result DAGNode
	err = s.withDAGStore(func(store taskdag.OrchestrationStore) error {
		current, vErr := s.validateNodeTransition(ctx, store, input)
		if vErr != nil {
			return vErr
		}
		if err := taskupdatelease.Validate(ctx, store, current, defaultWakeupLeaseInterval); err != nil {
			return err
		}
		oldStatus := current.Status
		if input.Status == "done" {
			if flow, ok := store.(taskdag.NodeFlowStore); ok {
				return s.completeNodeWithDownstream(ctx, flow, input, oldStatus, &result)
			}
		}
		if input.Status == "failed" {
			flow, ok := store.(taskdag.NodeFlowStore)
			if !ok {
				return fmt.Errorf("orchestration: task_update_node failed requires NodeFlowStore for dag_key=%s node_key=%s run_id=%d", input.DagKey, input.NodeKey, input.RunID)
			}
			return s.failNodeWithDownstream(ctx, flow, input, oldStatus, &result)
		}
		node, updateErr := store.UpdateNodeStatus(ctx, input)
		if updateErr != nil {
			return updateErr
		}
		nodeevents.Publish(s.eventBus, oldStatus, node)
		result = dagNodeDTO(*node)
		return nil
	})
	if err != nil {
		return DAGNode{}, err
	}
	return result, nil
}

// validateNodeTransition 在公开 task_update_node 写入前读取 run-scoped 当前状态。
// 该校验只覆盖工具/RPC 入口；dispatcher 热路径依赖 SQL fence 和状态白名单阻止并发写。
func (s *service) validateNodeTransition(ctx context.Context, store taskdag.OrchestrationStore, input taskdag.NodeStatusUpdate) (taskdag.Node, error) {
	runReader, ok := any(store).(taskdag.RunNodeReadStore)
	if !ok {
		return taskdag.Node{}, fmt.Errorf("validate transition: store does not implement RunNodeReadStore for run_id=%d", input.RunID)
	}
	nodes, err := runReader.ListRunNodes(ctx, input.DagKey, input.RunID)
	if err != nil {
		return taskdag.Node{}, fmt.Errorf("validate transition: list run nodes %s run_id=%d: %w", input.DagKey, input.RunID, err)
	}
	var current taskdag.Node
	found := false
	for _, n := range nodes {
		if n.NodeKey == input.NodeKey {
			current = n
			found = true
			break
		}
	}
	if !found {
		return taskdag.Node{}, fmt.Errorf("validate transition: node %s/%s not found", input.DagKey, input.NodeKey)
	}
	if err := nodeexec.ValidateTransition(nodeexec.NodeStatus(current.Status), nodeexec.NodeStatus(input.Status)); err != nil {
		return taskdag.Node{}, err
	}
	return current, nil
}

// completeNodeWithDownstream 完成节点时让 store 统一处理下游和 run 收尾。
// service 只发布事件和记日志，不自己重新扫 DAG。
func (s *service) completeNodeWithDownstream(ctx context.Context, flow taskdag.NodeFlowStore, input taskdag.NodeStatusUpdate, oldStatus string, result *DAGNode) error {
	res, err := flow.CompleteNodeAndScheduleDownstream(ctx, taskdag.CompleteNodeInput(input))
	if err != nil {
		return err
	}
	nodeevents.PublishComplete(s.eventBus, oldStatus, res)
	if res.Node != nil {
		*result = dagNodeDTO(*res.Node)
	}
	if len(res.ScheduledDownstream) > 0 && s.logger != nil {
		s.logger.Info("orchestration: scheduled downstream wakeups",
			"dag_key", input.DagKey,
			"completed_node", input.NodeKey,
			"count", len(res.ScheduledDownstream))
	}
	return nil
}

// failNodeWithDownstream 在公开调用把节点标 failed 时同步处理下游 pending 节点。
// 否则它们会一直等一个永远不会 done 的上游。
func (s *service) failNodeWithDownstream(ctx context.Context, flow taskdag.NodeFlowStore, input taskdag.NodeStatusUpdate, oldStatus string, result *DAGNode) error {
	reason := string(input.Result)
	if reason == "" {
		reason = "task_update_node status=failed"
	}
	res, err := flow.FailNodeAndCancelDownstream(ctx, taskdag.FailNodeInput{DagKey: input.DagKey, NodeKey: input.NodeKey, RunID: input.RunID, Reason: reason, FailFast: true})
	if err != nil {
		return err
	}
	nodeevents.PublishFail(s.eventBus, oldStatus, res)
	if res.Node != nil {
		*result = dagNodeDTO(*res.Node)
	}
	return nil
}

// upsertDAG 写入 DAG 模板记录，而不是正在跑的 run。
// runtime node只在 StartDAG 时从模板复制出来。
func upsertDAG(ctx context.Context, store taskdag.DAGMutationStore, req CreateDAGRequest) (*taskdag.DAG, error) {
	dagKey := strings.TrimSpace(req.DagKey)
	if dagKey == "" {
		return nil, errors.New("dag key is required")
	}
	return store.UpsertDAG(ctx, taskdag.DAG{
		DagKey:      dagKey,
		Title:       strings.TrimSpace(req.Title),
		Description: strings.TrimSpace(req.Description),
		Status:      defaultDAGStatus,
		CreatedBy:   strings.TrimSpace(req.CreatedBy),
		Metadata:    append(json.RawMessage(nil), req.Metadata...),
	})
}

// upsertDAGNodes 写入 DAG 模板节点并同步依赖边。
// store 若暴露批量端口则一次性写入；否则逐节点 Upsert，保持 DAGMutationStore 的基础接口足够窄。
func upsertDAGNodes(ctx context.Context, store taskdag.DAGMutationStore, dagKey string, nodes []CreateDAGNodeRequest) error {
	if len(nodes) == 0 {
		return nil
	}
	if batch, ok := store.(taskdag.BatchUpsertingNodeStore); ok {
		mapped := make([]taskdag.Node, len(nodes))
		for i, n := range nodes {
			mapped[i] = dagNodeFromRequest(dagKey, n)
		}
		_, err := batch.BatchUpsertNodes(ctx, mapped)
		return err
	}
	for _, node := range nodes {
		if _, err := store.UpsertNode(ctx, dagNodeFromRequest(dagKey, node)); err != nil {
			return err
		}
	}
	return nil
}

// loadDAGDetail 加载 DAG 的完整视图供 API 和调度器使用。
func loadDAGDetail(ctx context.Context, store taskdag.DAGDetailStore, dagKey string) (DAGDetail, error) {
	trimmedKey := strings.TrimSpace(dagKey)
	reader, hasVersion := store.(taskdag.DAGVersionReader)
	var versionBefore int64
	var err error
	if hasVersion {
		if versionBefore, err = reader.GetDAGVersion(ctx, trimmedKey); err != nil {
			return DAGDetail{}, fmt.Errorf("get dag version for detail: %w", err)
		}
	}
	dag, err := store.GetDAG(ctx, trimmedKey)
	if err != nil {
		return DAGDetail{}, err
	}
	nodes, err := store.ListNodes(ctx, dag.DagKey)
	if err != nil {
		return DAGDetail{}, err
	}
	summary := dagSummaryDTO(*dag)
	if hasVersion {
		versionAfter, err := reader.GetDAGVersion(ctx, dag.DagKey)
		if err != nil {
			return DAGDetail{}, fmt.Errorf("get dag version for detail: %w", err)
		}
		if versionAfter != versionBefore {
			return DAGDetail{}, fmt.Errorf("dag detail version changed while loading: dag=%s before=%d after=%d", dag.DagKey, versionBefore, versionAfter)
		}
		summary.Version = versionAfter
	}
	return DAGDetail{DAG: summary, Nodes: mapDAGNodes(nodes)}, nil
}

// dagNodeFromRequest 将 API 层节点请求转换为 taskdag 存储模型。
func dagNodeFromRequest(dagKey string, req CreateDAGNodeRequest) taskdag.Node {
	return taskdag.Node{
		DagKey:     strings.TrimSpace(dagKey),
		NodeKey:    strings.TrimSpace(req.NodeKey),
		Title:      strings.TrimSpace(req.Title),
		NodeType:   strings.TrimSpace(req.NodeType),
		AssignedTo: strings.TrimSpace(req.AssignedTo),
		DependsOn:  dependsOnJSON(req.DependsOn),
		CommandRef: strings.TrimSpace(req.CommandRef),
		Config:     append(json.RawMessage(nil), req.Config...),
	}
}

// dependsOnJSON 清理 depends_on 后编码为 JSON 数组，空依赖写入 []。
func dependsOnJSON(values []string) json.RawMessage {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		if candidate := strings.TrimSpace(value); candidate != "" {
			trimmed = append(trimmed, candidate)
		}
	}
	if len(trimmed) == 0 {
		return json.RawMessage("[]")
	}
	raw, _ := json.Marshal(trimmed)
	return raw
}

// nodeStatusUpdateFromRequest 校验 task_update_node 必填字段并转换为 store 入参。
func nodeStatusUpdateFromRequest(req UpdateNodeStatusRequest) (taskdag.NodeStatusUpdate, error) {
	if strings.TrimSpace(req.DagKey) == "" {
		return taskdag.NodeStatusUpdate{}, errors.New("dag key is required")
	}
	if strings.TrimSpace(req.NodeKey) == "" {
		return taskdag.NodeStatusUpdate{}, errors.New("node key is required")
	}
	if strings.TrimSpace(req.Status) == "" {
		return taskdag.NodeStatusUpdate{}, errors.New("status is required")
	}
	if req.RunID <= 0 {
		return taskdag.NodeStatusUpdate{}, fmt.Errorf("run_id is required for runtime node status update")
	}
	return taskdag.NodeStatusUpdate{DagKey: strings.TrimSpace(req.DagKey), NodeKey: strings.TrimSpace(req.NodeKey), RunID: req.RunID, Status: strings.TrimSpace(req.Status), Result: append(json.RawMessage(nil), req.Result...)}, nil
}

// getRunResponse 读取 run 对应的 runtime nodes，并组装 contract.GetRunResponse。
func getRunResponse(ctx context.Context, runStore taskdag.RunStore, runKey string, run *taskdag.Run) (contract.GetRunResponse, error) {
	if run == nil {
		return contract.GetRunResponse{}, fmt.Errorf("%w: %s", ErrRunNotFound, runKey)
	}
	runReader, ok := any(runStore).(taskdag.RunNodeReadStore)
	if !ok {
		return contract.GetRunResponse{}, fmt.Errorf("orchestration: GetRun(%q): run store does not implement RunNodeReadStore for run_id=%d", runKey, run.ID)
	}
	nodes, err := runReader.ListRunNodes(ctx, run.DagKey, run.ID)
	if err != nil {
		return contract.GetRunResponse{}, fmt.Errorf("orchestration: GetRun(%q): list runtime nodes for dag_key=%q run_id=%d: %w", runKey, run.DagKey, run.ID, err)
	}
	return contract.GetRunResponse{Run: dagRunDTO(*run), Nodes: mapDAGNodes(nodes)}, nil
}

// mapDAGSummaries 将存储层 DAG 列表映射为 API 摘要。
func mapDAGSummaries(items []taskdag.DAG) []DAGSummary {
	mapped := make([]DAGSummary, 0, len(items))
	for _, item := range items {
		mapped = append(mapped, dagSummaryDTO(item))
	}
	return mapped
}

// mapDAGNodes 将存储层节点列表映射为 API DTO。
func mapDAGNodes(items []taskdag.Node) []DAGNode {
	mapped := make([]DAGNode, 0, len(items))
	for _, item := range items {
		mapped = append(mapped, dagNodeDTO(item))
	}
	return mapped
}

// dagSummaryDTO 将 taskdag.DAG 转为对外摘要，并复制可变字段。
func dagSummaryDTO(item taskdag.DAG) DAGSummary {
	return DAGSummary{
		ID:              item.ID,
		DagKey:          item.DagKey,
		Version:         item.Version,
		Title:           item.Title,
		Description:     item.Description,
		Status:          item.Status,
		CreatedBy:       item.CreatedBy,
		Metadata:        append(json.RawMessage(nil), item.Metadata...),
		Trigger:         item.Trigger,
		CronExpr:        item.CronExpr,
		NextRunAt:       shared.CloneTime(item.NextRunAt),
		ScheduleEnabled: dagScheduleEnabled(item),
		StartedAt:       shared.CloneTime(item.StartedAt),
		FinishedAt:      shared.CloneTime(item.FinishedAt),
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
	}
}

// dagScheduleEnabled 判断 DAG 是否处于可调度状态。
func dagScheduleEnabled(item taskdag.DAG) bool {
	return strings.TrimSpace(item.Trigger) == "scheduled" && strings.TrimSpace(item.CronExpr) != "" && item.NextRunAt != nil
}

// dagNodeDTO 将 taskdag.Node 转为对外节点 DTO，并保留运行态字段。
func dagNodeDTO(item taskdag.Node) DAGNode {
	return DAGNode{
		ID:             item.ID,
		DagKey:         item.DagKey,
		NodeKey:        item.NodeKey,
		Title:          item.Title,
		NodeType:       item.NodeType,
		AssignedTo:     item.AssignedTo,
		DependsOn:      decodeDependsOn(item.DependsOn),
		Status:         item.Status,
		CommandRef:     item.CommandRef,
		Config:         append(json.RawMessage(nil), item.Config...),
		Result:         append(json.RawMessage(nil), item.Result...),
		StartedAt:      shared.CloneTime(item.StartedAt),
		FinishedAt:     shared.CloneTime(item.FinishedAt),
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
		ActiveTurnID:   trimStringPtr(item.ActiveTurnID),
		ActiveWakeupID: shared.CloneInt64(item.ActiveWakeupID),
		LastEventAt:    shared.CloneTime(item.LastEventAt),
		// spawning_thread_id 透出给 task_get_dag / DAG detail，供 UI 从节点行跳到子 agent thread。
		SpawningThreadID: trimStringPtr(item.SpawningThreadID),
	}
}

// decodeDependsOn 将节点依赖 JSON 转为字符串列表；非法 JSON 返回 nil。
func decodeDependsOn(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil
	}
	return values
}

// ----- DAG 生命周期入口 -----
// 本区集中处理 DAG run 启动、终止、删除和 ApplyOps。
// StartDAG 会创建 run 并复制模板版本；ApplyOps 只改模板和未来调度，不直接改运行中 runtime node。

// ErrLifecycleNotImplemented 是 legacy 未接线依赖的 sentinel 错误。
// 生产路径不应命中；测试裸构造或缺少 store 时可用 errors.Is 判断。
var ErrLifecycleNotImplemented = errors.New("orchestration lifecycle: not implemented in skeleton stage (T1.2/F4.x/F6.x)")

// ErrDAGNotFound 表示 StartDAG / TerminateDAG 调用时 dag_key 不存在。
var ErrDAGNotFound = errors.New("orchestration: dag_key not found")

// ErrDAGAlreadyRunning 是保留给删除/终止冲突翻译的历史 sentinel。
// 现在 StartDAG 支持按 run_key 幂等创建，不再用它表达正常的单 run 限制。
//
// 处理建议（与 ErrIdempotencyKeyExhausted 对照，"前者等/取消，后者换 idem"）：
// 调用方应轮询当前 run 直至完成 / 主动 Terminate 后用同 idem 重试，不需要换 idem。
var ErrDAGAlreadyRunning = errors.New("orchestration: dag has an active run (legacy single-run constraint)")

// ErrIdempotencyKeyExhausted 当 StartDAG 用同 idempotency key 调用，
// 但对应的 run 已是终态 (failed/cancelled) 时返回。
// 调用方应换新 idempotency key 重试。
var ErrIdempotencyKeyExhausted = errors.New("idempotency key exhausted: previous run is in terminal state, use a new key to retry")

// IdempotencyKeyExhaustedError 是 ErrIdempotencyKeyExhausted 的富错误包装：
// 携带旧 RunKey + 终态 Status，方便 MCP / 调用方做决策（也可 errors.Is
// 命中 ErrIdempotencyKeyExhausted sentinel）。
type IdempotencyKeyExhaustedError struct {
	RunKey string
	Status string
}

// Error 返回错误的可读文本。
func (e *IdempotencyKeyExhaustedError) Error() string {
	return fmt.Sprintf("%s (run_key=%s, status=%s)", ErrIdempotencyKeyExhausted.Error(), e.RunKey, e.Status)
}

// Unwrap 暴露底层错误，方便 errors.Is 或 errors.As 判断。
func (e *IdempotencyKeyExhaustedError) Unwrap() error { return ErrIdempotencyKeyExhausted }

// ErrRunStoreUnset 表示 service 未注入 RunStore（测试裸构造路径）不能调
// StartDAG。生产路径 ProvideService 会 setter 注入 RunStore。
var ErrRunStoreUnset = errors.New("orchestration: run store not configured")

// StartDAGRequest / StartDAGResponse 是 contract 包类型别名。
// 别名让 service 直接满足 contract.OrchestrationService，同时避免本包复制 wire DTO。
type StartDAGRequest = contract.StartDAGRequest
type StartDAGResponse = contract.StartDAGResponse

// TerminateDAGRequest 是终止一次 DAG run 的入参。
type TerminateDAGRequest = contract.TerminateDAGRequest

// DeleteDAG 删除 DAG 及其关联的节点和运行状态。
func (s *service) DeleteDAG(ctx context.Context, req contract.DeleteDAGRequest) error {
	dagKey := strings.TrimSpace(req.DagKey)
	if dagKey == "" {
		return errors.New("orchestration: dag key is required")
	}
	return s.withDAGStore(func(store taskdag.OrchestrationStore) error {
		deleter, ok := any(store).(taskdag.DAGDeleteStore)
		if ok {
			rows, err := deleter.DeleteDAG(ctx, dagKey)
			switch {
			case errors.Is(err, taskdag.ErrDAGDeleteActiveRun):
				return fmt.Errorf("%w: dag_key=%s", ErrDAGAlreadyRunning, dagKey)
			case platformdb.IsNotFound(err) || (err == nil && rows == 0):
				return fmt.Errorf("%w: dag_key=%s", ErrDAGNotFound, dagKey)
			case err != nil:
				return err
			}
			return nil
		}
		return errors.New("orchestration: dag delete store not configured")
	})
}

// file-count budget; typed op helpers live in dag_query.go.

// ErrApplyOpsInvalid 是 ApplyOps 顶层校验失败的 sentinel（InvalidArgument
// 类）。errors.Is 可用：errors.Is(err, ErrApplyOpsInvalid)。
var ErrApplyOpsInvalid = errors.New("orchestration: apply_ops invalid request")

// ErrVersionConflict 是 ApplyOps base_version OCC 冲突的 sentinel。
// errors.Is(err, ErrVersionConflict) 可命中。调用方应重新拉 dag.version
// 重试整套 ops（并重新干上下文决策）。
var ErrVersionConflict = errors.New("orchestration: apply_ops version conflict")

// ErrApplyOpsStoreNotConfigured 表示 service.dagStore 未实现 DAGOpsStore /
// DAGOpsTxRunner（测试裸构造路径）。生产路径 ProvideService 注入的
// taskdag.Store 同时实现两者、不会命中。
var ErrApplyOpsStoreNotConfigured = errors.New("orchestration: apply_ops dag store does not implement DAGOpsStore/DAGOpsTxRunner")

// ApplyOps 对 DAG 执行一组 typed ops（add_node / update_node / remove_node / update_dag），带 base_version OCC。
// 它是 AI 设计器、UI 表单和 ops MCP 工具的同一写入口：先校验版本，再解码白名单 op，最后进入事务规划和持久化。
//
// 错误分类决策：所有顶层失败统一包装 ErrApplyOpsInvalid，上层 MCP
// handler（translate*Error）按 errors.Is 转译为中英双语用户消息。这样
// service 层与 transport 层职责单一：service 决定「是不是合法」，transport
// 决定「怎么说人话」。
func (s *service) ApplyOps(ctx context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
	if req.BaseVersion < 0 {
		// base_version 必须非负：0 表示「首次写入空 DAG」，>0 是 OCC 期望
		// 版本。负数没有定义，直接拒。
		return contract.ApplyOpsResponse{}, fmt.Errorf("%w: base_version must be non-negative, got %d", ErrApplyOpsInvalid, req.BaseVersion)
	}

	var ops nodeexec.Ops
	// ops 字段为空时按「无操作」处理：解码空 RawMessage 在 encoding/json
	// 里会 panic 不直观，提前归一为 "null"。下游 applyTypedOps 会按当前 DAG
	// 版本决定是否返回同版本 no-op。
	raw := req.Ops
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	if err := json.Unmarshal(raw, &ops); err != nil {
		// nodeexec.Ops.UnmarshalJSON 已分类返出三种子错：顶层 JSON 非数组、单条 header 解析失败、
		// op discriminator 缺失或未知。这里统一包成 ErrApplyOpsInvalid，同时保留 nodeexec sentinel 链路。
		return contract.ApplyOpsResponse{}, fmt.Errorf("%w: %w", ErrApplyOpsInvalid, err)
	}

	return s.applyTypedOps(ctx, req.DagKey, req.BaseVersion, ops)
}

// applyTypedOps 是 4 个 op_kind 的事务入口。
// 它先做空操作短路，再把 add/update/remove/update_dag 统一交给 dag_query.go 的 plan/persist helper。
func (s *service) applyTypedOps(ctx context.Context, dagKey string, baseVersion int64, ops nodeexec.Ops) (contract.ApplyOpsResponse, error) {
	if s == nil || s.dagStore == nil {
		return contract.ApplyOpsResponse{}, ErrApplyOpsStoreNotConfigured
	}
	dagKey = strings.TrimSpace(dagKey)
	if dagKey == "" {
		return contract.ApplyOpsResponse{}, fmt.Errorf("%w: dag_key required", ErrApplyOpsInvalid)
	}
	parts, err := partitionOps(ops)
	if err != nil {
		return contract.ApplyOpsResponse{}, err
	}
	// 空 ops 在事务外短路：没有 add/update/remove/update_dag 时只需比对当前 version。
	// 这样合法 no-op 不会白付 FOR UPDATE 锁成本，也避免高并发下形成无意义锁竞争。
	if isNoopOpsBatch(parts) {
		return s.applyEmptyOpsShortCircuit(ctx, dagKey, baseVersion)
	}
	runner, ok := s.dagStore.(taskdag.DAGOpsTxRunner)
	if !ok {
		return contract.ApplyOpsResponse{}, ErrApplyOpsStoreNotConfigured
	}
	var resp contract.ApplyOpsResponse
	txErr := runner.WithDAGOpsTx(ctx, func(tx taskdag.DAGOpsStore) error {
		r, err := runOpsBatch(ctx, tx, dagKey, baseVersion, parts)
		if err != nil {
			return err
		}
		resp = r
		return nil
	})
	if txErr != nil {
		return contract.ApplyOpsResponse{}, txErr
	}
	return resp, nil
}

// isNoopOpsBatch 判断 ApplyOps 是否没有任何实际变更。
func isNoopOpsBatch(parts partitionedOps) bool {
	return len(parts.dagUpdates) == 0 && len(parts.adds) == 0 && len(parts.updates) == 0 && len(parts.removes) == 0
}

// applyEmptyOpsShortCircuit 让 no-op ApplyOps 保持无锁，同时保留与事务写路径一致的 OCC 错误语义。
func (s *service) applyEmptyOpsShortCircuit(ctx context.Context, dagKey string, baseVersion int64) (contract.ApplyOpsResponse, error) {
	reader, ok := s.dagStore.(taskdag.DAGVersionReader)
	if !ok {
		return contract.ApplyOpsResponse{}, ErrApplyOpsStoreNotConfigured
	}
	current, err := reader.GetDAGVersion(ctx, dagKey)
	if err != nil {
		return contract.ApplyOpsResponse{}, fmt.Errorf("get dag version: %w", err)
	}
	if current != baseVersion {
		return contract.ApplyOpsResponse{}, fmt.Errorf("%w: dag=%s expected=%d actual=%d", ErrVersionConflict, dagKey, baseVersion, current)
	}
	return contract.ApplyOpsResponse{NewVersion: current}, nil
}

// partitionOps / runOpsBatch / planOpsBatch / persistOpsBatch /
// existingNodesForPlan / existingFullForPlan / mergeAdjacency /
// persistAddNodeSpecs / persistUpdateChanges / indexExistingByKey /
// mergeNodePatch / isEmptyRawJSON 均位于 dag_query.go — 它们是 ApplyOps
// add/update 业务的 helper，拆出是为了让 dag.go 行数不超守卫上限。在同包
// 下拆不破可见性。
