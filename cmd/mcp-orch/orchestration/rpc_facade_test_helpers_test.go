package orchestration

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

type rpcFacadeTestService interface {
	contract.AgentLifecyclePort
	contract.TurnSubmissionPort
	contract.AgentRuntimePort
	contract.AgentReportPort
	contract.DAGCreateRuntime
	contract.DAGRuntime
	contract.DAGDeleteRuntime
	contract.DAGNodeStatusRuntime
}

func testRPCFacadeParams(svc rpcFacadeTestService) RPCFacadeParams {
	return RPCFacadeParams{
		Lifecycle:  svc,
		Turns:      svc,
		Runtime:    svc,
		Reports:    svc,
		DAGCreate:  svc,
		DAGRuntime: svc,
		DAGDelete:  svc,
		NodeStatus: svc,
	}
}
