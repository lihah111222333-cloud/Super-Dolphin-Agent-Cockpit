package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	orch "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpcommon "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// agentIDRegistry 记录正在启动中的 agent ID 预留，防止并发重复启动同一 ID。
type agentIDRegistry struct {
	mu           sync.Mutex
	reservations map[string]struct{}
}

var agentIDReg = &agentIDRegistry{}

// LaunchAgentInput 是 launch_agent MCP 工具的入参结构体。
type LaunchAgentInput struct {
	AgentID            string `json:"agent_id,omitempty"`
	Name               string `json:"name"`
	Prompt             string `json:"prompt,omitempty"`
	ContextMode        string `json:"context_mode,omitempty"`
	Context            string `json:"context,omitempty"`
	ParentID           string `json:"parent_id,omitempty"`
	AgentType          string `json:"agent_type,omitempty"`
	ReadOnly           bool   `json:"read_only,omitempty"`
	AgentKey           string `json:"agent_key,omitempty"`
	PromptKey          string `json:"prompt_key,omitempty"`
	MemoryScope        string `json:"memory_scope,omitempty"`
	CWD                string `json:"cwd,omitempty"`
	Provider           string `json:"provider,omitempty"`
	Model              string `json:"model,omitempty"`
	CodexHome          string `json:"codex_home,omitempty"`
	CodexInstanceKey   string `json:"codex_instance_key,omitempty"`
	CodexModelProvider string `json:"codex_model_provider,omitempty"`
	Effort             string `json:"effort,omitempty"`
	Language           string `json:"language,omitempty"`
	DisabledTools      string `json:"disabled_tools,omitempty"`
}

// SendMessageInput 是 send_message MCP 工具的入参结构体。
type SendMessageInput struct {
	AgentID    string `json:"agent_id"`
	Pos        string `json:"pos,omitempty"`
	Message    string `json:"message"`
	WaitReport *bool  `json:"wait_report,omitempty"`
	TimeoutMS  int    `json:"timeout_ms,omitempty"`
}

// AgentIDInput 是需要 agent ID 的通用工具入参结构体。
type AgentIDInput struct {
	AgentID string `json:"agent_id"`
	Pos     string `json:"pos,omitempty"`
}

// stopAgentInput 是 stop_agent MCP 工具的入参结构体。
type stopAgentInput struct {
	AgentID   string `json:"agent_id"`
	Pos       string `json:"pos,omitempty"`
	Wait      *bool  `json:"wait,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// ListAgentsInput 是 list_agents MCP 工具的入参结构体。
type ListAgentsInput struct {
	State           string `json:"state,omitempty"`
	CWD             string `json:"cwd,omitempty"`
	IncludeInactive bool   `json:"include_inactive,omitempty"`
	IncludeReports  bool   `json:"include_reports,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	Envelope        bool   `json:"envelope,omitempty"`
}

// ListAgentsOutput 是 list_agents 工具在 envelope 模式下的返回结构体。
type ListAgentsOutput struct {
	Agents    []contract.AgentSnapshot `json:"agents"`
	Data      []contract.AgentSnapshot `json:"data"`
	Total     int                      `json:"total"`
	Showing   int                      `json:"showing"`
	Truncated bool                     `json:"truncated"`
	Hint      string                   `json:"hint,omitempty"`
}

type agentSnapshotLister interface {
	ListAgents(context.Context) ([]contract.AgentSnapshot, error)
}

type agentLaunchPort interface {
	agentSnapshotLister
	LaunchAgent(context.Context, contract.LaunchRequest) error
	Snapshot(context.Context, string) (contract.AgentSnapshot, error)
}

type agentListPort interface {
	agentSnapshotLister
	GetReport(context.Context, string) (contract.AgentReportResult, error)
}

// launchAgentSnapshotter 是支持快照式启动的 orchestration service 可选接口。
type launchAgentSnapshotter interface {
	LaunchAgentSnapshot(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error)
}

// agentArchiver 是支持 ArchiveAgent 的 orchestration service 可选接口。
type agentArchiver interface {
	ArchiveAgent(context.Context, string) (orch.ArchiveOutcome, error)
}

