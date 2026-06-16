package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/eventcore"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type agentIDRegistry struct {
	mu           sync.Mutex
	reservations map[string]struct{}
}

var agentIDReg = &agentIDRegistry{}

// LaunchAgentInput carries input for tools operations.
type LaunchAgentInput struct {
	AgentID            string `json:"agent_id,omitempty"`
	Name               string `json:"name"`
	Prompt             string `json:"prompt,omitempty"`
	ParentID           string `json:"parent_id,omitempty"`
	AgentType          string `json:"agent_type,omitempty"`
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

// SendMessageInput carries input for tools operations.
type SendMessageInput struct {
	AgentID string `json:"agent_id"`
	Pos     string `json:"pos,omitempty"`
	Message string `json:"message"`
}

// AgentIDInput carries input for tools operations.
type AgentIDInput struct {
	AgentID string `json:"agent_id"`
	Pos     string `json:"pos,omitempty"`
}

// ListAgentsInput carries input for tools operations.
type ListAgentsInput struct {
	State           string `json:"state,omitempty"`
	CWD             string `json:"cwd,omitempty"`
	IncludeInactive bool   `json:"include_inactive,omitempty"`
	IncludeReports  bool   `json:"include_reports,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	Envelope        bool   `json:"envelope,omitempty"`
}

// ListAgentsOutput contains output returned by tools operations.
type ListAgentsOutput struct {
	Agents    []contract.AgentSnapshot `json:"agents"`
	Data      []contract.AgentSnapshot `json:"data"`
	Total     int                      `json:"total"`
	Showing   int                      `json:"showing"`
	Truncated bool                     `json:"truncated"`
	Hint      string                   `json:"hint,omitempty"`
}

type launchAgentSnapshotter interface {
	LaunchAgentSnapshot(context.Context, contract.LaunchRequest) (contract.AgentSnapshot, error)
}

type agentArchiver interface {
	ArchiveAgent(context.Context, string) error
}

// 下列包级 enum 切片是 schema 与 handler 层 requireEnum 的单一真源。
// 修改 schema 字面量时必须同步切片，反之亦然。
// memory_scope drift 暂不在本批改（见 docs/plans/dag改造实施计划.md §10
// follow-up），保留 validateMemoryScope 独立路径。
//
// The slices below are the single source of truth shared by the schema
// builder and the handler-layer requireEnum fallback. The memory_scope
// drift follow-up is tracked separately (see plans §10).
var (
	launchAgentProviderEnum = []string{"codex", "claude"}
)

// HandleLaunchAgent 处理启动代理。
func HandleLaunchAgent(svc contract.OrchestrationService) ToolHandler {
	return handleLaunchAgentWithExeFn(svc, os.Executable)
}

// handleLaunchAgentWithExeFn 处理带exefn的启动代理。
func handleLaunchAgentWithExeFn(svc contract.OrchestrationService, exeFn func() (string, error)) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in LaunchAgentInput) (map[string]any, error) {
		exe, err := exeFn()
		if err != nil {
			return nil, err
		}
		req, err := launchRequestFromExecutable(in, exe)
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
		// Async launch: return immediately so the MCP tool call never blocks
		// longer than the codex app-server's tool-call timeout. The actual
		// launch runs in the background; callers poll orchestration_list_agents
		// or orchestration_get_agent_report for status.
		//
		// Bounded lifetime: context.Background() is acceptable here because
		// AsyncLaunchTimeout caps the goroutine's maximum duration. No
		// service-level lifecycle ctx is available in the tools package.
		go func() {
			defer releaseAgentID()
			bgCtx, cancel := platformconfig.WithTimeout(context.Background(), platformconfig.AsyncLaunchTimeout)
			defer cancel()
			if err := svc.LaunchAgent(bgCtx, req); err != nil {
				pkglogger.Warn("orchestration_launch_agent: async launch failed",
					"agent_id", req.AgentID, "error", err)
			}
		}()
		return successResult(map[string]any{"agent_id": req.AgentID, "status": "launching"}), nil
	})
}

// matchingAgentID 处理matching代理ID。
func matchingAgentID(ctx context.Context, svc contract.OrchestrationService, agentID string) (contract.AgentSnapshot, bool, error) {
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

func activeAgentState(state string) bool {
	switch strings.TrimSpace(state) {
	case "provisioning", "idle", "turn_queued", "turn_starting", "turn_running", "awaiting_user_input", "recovering":
		return true
	default:
		return false
	}
}

func stoppingAgentState(state string) bool {
	return strings.TrimSpace(state) == "stopping"
}

func archivedAgentState(state string) bool {
	switch strings.TrimSpace(state) {
	case "stopped", "archived":
		return true
	default:
		return false
	}
}

func blocksLaunchAgentIDState(state string) bool {
	return activeAgentState(state) || stoppingAgentState(state) || archivedAgentState(state)
}

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

// reserveLaunchAgentID 处理reserve启动代理ID。
func reserveLaunchAgentID(ctx context.Context, svc contract.OrchestrationService, requested string) (string, func(), bool, error) {
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
	for i := 0; i < 64; i++ {
		if !launchAgentIDInUseLocked(candidate, existing) {
			agentIDReg.reservations[candidate] = struct{}{}
			return candidate, releaseLaunchAgentID(candidate), true, nil
		}
		candidate = shared.NewAgentID()
	}
	agentIDReg.reservations[candidate] = struct{}{}
	return candidate, releaseLaunchAgentID(candidate), true, nil
}

// existingLaunchAgentIDs 处理existing启动代理ids。
func existingLaunchAgentIDs(ctx context.Context, svc contract.OrchestrationService) (map[string]struct{}, map[string]struct{}, error) {
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

func releaseLaunchAgentID(agentID string) func() {
	return func() {
		agentIDReg.mu.Lock()
		delete(agentIDReg.reservations, strings.TrimSpace(agentID))
		agentIDReg.mu.Unlock()
	}
}

// HandleSendMessage 处理send消息。
func HandleSendMessage(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in SendMessageInput) (map[string]any, error) {
		submission, err := submissionFromMessage(ctx, svc, in)
		if err != nil {
			return nil, err
		}
		pkglogger.Warn("orchestration_send_message: submit begin",
			"agent_id", submission.AgentID,
			"thread_id", submission.ThreadID,
			"input_items", len(submission.Inputs),
			"message_len", len([]rune(strings.TrimSpace(in.Message))))
		if err := svc.SubmitTurn(ctx, submission); err != nil {
			pkglogger.Warn("orchestration_send_message: submit failed",
				"agent_id", submission.AgentID,
				"thread_id", submission.ThreadID,
				"error", err)
			return nil, err
		}
		pkglogger.Warn("orchestration_send_message: submit accepted",
			"agent_id", submission.AgentID,
			"thread_id", submission.ThreadID)
		return successResult(map[string]any{"agent_id": submission.AgentID}), nil
	})
}

// HandleStopAgent 处理stop代理。
func HandleStopAgent(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in AgentIDInput) (map[string]any, error) {
		agentID, err := resolveAgentIDInput(in.AgentID, in.Pos)
		if err != nil {
			return nil, err
		}
		archived := false
		if archiver, ok := svc.(agentArchiver); ok {
			pkglogger.Info("orchestration_stop_agent: dispatching to ArchiveAgent (recycle path)",
				"agent_id", agentID)
			if err := archiver.ArchiveAgent(ctx, agentID); err != nil {
				return nil, err
			}
			archived = true
		} else {
			pkglogger.Warn("orchestration_stop_agent: service does not implement agentArchiver; falling back to bare StopAgent (NO recycle-bin marking)",
				"agent_id", agentID,
				"svc_type", fmt.Sprintf("%T", svc))
			if err := svc.StopAgent(ctx, agentID); err != nil {
				return nil, err
			}
		}
		return successResult(map[string]any{"agent_id": agentID, "archived": archived}), nil
	})
}

// HandleListAgents 处理list代理。
func HandleListAgents(svc contract.OrchestrationService) ToolHandler {
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

func newListAgentsOutput(agents []contract.AgentSnapshot, limit int) ListAgentsOutput {
	env := newListEnvelope(agents, limit, "next: use get_agent_report pos=agent:<agent_id> for full report")
	return ListAgentsOutput{
		Agents:    agents,
		Data:      env.Data,
		Total:     env.Total,
		Showing:   env.Showing,
		Truncated: env.Truncated,
		Hint:      env.Hint,
	}
}

func listAgentSnapshots(ctx context.Context, svc contract.OrchestrationService) ([]contract.AgentSnapshot, error) {
	listCtx, cancel := platformconfig.WithTimeoutIfNone(ctx, platformconfig.RPCRequestTimeout)
	defer cancel()
	return svc.ListAgents(listCtx)
}

func hydrateListAgentReports(ctx context.Context, svc contract.OrchestrationService, agents []contract.AgentSnapshot) error {
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
			return fmt.Errorf("hydrate agent report %q: %w", agentID, err)
		}
		agents[i].LastReport = report.Report
	}
	return nil
}

// launchRequestFromExecutable 从可执行文件启动请求。
func launchRequestFromExecutable(in LaunchAgentInput, exe string) (contract.LaunchRequest, error) {
	// NOTE: the old standalone-mcp-orch guard has been removed.  When
	// GO_AGENT_CTL_RPC_ADDR is set the orchestration service uses
	// remoteLauncher which delegates to thread/start on the main app's RPC
	// server and never touches the Command field.  The guard was a relic of
	// the previous architecture where the desktop app embedded its own
	// orchestration module.
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
	memoryScope, err := validateMemoryScope(in.MemoryScope)
	if err != nil {
		return contract.LaunchRequest{}, err
	}
	req := contract.LaunchRequest{
		AgentID:     agentID,
		Name:        name,
		Prompt:      strings.TrimSpace(in.Prompt),
		ParentID:    strings.TrimSpace(in.ParentID),
		AgentType:   strings.TrimSpace(in.AgentType),
		AgentKey:    strings.TrimSpace(in.AgentKey),
		PromptKey:   strings.TrimSpace(in.PromptKey),
		MemoryScope: memoryScope,
		Cwd:         in.CWD,
		Command:     []string{strings.TrimSpace(exe)},
		Env:         launchEnv(provider, strings.TrimSpace(in.Model), strings.TrimSpace(in.Effort), strings.TrimSpace(in.CodexHome), strings.TrimSpace(in.CodexInstanceKey), strings.TrimSpace(in.CodexModelProvider)),
		Language:    strings.TrimSpace(in.Language),
	}
	if dt := strings.TrimSpace(in.DisabledTools); dt != "" {
		req.Env = append(req.Env, "AGENT_DISABLED_TOOLS="+dt)
	}
	return req, nil
}

func validateLaunchProvider(raw string) (string, error) {
	// provider 可选；空串/纯空白 → codex。
	// 非空时走 requireEnum 与 launchAgentProviderEnum 校验（单源驱动）。
	//
	// provider is optional; empty/whitespace defaults to codex. Non-empty values
	// are validated by requireEnum against launchAgentProviderEnum (single source
	// of truth shared with the schema).
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "" {
		return "codex", nil
	}
	return requireEnum(lower, "provider", launchAgentProviderEnum)
}

func validateMemoryScope(raw string) (string, error) {
	scope := strings.ToLower(strings.TrimSpace(raw))
	switch scope {
	case "", "project", "user", "local":
		return scope, nil
	default:
		return "", fmt.Errorf("invalid memory_scope %q: must be project, user, or local", raw)
	}
}

// launchEnv 启动env。
func launchEnv(provider, model, effort, codexHome, codexInstanceKey, codexModelProvider string) []string {
	var env []string
	if provider = strings.TrimSpace(provider); provider != "" {
		env = append(env, "AGENT_PROVIDER="+provider)
	}
	if model = strings.TrimSpace(model); model != "" {
		// remoteLauncher.Launch reads this back via envValue(req.Env, "AGENT_MODEL")
		// and forwards it as the `model` field on thread/start.
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

func submissionFromMessage(
	ctx context.Context,
	svc contract.OrchestrationService,
	in SendMessageInput,
) (contract.TurnSubmission, error) {
	agentID, err := resolveAgentIDInput(in.AgentID, in.Pos)
	if err != nil {
		return contract.TurnSubmission{}, err
	}
	message, err := requireTrimmed(in.Message, "message")
	if err != nil {
		return contract.TurnSubmission{}, err
	}
	return contract.TurnSubmission{
		AgentID:  agentID,
		ThreadID: submissionThreadID(ctx, svc, agentID),
		Inputs: []shareddto.InputItem{{
			Type:    "text",
			Content: message,
		}},
	}, nil
}

func submissionThreadID(ctx context.Context, svc contract.OrchestrationService, agentID string) string {
	snapshot, err := svc.Snapshot(ctx, agentID)
	if err == nil && strings.TrimSpace(snapshot.ThreadID) != "" {
		return strings.TrimSpace(snapshot.ThreadID)
	}
	return agentID
}
