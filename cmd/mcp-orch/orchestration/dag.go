package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
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
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
}

func (p *updateNodeParams) UnmarshalJSON(data []byte) error {
	type current updateNodeParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		DagKey  string `json:"dagKey"`
		NodeKey string `json:"nodeKey"`
	}) error {
		*p = updateNodeParams(*raw)
		if strings.TrimSpace(p.DagKey) == "" {
			p.DagKey = strings.TrimSpace(legacy.DagKey)
		}
		if strings.TrimSpace(p.NodeKey) == "" {
			p.NodeKey = strings.TrimSpace(legacy.NodeKey)
		}
		return nil
	})
}

func (s *service) CreateDAG(ctx context.Context, req CreateDAGRequest) (DAGDetail, error) {
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
		// S7.2: 状态转移合法性校验。用 ListNodes 拿当前 from 状态，用
		// nodeexec.ValidateTransition 检查；接口不提供单节点读，用 ListNodes
		// + 筛选是当前唯一路径。F 阶段可加 GetNode 小接口优化。
		if vErr := s.validateNodeTransition(ctx, store, input); vErr != nil {
			return vErr
		}
		// Phase 3.5w: status="done" 切到 CompleteNodeAndScheduleDownstream
		// 让 store 同事务自动 enqueue 下游 ready 节点（生产路径自动 spawn）。
		// 生产 dagStore 实际是 taskdag.Store（embed NodeFlowStore），type
		// assertion 拿到该能力；测试 mock 落到普通 UpdateNodeStatus 不破。
		if input.Status == "done" {
			if flow, ok := store.(taskdag.NodeFlowStore); ok {
				return s.completeNodeWithDownstream(ctx, flow, input, &result)
			}
		}
		node, updateErr := store.UpdateNodeStatus(ctx, input)
		if updateErr != nil {
			return updateErr
		}
		result = dagNodeDTO(*node)
		return nil
	})
	if err != nil {
		return DAGNode{}, err
	}
	return result, nil
}

// validateNodeTransition 走 ListNodes 取当前 node.status，交 nodeexec.ValidateTransition
// 检查。节点不存在时返回明确错误（防 typo / 并发删节点）。
func (s *service) validateNodeTransition(ctx context.Context, store taskdag.OrchestrationStore, input taskdag.NodeStatusUpdate) error {
	nodes, err := store.ListNodes(ctx, input.DagKey)
	if err != nil {
		return fmt.Errorf("validate transition: list nodes %s: %w", input.DagKey, err)
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
		return fmt.Errorf("validate transition: node %s/%s not found", input.DagKey, input.NodeKey)
	}
	return nodeexec.ValidateTransition(nodeexec.NodeStatus(fromStatus), nodeexec.NodeStatus(input.Status))
}

// completeNodeWithDownstream 走 store NodeFlowStore，3.5w 接通点。
func (s *service) completeNodeWithDownstream(ctx context.Context, flow taskdag.NodeFlowStore, input taskdag.NodeStatusUpdate, result *DAGNode) error {
	res, err := flow.CompleteNodeAndScheduleDownstream(ctx, taskdag.CompleteNodeInput(input))
	if err != nil {
		return err
	}
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

func upsertDAGNodes(ctx context.Context, store taskdag.DAGMutationStore, dagKey string, nodes []CreateDAGNodeRequest) error {
	for _, node := range nodes {
		if _, err := store.UpsertNode(ctx, dagNodeFromRequest(dagKey, node)); err != nil {
			return err
		}
	}
	return nil
}

func loadDAGDetail(ctx context.Context, store taskdag.DAGDetailStore, dagKey string) (DAGDetail, error) {
	dag, err := store.GetDAG(ctx, strings.TrimSpace(dagKey))
	if err != nil {
		return DAGDetail{}, err
	}
	nodes, err := store.ListNodes(ctx, dag.DagKey)
	if err != nil {
		return DAGDetail{}, err
	}
	return DAGDetail{DAG: dagSummaryDTO(*dag), Nodes: mapDAGNodes(nodes)}, nil
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
	return taskdag.NodeStatusUpdate{
		DagKey:  strings.TrimSpace(req.DagKey),
		NodeKey: strings.TrimSpace(req.NodeKey),
		Status:  strings.TrimSpace(req.Status),
		Result:  append(json.RawMessage(nil), req.Result...),
	}, nil
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
		ID:          item.ID,
		DagKey:      item.DagKey,
		Title:       item.Title,
		Description: item.Description,
		Status:      item.Status,
		CreatedBy:   item.CreatedBy,
		Metadata:    append(json.RawMessage(nil), item.Metadata...),
		StartedAt:   shared.CloneTime(item.StartedAt),
		FinishedAt:  shared.CloneTime(item.FinishedAt),
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
	}
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
		ActiveTurnID:   cloneString(item.ActiveTurnID),
		ActiveWakeupID: cloneInt64(item.ActiveWakeupID),
		LastEventAt:    shared.CloneTime(item.LastEventAt),
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

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copied := strings.TrimSpace(*value)
	return &copied
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
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

// StartDAGRequest 是触发 DAG 一次新执行的入参。
type StartDAGRequest struct {
	DagKey         string // 必填
	TriggerSource  string // manual | auto | scheduled | external
	IdempotencyKey string // 可选，防重复 run
}

// StartDAGResponse 是 StartDAG 的返回。
type StartDAGResponse struct {
	RunKey  string // 新 run 的唯一键（例 dag_xxx#run_2026-05-10T08:00）
	Version int64  // 该 run snapshot 的 dag.version
}

// TerminateDAGRequest 是终止一次 DAG run 的入参。
type TerminateDAGRequest struct {
	DagKey string // 必填
	RunKey string // 必填，目标 run
	Reason string // 可选，写入 events 字段
}

// StartDAG 触发 DAG 一次新执行（创建 run、初始化节点、第一批 ready 节点入队）。
// 骨架阶段：仅返回 ErrLifecycleNotImplemented；T1.2 真实落地。
func (s *service) StartDAG(_ context.Context, _ StartDAGRequest) (StartDAGResponse, error) {
	return StartDAGResponse{}, ErrLifecycleNotImplemented
}

// TerminateDAG 终止一次 run（标 cancelled，级联取消 pending/ready 节点）。
// 骨架阶段：仅返回 ErrLifecycleNotImplemented；F6.x 真实落地。
func (s *service) TerminateDAG(_ context.Context, _ TerminateDAGRequest) error {
	return ErrLifecycleNotImplemented
}