// 下列包级 enum 切片是 schema 与 handler 层 requireEnum 的单一真源。
// 修改 schema 字面量时必须同步切片，反之亦然。
// memory_scope 仍走独立校验函数，避免把语义范围和 launch provider/context 枚举混在一起。
var (
	launchAgentProviderEnum    = []string{"codex", "claude"}
	launchAgentContextModeEnum = []string{launchContextModeMinimal, launchContextModeFocused, launchContextModeForked}
)

const (
	launchContextModeMinimal = "minimal"
	launchContextModeFocused = "focused"
	launchContextModeForked  = "forked"
)

const subAgentDelegationDepthLimitMessage = "Sub-agents are not allowed to spawn further agents (delegation depth limit)."

func defaultLaunchAgentDisabledTools() ([]string, error) {
	tools, err := contract.OrchestrationLaunchDefaultDisabledTools()
	if err != nil {
		return nil, err
	}
	tools = append(tools, "spawn_agent")
	return tools, nil
}

// HandleLaunchAgent 注册 launch_agent 工具处理器，默认使用当前可执行文件重启子进程。
func HandleLaunchAgent(svc agentLaunchPort) ToolHandler {
	return handleLaunchAgentWithExeFn(svc, os.Executable)
}

// handleLaunchAgentWithExeFn 构造启动请求并处理同步快照启动或异步后台启动。
// agent_id 预留必须覆盖整个启动窗口，防止并发请求同时启动同一逻辑 agent。
func handleLaunchAgentWithExeFn(svc agentLaunchPort, exeFn func() (string, error)) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in LaunchAgentInput) (map[string]any, error) {
		req, err := launchRequestForHandler(ctx, svc, in, exeFn)
		if err != nil {
			return nil, err
		}
		pkglogger.Debug("orchestration_launch_agent: request config trace",
			"agent_id", req.AgentID,
			"name", strings.TrimSpace(in.Name),
			"provider", strings.TrimSpace(in.Provider),
			"model", strings.TrimSpace(in.Model),
			"effort", strings.TrimSpace(in.Effort),
			"cwd", strings.TrimSpace(in.CWD),
			"has_effort", strings.TrimSpace(in.Effort) != "",
		)
		snapshot, matched, err := matchingAgentID(ctx, svc, req.AgentID)
		if err != nil {
			return nil, err
		}
		if matched {
			result := launchAgentAcceptedResult(snapshot, req.AgentID)
			result["status"] = "existing"
			return successResult(result), nil
		}
		agentID, releaseAgentID, reserved, err := reserveLaunchAgentID(ctx, svc, req.AgentID)
		if err != nil {
			return nil, err
		}
		req.AgentID = agentID
		if !reserved {
			return nil, fmt.Errorf("agent %q launch already in progress", req.AgentID)
		}
		if snapshotSvc, ok := svc.(launchAgentSnapshotter); ok {
			defer releaseAgentID()
			snapshot, err := snapshotSvc.LaunchAgentSnapshot(ctx, req)
			if err != nil {
				return nil, err
			}
			return successResult(launchAgentAcceptedResult(snapshot, req.AgentID)), nil
		}
		// 异步启动立即返回，避免 MCP 工具调用超过 app-server 超时；
		// 后台 goroutine 有 AsyncLaunchTimeout 上限，调用方通过 list/report 工具观察结果。
		safego.Go(context.Background(), nil, "mcp-orch.tools.launchAgent", func(context.Context) {
			defer releaseAgentID()
			bgCtx, cancel := platformconfig.WithTimeout(context.Background(), platformconfig.AsyncLaunchTimeout)
			defer cancel()
			if err := svc.LaunchAgent(bgCtx, req); err != nil {
				pkglogger.Warn("orchestration_launch_agent: async launch failed",
					"agent_id", req.AgentID, "error", err)
			}
		})
		return successResult(map[string]any{"agent_id": req.AgentID, "status": "launching"}), nil
	})
}

// launchRequestForHandler 先做调用者深度校验，再把 handler 输入转成启动请求。
// 深度校验必须在 reserve/launch 前发生，避免子 agent 通过旧名或短名进入后续流程。
func launchRequestForHandler(ctx context.Context, svc agentLaunchPort, in LaunchAgentInput, exeFn func() (string, error)) (contract.LaunchRequest, error) {
	if err := rejectChildAgentDelegation(ctx, svc); err != nil {
		return contract.LaunchRequest{}, err
	}
	exe, err := exeFn()
	if err != nil {
		return contract.LaunchRequest{}, err
	}
	return launchRequestFromExecutable(in, exe)
}

