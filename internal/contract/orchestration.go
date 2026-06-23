package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

var ErrAgentNotFound = errors.New("agent not found")

var (
	ErrLaunchCWDRequired = errors.New("launch cwd is required")
	ErrLaunchCWDInvalid  = errors.New("launch cwd is invalid")
)

// ValidateLaunchCWD 校验启动工作目录。
func ValidateLaunchCWD(cwd, parentID string) error {
	trimmedCWD := strings.TrimSpace(cwd)
	parentID = strings.TrimSpace(parentID)
	if cwd != "" && trimmedCWD == "" {
		return fmt.Errorf("%w: launch_agent cwd must not be blank or whitespace-only", ErrLaunchCWDInvalid)
	}
	if trimmedCWD == "" {
		if parentID != "" {
			return fmt.Errorf("%w: launch_agent cwd is required; parent_id %q was not found or has no cwd", ErrLaunchCWDRequired, parentID)
		}
		return fmt.Errorf("%w: launch_agent cwd is required; pass cwd or parent_id whose agent has cwd", ErrLaunchCWDRequired)
	}
	if trimmedCWD != cwd {
		return fmt.Errorf("%w: launch_agent cwd must not contain surrounding whitespace", ErrLaunchCWDInvalid)
	}
	if trimmedCWD == "." || !filepath.IsAbs(trimmedCWD) {
		return fmt.Errorf("%w: launch_agent cwd must be an absolute path", ErrLaunchCWDInvalid)
	}
	return nil
}

// DAGRuntime is the narrow DAG read/start boundary used by the desktop app.
// The app process does not embed mcp-orch; production adapters may satisfy this
// by proxying to an active mcp-orch peer.
type DAGRuntime interface {
	GetDAG(ctx context.Context, dagKey string) (DAGDetail, error)
	ListDAGs(ctx context.Context, filter ListDAGsFilter) ([]DAGSummary, error)
	StartDAG(ctx context.Context, req StartDAGRequest) (StartDAGResponse, error)
	TerminateDAG(ctx context.Context, req TerminateDAGRequest) error
	ListRuns(ctx context.Context, req ListRunsRequest) (ListRunsResponse, error)
	GetRun(ctx context.Context, req GetRunRequest) (GetRunResponse, error)
	// ApplyOps 对 DAG 执行一组 typed ops (add/update/remove/update_dag) + base_version OCC。
	// Ops 字段是 raw JSON（wire 格式），service 内部解码为 nodeexec.Ops。
	ApplyOps(ctx context.Context, req ApplyOpsRequest) (ApplyOpsResponse, error)
}

type DAGDeleteRuntime interface {
	DeleteDAG(ctx context.Context, req DeleteDAGRequest) error
}

// DAGCreateRuntime 是桌面端通过 mcp-orch 创建 DAG 模板的最小写入边界。
// 它只负责落库模板；是否立即启动由调用方在创建成功后显式调用 StartDAG。
type DAGCreateRuntime interface {
	CreateDAG(ctx context.Context, req CreateDAGRequest) (DAGDetail, error)
}

// OrchestrationService defines the shared orchestration boundary used by
// internal modules and the MCP orchestration runtime.
type OrchestrationService interface {
	DAGRuntime
	DAGDeleteRuntime
	LaunchAgent(ctx context.Context, req LaunchRequest) error
	ListAgents(ctx context.Context) ([]AgentSnapshot, error)
	StopAgent(ctx context.Context, agentID string) error
	InterruptAgent(ctx context.Context, agentID string, source string) (AgentStateResult, error)
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
	UpdateNodeStatus(ctx context.Context, req UpdateNodeStatusRequest) (DAGNode, error)
	// GetRun 按 run_key 查询单条 run（task_get_run MCP 工具承载点）。
	// F6.5 后返回该 run 的 runtime nodes；task_get_dag 只读 DAG 模板节点。
	// GetRun fetches a single run by run_key (backs the task_get_run MCP tool).
	// After F6.5 it returns the run's runtime nodes; task_get_dag reads only
	// DAG template nodes.
	GetRun(ctx context.Context, req GetRunRequest) (GetRunResponse, error)
	// DispatchNode 是 ADR-004 「无 assignee 就绪节点」的显式推进入口：
	// 给粘在 ready / pending 无 assignee 状态的节点指派一个 assigned_to
	// 后立即 enqueue 一条 wakeup，让 dispatcher 能指取跳 launch。
	// 当前实现接通 F6.4 runtime-node dispatch 路径。
	//
	// DispatchNode is the explicit-resume entrypoint for ready/pending nodes
	// that lack an assigned_to (ADR-004 §Open Q1). It records the assignee
	// and enqueues a wakeup so the dispatcher can pick the node up.
	DispatchNode(ctx context.Context, req DispatchNodeRequest) (DispatchNodeResponse, error)
}

