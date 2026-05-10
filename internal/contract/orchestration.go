package contract

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

var ErrAgentNotFound = errors.New("agent not found")

// OrchestrationService defines the shared orchestration boundary used by
// internal modules and the MCP orchestration runtime.
type OrchestrationService interface {
	LaunchAgent(ctx context.Context, req LaunchRequest) error
	ListAgents(ctx context.Context) ([]AgentSnapshot, error)
	StopAgent(ctx context.Context, agentID string) error
	SubmitTurn(ctx context.Context, req TurnSubmission) error
	CompleteTurn(ctx context.Context, agentID, turnID string, success bool, errMsg string) error
	Recover(ctx context.Context, agentID string) error
	BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error
	Snapshot(ctx context.Context, agentID string) (AgentSnapshot, error)
	UpdateRuntime(ctx context.Context, report RuntimeReport) error
	GetState(ctx context.Context, agentID string) (AgentStateResult, error)
	GetReport(ctx context.Context, agentID string) (AgentReportResult, error)
	RememberReportRequest(ctx context.Context, req RememberReportRequest) (RememberReportRequestResult, error)
	HandleReportEvent(ctx context.Context, event ReportEvent) (ReportEventResult, error)
	CreateDAG(ctx context.Context, req CreateDAGRequest) (DAGDetail, error)
	GetDAG(ctx context.Context, dagKey string) (DAGDetail, error)
	ListDAGs(ctx context.Context, filter ListDAGsFilter) ([]DAGSummary, error)
	UpdateNodeStatus(ctx context.Context, req UpdateNodeStatusRequest) (DAGNode, error)
	// StartDAG 触发 DAG 一次新执行（骨架阶段 stub，返回 ErrLifecycleNotImplemented）。
	StartDAG(ctx context.Context, req StartDAGRequest) (StartDAGResponse, error)
	// GetRun 按 run_key 查询单条 run（task_get_run MCP 工具承载点）。
	// 节点信息不内联，调用方需另查 task_get_dag。
	//
	// GetRun fetches a single run by run_key (backs the task_get_run MCP tool).
	// Node-level data is intentionally not inlined; callers go through
	// task_get_dag for that.
	GetRun(ctx context.Context, req GetRunRequest) (GetRunResponse, error)
	// ApplyOps 对 DAG 执行一组 typed ops (add/update/remove/update_dag) + base_version OCC。
	// Ops 字段是 raw JSON（wire 格式），service 内部解码为 nodeexec.Ops。
	ApplyOps(ctx context.Context, req ApplyOpsRequest) (ApplyOpsResponse, error)
	// ListRuns 列出指定 DAG 的最近 run（dag_key 必填，可选 status / limit）。
	// ListRuns lists recent runs for a DAG (dag_key required, optional status / limit).
	ListRuns(ctx context.Context, req ListRunsRequest) (ListRunsResponse, error)
}

// ListRunsRequest 是 OrchestrationService.ListRuns 的入参（T3.2）。
// dag_key 必填；status / limit 可选。limit=0 时由 service 走默认上限。
//
// ListRunsRequest is the input for OrchestrationService.ListRuns (T3.2).
// DagKey is required; Status / Limit are optional. Limit=0 uses the
// service-side default cap.
type ListRunsRequest struct {
	DagKey string
	Status string
	Limit  int32
}

// ListRunsResponse 用对象包裹 runs slice，给后续扩展（next_cursor / total
// 等分页/聚合字段）留位，避免一开始就把 wire 形状钉成裸数组。
//
// ListRunsResponse wraps the runs slice in an object so the wire shape can
// grow extensions (next_cursor / total etc.) without a breaking change.
type ListRunsResponse struct {
	Runs []Run `json:"runs"`
}

// OrchestrationSessionCleaner is the owner-side contract for releasing
// any platform-owned session bound to a given agent when the agent
// stops. The orchestration service calls this at stop/exit time; the
// production adapter lives in internal/provider/unified.
//
// P22 P4 S4b: RemoveSessionGeneration was previously a side-channel
// method exposed via a local `generationAwareSessionCleaner` interface
// inside cmd/mcp-orch/orchestration/process_lifecycle.go; the service
// type-asserted sessionCleaner to that private interface. P4 §279
// upgrades such local private extensions into the owner contract
// directly: every OrchestrationSessionCleaner implementation now
// commits to the generation-aware variant, and implementations that do
// not track per-agent generations (e.g. noopSessionCleaner in
// cmd/mcp-orch standalone mode) simply fall back to calling their own
// RemoveSession or return.
type OrchestrationSessionCleaner interface {
	// RemoveSession drops any bound session for the agent. Callers use
	// this when the agent's current generation is unknown.
	RemoveSession(agentID string)

	// RemoveSessionGeneration drops the session associated with a
	// specific generation counter, so concurrent stop + re-launch races
	// cannot accidentally evict a fresh session. Implementations that
	// have no concept of a generation may treat this as a no-op or
	// delegate to RemoveSession.
	RemoveSessionGeneration(agentID string, generation uint64)
}

type TurnSubmission = turndto.TurnSubmission

