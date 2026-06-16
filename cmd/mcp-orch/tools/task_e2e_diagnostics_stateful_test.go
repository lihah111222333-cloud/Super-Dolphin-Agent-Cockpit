package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

type diagnosticStatefulService struct {
	golden.OrchestrationStub

	t                 *testing.T
	dags              map[string]contract.DAGDetail
	runs              []contract.Run
	wakeups           int64
	startDAGCalls     int
	wantListDAGsLimit int
}

func newDiagnosticStatefulService(t *testing.T, details map[string]contract.DAGDetail) *diagnosticStatefulService {
	t.Helper()
	svc := &diagnosticStatefulService{
		t:    t,
		dags: make(map[string]contract.DAGDetail),
	}
	for dagKey, detail := range details {
		svc.dags[dagKey] = cloneDAGDetail(detail)
	}
	return svc
}

// CreateDAG 模拟持久化边界：只有 handler 校验通过后才会把 DAG 模板写入内存状态。
func (s *diagnosticStatefulService) CreateDAG(_ context.Context, req contract.CreateDAGRequest) (contract.DAGDetail, error) {
	detail := contract.DAGDetail{
		DAG: contract.DAGSummary{
			DagKey:      req.DagKey,
			Version:     1,
			Title:       req.Title,
			Description: req.Description,
			Status:      "draft",
			CreatedBy:   req.CreatedBy,
			Metadata:    append(json.RawMessage(nil), req.Metadata...),
		},
		Nodes: createDiagnosticDAGNodes(req),
	}
	s.dags[req.DagKey] = cloneDAGDetail(detail)
	return cloneDAGDetail(detail), nil
}

func createDiagnosticDAGNodes(req contract.CreateDAGRequest) []contract.DAGNode {
	nodes := make([]contract.DAGNode, 0, len(req.Nodes))
	for _, node := range req.Nodes {
		nodes = append(nodes, contract.DAGNode{
			DagKey:     req.DagKey,
			NodeKey:    node.NodeKey,
			Title:      node.Title,
			NodeType:   node.NodeType,
			AssignedTo: node.AssignedTo,
			DependsOn:  append([]string(nil), node.DependsOn...),
			Config:     append(json.RawMessage(nil), node.Config...),
		})
	}
	return nodes
}

func (s *diagnosticStatefulService) GetDAG(_ context.Context, dagKey string) (contract.DAGDetail, error) {
	detail, ok := s.dags[strings.TrimSpace(dagKey)]
	if !ok {
		return contract.DAGDetail{}, fmt.Errorf("dag %s not found", dagKey)
	}
	return cloneDAGDetail(detail), nil
}

func (s *diagnosticStatefulService) ListDAGs(_ context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
	if s.wantListDAGsLimit != 0 && filter.Limit != s.wantListDAGsLimit {
		s.t.Fatalf("ListDAGs limit = %d, want %d", filter.Limit, s.wantListDAGsLimit)
	}
	out := s.sortedDAGSummaries()
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *diagnosticStatefulService) sortedDAGSummaries() []contract.DAGSummary {
	out := make([]contract.DAGSummary, 0, len(s.dags))
	for _, detail := range s.dags {
		out = append(out, cloneDAGSummary(detail.DAG))
	}
	slices.SortFunc(out, func(a, b contract.DAGSummary) int {
		return strings.Compare(a.DagKey, b.DagKey)
	})
	return out
}

func (s *diagnosticStatefulService) ApplyOps(_ context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
	detail, err := s.dagForApplyOps(req)
	if err != nil {
		return contract.ApplyOpsResponse{}, err
	}
	ops, err := decodeDiagnosticApplyOps(req.Ops)
	if err != nil {
		return contract.ApplyOpsResponse{}, err
	}
	for _, op := range ops {
		if err := applyDiagnosticDAGPatch(&detail.DAG, op.Patch); err != nil {
			return contract.ApplyOpsResponse{}, err
		}
	}
	detail.DAG.Version++
	s.dags[req.DagKey] = cloneDAGDetail(detail)
	return contract.ApplyOpsResponse{NewVersion: detail.DAG.Version}, nil
}

func (s *diagnosticStatefulService) dagForApplyOps(req contract.ApplyOpsRequest) (contract.DAGDetail, error) {
	detail, ok := s.dags[strings.TrimSpace(req.DagKey)]
	if !ok {
		return contract.DAGDetail{}, fmt.Errorf("dag %s not found", req.DagKey)
	}
	if detail.DAG.Version != req.BaseVersion {
		return contract.DAGDetail{}, fmt.Errorf("base version %d does not match current version %d", req.BaseVersion, detail.DAG.Version)
	}
	return detail, nil
}

type diagnosticApplyOp struct {
	Op    string                     `json:"op"`
	Patch map[string]json.RawMessage `json:"patch"`
}