// DispatchNodeRequest is the input for OrchestrationService.DispatchNode. F6.5
// requires RunID so manual dispatch targets a runtime node, not the template.
type DispatchNodeRequest struct {
	DagKey, NodeKey string
	RunID           int64
	AssignedTo      string
}

// DispatchNodeResponse 报告本次 dispatch 的后果：是否新 enqueue 了
// wakeup（WakeupID > 0 + Enqueued=true）还是 ON CONFLICT (idempotency_key)
// 去重（Enqueued=false）。Node 字段是赋值后的最新 DAGNode。
//
// DispatchNodeResponse reports the outcome: Enqueued=true means a fresh
// wakeup row was inserted; Enqueued=false means ON CONFLICT skipped a
// duplicate (idempotent re-dispatch). Node is the node DTO post-assign.
type DispatchNodeResponse struct {
	Node     DAGNode `json:"node"`
	WakeupID int64   `json:"wakeup_id,omitempty"`
	Enqueued bool    `json:"enqueued"`
}

// TerminateDAGRequest asks the runtime to cancel a running DAG execution.
// RunKey is required so callers cancel one execution instance, not the DAG
// template. DagKey is kept as a fence against cross-DAG run_key mistakes.
type TerminateDAGRequest struct {
	DagKey string `json:"dag_key"`
	RunKey string `json:"run_key"`
	Reason string `json:"reason,omitempty"`
}