// OrchestrationTurnStarter is the owner-side contract the orchestration
// service uses to route newly queued turns into the turn module.
//
// P22 P4 S4a: WaitForSessionReady was previously a side-channel method
// exposed via a local `sessionReadyWaiter` interface inside
// cmd/mcp-orch/orchestration/helpers.go; the service type-asserted
// turnStarter to that private interface. P4 §279 upgrades such local
// private extensions into the owner contract directly: every
// OrchestrationTurnStarter implementation now commits to the ready-wait
// semantics, and implementations that have no real wait to perform
// (e.g. noopTurnStarter in mcp-orch standalone mode) simply return nil.
type OrchestrationTurnStarter interface {
	StartTurn(ctx context.Context, submission TurnSubmission) (string, error)

	// WaitForSessionReady blocks until the agent's underlying session is
	// ready to accept a submission, or ctx is canceled / timeout elapses.
	// Return nil when the wait is unnecessary (e.g. standalone / noop
	// implementations that do not manage a session lifecycle).
	WaitForSessionReady(ctx context.Context, agentID string, timeout time.Duration) error
}

type LaunchRequest struct {
	AgentID      string
	Name         string
	Prompt       string
	Instructions string
	ParentID     string
	AgentType    string
	AgentKey     string
	MemoryScope  string
	Cwd          string
	Language     string
	Command      []string
	Env          []string
}

type AgentSnapshot struct {
	ID             string    `json:"id"`
	AgentID        string    `json:"agent_id"`
	Name           string    `json:"name"`
	ParentID       string    `json:"parent_id,omitempty"`
	Port           int       `json:"port"`
	PortSource     string    `json:"port_source,omitempty"`
	PID            int       `json:"pid,omitempty"`
	ThreadID       string    `json:"thread_id"`
	ActiveTurnID   string    `json:"active_turn_id,omitempty"`
	Cwd            string    `json:"cwd"`
	State          string    `json:"state"`
	Provider       string    `json:"provider,omitempty"`
	ProviderSource string    `json:"provider_source,omitempty"`
	LastReport     string    `json:"last_report,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
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

// DAG v2 骨架阶段 T1.1: StartDAG 生命周期入参出参。
// 骨架阶段 service 实现返回 ErrLifecycleNotImplemented；T1.2 接通真实路径
// （创建 task_dag_runs 行 + snapshot dag.version）。契约见
// docs/adr/0001-dag-v2-contracts.md §2.1 + 骨架阶段补丁 2。
type StartDAGRequest struct {
	DagKey         string
	TriggerSource  string // manual | auto | scheduled | external
	IdempotencyKey string // 可选，防重复 run
}

type StartDAGResponse struct {
	RunKey  string // 新 run 的唯一键（例 dag_xxx#run_2026-05-10T08:00）
	Version int64  // 该 run snapshot 的 dag.version
}

// DAG v2 骨架阶段 T2.1+T2.2: ApplyOps 入参出参。
// Ops 是 raw JSON（typed 解码由 service 内部 nodeexec.Ops UnmarshalJSON 处理）
// 以免 contract 包依赖 mcp-orch 内部的 nodeexec 子包。
// 骨架阶段 service 实现返回 ErrLifecycleNotImplemented；F4.1-F4.5 真实补齐
// add/update/remove + 环检测 + base_version OCC。
type ApplyOpsRequest struct {
	DagKey      string
	BaseVersion int64
	Ops         json.RawMessage // typed shape 见 nodeexec.Ops
}

type ApplyOpsResponse struct {
	NewVersion int64
}

// GetRunRequest 是 task_get_run / OrchestrationService.GetRun 的入参。
// run_key 必填，服务端 trim 后空串拒绝。
//
// GetRunRequest is the input for task_get_run / OrchestrationService.GetRun.
// run_key is required; the service trims and rejects empty values.
type GetRunRequest struct {
	RunKey string
}

// GetRunResponse 是 task_get_run / OrchestrationService.GetRun 的出参。
// 决策：不 inline 节点信息（调用方走 task_get_dag 拿 DAG 模板 + 节点），
// 保证单一职责、避免与 DAG 表联查 N+1。
//
// GetRunResponse is the output for task_get_run / OrchestrationService.GetRun.
// Decision: nodes are intentionally NOT inlined; callers fetch DAG template +
// nodes via task_get_dag, keeping responsibilities single and avoiding an
// implicit join with the dag-node table.
type GetRunResponse struct {
	Run Run `json:"run"`
}

// Run 是 task_dag_runs 的外露 DTO，镜像 cmd/mcp-orch/store/taskdag.Run 字段。
// contract 包不依赖 mcp-orch 内部 store 包，故这里独立声明同形状。
// service 层 dagRunDTO helper 负责转换。
//
// Run is the wire-side DTO for task_dag_runs, mirroring the field set of
// cmd/mcp-orch/store/taskdag.Run. The contract package does not depend on the
// internal mcp-orch store package, so the same shape is declared here. Service
// layer's dagRunDTO helper is responsible for the conversion.
type Run struct {
	ID                 int64           `json:"id"`
	RunKey             string          `json:"run_key"`
	DagKey             string          `json:"dag_key"`
	DagVersionSnapshot int64           `json:"dag_version_snapshot"`
	TriggerSource      string          `json:"trigger_source,omitempty"`
	Status             string          `json:"status"`
	StartedAt          time.Time       `json:"started_at"`
	FinishedAt         *time.Time      `json:"finished_at,omitempty"`
	Events             json.RawMessage `json:"events,omitempty"`
	BudgetUsed         int64           `json:"budget_used"`
	BudgetLimit        *int64          `json:"budget_limit,omitempty"`
	Metadata           json.RawMessage `json:"metadata,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
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