func decodeDiagnosticApplyOps(raw json.RawMessage) ([]diagnosticApplyOp, error) {
	var ops []diagnosticApplyOp
	if err := json.Unmarshal(raw, &ops); err != nil {
		return nil, err
	}
	for _, op := range ops {
		if op.Op != "update_dag" {
			return nil, fmt.Errorf("unsupported op %q", op.Op)
		}
	}
	return ops, nil
}

func applyDiagnosticDAGPatch(summary *contract.DAGSummary, patch map[string]json.RawMessage) error {
	for key, raw := range patch {
		if err := applyDiagnosticDAGPatchField(summary, key, raw); err != nil {
			return err
		}
	}
	return nil
}

func applyDiagnosticDAGPatchField(summary *contract.DAGSummary, key string, raw json.RawMessage) error {
	switch key {
	case "trigger":
		return applyDiagnosticTriggerPatch(summary, raw)
	case "cron_expr":
		return json.Unmarshal(raw, &summary.CronExpr)
	case "title":
		return json.Unmarshal(raw, &summary.Title)
	case "description":
		return json.Unmarshal(raw, &summary.Description)
	default:
		return fmt.Errorf("unsupported patch field %q", key)
	}
}

func applyDiagnosticTriggerPatch(summary *contract.DAGSummary, raw json.RawMessage) error {
	if err := json.Unmarshal(raw, &summary.Trigger); err != nil {
		return err
	}
	if summary.Trigger == "scheduled" {
		summary.ScheduleEnabled = true
	}
	return nil
}

func (s *diagnosticStatefulService) StartDAG(_ context.Context, req contract.StartDAGRequest) (contract.StartDAGResponse, error) {
	s.startDAGCalls++
	if _, ok := s.dags[strings.TrimSpace(req.DagKey)]; !ok {
		return contract.StartDAGResponse{}, fmt.Errorf("dag %s not found", req.DagKey)
	}
	runKey := req.DagKey + "#run-1"
	s.runs = append(s.runs, contract.Run{RunKey: runKey, DagKey: req.DagKey, Status: "running"})
	s.wakeups++
	return contract.StartDAGResponse{
		RunKey:           runKey,
		Version:          1,
		ReadyRootNodes:   1,
		ScheduledWakeups: 1,
		ExecutionState:   contract.StartDAGExecutionQueued,
	}, nil
}

func (s *diagnosticStatefulService) ListRuns(_ context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
	if req.Limit != 1 && req.Limit != 0 {
		s.t.Fatalf("ListRuns(%s) limit = %d, want recent-only lookup", req.DagKey, req.Limit)
	}
	if len(s.runs) == 0 {
		return contract.ListRunsResponse{
			Runs: []contract.Run{{RunKey: req.DagKey + "#run-1", DagKey: req.DagKey, Status: "failed"}},
		}, nil
	}
	return contract.ListRunsResponse{Runs: s.runsForDAG(req.DagKey)}, nil
}

func (s *diagnosticStatefulService) runsForDAG(dagKey string) []contract.Run {
	out := make([]contract.Run, 0, len(s.runs))
	for _, run := range s.runs {
		if run.DagKey == dagKey {
			out = append(out, run)
		}
	}
	return out
}

func assertDAGNotStored(t *testing.T, svc *diagnosticStatefulService, dagKey string) {
	t.Helper()
	if _, err := svc.GetDAG(context.Background(), dagKey); err == nil {
		t.Fatalf("GetDAG(%q) error = nil, want missing DAG after validation failure", dagKey)
	}
	if len(svc.dags) != 0 {
		t.Fatalf("stored DAGs after validation failure = %#v, want empty", svc.dags)
	}
}

func assertNoRunsOrWakeups(t *testing.T, svc *diagnosticStatefulService) {
	t.Helper()
	if len(svc.runs) != 0 || svc.wakeups != 0 || svc.startDAGCalls != 0 {
		t.Fatalf(
			"state after validation failure = runs:%d wakeups:%d start_calls:%d, want all zero",
			len(svc.runs),
			svc.wakeups,
			svc.startDAGCalls,
		)
	}
}

func cloneDAGDetail(detail contract.DAGDetail) contract.DAGDetail {
	out := detail
	out.DAG = cloneDAGSummary(detail.DAG)
	out.Nodes = append([]contract.DAGNode(nil), detail.Nodes...)
	for i := range out.Nodes {
		out.Nodes[i].DependsOn = append([]string(nil), out.Nodes[i].DependsOn...)
		out.Nodes[i].Config = append(json.RawMessage(nil), out.Nodes[i].Config...)
	}
	return out
}

func cloneDAGSummary(summary contract.DAGSummary) contract.DAGSummary {
	out := summary
	out.Metadata = append(json.RawMessage(nil), summary.Metadata...)
	return out
}
