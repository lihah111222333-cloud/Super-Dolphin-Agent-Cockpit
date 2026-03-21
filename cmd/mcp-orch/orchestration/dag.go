package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
)

const defaultDAGStatus = "draft"

func (s *service) CreateDAG(ctx context.Context, req CreateDAGRequest) (DAGDetail, error) {
	store, err := s.dagStoreOrErr()
	if err != nil {
		return DAGDetail{}, err
	}
	var detail DAGDetail
	err = store.WithTx(ctx, func(txStore taskdag.Store) error {
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
	if err != nil {
		return DAGDetail{}, err
	}
	return detail, nil
}

func (s *service) GetDAG(ctx context.Context, dagKey string) (DAGDetail, error) {
	store, err := s.dagStoreOrErr()
	if err != nil {
		return DAGDetail{}, err
	}
	return loadDAGDetail(ctx, store, dagKey)
}

func (s *service) ListDAGs(ctx context.Context, filter ListDAGsFilter) ([]DAGSummary, error) {
	store, err := s.dagStoreOrErr()
	if err != nil {
		return nil, err
	}
	dags, err := store.ListDAGs(ctx, taskdag.ListDAGsFilter{
		Status:  strings.TrimSpace(filter.Status),
		Keyword: strings.TrimSpace(filter.Keyword),
		Limit:   normalizeDAGListLimit(filter.Limit),
	})
	if err != nil {
		return nil, err
	}
	return mapDAGSummaries(dags), nil
}

func (s *service) UpdateNodeStatus(ctx context.Context, req UpdateNodeStatusRequest) (DAGNode, error) {
	store, err := s.dagStoreOrErr()
	if err != nil {
		return DAGNode{}, err
	}
	input, err := nodeStatusUpdateFromRequest(req)
	if err != nil {
		return DAGNode{}, err
	}
	node, err := store.UpdateNodeStatus(ctx, input)
	if err != nil {
		return DAGNode{}, err
	}
	return dagNodeDTO(*node), nil
}

func (s *service) dagStoreOrErr() (taskdag.Store, error) {
	if s.dagStore == nil {
		return nil, errors.New("dag store is not configured")
	}
	return s.dagStore, nil
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