// rejectChildAgentDelegation 用可信工具作用域判断调用者深度。
// 第一版只允许根 agent 派生直接子 agent；已有 parent_id 的子 agent 再委派会被工具层阻断。
func rejectChildAgentDelegation(ctx context.Context, svc agentLaunchPort) error {
	scope, ok := mcpcommon.ToolScopeFromContext(ctx)
	if !ok || strings.TrimSpace(scope.AgentID) == "" {
		return nil
	}
	snapshotCtx, cancel := platformconfig.WithTimeoutIfNone(ctx, platformconfig.RPCRequestTimeout)
	defer cancel()
	snapshot, err := svc.Snapshot(snapshotCtx, scope.AgentID)
	if err != nil {
		return fmt.Errorf("verify launch_agent caller %q delegation depth: %w", scope.AgentID, err)
	}
	if strings.TrimSpace(snapshot.ParentID) == "" {
		return nil
	}
	return fmt.Errorf("%s", subAgentDelegationDepthLimitMessage)
}

// matchingAgentID 在已有快照里查找同一逻辑 agent id。
// 活跃 agent 直接复用，stopping/archived 状态则 fail-fast，避免同名重启覆盖未收尾状态。
func matchingAgentID(ctx context.Context, svc agentLaunchPort, agentID string) (contract.AgentSnapshot, bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return contract.AgentSnapshot{}, false, nil
	}
	agents, err := listAgentSnapshots(ctx, svc)
	if err != nil {
		return contract.AgentSnapshot{}, false, err
	}
	for _, agent := range agents {
		if !(strings.TrimSpace(agent.ID) == agentID || strings.TrimSpace(agent.AgentID) == agentID || strings.TrimSpace(agent.LaunchID) == agentID) {
			continue
		}
		if stoppingAgentState(agent.State) {
			return contract.AgentSnapshot{}, false, fmt.Errorf("agent %q is stopping", agentID)
		}
		if archivedAgentState(agent.State) {
			return contract.AgentSnapshot{}, false, fmt.Errorf("agent %q is archived; restore it before relaunching with the same agent_id", agentID)
		}
		if activeAgentState(agent.State) {
			return agent, true, nil
		}
	}
	return contract.AgentSnapshot{}, false, nil
}

// activeAgentState 归类仍可接收消息或仍在启动/恢复中的 agent 状态。
func activeAgentState(state string) bool {
	switch strings.TrimSpace(state) {
	case "provisioning", "idle", "turn_queued", "turn_starting", "turn_running", "awaiting_user_input", "recovering":
		return true
	default:
		return false
	}
}

// stoppingAgentState 识别正在停止但尚未收口的 agent 状态。
func stoppingAgentState(state string) bool {
	return strings.TrimSpace(state) == "stopping"
}

// archivedAgentState 识别会阻止同名重启的已停止或已归档状态。
func archivedAgentState(state string) bool {
	switch strings.TrimSpace(state) {
	case "stopped", "archived":
		return true
	default:
		return false
	}
}

// blocksLaunchAgentIDState 汇总会阻止重复使用同一 ID 启动的状态集合。
func blocksLaunchAgentIDState(state string) bool {
	return activeAgentState(state) || stoppingAgentState(state) || archivedAgentState(state)
}

// launchAgentAcceptedResult 从快照构造 launch_agent 成功返回的 map。
func launchAgentAcceptedResult(snapshot contract.AgentSnapshot, reservedID string) map[string]any {
	agentID := shared.FirstTrimmed(snapshot.AgentID, snapshot.ID, reservedID)
	result := map[string]any{"agent_id": agentID, "status": "launching"}
	if runtimeID := strings.TrimSpace(snapshot.ID); runtimeID != "" && runtimeID != agentID {
		result["launch_id"] = runtimeID
	}
	if threadID := strings.TrimSpace(snapshot.ThreadID); threadID != "" {
		result["thread_id"] = threadID
	}
	return result
}

