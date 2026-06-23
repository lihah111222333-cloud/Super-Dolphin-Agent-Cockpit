package golden

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type OrchestrationStub struct {
	StartDAGFunc              func(context.Context, contract.StartDAGRequest) (contract.StartDAGResponse, error)
	TerminateDAGFunc          func(context.Context, contract.TerminateDAGRequest) error
	DeleteDAGFunc             func(context.Context, contract.DeleteDAGRequest) error
	GetRunFunc                func(context.Context, contract.GetRunRequest) (contract.GetRunResponse, error)
	ApplyOpsFunc              func(context.Context, contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error)
	ListRunsFunc              func(context.Context, contract.ListRunsRequest) (contract.ListRunsResponse, error)
	LaunchAgentFunc           func(context.Context, contract.LaunchRequest) error
	LaunchAgentSnapshotFunc   func(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error)
	ListAgentsFunc            func(context.Context) ([]contract.AgentSnapshot, error)
	StopAgentFunc             func(context.Context, string) error
	InterruptAgentFunc        func(context.Context, string, string) (contract.AgentStateResult, error)
	SubmitTurnFunc            func(context.Context, contract.TurnSubmission) error
	CompleteTurnFunc          func(context.Context, string, string, bool, string) error
	RecoverFunc               func(context.Context, string) error
	BindSessionGenerationFunc func(context.Context, string, uint64) error
	SnapshotFunc              func(context.Context, string) (contract.AgentSnapshot, error)
	UpdateRuntimeFunc         func(context.Context, contract.RuntimeReport) error
	GetStateFunc              func(context.Context, string) (contract.AgentStateResult, error)
	GetReportFunc             func(context.Context, string) (contract.AgentReportResult, error)
	RememberReportRequestFunc func(context.Context, contract.RememberReportRequest) (contract.RememberReportRequestResult, error)
	HandleReportEventFunc     func(context.Context, contract.ReportEvent) (contract.ReportEventResult, error)
	CreateDAGFunc             func(context.Context, contract.CreateDAGRequest) (contract.DAGDetail, error)
	GetDAGFunc                func(context.Context, string) (contract.DAGDetail, error)
	ListDAGsFunc              func(context.Context, contract.ListDAGsFilter) ([]contract.DAGSummary, error)
	UpdateNodeStatusFunc      func(context.Context, contract.UpdateNodeStatusRequest) (contract.DAGNode, error)
	DispatchNodeFunc          func(context.Context, contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error)
}

// LaunchAgent 启动代理。
func (s *OrchestrationStub) LaunchAgent(ctx context.Context, req contract.LaunchRequest) error {
	if s.LaunchAgentFunc != nil {
		return s.LaunchAgentFunc(ctx, req)
	}
	return nil
}

// LaunchAgentSnapshot 启动代理快照。
func (s *OrchestrationStub) LaunchAgentSnapshot(ctx context.Context, req contract.LaunchRequest) (contract.AgentSnapshot, error) {
	if s.LaunchAgentSnapshotFunc != nil {
		return s.LaunchAgentSnapshotFunc(ctx, req)
	}
	if err := s.LaunchAgent(ctx, req); err != nil {
		return contract.AgentSnapshot{}, err
	}
	if s.SnapshotFunc != nil {
		return s.Snapshot(ctx, req.AgentID)
	}
	return contract.AgentSnapshot{ID: req.AgentID, AgentID: req.AgentID, State: "launching"}, nil
}

// ListAgents 列出代理。
func (s *OrchestrationStub) ListAgents(ctx context.Context) ([]contract.AgentSnapshot, error) {
	if s.ListAgentsFunc != nil {
		return s.ListAgentsFunc(ctx)
	}
	return nil, nil
}

// StopAgent 停止代理。
func (s *OrchestrationStub) StopAgent(ctx context.Context, agentID string) error {
	if s.StopAgentFunc != nil {
		return s.StopAgentFunc(ctx, agentID)
	}
	return nil
}

// InterruptAgent 中断代理当前 turn。
func (s *OrchestrationStub) InterruptAgent(ctx context.Context, agentID string, source string) (contract.AgentStateResult, error) {
	if s.InterruptAgentFunc != nil {
		return s.InterruptAgentFunc(ctx, agentID, source)
	}
	return contract.AgentStateResult{}, nil
}

// SubmitTurn 提交turn。
func (s *OrchestrationStub) SubmitTurn(ctx context.Context, req contract.TurnSubmission) error {
	if s.SubmitTurnFunc != nil {
		return s.SubmitTurnFunc(ctx, req)
	}
	return nil
}

// CompleteTurn 完成turn。
func (s *OrchestrationStub) CompleteTurn(ctx context.Context, agentID, turnID string, success bool, errMsg string) error {
	if s.CompleteTurnFunc != nil {
		return s.CompleteTurnFunc(ctx, agentID, turnID, success, errMsg)
	}
	return nil
}

// Recover 恢复模块。
func (s *OrchestrationStub) Recover(ctx context.Context, agentID string) error {
	if s.RecoverFunc != nil {
		return s.RecoverFunc(ctx, agentID)
	}
	return nil
}

// BindSessionGeneration 绑定会话代际。
func (s *OrchestrationStub) BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error {
	if s.BindSessionGenerationFunc != nil {
		return s.BindSessionGenerationFunc(ctx, agentID, generation)
	}
	return nil
}

// Snapshot 处理快照。
func (s *OrchestrationStub) Snapshot(ctx context.Context, agentID string) (contract.AgentSnapshot, error) {
	if s.SnapshotFunc != nil {
		return s.SnapshotFunc(ctx, agentID)
	}
	return contract.AgentSnapshot{}, nil
}

