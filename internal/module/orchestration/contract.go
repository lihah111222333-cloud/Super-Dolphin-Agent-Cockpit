package orchestration

import (
	"context"
	"encoding/json"
	"time"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

type Service interface {
	LaunchAgent(ctx context.Context, req LaunchRequest) error
	ListAgents(ctx context.Context) ([]AgentSnapshot, error)
	StopAgent(ctx context.Context, agentID string) error
	SubmitTurn(ctx context.Context, req TurnSubmission) error
	CompleteTurn(ctx context.Context, agentID, turnID string, success bool, errMsg string) error
	Recover(ctx context.Context, agentID string) error
	Snapshot(ctx context.Context, agentID string) (AgentSnapshot, error)
	SetReport(ctx context.Context, agentID, report string) error
	GetState(ctx context.Context, agentID string) (AgentStateResult, error)
	GetReport(ctx context.Context, agentID string) (AgentReportResult, error)
	RememberReportRequest(ctx context.Context, req RememberReportRequest) (RememberReportRequestResult, error)
	HandleReportEvent(ctx context.Context, event ReportEvent) (ReportEventResult, error)
	CreateDAG(ctx context.Context, req CreateDAGRequest) (DAGDetail, error)
	GetDAG(ctx context.Context, dagKey string) (DAGDetail, error)
	ListDAGs(ctx context.Context, filter ListDAGsFilter) ([]DAGSummary, error)
	UpdateNodeStatus(ctx context.Context, req UpdateNodeStatusRequest) (DAGNode, error)
}

type SessionCleaner interface {
	RemoveSession(agentID string)
}

type TurnSubmission = turndto.TurnSubmission

type LaunchRequest struct {
	AgentID  string
	Name     string
	ParentID string
	Cwd      string
	Command  []string
	Env      []string
}

type AgentSnapshot struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ParentID   string `json:"parent_id,omitempty"`
	Port       int    `json:"port"`
	ThreadID   string `json:"thread_id"`
	Cwd        string `json:"cwd"`
	State      string `json:"state"`
	Provider   string `json:"provider,omitempty"`
	LastReport string `json:"last_report,omitempty"`
}

type AgentStateResult struct {
	AgentID string `json:"agent_id"`
	State   string `json:"state"`
}

type AgentReportMetadata struct {
	RequesterIDs []string `json:"requester_ids,omitempty"`
}

type AgentReportResult struct {
	AgentID  string               `json:"agent_id"`
	Report   string               `json:"report"`
	State    string               `json:"state"`
	Metadata *AgentReportMetadata `json:"metadata,omitempty"`
}

type RememberReportRequest struct {
	AgentID     string
	RequesterID string
}

type RememberReportRequestResult struct {
	Success     bool   `json:"success"`
	AgentID     string `json:"agent_id"`
	RequesterID string `json:"requester_id"`
}

type ReportEvent struct {
	AgentID   string
	Report    string
	EventType string
	EventData json.RawMessage
}

type ReportEventResult struct {
	Success              bool     `json:"success"`
	AgentID              string   `json:"agent_id"`
	EventType            string   `json:"event_type,omitempty"`
	Report               string   `json:"report,omitempty"`
	NotifiedRequesterIDs []string `json:"notified_requester_ids,omitempty"`
}

type CreateDAGRequest struct {
	DagKey      string
	Title       string
	Description string
	CreatedBy   string
	Metadata    json.RawMessage
	Nodes       []CreateDAGNodeRequest
}

type CreateDAGNodeRequest struct {
	NodeKey    string
	Title      string
	NodeType   string
	AssignedTo string
	DependsOn  []string
	CommandRef string
	Config     json.RawMessage
}

type ListDAGsFilter struct {
	Status  string
	Keyword string
	Limit   int
}

type UpdateNodeStatusRequest struct {
	DagKey  string
	NodeKey string
	Status  string
	Result  json.RawMessage
}

type DAGSummary struct {
	ID          int64           `json:"id"`
	DagKey      string          `json:"dag_key"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Status      string          `json:"status"`
	CreatedBy   string          `json:"created_by,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type DAGNode struct {
	ID             int64           `json:"id"`
	DagKey         string          `json:"dag_key"`
	NodeKey        string          `json:"node_key"`
	Title          string          `json:"title"`
	NodeType       string          `json:"node_type,omitempty"`
	AssignedTo     string          `json:"assigned_to,omitempty"`
	DependsOn      []string        `json:"depends_on,omitempty"`
	Status         string          `json:"status"`
	CommandRef     string          `json:"command_ref,omitempty"`
	Config         json.RawMessage `json:"config,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	ActiveTurnID   *string         `json:"active_turn_id,omitempty"`
	ActiveWakeupID *int64          `json:"active_wakeup_id,omitempty"`
	LastEventAt    *time.Time      `json:"last_event_at,omitempty"`
}

type DAGDetail struct {
	DAG   DAGSummary `json:"dag"`
	Nodes []DAGNode  `json:"nodes,omitempty"`
}
