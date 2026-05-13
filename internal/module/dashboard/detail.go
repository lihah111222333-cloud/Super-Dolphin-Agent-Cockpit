package dashboard

import (
	"context"
	"errors"
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

func (s *service) ListDAGs(ctx context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
	if s.orchestration == nil {
		return nil, errOrchestrationServiceNotAvailable
	}
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	filter.Limit = util.ClampLimit(filter.Limit, 1, maxLogLimit, defaultLogLimit)
	return s.orchestration.ListDAGs(ctx, filter)
}

func (s *service) GetDAGDetail(ctx context.Context, dagKey string) (*contract.DAGDetail, error) {
	if s.orchestration == nil {
		return nil, errOrchestrationServiceNotAvailable
	}
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return nil, errors.New("dashboard: dag key is required")
	}
	detail, err := s.orchestration.GetDAG(ctx, key)
	if err != nil {
		return nil, err
	}
	return &detail, nil
}

func (s *service) ListDAGRuns(ctx context.Context, dagKey string, limit int32) ([]contract.Run, error) {
	if s.orchestration == nil {
		return nil, errOrchestrationServiceNotAvailable
	}
	key := strings.TrimSpace(dagKey)
	if key == "" {
		return nil, errors.New("dashboard: dag key is required")
	}
	limit = int32(util.ClampLimit(int(limit), 1, maxLogLimit, 50))
	resp, err := s.orchestration.ListRuns(ctx, contract.ListRunsRequest{
		DagKey: key,
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
