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

// ErrAgentNotFound 表示 orchestration 查询目标 agent 不存在。
var ErrAgentNotFound = errors.New("agent not found")

var (
	// 启动 cwd 校验错误哨兵，供 launch 工具区分缺参和非法路径。
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

// DAGRuntime 是桌面端读写和启动 DAG 的窄边界。
// app 进程不内嵌 mcp-orch，生产适配器通常代理到当前活跃的 mcp-orch peer。
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

// DAGDeleteRuntime 是 DAG 模板删除能力的独立边界。
// 删除动作影响持久化模板，调用方必须显式依赖此接口而不是默认包含在只读 runtime 中。
type DAGDeleteRuntime interface {
	DeleteDAG(ctx context.Context, req DeleteDAGRequest) error
}

// DAGCreateRuntime 是桌面端通过 mcp-orch 创建 DAG 模板的最小写入边界。
// 它只负责落库模板；是否立即启动由调用方在创建成功后显式调用 StartDAG。
type DAGCreateRuntime interface {
	CreateDAG(ctx context.Context, req CreateDAGRequest) (DAGDetail, error)
}

// OrchestrationService 是内部模块和 MCP orchestration runtime 共用的总边界。
// 这里聚合 agent 生命周期、DAG runtime、报告和恢复入口，调用方不应在本接口外私接 mcp-orch 内部实现。
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
	// 返回值包含该 run 的 runtime node 快照；DAG 模板节点仍由 task_get_dag 读取。
	GetRun(ctx context.Context, req GetRunRequest) (GetRunResponse, error)
	// DispatchNode 显式恢复 ready/pending 且缺少 assignee 的 runtime node。
	// 它写入 assigned_to 后立即 enqueue wakeup，让 dispatcher 能继续派发该节点。
	DispatchNode(ctx context.Context, req DispatchNodeRequest) (DispatchNodeResponse, error)
}

// DispatchNodeRequest 是 OrchestrationService.DispatchNode 的入参。
// RunID 必填，确保手动派发落到某次运行的 runtime node，而不是误改 DAG 模板节点。
type DispatchNodeRequest struct {
	DagKey, NodeKey string
	RunID           int64
	AssignedTo      string
}

// DispatchNodeResponse 报告本次 dispatch 的后果：是否新 enqueue 了
// wakeup（WakeupID > 0 + Enqueued=true）还是 ON CONFLICT (idempotency_key)
// 去重（Enqueued=false）。Node 字段是赋值后的最新 DAGNode。
type DispatchNodeResponse struct {
	Node     DAGNode `json:"node"`
	WakeupID int64   `json:"wakeup_id,omitempty"`
	Enqueued bool    `json:"enqueued"`
}

// TerminateDAGRequest 要求 runtime 取消某次正在运行的 DAG。
// RunKey 定位一次执行实例，DagKey 作为防串 DAG 的额外校验围栏。
type TerminateDAGRequest struct {
	DagKey string `json:"dag_key"`
	RunKey string `json:"run_key"`
	Reason string `json:"reason,omitempty"`
}

