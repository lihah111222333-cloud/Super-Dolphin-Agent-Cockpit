package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
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

func (s *service) ListDAGs(ctx context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return nil, errOrchestrationServiceNotAvailable
	}
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	filter.Limit = util.ClampLimit(filter.Limit, 1, maxLogLimit, defaultLogLimit)
	return runtime.ListDAGs(ctx, filter)
}

func (s *service) GetDAGDetail(ctx context.Context, dagKey string) (*contract.DAGDetail, error) {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return nil, errOrchestrationServiceNotAvailable
	}
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return nil, errors.New("dashboard: dag key is required")
	}
	detail, err := runtime.GetDAG(ctx, key)
	if err != nil {
		return nil, err
	}
	return &detail, nil
}

func (s *service) ListDAGRuns(ctx context.Context, dagKey, status string, limit int32) ([]contract.Run, error) {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return nil, errOrchestrationServiceNotAvailable
	}
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return nil, errors.New("dashboard: dag key is required")
	}
	limit = int32(util.ClampLimit(int(limit), 1, maxLogLimit, 50))
	resp, err := runtime.ListRuns(ctx, contract.ListRunsRequest{
		DagKey: key,
		Status: strings.TrimSpace(status),
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

func (s *service) GetDAGRun(ctx context.Context, runKey string) (contract.GetRunResponse, error) {
	runtime := s.effectiveDAGRuntime()
	if runtime == nil {
		return contract.GetRunResponse{}, errOrchestrationServiceNotAvailable
	}
	key := strings.TrimSpace(runKey)
	if key == "" {
		return contract.GetRunResponse{}, errors.New("dashboard: run key is required")
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