// reserveLaunchAgentID 在进程内登记启动中的 agent id，并返回释放函数。
// 这个锁只覆盖本进程并发；已有运行态 ID 仍以 orchestration service 快照为准。
func reserveLaunchAgentID(ctx context.Context, svc agentLaunchPort, requested string) (string, func(), bool, error) {
	existing, activeExisting, err := existingLaunchAgentIDs(ctx, svc)
	if err != nil {
		return "", nil, false, err
	}
	agentIDReg.mu.Lock()
	defer agentIDReg.mu.Unlock()
	if agentIDReg.reservations == nil {
		agentIDReg.reservations = make(map[string]struct{})
	}
	candidate := strings.TrimSpace(requested)
	if candidate != "" {
		if _, ok := agentIDReg.reservations[candidate]; ok {
			return candidate, func() {}, false, nil
		}
		if _, ok := activeExisting[candidate]; ok {
			return candidate, func() {}, false, nil
		}
		agentIDReg.reservations[candidate] = struct{}{}
		return candidate, releaseLaunchAgentID(candidate), true, nil
	}
	candidate = shared.NewAgentID()
	for range 64 {
		if !launchAgentIDInUseLocked(candidate, existing) {
			agentIDReg.reservations[candidate] = struct{}{}
			return candidate, releaseLaunchAgentID(candidate), true, nil
		}
		candidate = shared.NewAgentID()
	}
	agentIDReg.reservations[candidate] = struct{}{}
	return candidate, releaseLaunchAgentID(candidate), true, nil
}

// existingLaunchAgentIDs 汇总 runtime id、agent_id 和 launch_id，供新启动避开冲突。
// activeExisting 只记录会阻塞复用的状态，历史 stopped/archived 仍由上层给出明确错误。
func existingLaunchAgentIDs(ctx context.Context, svc agentLaunchPort) (map[string]struct{}, map[string]struct{}, error) {
	existing := make(map[string]struct{})
	activeExisting := make(map[string]struct{})
	if svc == nil {
		return existing, activeExisting, nil
	}
	agents, err := listAgentSnapshots(ctx, svc)
	if err != nil {
		return nil, nil, err
	}
	for _, agent := range agents {
		if id := strings.TrimSpace(agent.ID); id != "" {
			existing[id] = struct{}{}
			if blocksLaunchAgentIDState(agent.State) {
				activeExisting[id] = struct{}{}
			}
		}
		if id := strings.TrimSpace(agent.AgentID); id != "" {
			existing[id] = struct{}{}
			if blocksLaunchAgentIDState(agent.State) {
				activeExisting[id] = struct{}{}
			}
		}
		if id := strings.TrimSpace(agent.LaunchID); id != "" && blocksLaunchAgentIDState(agent.State) {
			activeExisting[id] = struct{}{}
		}
	}
	return existing, activeExisting, nil
}

// launchAgentIDInUseLocked 在持有锁时检查 agentID 是否已被占用或预留。
func launchAgentIDInUseLocked(agentID string, existing map[string]struct{}) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return true
	}
	if _, ok := existing[agentID]; ok {
		return true
	}
	_, ok := agentIDReg.reservations[agentID]
	return ok
}

// releaseLaunchAgentID 返回释放 agentID 预留的闭包函数。
func releaseLaunchAgentID(agentID string) func() {
	return func() {
		agentIDReg.mu.Lock()
		delete(agentIDReg.reservations, strings.TrimSpace(agentID))
		agentIDReg.mu.Unlock()
	}
}

// HandleSendMessage 向已有 agent 提交文本 turn；wait_report=true 时只允许 idle 后续消息。
func HandleSendMessage(svc sendMessagePort) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in SendMessageInput) (map[string]any, error) {
		if sendMessageShouldWaitReport(in) {
			return submitMessageAndWaitForReport(ctx, svc, in)
		}
		submission, err := submissionFromMessage(ctx, svc, in)
		if err != nil {
			return nil, err
		}
		if err := submitSendMessageTurn(ctx, svc, submission, in.Message); err != nil {
			return nil, err
		}
		return successResult(map[string]any{"agent_id": submission.AgentID}), nil
	})
}