// UpdateRuntime 更新运行时。
func (s *OrchestrationStub) UpdateRuntime(ctx context.Context, report contract.RuntimeReport) error {
	if s.UpdateRuntimeFunc != nil {
		return s.UpdateRuntimeFunc(ctx, report)
	}
	return nil
}

// GetState 读取状态。
func (s *OrchestrationStub) GetState(ctx context.Context, agentID string) (contract.AgentStateResult, error) {
	if s.GetStateFunc != nil {
		return s.GetStateFunc(ctx, agentID)
	}
	return contract.AgentStateResult{}, nil
}

// GetReport 读取report。
func (s *OrchestrationStub) GetReport(ctx context.Context, agentID string) (contract.AgentReportResult, error) {
	if s.GetReportFunc != nil {
		return s.GetReportFunc(ctx, agentID)
	}
	return contract.AgentReportResult{}, nil
}

// RememberReportRequest 处理rememberreport请求。
func (s *OrchestrationStub) RememberReportRequest(ctx context.Context, req contract.RememberReportRequest) (contract.RememberReportRequestResult, error) {
	if s.RememberReportRequestFunc != nil {
		return s.RememberReportRequestFunc(ctx, req)
	}
	return contract.RememberReportRequestResult{}, nil
}

// HandleReportEvent 处理report事件。
func (s *OrchestrationStub) HandleReportEvent(ctx context.Context, event contract.ReportEvent) (contract.ReportEventResult, error) {
	if s.HandleReportEventFunc != nil {
		return s.HandleReportEventFunc(ctx, event)
	}
	return contract.ReportEventResult{}, nil
}

// CreateDAG 创建DAG。
func (s *OrchestrationStub) CreateDAG(ctx context.Context, req contract.CreateDAGRequest) (contract.DAGDetail, error) {
	if s.CreateDAGFunc != nil {
		return s.CreateDAGFunc(ctx, req)
	}
	return contract.DAGDetail{}, nil
}

// GetDAG 读取DAG。
func (s *OrchestrationStub) GetDAG(ctx context.Context, dagKey string) (contract.DAGDetail, error) {
	if s.GetDAGFunc != nil {
		return s.GetDAGFunc(ctx, dagKey)
	}
	return contract.DAGDetail{}, nil
}

// ListDAGs 列出dags。
func (s *OrchestrationStub) ListDAGs(ctx context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
	if s.ListDAGsFunc != nil {
		return s.ListDAGsFunc(ctx, filter)
	}
	return nil, nil
}

// UpdateNodeStatus 更新节点状态。
func (s *OrchestrationStub) UpdateNodeStatus(ctx context.Context, req contract.UpdateNodeStatusRequest) (contract.DAGNode, error) {
	if s.UpdateNodeStatusFunc != nil {
		return s.UpdateNodeStatusFunc(ctx, req)
	}
	return contract.DAGNode{}, nil
}

// StartDAG 是 T1.1 加的接口方法；骨架阶段 stub 返回零值。
func (s *OrchestrationStub) StartDAG(ctx context.Context, req contract.StartDAGRequest) (contract.StartDAGResponse, error) {
	if s.StartDAGFunc != nil {
		return s.StartDAGFunc(ctx, req)
	}
	return contract.StartDAGResponse{}, nil
}

// TerminateDAG 处理terminateDAG。
func (s *OrchestrationStub) TerminateDAG(ctx context.Context, req contract.TerminateDAGRequest) error {
	if s.TerminateDAGFunc != nil {
		return s.TerminateDAGFunc(ctx, req)
	}
	return nil
}

// DeleteDAG 删除DAG。
func (s *OrchestrationStub) DeleteDAG(ctx context.Context, req contract.DeleteDAGRequest) error {
	if s.DeleteDAGFunc != nil {
		return s.DeleteDAGFunc(ctx, req)
	}
	return nil
}

// ApplyOps 是 T2.1+T2.2 加的接口方法；骨架阶段 stub 返回零值。
func (s *OrchestrationStub) ApplyOps(ctx context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
	if s.ApplyOpsFunc != nil {
		return s.ApplyOpsFunc(ctx, req)
	}
	return contract.ApplyOpsResponse{}, nil
}

// GetRun 是 T3.1 加的接口方法；stub 默认返回零值，测试按需注入 GetRunFunc。
//
// GetRun is the T3.1 interface method; the stub returns a zero value by
// default and tests inject GetRunFunc as needed.
func (s *OrchestrationStub) GetRun(ctx context.Context, req contract.GetRunRequest) (contract.GetRunResponse, error) {
	if s.GetRunFunc != nil {
		return s.GetRunFunc(ctx, req)
	}
	return contract.GetRunResponse{}, nil
}

// ListRuns 是 T3.2 加的接口方法；stub 默认返空 runs slice。
// ListRuns is the T3.2 interface method; stub defaults to an empty runs slice.
func (s *OrchestrationStub) ListRuns(ctx context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
	if s.ListRunsFunc != nil {
		return s.ListRunsFunc(ctx, req)
	}
	return contract.ListRunsResponse{}, nil
}

// DispatchNode 是 dispatcher wiring batch §4 加的接口方法 (ADR-004 §Open Q1)；
// stub 默认返零值；测试注入 DispatchNodeFunc 时走真实分支。
//
// DispatchNode is the dispatcher wiring batch addition for ADR-004 (Open Q1);
// stub returns a zero value by default. Tests inject DispatchNodeFunc.
func (s *OrchestrationStub) DispatchNode(ctx context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error) {
	if s.DispatchNodeFunc != nil {
		return s.DispatchNodeFunc(ctx, req)
	}
	return contract.DispatchNodeResponse{}, nil
}