// WorkflowRecoveryAction 是 workflow workbench 暴露的受控恢复动作。
// Enabled=false 时仅用于说明能力缺口或策略阻断，前端不能直接执行。
type WorkflowRecoveryAction struct {
	Action  string `json:"action"`
	Label   string `json:"label,omitempty"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
	Policy  string `json:"policy,omitempty"`
}

// WorkflowArtifactLink 是 run/node 摘要中可展示的轻量产物引用。
// 这里只放可展示/跳转字段，不承诺文件内容已物化。
type WorkflowArtifactLink struct {
	Kind    string `json:"kind,omitempty"`
	Label   string `json:"label,omitempty"`
	Path    string `json:"path,omitempty"`
	URL     string `json:"url,omitempty"`
	NodeKey string `json:"node_key,omitempty"`
}

// DeleteDAGRequest 用 dag_key 定位要删除的 DAG 模板。
type DeleteDAGRequest struct {
	DagKey string `json:"dag_key"`
}

// ListRunsRequest 是 DAG run 列表查询的 wire 入参。
// dag_key 必填；status 和 limit 可选，limit=0 时由 service 使用受控默认上限。
type ListRunsRequest struct {
	DagKey string
	Status string
	Limit  int32
}

// ListRunsResponse 用对象包裹 runs slice，给分页元数据（next_cursor / total
// 等分页/聚合字段）留位，避免一开始就把 wire 形状钉成裸数组。
type ListRunsResponse struct {
	Runs []Run `json:"runs"`
}

// OrchestrationSessionCleaner 是 agent 停止时释放平台侧 session 的 owner-side 契约。
// orchestration service 在 stop/exit 路径调用它，生产适配器位于 internal/provider/unified。
//
// RemoveSessionGeneration 用 generation 护栏处理 stop 与 relaunch 并发；不跟踪 generation
// 的实现必须明确退化为 RemoveSession 或 no-op，不能让调用方猜测清理语义。
type OrchestrationSessionCleaner interface {
	// RemoveSession 删除 agent 绑定的任意 session；调用方在未知当前 generation 时使用。
	RemoveSession(agentID string)

	// RemoveSessionGeneration 删除指定 generation 关联的 session，避免 stop + relaunch 竞态误删新会话。
	RemoveSessionGeneration(agentID string, generation uint64)
}

// TurnSubmission 是 turn DTO 中提交请求的 contract 别名。
type TurnSubmission = turndto.TurnSubmission

// OrchestrationTurnStarter 是 orchestration service 把新排队 turn 送入 turn 模块的 owner-side 契约。
//
// WaitForSessionReady 是提交前的 readiness 护栏，所有实现都必须明确表达“等待会话可提交”的语义；
// standalone/noop 实现没有真实 session 生命周期时可以直接返回 nil。
type OrchestrationTurnStarter interface {
	StartTurn(ctx context.Context, submission TurnSubmission) (string, error)

	// WaitForSessionReady 阻塞直到 agent 底层 session 可接受提交，或 ctx 取消/超时。
	WaitForSessionReady(ctx context.Context, agentID string, timeout time.Duration) error
}

// LaunchRequest 是 orchestration 启动 agent 的 wire 入参。
// 线程、prompt、工作区和进程环境都通过该结构跨模块传递，service 负责校验 cwd 和父子关系。
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

// AgentSnapshot 是 orchestration 对外展示的 agent runtime 快照。
// PortSource、ProviderSource 等来源字段用于解释自动探测结果，不能作为强配置回写。
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

// NormalizeUnixTime 把秒、毫秒、微秒或纳秒级 Unix 时间规范化为 time.Time。
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

// AgentStateResult 是 agent 状态查询的轻量 wire 返回。
type AgentStateResult struct {
	AgentID string `json:"agent_id"`
	State   string `json:"state"`
}

// AgentReportMetadata 保存 report 请求方等附加元信息。
// requester 列表用于 report 到达时唤醒等待方，不影响 agent 状态本身。
type AgentReportMetadata struct {
	RequesterIDs []string `json:"requester_ids,omitempty"`
}

// AgentReportResult 是 agent 最新报告及其状态的 wire 查询结果。
type AgentReportResult struct {
	AgentID   string               `json:"agent_id"`
	Report    string               `json:"report"`
	ReportSeq int64                `json:"report_seq"`
	UpdatedAt time.Time            `json:"updated_at,omitzero"`
	State     string               `json:"state"`
	Metadata  *AgentReportMetadata `json:"metadata,omitempty"`
}

// RememberReportRequest 登记某个 requester 正在等待指定 agent 的 report。
type RememberReportRequest struct {
	AgentID     string
	RequesterID string
}

// RememberReportRequestResult 是 report 等待登记后的 wire 确认结果。
type RememberReportRequestResult struct {
	Success     bool   `json:"success"`
	AgentID     string `json:"agent_id"`
	RequesterID string `json:"requester_id"`
}

// ReportEvent 是 agent report 事件的跨模块载荷。
// EventData 保留原始 JSON，避免 contract 层绑定具体事件子类型。
type ReportEvent struct {
	AgentID   string
	Report    string
	EventType string
	EventData json.RawMessage
}

// ReportEventResult 是处理 report 事件后的广播和状态更新结果。
// NotifiedRequesterIDs 明确列出已唤醒等待方，避免调用方用 Success 猜测通知范围。
type ReportEventResult struct {
	Success              bool      `json:"success"`
	AgentID              string    `json:"agent_id"`
	EventType            string    `json:"event_type,omitempty"`
	Report               string    `json:"report,omitempty"`
	ReportSeq            int64     `json:"report_seq,omitempty"`
	UpdatedAt            time.Time `json:"updated_at,omitzero"`
	NotifiedRequesterIDs []string  `json:"notified_requester_ids,omitempty"`
}

// CreateDAGRequest 是新建 DAG 模板及初始节点的 wire 入参。
// Metadata 和 node Config 保持 RawMessage，避免 contract 包依赖 mcp-orch 内部配置类型。
type CreateDAGRequest struct {
	DagKey      string
	Title       string
	Description string
	CreatedBy   string
	Metadata    json.RawMessage
	Nodes       []CreateDAGNodeRequest
}

// CreateDAGNodeRequest 是创建 DAG 模板时的单节点 wire 入参。
// DependsOn 保持节点 key 列表，service 负责校验依赖存在性和环路。
type CreateDAGNodeRequest struct {
	NodeKey    string
	Title      string
	NodeType   string
	AssignedTo string
	DependsOn  []string
	Reads      []string
	Writes     []string
	CommandRef string
	Config     json.RawMessage
}

// ListDAGsFilter 是 DAG 列表查询的过滤条件。
// Limit 为 0 时由 service 应用默认上限，避免 contract 层硬编码分页策略。
type ListDAGsFilter struct {
	Status  string
	Keyword string
	Limit   int
}

// UpdateNodeStatusRequest 是 runtime node 状态更新的 wire 入参。
// RunID 确保更新限定在一次 DAG run 内，避免同名模板节点被误改。
type UpdateNodeStatusRequest struct {
	DagKey, NodeKey string
	RunID           int64
	Status          string
	Result          json.RawMessage
}

// StartDAGRequest 是启动一次 DAG run 的 wire 入参。
// service 会创建 task_dag_runs 行并快照当前 DAG version；IdempotencyKey 用于防重复启动。
type StartDAGRequest struct {
	DagKey         string
	TriggerSource  string // manual | auto | scheduled | external
	IdempotencyKey string // 可选，防重复 run
}

const (
	// StartDAGExecution* 常量描述 StartDAGResponse.ExecutionState 的稳定取值。
	StartDAGExecutionQueued             = "queued"
	StartDAGExecutionWaitingForAssignee = "waiting_for_assignee"
	StartDAGExecutionRunning            = "running"
	StartDAGExecutionNoReadyRoots       = "no_ready_roots"
	StartDAGExecutionSucceeded          = "succeeded"
)

// StartDAGResponse 是启动 DAG run 后返回给 task_start_dag 调用方的执行摘要。
type StartDAGResponse struct {
	RunID            int64  `json:"run_id,omitempty"`  // task_dag_runs.id；dispatch runtime node 时需要
	RunKey           string `json:"run_key"`           // 新 run 的唯一键（例 dag_alpha#run_2026-05-10T08:00）
	Version          int64  `json:"version"`           // 该 run snapshot 的 dag.version
	ReadyRootNodes   int64  `json:"ready_root_nodes"`  // 本次 start 置为 ready 的根节点数
	ScheduledWakeups int64  `json:"scheduled_wakeups"` // 已 enqueue 的根节点 wakeup 数
	ExecutionState   string `json:"execution_state,omitempty"`
	Warning          string `json:"warning,omitempty"`
}

// NewStartDAGResponse 创建新 DAG run 的启动响应，并根据 ready/wakeup 数量补充诊断状态。
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

// NewExistingStartDAGResponse 为幂等命中已有 run 的场景生成启动响应。
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

// StartDAGExecutionDiagnostics 根据根节点就绪和 wakeup 入队情况生成可展示的执行诊断。
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

// ApplyOpsRequest 是 DAG 模板增删改操作的 wire 入参。
// Ops 保持 raw JSON，由 service 内部解码 typed ops；contract 包不依赖 mcp-orch 内部 nodeexec 子包。
// BaseVersion 提供乐观并发控制，service 负责环检测和版本推进。
type ApplyOpsRequest struct {
	DagKey      string
	BaseVersion int64
	Ops         json.RawMessage // typed shape 见 nodeexec.Ops
}

// ApplyOpsResponse 返回 DAG ops 成功应用后的新版本号。
type ApplyOpsResponse struct {
	NewVersion int64 `json:"new_version"`
}

// GetRunRequest 是 task_get_run / OrchestrationService.GetRun 的入参。
// run_key 必填，服务端 trim 后空串拒绝。
type GetRunRequest struct {
	RunKey string
}

// GetRunResponse 是 task_get_run / OrchestrationService.GetRun 的出参。
// Nodes 返回该 run 的 runtime node 快照；模板节点由 task_get_dag 读取，避免混淆运行态和模板态。
type GetRunResponse struct {
	Run   Run       `json:"run"`
	Nodes []DAGNode `json:"nodes,omitempty"`
}

// Run 是 task_dag_runs 的对外 wire DTO。
// contract 包独立声明该形状，避免 UI/tool 调用方依赖 mcp-orch store 内部类型。
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

// FinalOutputFileRef 是从 run metadata 中解析出的最终文件产物引用。
type FinalOutputFileRef struct {
	Path          string
	SourceNodeKey string
}

var (
	// ErrFinalOutputMetadataInvalid 表示 run metadata JSON 无法解析。
	ErrFinalOutputMetadataInvalid = errors.New("final output metadata is invalid")
	// ErrFinalOutputInvalid 表示 metadata.final_output 无法按支持的文件输出形状解析。
	ErrFinalOutputInvalid = errors.New("final_output is invalid")
)

// finalOutputMetadataEnvelope 匹配 run metadata 中 final_output 外层对象。
type finalOutputMetadataEnvelope struct {
	FinalOutput json.RawMessage `json:"final_output"`
}

// finalOutputFilePayload 匹配 final_output 支持的 file/sharedfile 载荷形状。
type finalOutputFilePayload struct {
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	SourceNodeKey string `json:"source_node_key"`
	SharedFile    *struct {
		Path string `json:"path"`
	} `json:"sharedfile"`
}

// FinalOutputFileFromRunMetadataStrict 从运行记录 metadata 中提取最终文件产物。
// 缺失、非 file 类型或空路径返回 ok=false；JSON 结构损坏会返回显式错误，避免调用方误当成“无产物”。
func FinalOutputFileFromRunMetadataStrict(metadataJSON json.RawMessage) (FinalOutputFileRef, bool, error) {
	if isEmptyJSON(metadataJSON) {
		return FinalOutputFileRef{}, false, nil
	}
	var metadata finalOutputMetadataEnvelope
	if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
		return FinalOutputFileRef{}, false, fmt.Errorf("%w: parse run metadata: %w", ErrFinalOutputMetadataInvalid, err)
	}
	if isEmptyJSON(metadata.FinalOutput) {
		return FinalOutputFileRef{}, false, nil
	}
	var output finalOutputFilePayload
	if err := json.Unmarshal(metadata.FinalOutput, &output); err != nil {
		return FinalOutputFileRef{}, false, fmt.Errorf("%w: parse final_output: %w", ErrFinalOutputInvalid, err)
	}
	if kind := strings.TrimSpace(output.Kind); kind != "" && kind != "file" {
		return FinalOutputFileRef{}, false, nil
	}
	path := strings.TrimSpace(output.Path)
	if path == "" && output.SharedFile != nil {
		path = strings.TrimSpace(output.SharedFile.Path)
	}
	if path == "" {
		return FinalOutputFileRef{}, false, nil
	}
	return FinalOutputFileRef{
		Path:          path,
		SourceNodeKey: strings.TrimSpace(output.SourceNodeKey),
	}, true, nil
}

// FinalOutputFileFromRunMetadata 从运行记录 metadata 中提取最终文件产物。
// 这是兼容旧调用方的宽松 wrapper，解析错误会折叠成 ok=false；需要区分坏数据时应调用 Strict 版本。
func FinalOutputFileFromRunMetadata(metadataJSON json.RawMessage) (FinalOutputFileRef, bool) {
	ref, ok, err := FinalOutputFileFromRunMetadataStrict(metadataJSON)
	if err != nil {
		return FinalOutputFileRef{}, false
	}
	return ref, ok
}

// isEmptyJSON 判断 RawMessage 是否为空值或 JSON null。
func isEmptyJSON(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}

// DAGSummary 是 DAG 模板列表和详情中的模板摘要。
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

// DAGNode 是 DAG 模板节点或 runtime node 的对外 DTO。
// 运行期字段用 omitempty 指针区分“没有运行态”和“运行态字段为零值”。
type DAGNode struct {
	ID             int64           `json:"id"`
	DagKey         string          `json:"dag_key"`
	NodeKey        string          `json:"node_key"`
	Title          string          `json:"title"`
	NodeType       string          `json:"node_type,omitempty"`
	AssignedTo     string          `json:"assigned_to,omitempty"`
	DependsOn      []string        `json:"depends_on,omitempty"`
	Reads          []string        `json:"reads,omitempty"`
	Writes         []string        `json:"writes,omitempty"`
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
	// SpawningThreadID 记录该 runtime node 最近一次 spawn 出的 child agent thread id。
	// UI 用它建立节点到子线程的跳转；nil 表示未 spawn 或该节点不是 agent executor。
	SpawningThreadID *string                `json:"spawning_thread_id,omitempty"`
	Executor         string                 `json:"executor,omitempty"`
	FailureClass     string                 `json:"failure_class,omitempty"`
	LastWakeupAt     *time.Time             `json:"last_wakeup_at,omitempty"`
	ArtifactLinks    []WorkflowArtifactLink `json:"artifact_links,omitempty"`
	NextAction       string                 `json:"next_action,omitempty"`
}

// DAGDetail 将 DAG 模板摘要和节点列表组合成详情响应。
type DAGDetail struct {
	DAG   DAGSummary `json:"dag"`
	Nodes []DAGNode  `json:"nodes,omitempty"`
}

// ----- DAG 事件辅助函数 -----

// task_get_run 返回的 events 是 json.RawMessage。这里提供 typed helper，
// 让 UI、工具和测试共享同一 decoder 路径，而不是各端手写解析逻辑。

// DAGEvent 是 task_dag_runs.events jsonb 数组里单个事件的公共结构。
//
// node_spawn 事件使用 `{kind:"node_spawn", node_key, prev_thread_id, thread_id, ts}` 形状，
// 用来描述 runtime node spawn 子 agent thread 的链路。未知 kind 不会被解析阶段拒绝，
// 调用方按 Kind 决定是否消费，保证事件 union 可扩展。
//
// 字段说明：
//   - Kind: discriminator。当前已知值包含 "node_spawn"。
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
	// DAGEventKindNodeSpawn 表示 runtime node spawn 子 agent thread 的事件。
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