// HandleStopAgent 停止或归档 agent，并可等待 list_agents 快照进入终态。
func HandleStopAgent(svc contract.AgentLifecyclePort) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in stopAgentInput) (map[string]any, error) {
		agentID, err := resolveAgentIDInput(in.AgentID, in.Pos)
		if err != nil {
			return nil, err
		}
		if in.Wait != nil && *in.Wait {
			if _, err := stopAgentWaitTimeout(in.TimeoutMS); err != nil {
				return nil, err
			}
		}
		archived := false
		if archiver, ok := svc.(agentArchiver); ok {
			pkglogger.Info("orchestration_stop_agent: dispatching to ArchiveAgent (recycle path)",
				"agent_id", agentID)
			outcome, err := archiver.ArchiveAgent(ctx, agentID)
			if err != nil {
				return nil, err
			}
			archived = outcome.Archived()
			if !archived {
				return nil, fmt.Errorf("%w: %s", contract.ErrAgentNotFound, agentID)
			}
		} else {
			pkglogger.Warn("orchestration_stop_agent: service does not implement agentArchiver; falling back to bare StopAgent (NO recycle-bin marking)",
				"agent_id", agentID,
				"svc_type", fmt.Sprintf("%T", svc))
			if err := svc.StopAgent(ctx, agentID); err != nil {
				return nil, err
			}
		}
		return stopAgentResult(ctx, svc, agentID, archived, in)
	})
}

// HandleListAgents 列出 agent 快照，默认只返回活跃项且不携带报告正文。
func HandleListAgents(svc agentListPort) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in ListAgentsInput) (any, error) {
		cwdFilter, err := listAgentsCWDFilter(ctx, in.CWD)
		if err != nil {
			return nil, err
		}
		agents, err := listAgentSnapshots(ctx, svc)
		if err != nil {
			pkglogger.Warn("orchestration_list_agents: list failed",
				"state", strings.TrimSpace(in.State),
				"cwd", cwdFilter,
				"include_inactive", in.IncludeInactive,
				"include_reports", in.IncludeReports,
				"limit", in.Limit,
				"error", err)
			return nil, err
		}
		filtered := filterListAgentSnapshots(agents, in, cwdFilter)
		if in.IncludeReports {
			if err := hydrateListAgentReports(ctx, svc, filtered); err != nil {
				return nil, err
			}
		}
		if len(filtered) != len(agents) || !in.IncludeReports {
			pkglogger.Warn("orchestration_list_agents: compacted response",
				"total", len(agents),
				"returned", len(filtered),
				"state", strings.TrimSpace(in.State),
				"cwd", cwdFilter,
				"include_inactive", in.IncludeInactive,
				"include_reports", in.IncludeReports,
				"limit", in.Limit)
		}
		if in.Envelope {
			return newListAgentsOutput(filtered, in.Limit), nil
		}
		return filtered, nil
	})
}

// newListAgentsOutput 构造 list_agents 的 envelope 返回对象。
func newListAgentsOutput(agents []contract.AgentSnapshot, limit int) ListAgentsOutput {
	env := newListEnvelope(agents, limit, "next: single report -> get_agent_report pos=agent:<agent_id>; batch reports -> get_agent_reports agent_ids=[...]")
	return ListAgentsOutput{
		Agents:    agents,
		Data:      env.Data,
		Total:     env.Total,
		Showing:   env.Showing,
		Truncated: env.Truncated,
		Hint:      env.Hint,
	}
}

// listAgentSnapshots 带超时保护地获取所有 agent 快照列表。
func listAgentSnapshots(ctx context.Context, svc agentSnapshotLister) ([]contract.AgentSnapshot, error) {
	listCtx, cancel := platformconfig.WithTimeoutIfNone(ctx, platformconfig.RPCRequestTimeout)
	defer cancel()
	return svc.ListAgents(listCtx)
}

// hydrateListAgentReports 为缺少 LastReport 的快照补取报告；找不到 agent 只跳过，其他错误立即返回。
func hydrateListAgentReports(ctx context.Context, svc agentListPort, agents []contract.AgentSnapshot) error {
	reportCtx, cancel := platformconfig.WithTimeoutIfNone(ctx, platformconfig.RPCRequestTimeout)
	defer cancel()
	for i := range agents {
		if strings.TrimSpace(agents[i].LastReport) != "" {
			continue
		}
		agentID := shared.FirstTrimmed(agents[i].AgentID, agents[i].ID)
		if agentID == "" {
			continue
		}
		report, err := svc.GetReport(reportCtx, agentID)
		if err != nil {
			if errors.Is(err, contract.ErrAgentNotFound) {
				continue
			}
			return fmt.Errorf("hydrate agent report %q: %w", agentID, err)
		}
		agents[i].LastReport = report.Report
	}
	return nil
}

