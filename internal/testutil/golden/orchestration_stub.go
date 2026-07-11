package golden

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// OrchestrationStub 是测试用编排服务替身。
// 每个方法优先调用对应 Func，未注入时返回零值，方便 golden 测试只覆盖关心的接口。
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

type orchestrationAgentLifecycleStub = OrchestrationStub
type orchestrationAgentTurnStub = OrchestrationStub
type orchestrationReportStub = OrchestrationStub
type orchestrationDAGStub = OrchestrationStub
type orchestrationDAGRunStub = OrchestrationStub

// LaunchAgent 只把请求转交给测试注入的启动函数。
// 未注入时返回 nil，避免 golden 测试被无关编排能力阻塞。
func (s *orchestrationAgentLifecycleStub) LaunchAgent(ctx context.Context, req contract.LaunchRequest) error {
	if s.LaunchAgentFunc != nil {
		return s.LaunchAgentFunc(ctx, req)
	}
	return nil
}

// LaunchAgentSnapshot 启动 agent 并返回快照，未注入快照函数时返回 launching 零状态。
func (s *orchestrationAgentLifecycleStub) LaunchAgentSnapshot(ctx context.Context, req contract.LaunchRequest) (contract.AgentSnapshot, error) {
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

// ListAgents 返回测试注入的 agent 快照列表。
// 未注入时返回 nil slice，表示当前用例没有声明 agent 列表依赖。
func (s *orchestrationAgentLifecycleStub) ListAgents(ctx context.Context) ([]contract.AgentSnapshot, error) {
	if s.ListAgentsFunc != nil {
		return s.ListAgentsFunc(ctx)
	}
	return nil, nil
}

// StopAgent 委托测试注入的停止函数。
// 未注入时视为无操作，让只验证 RPC 载荷的用例无需维护 agent 状态机。
func (s *orchestrationAgentLifecycleStub) StopAgent(ctx context.Context, agentID string) error {
	if s.StopAgentFunc != nil {
		return s.StopAgentFunc(ctx, agentID)
	}
	return nil
}

// InterruptAgent 委托测试注入的中断函数。
// 未注入时返回零状态，表示当前用例不关心中断后的状态变化。
func (s *orchestrationAgentLifecycleStub) InterruptAgent(ctx context.Context, agentID string, source string) (contract.AgentStateResult, error) {
	if s.InterruptAgentFunc != nil {
		return s.InterruptAgentFunc(ctx, agentID, source)
	}
	return contract.AgentStateResult{}, nil
}

// SubmitTurn 只把 turn 提交请求转交给测试注入函数。
// 未注入时返回 nil，适合只覆盖 handler 路由而不启动真实会话的用例。
func (s *orchestrationAgentTurnStub) SubmitTurn(ctx context.Context, req contract.TurnSubmission) error {
	if s.SubmitTurnFunc != nil {
		return s.SubmitTurnFunc(ctx, req)
	}
	return nil
}

// CompleteTurn 委托测试注入函数记录 turn 结束结果。
// 未注入时不维护内存状态，避免测试替身暗自模拟真实编排逻辑。
func (s *orchestrationAgentTurnStub) CompleteTurn(ctx context.Context, agentID, turnID string, success bool, errMsg string) error {
	if s.CompleteTurnFunc != nil {
		return s.CompleteTurnFunc(ctx, agentID, turnID, success, errMsg)
	}
	return nil
}

// Recover 委托测试注入函数执行恢复断言。
// 未注入时返回 nil，表示当前用例未覆盖 agent 恢复路径。
func (s *orchestrationAgentLifecycleStub) Recover(ctx context.Context, agentID string) error {
	if s.RecoverFunc != nil {
		return s.RecoverFunc(ctx, agentID)
	}
	return nil
}

// BindSessionGeneration 委托测试注入函数校验会话代际绑定。
// 未注入时不保存代际，避免 golden 测试产生隐藏状态。
func (s *orchestrationAgentTurnStub) BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error {
	if s.BindSessionGenerationFunc != nil {
		return s.BindSessionGenerationFunc(ctx, agentID, generation)
	}
	return nil
}

// Snapshot 返回测试注入的 agent 快照。
// 未注入时返回零值，表示调用方需要在用例里显式声明快照期望。
func (s *orchestrationAgentLifecycleStub) Snapshot(ctx context.Context, agentID string) (contract.AgentSnapshot, error) {
	if s.SnapshotFunc != nil {
		return s.SnapshotFunc(ctx, agentID)
	}
	return contract.AgentSnapshot{}, nil
}

// UpdateRuntime 委托测试注入函数接收 runtime report。
// 未注入时不缓存 report，避免测试替身和真实 runtime 状态产生偏差。
func (s *orchestrationAgentTurnStub) UpdateRuntime(ctx context.Context, report contract.RuntimeReport) error {
	if s.UpdateRuntimeFunc != nil {
		return s.UpdateRuntimeFunc(ctx, report)
	}
	return nil
}

// GetState 返回测试注入的 agent 状态。
// 未注入时返回零值，表示当前用例没有声明状态读取期望。
func (s *orchestrationAgentLifecycleStub) GetState(ctx context.Context, agentID string) (contract.AgentStateResult, error) {
	if s.GetStateFunc != nil {
		return s.GetStateFunc(ctx, agentID)
	}
	return contract.AgentStateResult{}, nil
}

// GetReport 委托测试注入函数读取 agent report。
// 未注入时返回零值，避免测试替身生成真实报告结构。
func (s *orchestrationAgentTurnStub) GetReport(ctx context.Context, agentID string) (contract.AgentReportResult, error) {
	if s.GetReportFunc != nil {
		return s.GetReportFunc(ctx, agentID)
	}
	return contract.AgentReportResult{}, nil
}

// RememberReportRequest 委托测试注入函数校验报告记忆请求。
// 未注入时返回零值，表示该 golden 用例不覆盖记忆报告副作用。
func (s *orchestrationReportStub) RememberReportRequest(ctx context.Context, req contract.RememberReportRequest) (contract.RememberReportRequestResult, error) {
	if s.RememberReportRequestFunc != nil {
		return s.RememberReportRequestFunc(ctx, req)
	}
	return contract.RememberReportRequestResult{}, nil
}

// HandleReportEvent 委托测试注入函数处理报告事件。
// 未注入时返回零值，避免测试替身自行分发表驱动事件。
func (s *orchestrationReportStub) HandleReportEvent(ctx context.Context, event contract.ReportEvent) (contract.ReportEventResult, error) {
	if s.HandleReportEventFunc != nil {
		return s.HandleReportEventFunc(ctx, event)
	}
	return contract.ReportEventResult{}, nil
}

// CreateDAG 委托测试注入函数创建 DAG。
// 未注入时返回零值，表示用例未要求维护 DAG 持久化状态。
func (s *orchestrationDAGStub) CreateDAG(ctx context.Context, req contract.CreateDAGRequest) (contract.DAGDetail, error) {
	if s.CreateDAGFunc != nil {
		return s.CreateDAGFunc(ctx, req)
	}
	return contract.DAGDetail{}, nil
}

// GetDAG 返回测试注入的 DAG 详情。
// 未注入时返回零值，避免测试替身假装存在 DAG 存储。
func (s *orchestrationDAGStub) GetDAG(ctx context.Context, dagKey string) (contract.DAGDetail, error) {
	if s.GetDAGFunc != nil {
		return s.GetDAGFunc(ctx, dagKey)
	}
	return contract.DAGDetail{}, nil
}

// ListDAGs 委托测试注入函数返回 DAG 列表。
// 未注入时返回 nil slice，表示当前用例没有列表读取期望。
func (s *orchestrationDAGStub) ListDAGs(ctx context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
	if s.ListDAGsFunc != nil {
		return s.ListDAGsFunc(ctx, filter)
	}
	return nil, nil
}

// UpdateNodeStatus 委托测试注入函数校验节点状态更新。
// 未注入时返回零值，避免测试替身维护部分 DAG 状态。
func (s *orchestrationDAGStub) UpdateNodeStatus(ctx context.Context, req contract.UpdateNodeStatusRequest) (contract.DAGNode, error) {
	if s.UpdateNodeStatusFunc != nil {
		return s.UpdateNodeStatusFunc(ctx, req)
	}
	return contract.DAGNode{}, nil
}

// StartDAG 委托测试注入函数启动 DAG。
// 未注入时返回零值，适合只验证工具层请求映射的用例。
func (s *orchestrationDAGStub) StartDAG(ctx context.Context, req contract.StartDAGRequest) (contract.StartDAGResponse, error) {
	if s.StartDAGFunc != nil {
		return s.StartDAGFunc(ctx, req)
	}
	return contract.StartDAGResponse{}, nil
}

// TerminateDAG 委托测试注入函数终止 DAG。
// 未注入时返回 nil，表示当前用例不覆盖终止副作用。
func (s *orchestrationDAGStub) TerminateDAG(ctx context.Context, req contract.TerminateDAGRequest) error {
	if s.TerminateDAGFunc != nil {
		return s.TerminateDAGFunc(ctx, req)
	}
	return nil
}

// DeleteDAG 委托测试注入函数删除 DAG。
// 未注入时返回 nil，避免测试替身隐式维护删除后的列表状态。
func (s *orchestrationDAGStub) DeleteDAG(ctx context.Context, req contract.DeleteDAGRequest) error {
	if s.DeleteDAGFunc != nil {
		return s.DeleteDAGFunc(ctx, req)
	}
	return nil
}

// ApplyOps 委托测试注入函数应用 DAG 操作。
// 未注入时返回零值，表示用例没有声明批量操作结果。
func (s *orchestrationDAGRunStub) ApplyOps(ctx context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
	if s.ApplyOpsFunc != nil {
		return s.ApplyOpsFunc(ctx, req)
	}
	return contract.ApplyOpsResponse{}, nil
}

// GetRun 委托测试注入函数读取 DAG run 详情。
// 未注入时返回零值，避免替身构造不完整运行态。
func (s *orchestrationDAGRunStub) GetRun(ctx context.Context, req contract.GetRunRequest) (contract.GetRunResponse, error) {
	if s.GetRunFunc != nil {
		return s.GetRunFunc(ctx, req)
	}
	return contract.GetRunResponse{}, nil
}

// ListRuns 委托测试注入函数读取 DAG run 列表。
// 未注入时返回零值，表示当前用例未覆盖运行列表。
func (s *orchestrationDAGRunStub) ListRuns(ctx context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
	if s.ListRunsFunc != nil {
		return s.ListRunsFunc(ctx, req)
	}
	return contract.ListRunsResponse{}, nil
}

// DispatchNode 委托测试注入函数派发 DAG 节点。
// 未注入时返回零值，避免测试替身启动真实节点执行流程。
func (s *orchestrationDAGRunStub) DispatchNode(ctx context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error) {
	if s.DispatchNodeFunc != nil {
		return s.DispatchNodeFunc(ctx, req)
	}
	return contract.DispatchNodeResponse{}, nil
}
