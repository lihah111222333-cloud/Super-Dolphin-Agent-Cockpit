package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeevents"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const defaultDAGStatus = "draft"

type createDAGParams struct {
	DagKey      string                `json:"dag_key"`
	Title       string                `json:"title"`
	Description string                `json:"description,omitempty"`
	CreatedBy   string                `json:"created_by,omitempty"`
	Metadata    json.RawMessage       `json:"metadata,omitempty"`
	Nodes       []createDAGNodeParams `json:"nodes,omitempty"`
}

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

type createDAGNodeParams struct {
	NodeKey    string          `json:"node_key"`
	Title      string          `json:"title"`
	NodeType   string          `json:"node_type,omitempty"`
	AssignedTo string          `json:"assigned_to,omitempty"`
	DependsOn  []string        `json:"depends_on,omitempty"`
	CommandRef string          `json:"command_ref,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
}

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

type listDAGsParams struct {
	Status  string `json:"status,omitempty"`
	Keyword string `json:"keyword,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type updateNodeParams struct {
	dagNodeParams
	RunID  int64           `json:"run_id"`
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
}

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

func (s *service) UpdateNodeStatus(ctx context.Context, req UpdateNodeStatusRequest) (DAGNode, error) {
	input, err := nodeStatusUpdateFromRequest(req)
	if err != nil {
		return DAGNode{}, err
	}
	var result DAGNode
	err = s.withDAGStore(func(store taskdag.OrchestrationStore) error {
		oldStatus, vErr := s.validateNodeTransition(ctx, store, input)
		if vErr != nil {
			return vErr
		}
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

// validateNodeTransition checks the run-scoped current status before public
// task_update_node writes; dispatcher fast paths use SQL fences instead.
func (s *service) validateNodeTransition(ctx context.Context, store taskdag.OrchestrationStore, input taskdag.NodeStatusUpdate) (string, error) {
	runReader, ok := any(store).(taskdag.RunNodeReadStore)
	if !ok {
		return "", fmt.Errorf("validate transition: store does not implement RunNodeReadStore for run_id=%d", input.RunID)
	}
	nodes, err := runReader.ListRunNodes(ctx, input.DagKey, input.RunID)
	if err != nil {
		return "", fmt.Errorf("validate transition: list run nodes %s run_id=%d: %w", input.DagKey, input.RunID, err)
	}
	var fromStatus string
	found := false
	for _, n := range nodes {
		if n.NodeKey == input.NodeKey {
			fromStatus = n.Status
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("validate transition: node %s/%s not found", input.DagKey, input.NodeKey)
	}
	if err := nodeexec.ValidateTransition(nodeexec.NodeStatus(fromStatus), nodeexec.NodeStatus(input.Status)); err != nil {
		return "", err
	}
	return fromStatus, nil
}

// completeNodeWithDownstream 走 store NodeFlowStore，3.5w 接通点。
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

// upsertDAGNodes writes template nodes, using the optional batch port when the
// store exposes it so DAGMutationStore stays within its interface budget.
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

func mapDAGSummaries(items []taskdag.DAG) []DAGSummary {
	mapped := make([]DAGSummary, 0, len(items))
	for _, item := range items {
		mapped = append(mapped, dagSummaryDTO(item))
	}
	return mapped
}

func mapDAGNodes(items []taskdag.Node) []DAGNode {
	mapped := make([]DAGNode, 0, len(items))
	for _, item := range items {
		mapped = append(mapped, dagNodeDTO(item))
	}
	return mapped
}

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

func dagScheduleEnabled(item taskdag.DAG) bool {
	return strings.TrimSpace(item.Trigger) == "scheduled" && strings.TrimSpace(item.CronExpr) != "" && item.NextRunAt != nil
}

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
		// F1.5 / ADR-009: spawning_thread_id 透出给 task_get_dag / DAG detail
		// 调用方（UI 拼「节点行 → 子 agent thread」跳转链接）。
		SpawningThreadID: trimStringPtr(item.SpawningThreadID),
	}
}

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

// =====================================================
// DAG 生命周期入口（S2.1 骨架接口位 + stub）
// =====================================================
//
// StartDAG / TerminateDAG / ApplyOps 的真实实现在 T1.2 / F4.x / F6.x 落地；
// 骨架阶段只定签名 + ErrLifecycleNotImplemented，让上层接口稳定。
//
// 真实运行时还会创建 task_dag_runs 行 + snapshot dag.version + 写
// node.run_id 等（见蓝图 v2 §5 决策"DAG 模板 + run 实例"模型 + S3.3 migration）。

// ErrLifecycleNotImplemented 是骨架阶段 stub 方法的 sentinel 错误。
// errors.Is 可用：errors.Is(err, ErrLifecycleNotImplemented)。
var ErrLifecycleNotImplemented = errors.New("orchestration lifecycle: not implemented in skeleton stage (T1.2/F4.x/F6.x)")

// ErrDAGNotFound 表示 StartDAG / TerminateDAG 调用时 dag_key 不存在。
//
// ErrDAGNotFound is returned when StartDAG / TerminateDAG is invoked with a
// dag_key that does not exist in storage.
var ErrDAGNotFound = errors.New("orchestration: dag_key not found")

// ErrDAGAlreadyRunning 是 F6.5 前 T1.2-mid 单 run 约束的历史 sentinel。
// 0089 移除 dag-level running unique 后 StartDAG 不再正常返回它。
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

func (e *IdempotencyKeyExhaustedError) Error() string {
	return fmt.Sprintf("%s (run_key=%s, status=%s)", ErrIdempotencyKeyExhausted.Error(), e.RunKey, e.Status)
}

func (e *IdempotencyKeyExhaustedError) Unwrap() error { return ErrIdempotencyKeyExhausted }

// ErrRunStoreUnset 表示 service 未注入 RunStore（测试裸构造路径）不能调
// StartDAG。生产路径 ProvideService 会 setter 注入 RunStore。
var ErrRunStoreUnset = errors.New("orchestration: run store not configured")

// StartDAGRequest / StartDAGResponse 现为 contract 包类型别名，让 service
// 能直接实现 contract.OrchestrationService 接口（T1.1 接通）。
type StartDAGRequest = contract.StartDAGRequest
type StartDAGResponse = contract.StartDAGResponse

// TerminateDAGRequest 是终止一次 DAG run 的入参。
type TerminateDAGRequest = contract.TerminateDAGRequest

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

// ApplyOps 对 DAG 执行一组 typed ops（add_node / update_node / remove_node /
// update_dag），带 base_version OCC。是 AI 设计师 + UI 表单 + ops MCP 工具
// 的同一接入点。
//
// 顶层分三段（F4.0）：
//  1. base_version 非负（OCC 单调约束的必要条件）；
//  2. ops 透过 nodeexec.Ops UnmarshalJSON 解码为 typed slice — 解码本身
//     包含 op discriminator 白名单校验（缺 op / 未知 op 都在那一层报错）；
//  3. 透传到 applyTypedOps 跑业务（F4.1-F4.4 落地，骨架返
//     ErrLifecycleNotImplemented）。
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
	// 里会 panic 不直观，提前归一为 "null"。下游 applyTypedOps 收到 nil
	// slice 走主路径，由 F4.1+ 决定是 noop 还是错。
	raw := req.Ops
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	if err := json.Unmarshal(raw, &ops); err != nil {
		// nodeexec.Ops.UnmarshalJSON 已经分类返出三种子错（顶层 JSON 不是
		// 数组 / 单条 header 不解 / op discriminator 缺失或未知），这里
		// 统一包成 ErrApplyOpsInvalid 让上层 errors.Is 命中。原始信息
		// 通过 %w 保留供调试 / 日志。
		// nodeexec.Ops.UnmarshalJSON already classifies the three sub-cases
		// (bad outer JSON / bad item header / missing or unknown op
		// discriminator); wrap them all as ErrApplyOpsInvalid so callers can
		// match with errors.Is. 双 %w 链（Go 1.20+）保留 nodeexec sentinel
		// 子错误链，让 errors.Is 仍能命中具体分类。
		return contract.ApplyOpsResponse{}, fmt.Errorf("%w: %w", ErrApplyOpsInvalid, err)
	}

	return s.applyTypedOps(ctx, req.DagKey, req.BaseVersion, ops)
}

// applyTypedOps 是 4 个 op_kind 业务实现的容器（F4.1-F4.4）。F4.1 接 add_node、
// F4.2 接 update_node，F4.3 接 remove_node，F4.4 接 update_dag。空 ops 返 noop。

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
	// R3 P2 #3 空 ops 事务外短路：apply_ops 不带 add/update/remove 时不需走事务也不需 FOR UPDATE
	// 锁。仅需拿到当前 task_dags.version 与 base_version 对齐判 OCC 同庄、返回同一 version
	// 即可。避免「合法调用但什么都不干」仍然白付 OCC 锁代价（并发则反过来变成锁竞争热点）。
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

func isNoopOpsBatch(parts partitionedOps) bool {
	return len(parts.dagUpdates) == 0 && len(parts.adds) == 0 && len(parts.updates) == 0 && len(parts.removes) == 0
}

// applyEmptyOpsShortCircuit keeps noop ApplyOps lock-free while preserving the
// same OCC and missing-store errors as the transactional write path.
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
