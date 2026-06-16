package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

var errOrchestrationServiceNotAvailable = errors.New("dashboard: orchestration service not available")

type buildMetadata struct {
	version   string
	commit    string
	buildTime string
	dirty     bool
	goVersion string
	runtime   string
}

func turnHistoryFromSnapshot(snapshot AgentSnapshot) []TurnRef {
	turnID := strings.TrimSpace(snapshot.ActiveTurnID)
	if turnID == "" {
		return []TurnRef{}
	}
	return []TurnRef{{
		TurnID:   turnID,
		ThreadID: strings.TrimSpace(snapshot.ThreadID),
		AgentID:  strings.TrimSpace(snapshot.ID),
		Status:   strings.TrimSpace(snapshot.State),
	}}
}

func (s *service) effectiveDAGRuntime() contract.DAGRuntime {
	if s == nil {
		return nil
	}
	if s.dagRuntime != nil {
		return s.dagRuntime
	}
	return s.orchestration
}

// ListDAGs 列出dags。
func (s *service) ListDAGs(ctx context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	filter.Limit = kernel.ClampLimit(filter.Limit, 1, maxLogLimit, defaultLogLimit)
	if s.hasDAGSnapshotQueries() {
		return s.listDAGsFromSnapshot(ctx, filter)
	}
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return nil, errOrchestrationServiceNotAvailable
	}
	return runtime.ListDAGs(ctx, filter)
}

// GetDAGDetail 读取 DAG 详情。
func (s *service) GetDAGDetail(ctx context.Context, dagKey string) (*contract.DAGDetail, error) {
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return nil, errors.New("dashboard: dag key is required")
	}
	if s.hasDAGSnapshotQueries() {
		return s.getDAGDetailFromSnapshot(ctx, key)
	}
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return nil, errOrchestrationServiceNotAvailable
	}
	detail, err := runtime.GetDAG(ctx, key)
	if err != nil {
		return nil, err
	}
	return &detail, nil
}

// ListDAGRuns 列出DAG运行记录。
func (s *service) ListDAGRuns(ctx context.Context, dagKey, status string, limit int32) ([]contract.Run, error) {
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return nil, errors.New("dashboard: dag key is required")
	}
	limit = int32(kernel.ClampLimit(int(limit), 1, maxLogLimit, 50))
	status = strings.TrimSpace(status)
	if s.hasDAGSnapshotQueries() {
		return s.listDAGRunsFromSnapshot(ctx, key, status, limit)
	}
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return nil, errOrchestrationServiceNotAvailable
	}
	resp, err := runtime.ListRuns(ctx, contract.ListRunsRequest{
		DagKey: key,
		Status: status,
		Limit:  limit,
	})
	if err != nil {
		return nil, err
	}
	if resp.Runs == nil {
		return []contract.Run{}, nil
	}
	return resp.Runs, nil
}

// GetDAGRun 读取DAG运行记录。
func (s *service) GetDAGRun(ctx context.Context, runKey string) (contract.GetRunResponse, error) {
	key := strings.TrimSpace(runKey)
	if key == "" {
		return contract.GetRunResponse{}, errors.New("dashboard: run key is required")
	}
	if s.hasDAGSnapshotQueries() {
		return s.getDAGRunFromSnapshot(ctx, key)
	}
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return contract.GetRunResponse{}, errOrchestrationServiceNotAvailable
	}
	resp, err := runtime.GetRun(ctx, contract.GetRunRequest{RunKey: key})
	if err != nil {
		return contract.GetRunResponse{}, err
	}
	if resp.Nodes == nil {
		resp.Nodes = []contract.DAGNode{}
	}
	return resp, nil
}

// StartDAG 启动DAG。
func (s *service) StartDAG(ctx context.Context, dagKey, triggerSource, idempotencyKey string) (contract.StartDAGResponse, error) {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return contract.StartDAGResponse{}, errOrchestrationServiceNotAvailable
	}
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return contract.StartDAGResponse{}, errors.New("dashboard: dag key is required")
	}
	source := strings.TrimSpace(triggerSource)
	if source == "" {
		source = "manual"
	}
	if source != "manual" {
		return contract.StartDAGResponse{}, errors.New("dashboard: dag start trigger source must be manual")
	}
	return runtime.StartDAG(ctx, contract.StartDAGRequest{
		DagKey:         key,
		TriggerSource:  source,
		IdempotencyKey: strings.TrimSpace(idempotencyKey),
	})
}