// launchRequestFromExecutable 把工具入参转换成服务层 LaunchRequest。
// Command 只保留当前可执行文件路径；远端 launcher 会通过 Env 读取 provider/model 等运行配置。
func launchRequestFromExecutable(in LaunchAgentInput, exe string) (contract.LaunchRequest, error) {
	name, err := requireTrimmed(in.Name, "name")
	if err != nil {
		return contract.LaunchRequest{}, err
	}
	agentID := strings.TrimSpace(in.AgentID)
	if agentID == "" {
		agentID = shared.NewAgentID()
	}
	provider, err := validateLaunchProvider(in.Provider)
	if err != nil {
		return contract.LaunchRequest{}, err
	}
	if err := rejectUnsupportedClaudeChildLaunch(provider, in.ParentID); err != nil {
		return contract.LaunchRequest{}, err
	}
	memoryScope, err := validateMemoryScope(in.MemoryScope)
	if err != nil {
		return contract.LaunchRequest{}, err
	}
	contextMode := normalizedLaunchContextMode(in.ContextMode)
	prompt, err := launchPromptFromContextMode(in)
	if err != nil {
		return contract.LaunchRequest{}, err
	}
	req := contract.LaunchRequest{
		AgentID:     agentID,
		Name:        name,
		Prompt:      prompt,
		ParentID:    strings.TrimSpace(in.ParentID),
		ContextMode: contextMode,
		AgentType:   strings.TrimSpace(in.AgentType),
		AgentKey:    strings.TrimSpace(in.AgentKey),
		PromptKey:   strings.TrimSpace(in.PromptKey),
		MemoryScope: memoryScope,
		Cwd:         in.CWD,
		Command:     []string{strings.TrimSpace(exe)},
		Env:         launchEnv(provider, strings.TrimSpace(in.Model), strings.TrimSpace(in.Effort), strings.TrimSpace(in.CodexHome), strings.TrimSpace(in.CodexInstanceKey), strings.TrimSpace(in.CodexModelProvider)),
		Language:    strings.TrimSpace(in.Language),
	}
	readOnlyToolSurface := launchReadOnlyToolSurface(in)
	dt, err := mergeLaunchDisabledTools(readOnlyToolSurface, in.DisabledTools)
	if err != nil {
		return contract.LaunchRequest{}, err
	}
	if dt != "" {
		req.Env = append(req.Env, "AGENT_DISABLED_TOOLS="+dt)
	}
	if strings.EqualFold(provider, "codex") {
		req.Env = append(req.Env, "AGENT_CODEX_DISABLED_NATIVE_TOOLS="+strings.Join(launchCodexNativeDisabledTools(readOnlyToolSurface), ","))
	}
	return req, nil
}

// mergeLaunchDisabledTools 合并 launch 默认禁用项、只读 agent 工具面禁用项和用户指定的额外禁用工具。
func mergeLaunchDisabledTools(readOnlyToolSurface bool, userValue string) (string, error) {
	defaults, err := defaultLaunchAgentDisabledTools()
	if err != nil {
		return "", err
	}
	if readOnlyToolSurface {
		defaults = append(defaults, contract.ReadOnlyAgentDeniedTools()...)
	}
	return joinUniqueCSV(defaults, userValue), nil
}

func launchCodexNativeDisabledTools(readOnlyToolSurface bool) []string {
	if readOnlyToolSurface {
		return contract.ReadOnlyCodexNativeDeniedTools()
	}
	return []string{contract.CodexNativeToolSpawnAgent}
}

func launchReadOnlyToolSurface(in LaunchAgentInput) bool {
	if in.ReadOnly {
		return true
	}
	return readOnlyLaunchAgentType(in.AgentType)
}

func readOnlyLaunchAgentType(agentType string) bool {
	switch contract.AgentType(strings.TrimSpace(agentType)) {
	case contract.AgentTypeExplore, contract.AgentTypePlan:
		return true
	default:
		return false
	}
}

