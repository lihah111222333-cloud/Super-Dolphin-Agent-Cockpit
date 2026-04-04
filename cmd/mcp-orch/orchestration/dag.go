package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
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
	err := s.withDAGStore(func(store taskdag.Store) error {
		return store.WithTx(ctx, func(txStore taskdag.Store) error {
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
	err := s.withDAGStore(func(store taskdag.Store) error {
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
	err := s.withDAGStore(func(store taskdag.Store) error {
		dags, listErr := store.ListDAGs(ctx, taskdag.ListDAGsFilter{
			Status:  strings.TrimSpace(filter.Status),
			Keyword: strings.TrimSpace(filter.Keyword),
			Limit:   normalizeDAGListLimit(filter.Limit),
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
	err = s.withDAGStore(func(store taskdag.Store) error {
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

func upsertDAG(ctx context.Context, store taskdag.Store, req CreateDAGRequest) (*taskdag.DAG, error) {
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

func upsertDAGNodes(ctx context.Context, store taskdag.Store, dagKey string, nodes []CreateDAGNodeRequest) error {
	for _, node := range nodes {
		if _, err := store.UpsertNode(ctx, dagNodeFromRequest(dagKey, node)); err != nil {
			return err
		}
	}
	return nil
}

func loadDAGDetail(ctx context.Context, store taskdag.Store, dagKey string) (DAGDetail, error) {
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

func normalizeDAGListLimit(limit int) int32 {
	if limit <= 0 {
		return 50
	}
	return int32(limit)
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
		StartedAt:   cloneTime(item.StartedAt),
		FinishedAt:  cloneTime(item.FinishedAt),
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
		StartedAt:      cloneTime(item.StartedAt),
		FinishedAt:     cloneTime(item.FinishedAt),
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
		ActiveTurnID:   cloneString(item.ActiveTurnID),
		ActiveWakeupID: cloneInt64(item.ActiveWakeupID),
		LastEventAt:    cloneTime(item.LastEventAt),
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
