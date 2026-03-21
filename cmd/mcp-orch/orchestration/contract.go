package orchestration

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

type Service = contract.OrchestrationService
type SessionCleaner = contract.OrchestrationSessionCleaner
type TurnStarter = contract.OrchestrationTurnStarter

type TurnSubmission = contract.TurnSubmission
type RuntimeReport = contract.RuntimeReport

type LaunchRequest = contract.LaunchRequest
type AgentSnapshot = contract.AgentSnapshot
type AgentStateResult = contract.AgentStateResult
type AgentReportMetadata = contract.AgentReportMetadata
type AgentReportResult = contract.AgentReportResult
type RememberReportRequest = contract.RememberReportRequest
type RememberReportRequestResult = contract.RememberReportRequestResult
type ReportEvent = contract.ReportEvent
type ReportEventResult = contract.ReportEventResult
type CreateDAGRequest = contract.CreateDAGRequest
type CreateDAGNodeRequest = contract.CreateDAGNodeRequest
type ListDAGsFilter = contract.ListDAGsFilter
type UpdateNodeStatusRequest = contract.UpdateNodeStatusRequest
type DAGSummary = contract.DAGSummary
type DAGNode = contract.DAGNode
type DAGDetail = contract.DAGDetail