// joinUniqueCSV 把 defaults 和 extra（逗号分隔）合并为去重的 CSV 字符串。
func joinUniqueCSV(defaults []string, extra string) string {
	seen := make(map[string]struct{}, len(defaults))
	out := make([]string, 0, len(defaults))
	add := func(value string) {
		for item := range strings.SplitSeq(value, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	for _, item := range defaults {
		add(item)
	}
	add(extra)
	return strings.Join(out, ",")
}

// launchPromptFromContextMode 把 launch_agent 的上下文模式整理成首轮 prompt。
// minimal 保持旧行为；focused 只拼接调用者显式筛选后的 context，不读取父线程历史。
func launchPromptFromContextMode(in LaunchAgentInput) (string, error) {
	mode := normalizedLaunchContextMode(in.ContextMode)
	prompt := strings.TrimSpace(in.Prompt)
	contextText := strings.TrimSpace(in.Context)
	switch mode {
	case launchContextModeMinimal:
		if contextText != "" {
			return "", fmt.Errorf("context_mode=minimal does not accept context field, but got: %q", contextText)
		}
		return prompt, nil
	case launchContextModeFocused:
		if contextText == "" {
			return "", fmt.Errorf("context_mode=focused requires non-empty context field")
		}
		return fmt.Sprintf("【相关上下文】\n%s\n\n【任务】\n%s", contextText, prompt), nil
	case launchContextModeForked:
		if strings.TrimSpace(in.ParentID) == "" {
			return "", fmt.Errorf("context_mode=forked requires non-empty parent_id")
		}
		if contextText != "" {
			return "", fmt.Errorf("context_mode=forked does not accept context field, but got: %q", contextText)
		}
		return prompt, nil
	default:
		return "", fmt.Errorf("unsupported context_mode: %q, allowed values: minimal, focused, forked", strings.TrimSpace(in.ContextMode))
	}
}

// normalizedLaunchContextMode 将 context_mode 规范为小写，空值默认为 minimal。
func normalizedLaunchContextMode(raw string) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		return launchContextModeMinimal
	}
	return mode
}

// validateLaunchProvider 校验并规范化 provider 字段，空值默认为 codex。
func validateLaunchProvider(raw string) (string, error) {
	// provider 可选；空串/纯空白 → codex。
	// 非空时走 requireEnum 与 launchAgentProviderEnum 校验（单源驱动）。
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "" {
		return "codex", nil
	}
	return requireEnum(lower, "provider", launchAgentProviderEnum)
}

// rejectUnsupportedClaudeChildLaunch 阻断第一版暂不支持的 Claude 子 agent 编排。
// 普通 Claude root launch 保持兼容；只要带 parent_id 就属于父子编排路径，必须 fail-fast。
func rejectUnsupportedClaudeChildLaunch(provider, parentID string) error {
	if strings.TrimSpace(parentID) == "" || !strings.EqualFold(strings.TrimSpace(provider), "claude") {
		return nil
	}
	return fmt.Errorf("Claude sub-agent orchestration is not supported; launch_agent child agents currently support provider=codex only")
}

// validateMemoryScope 校验并规范化 memory_scope 字段，空值视为合法。
func validateMemoryScope(raw string) (string, error) {
	scope := strings.ToLower(strings.TrimSpace(raw))
	switch scope {
	case "", "project", "user", "local":
		return scope, nil
	default:
		return "", fmt.Errorf("invalid memory_scope %q: must be project, user, or local", raw)
	}
}

// launchEnv 组装传给子 agent 运行时的环境变量，空字段不写入 Env。
func launchEnv(provider, model, effort, codexHome, codexInstanceKey, codexModelProvider string) []string {
	var env []string
	if provider = strings.TrimSpace(provider); provider != "" {
		env = append(env, "AGENT_PROVIDER="+provider)
	}
	if model = strings.TrimSpace(model); model != "" {
		// 远端 launcher 会把 AGENT_MODEL 转成 thread/start 的 model 字段。
		env = append(env, "AGENT_MODEL="+model)
	}
	if effort = strings.TrimSpace(effort); effort != "" {
		env = append(env, "AGENT_EFFORT="+effort)
	}
	if codexHome = strings.TrimSpace(codexHome); codexHome != "" {
		env = append(env, "AGENT_CODEX_HOME="+codexHome)
	}
	if codexInstanceKey = strings.TrimSpace(codexInstanceKey); codexInstanceKey != "" {
		env = append(env, "AGENT_CODEX_INSTANCE_KEY="+codexInstanceKey)
	}
	if codexModelProvider = strings.TrimSpace(codexModelProvider); codexModelProvider != "" {
		env = append(env, "AGENT_CODEX_MODEL_PROVIDER="+codexModelProvider)
	}
	return env
}