// DispatchDAGNode 派发DAG节点。
func (s *service) DispatchDAGNode(ctx context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error) {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return contract.DispatchNodeResponse{}, errOrchestrationServiceNotAvailable
	}
	dispatcher, ok := any(runtime).(interface {
		DispatchNode(context.Context, contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error)
	})
	if !ok {
		return contract.DispatchNodeResponse{}, errOrchestrationServiceNotAvailable
	}
	request := contract.DispatchNodeRequest{
		DagKey:     strings.TrimSpace(req.DagKey),
		NodeKey:    strings.TrimSpace(req.NodeKey),
		RunID:      req.RunID,
		AssignedTo: strings.TrimSpace(req.AssignedTo),
	}
	if request.DagKey == "" {
		return contract.DispatchNodeResponse{}, errors.New("dashboard: dag key is required")
	}
	if request.NodeKey == "" {
		return contract.DispatchNodeResponse{}, errors.New("dashboard: node key is required")
	}
	if request.RunID <= 0 {
		return contract.DispatchNodeResponse{}, errors.New("dashboard: runId is required")
	}
	if request.AssignedTo == "" {
		return contract.DispatchNodeResponse{}, errors.New("dashboard: assignedTo is required")
	}
	return dispatcher.DispatchNode(ctx, request)
}

// TerminateDAG 处理terminateDAG。
func (s *service) TerminateDAG(ctx context.Context, dagKey, runKey, reason string) error {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return errOrchestrationServiceNotAvailable
	}
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return errors.New("dashboard: dag key is required")
	}
	run := strings.TrimSpace(runKey)
	if run == "" {
		return errors.New("dashboard: run key is required")
	}
	cause := strings.TrimSpace(reason)
	if cause == "" {
		cause = "user_requested"
	}
	return runtime.TerminateDAG(ctx, contract.TerminateDAGRequest{
		DagKey: key,
		RunKey: run,
		Reason: cause,
	})
}

// DeleteDAG 删除DAG。
func (s *service) DeleteDAG(ctx context.Context, dagKey string) error {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return errOrchestrationServiceNotAvailable
	}
	deleter, ok := any(runtime).(contract.DAGDeleteRuntime)
	if !ok {
		return errOrchestrationServiceNotAvailable
	}
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return errors.New("dashboard: dag key is required")
	}
	return deleter.DeleteDAG(ctx, contract.DeleteDAGRequest{DagKey: key})
}

// ApplyDAGOps 应用DAGops。
func (s *service) ApplyDAGOps(ctx context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return contract.ApplyOpsResponse{}, errOrchestrationServiceNotAvailable
	}
	key := strings.TrimSpace(req.DagKey)
	if key == "" {
		return contract.ApplyOpsResponse{}, errors.New("dashboard: dag key is required")
	}
	if req.BaseVersion < 0 {
		return contract.ApplyOpsResponse{}, errors.New("dashboard: dag base version must be non-negative")
	}
	if err := validateDashboardApplyOps(req.Ops); err != nil {
		return contract.ApplyOpsResponse{}, err
	}
	return runtime.ApplyOps(ctx, contract.ApplyOpsRequest{
		DagKey:      key,
		BaseVersion: req.BaseVersion,
		Ops:         append([]byte(nil), req.Ops...),
	})
}

func validateDashboardApplyOps(raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return errors.New("dashboard: dag ops are required")
	}
	var ops []json.RawMessage
	if err := json.Unmarshal(trimmed, &ops); err != nil {
		return fmt.Errorf("dashboard: dag ops must be a non-empty array: %w", err)
	}
	if len(ops) == 0 {
		return errors.New("dashboard: dag ops are required")
	}
	return nil
}