// WorkflowRecoveryAction 描述工作台可以展示或触发的受控恢复动作。
// Enabled=false 时仅用于说明能力缺口或策略阻断，前端不能直接执行。
type WorkflowRecoveryAction struct {
	Action  string `json:"action"`
	Label   string `json:"label,omitempty"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
	Policy  string `json:"policy,omitempty"`
}

// WorkflowArtifactLink 是运行或节点摘要里暴露的轻量产物引用。
// 这里只放可展示/跳转字段，不承诺文件内容已物化。
type WorkflowArtifactLink struct {
	Kind    string `json:"kind,omitempty"`
	Label   string `json:"label,omitempty"`
	Path    string `json:"path,omitempty"`
	URL     string `json:"url,omitempty"`
	NodeKey string `json:"node_key,omitempty"`
}

type DeleteDAGRequest struct {
	DagKey string `json:"dag_key"`
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

// ListRunsResponse 用对象包裹 runs slice，给分页元数据（next_cursor / total
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
	AgentID        string
	Name           string
	Prompt         string
	Instructions   string
	ParentID       string
	ParentThreadID string
	ContextMode    string
	AgentType      string
	AgentKey       string
	PromptKey      string
	MemoryScope    string
	Cwd            string
	Language       string
	Command        []string
	Env            []string
}

type AgentSnapshot struct {
	ID             string    `json:"id"`
	AgentID        string    `json:"agent_id"`
	LaunchID       string    `json:"launch_id,omitempty"`
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
	CreatedAt      time.Time `json:"created_at,omitzero"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// NormalizeUnixTime 规范化Unix时间。
func NormalizeUnixTime(values ...int64) time.Time {
	for _, value := range values {
		if value <= 0 {
			continue
		}
		scale := int64(1)
		for value/scale > 253402300799 && scale < int64(time.Second) {
			scale *= 1000
		}
		return time.Unix(value/scale, (value%scale)*(int64(time.Second)/scale))
	}
	return time.Time{}
}

type AgentStateResult struct {
	AgentID string `json:"agent_id"`
	State   string `json:"state"`
}

type AgentReportMetadata struct {
	RequesterIDs []string `json:"requester_ids,omitempty"`
}

type AgentReportResult struct {
	AgentID   string               `json:"agent_id"`
	Report    string               `json:"report"`
	ReportSeq int64                `json:"report_seq"`
	UpdatedAt time.Time            `json:"updated_at,omitzero"`
	State     string               `json:"state"`
	Metadata  *AgentReportMetadata `json:"metadata,omitempty"`
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
	Success              bool      `json:"success"`
	AgentID              string    `json:"agent_id"`
	EventType            string    `json:"event_type,omitempty"`
	Report               string    `json:"report,omitempty"`
	ReportSeq            int64     `json:"report_seq,omitempty"`
	UpdatedAt            time.Time `json:"updated_at,omitzero"`
	NotifiedRequesterIDs []string  `json:"notified_requester_ids,omitempty"`
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
	DagKey, NodeKey string
	RunID           int64
	Status          string
	Result          json.RawMessage
}

// DAG v2 StartDAG 生命周期入参出参。实现创建 task_dag_runs 行并
// snapshot dag.version；契约见 docs/adr/0001-dag-v2-contracts.md §2.1。
type StartDAGRequest struct {
	DagKey         string
	TriggerSource  string // manual | auto | scheduled | external
	IdempotencyKey string // 可选，防重复 run
}

const (
	StartDAGExecutionQueued             = "queued"
	StartDAGExecutionWaitingForAssignee = "waiting_for_assignee"
	StartDAGExecutionRunning            = "running"
	StartDAGExecutionNoReadyRoots       = "no_ready_roots"
	StartDAGExecutionSucceeded          = "succeeded"
)

type StartDAGResponse struct {
	RunID            int64  `json:"run_id,omitempty"`  // task_dag_runs.id；dispatch runtime node 时需要
	RunKey           string `json:"run_key"`           // 新 run 的唯一键（例 dag_alpha#run_2026-05-10T08:00）
	Version          int64  `json:"version"`           // 该 run snapshot 的 dag.version
	ReadyRootNodes   int64  `json:"ready_root_nodes"`  // 本次 start 置为 ready 的根节点数
	ScheduledWakeups int64  `json:"scheduled_wakeups"` // 已 enqueue 的根节点 wakeup 数
	ExecutionState   string `json:"execution_state,omitempty"`
	Warning          string `json:"warning,omitempty"`
}

// NewStartDAGResponse 创建起点DAG响应。
func NewStartDAGResponse(runID int64, runKey string, version, readyRootNodes, scheduledWakeups int64) StartDAGResponse {
	state, warning := StartDAGExecutionDiagnostics(readyRootNodes, scheduledWakeups)
	return StartDAGResponse{
		RunID:            runID,
		RunKey:           runKey,
		Version:          version,
		ReadyRootNodes:   readyRootNodes,
		ScheduledWakeups: scheduledWakeups,
		ExecutionState:   state,
		Warning:          warning,
	}
}

// NewExistingStartDAGResponse 创建existing起点DAG响应。
func NewExistingStartDAGResponse(runID int64, runKey string, version int64, status string, scheduledWakeups int64) StartDAGResponse {
	state := StartDAGExecutionRunning
	if scheduledWakeups > 0 {
		state = StartDAGExecutionQueued
	}
	if status == "succeeded" {
		state = StartDAGExecutionSucceeded
	}
	return StartDAGResponse{RunID: runID, RunKey: runKey, Version: version, ScheduledWakeups: scheduledWakeups, ExecutionState: state}
}

// StartDAGExecutionDiagnostics 启动DAGexecution诊断。
func StartDAGExecutionDiagnostics(readyRootNodes, scheduledWakeups int64) (string, string) {
	if scheduledWakeups > 0 {
		return StartDAGExecutionQueued, ""
	}
	if readyRootNodes > 0 {
		return StartDAGExecutionWaitingForAssignee,
			"run 已启动，但首个步骤没有配置执行者，无法自动派发；请先为根步骤设置 assigned_to 后重新运行，或调用 task_dispatch_node 为 ready 节点指定 assigned_to。"
	}
	return StartDAGExecutionNoReadyRoots, "run 已启动，但没有可调度的根节点；请检查 DAG 节点依赖。"
}

// DAG v2 ApplyOps 入参出参。
// Ops 是 raw JSON（typed 解码由 service 内部 nodeexec.Ops UnmarshalJSON 处理）
// 以免 contract 包依赖 mcp-orch 内部的 nodeexec 子包。
// service 实现 add/update/remove + 环检测 + base_version OCC。
type ApplyOpsRequest struct {
	DagKey      string
	BaseVersion int64
	Ops         json.RawMessage // typed shape 见 nodeexec.Ops
}

type ApplyOpsResponse struct {
	NewVersion int64 `json:"new_version"`
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
// F6.5 后 Nodes 是当前 run 的 runtime node 快照；DAG 模板仍由 task_get_dag 读取。
//
// GetRunResponse is the output for task_get_run / OrchestrationService.GetRun.
// After F6.5 Nodes carries the runtime-node snapshot for this run; the DAG
// template remains available through task_get_dag.
type GetRunResponse struct {
	Run   Run       `json:"run"`
	Nodes []DAGNode `json:"nodes,omitempty"`
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
	ID                 int64                    `json:"id"`
	RunKey             string                   `json:"run_key"`
	DagKey             string                   `json:"dag_key"`
	DagVersionSnapshot int64                    `json:"dag_version_snapshot"`
	TriggerSource      string                   `json:"trigger_source,omitempty"`
	Status             string                   `json:"status"`
	StartedAt          time.Time                `json:"started_at"`
	FinishedAt         *time.Time               `json:"finished_at,omitempty"`
	Events             json.RawMessage          `json:"events,omitempty"`
	BudgetUsed         int64                    `json:"budget_used"`
	BudgetLimit        *int64                   `json:"budget_limit,omitempty"`
	Metadata           json.RawMessage          `json:"metadata,omitempty"`
	CreatedAt          time.Time                `json:"created_at"`
	UpdatedAt          time.Time                `json:"updated_at"`
	DerivedState       string                   `json:"derived_state,omitempty"`
	BlockedReason      string                   `json:"blocked_reason,omitempty"`
	NextAction         string                   `json:"next_action,omitempty"`
	ArtifactCount      int                      `json:"artifact_count,omitempty"`
	RecoveryActions    []WorkflowRecoveryAction `json:"recovery_actions,omitempty"`
}

type FinalOutputFileRef struct {
	Path          string
	SourceNodeKey string
}

type finalOutputMetadataEnvelope struct {
	FinalOutput json.RawMessage `json:"final_output"`
}

type finalOutputFilePayload struct {
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	SourceNodeKey string `json:"source_node_key"`
	SharedFile    *struct {
		Path string `json:"path"`
	} `json:"sharedfile"`
}

// FinalOutputFileFromRunMetadata 从运行记录元数据处理finaloutput文件。
func FinalOutputFileFromRunMetadata(metadataJSON json.RawMessage) (FinalOutputFileRef, bool) {
	if isEmptyJSON(metadataJSON) {
		return FinalOutputFileRef{}, false
	}
	var metadata finalOutputMetadataEnvelope
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil || isEmptyJSON(metadata.FinalOutput) {
		return FinalOutputFileRef{}, false
	}
	var output finalOutputFilePayload
	if err := json.Unmarshal(metadata.FinalOutput, &output); err != nil {
		return FinalOutputFileRef{}, false
	}
	if kind := strings.TrimSpace(output.Kind); kind != "" && kind != "file" {
		return FinalOutputFileRef{}, false
	}
	path := strings.TrimSpace(output.Path)
	if path == "" && output.SharedFile != nil {
		path = strings.TrimSpace(output.SharedFile.Path)
	}
	if path == "" {
		return FinalOutputFileRef{}, false
	}
	return FinalOutputFileRef{
		Path:          path,
		SourceNodeKey: strings.TrimSpace(output.SourceNodeKey),
	}, true
}

func isEmptyJSON(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}

type DAGSummary struct {
	ID              int64           `json:"id"`
	DagKey          string          `json:"dag_key"`
	Version         int64           `json:"version"`
	Title           string          `json:"title"`
	Description     string          `json:"description,omitempty"`
	Status          string          `json:"status"`
	CreatedBy       string          `json:"created_by,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	Trigger         string          `json:"trigger,omitempty"`
	CronExpr        string          `json:"cron_expr,omitempty"`
	NextRunAt       *time.Time      `json:"next_run_at,omitempty"`
	ScheduleEnabled bool            `json:"schedule_enabled"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	FinishedAt      *time.Time      `json:"finished_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
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
	// SpawningThreadID — DAG v2 F1.5 / ADR-009：AgentExecutor spawn 出的最近一
	// 次 child agent thread id（软关联）；UI 用它拼「节点行 → 子 agent thread」
	// 跳转链接，不再解析 result jsonb。NULL 表示未 spawn / 本节点非 agent。
	SpawningThreadID *string                `json:"spawning_thread_id,omitempty"`
	Executor         string                 `json:"executor,omitempty"`
	FailureClass     string                 `json:"failure_class,omitempty"`
	LastWakeupAt     *time.Time             `json:"last_wakeup_at,omitempty"`
	ArtifactLinks    []WorkflowArtifactLink `json:"artifact_links,omitempty"`
	NextAction       string                 `json:"next_action,omitempty"`
}

type DAGDetail struct {
	DAG   DAGSummary `json:"dag"`
	Nodes []DAGNode  `json:"nodes,omitempty"`
}

// =====================================================
// DAG events helpers (R2 P2 #4)
//
// task_get_run 返 events 是 json.RawMessage，UI 不该重复手拼解。这里提供
// 一个公共 DAGEvent typed struct + Parse / Filter helper，让多端客户端走
// 同一 decoder 路径。与 DAGSummary / DAGNode 同在 orchestration.go，限制
// internal/contract 包文件数 ≤ 30（代码守卫上限）。
// =====================================================

// DAGEvent 是 task_dag_runs.events jsonb 数组里单个事件的公共结构。
//
// 起源：F1.5 把 `{kind:"node_spawn", node_key, prev_thread_id, thread_id, ts}`
// append 到 events，目的是给 UI 拉子 agent thread 历史链。task_get_run
// 当前透传整个 events 为 json.RawMessage，没有官方 helper 让 UI 解；UI 各端
// 各自手 parse 容易写歪，故此提供 ParseDAGEvents 统一入口。
//
// 字段说明：
//   - Kind: discriminator。当前唯一已知值 "node_spawn"；新事件类型在
//     migration / store 层加，本结构体保持 union 形态：未知 kind 也能解
//     出来（其他字段会留空），调用方按 Kind 自分发。
//   - NodeKey: 触发事件的节点 key。
//   - PrevThreadID / ThreadID: spawn 重试场景下的「旧 / 新」thread。新建
//     场景下 PrevThreadID 是空串。
//   - TS: 事件时间戳 RFC3339Nano 字符串；保持字符串方便 UI 渲染、不强迫
//     解 time.Time。需精确比较时调用方再 time.Parse。
type DAGEvent struct {
	Kind         string `json:"kind"`
	NodeKey      string `json:"node_key,omitempty"`
	PrevThreadID string `json:"prev_thread_id,omitempty"`
	ThreadID     string `json:"thread_id,omitempty"`
	TS           string `json:"ts,omitempty"`
}

// DAGEventKind 列举已知事件类型；常量化方便 UI 按 kind 分发。
//
// 新增事件类型时：
//  1. store 层往 events 数组 append 时用同名字符串
//  2. 本文件加常量
//  3. 必要时 DAGEvent 加可选字段（保持 json:",omitempty"）
type DAGEventKind = string

const (
	// DAGEventKindNodeSpawn 是 F1.5 唯一已落的事件类型：spawn 子 agent 时
	// 把上次 thread id 与本次 thread id 记进 events 历史链。
	DAGEventKindNodeSpawn DAGEventKind = "node_spawn"
)

// ParseDAGEvents 把 task_get_run 返回里的 events json.RawMessage 解成一组
// DAGEvent。
//
// 行为：
//   - raw 是 nil / 长度 0 / "null" → 返回 nil, nil（无事件，非错误）
//   - 非 JSON 数组 → 返 nil + error
//   - 数组元素非 object → 返 nil + 包了 ordinal 的 error
//   - 未知 kind 不报错；DAGEvent.Kind 保留原值，调用方按需 ignore
func ParseDAGEvents(raw json.RawMessage) ([]DAGEvent, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		return nil, fmt.Errorf("dag events: parse outer array: %w", err)
	}
	out := make([]DAGEvent, 0, len(elements))
	for i, el := range elements {
		var ev DAGEvent
		if err := json.Unmarshal(el, &ev); err != nil {
			return nil, fmt.Errorf("dag events: element [%d]: %w", i, err)
		}
		out = append(out, ev)
	}
	return out, nil
}

// FilterEventsByKind 过出指定 kind 的事件，保持原顺序。kind 空串 → 直接
// 返回原切片（noop），便于 UI 一行链式调用。
func FilterEventsByKind(events []DAGEvent, kind DAGEventKind) []DAGEvent {
	if kind == "" {
		return events
	}
	out := make([]DAGEvent, 0, len(events))
	for _, ev := range events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}
