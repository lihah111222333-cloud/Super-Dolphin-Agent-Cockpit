package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

var osExecutable = os.Executable

var (
	launchAgentIDMu           sync.Mutex
	launchAgentIDReservations map[string]struct{}
)

type LaunchAgentInput struct {
	AgentID     string `json:"agent_id,omitempty"`
	Name        string `json:"name"`
	Prompt      string `json:"prompt,omitempty"`
	ParentID    string `json:"parent_id,omitempty"`
	AgentType   string `json:"agent_type,omitempty"`
	AgentKey    string `json:"agent_key,omitempty"`
	MemoryScope string `json:"memory_scope,omitempty"`
	CWD         string `json:"cwd,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
}

type SendMessageInput struct {
	AgentID string `json:"agent_id"`
	Message string `json:"message"`
}

type AgentIDInput struct {
	AgentID string `json:"agent_id"`
}

func HandleLaunchAgent(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in LaunchAgentInput) (map[string]any, error) {
		req, err := launchRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		agentID, releaseAgentID := reserveLaunchAgentID(ctx, svc, req.AgentID)
		req.AgentID = agentID
		// Async launch: return immediately so the MCP tool call never blocks
		// longer than the codex app-server's tool-call timeout. The actual
		// launch runs in the background; callers poll orchestration_list_agents
		// or orchestration_get_agent_report for status.
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

func reserveLaunchAgentID(ctx context.Context, svc contract.OrchestrationService, requested string) (string, func()) {
	existing := existingLaunchAgentIDs(ctx, svc)
	launchAgentIDMu.Lock()
	defer launchAgentIDMu.Unlock()
	if launchAgentIDReservations == nil {
		launchAgentIDReservations = make(map[string]struct{})
	}
	candidate := strings.TrimSpace(requested)
	if candidate == "" {
		candidate = shared.NewAgentID()
	}
	for i := 0; i < 64; i++ {
		if !launchAgentIDInUseLocked(candidate, existing) {
			launchAgentIDReservations[candidate] = struct{}{}
			return candidate, releaseLaunchAgentID(candidate)
		}
		candidate = shared.NewAgentID()
	}
	launchAgentIDReservations[candidate] = struct{}{}
	return candidate, releaseLaunchAgentID(candidate)
}

func existingLaunchAgentIDs(ctx context.Context, svc contract.OrchestrationService) map[string]struct{} {
	existing := make(map[string]struct{})
	if svc == nil {
		return existing
	}
	agents, err := svc.ListAgents(ctx)
	if err != nil {
		return existing
	}
	for _, agent := range agents {
		if id := strings.TrimSpace(agent.ID); id != "" {
			existing[id] = struct{}{}
		}
		if id := strings.TrimSpace(agent.AgentID); id != "" {
			existing[id] = struct{}{}
		}
	}
	return existing
}

func launchAgentIDInUseLocked(agentID string, existing map[string]struct{}) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return true
	}
	if _, ok := existing[agentID]; ok {
		return true
	}
	_, ok := launchAgentIDReservations[agentID]
	return ok
}

func releaseLaunchAgentID(agentID string) func() {
	return func() {
		launchAgentIDMu.Lock()
		delete(launchAgentIDReservations, strings.TrimSpace(agentID))
		launchAgentIDMu.Unlock()
	}
}

func HandleSendMessage(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in SendMessageInput) (map[string]any, error) {
		submission, err := submissionFromMessage(ctx, svc, in)
		if err != nil {
			return nil, err
		}
		if err := svc.SubmitTurn(ctx, submission); err != nil {
			return nil, err
		}
		return successResult(map[string]any{"agent_id": submission.AgentID}), nil
	})
}

func HandleStopAgent(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in AgentIDInput) (map[string]any, error) {
		agentID, err := requireTrimmed(in.AgentID, "agent_id")
		if err != nil {
			return nil, err
		}
		if err := svc.StopAgent(ctx, agentID); err != nil {
			return nil, err
		}
		return successResult(map[string]any{"agent_id": agentID}), nil
	})
}

func HandleListAgents(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, _ struct{}) (any, error) {
		return svc.ListAgents(ctx)
	})
}

func HandleGetAgentReport(svc contract.OrchestrationService) ToolHandler {
	return makeHandler(svc, "orchestration service", func(ctx context.Context, in AgentIDInput) (any, error) {
		agentID, err := requireTrimmed(in.AgentID, "agent_id")
		if err != nil {
			return nil, err
		}
		return svc.GetReport(ctx, agentID)
	})
}

func orchestrationToolDefinitions(svc contract.OrchestrationService) []ToolDefinition {
	return buildToolDefinitions(
		defineTool("orchestration_launch_agent", "Launch a managed orchestration agent.", ObjectSchema(map[string]Schema{
			"agent_id":     StringSchema("Optional persisted orchestration agent ID. When omitted, the launcher generates an agent_... ID; never use name as the truth source."),
			"name":         StringSchema("User-facing agent name. Prefer a short friendly name tied to the task; avoid paths, IDs, and generic labels like worker-agent."),
			"prompt":       StringSchema("Optional initial prompt to submit as the launched agent's first turn."),
			"parent_id":    StringSchema("Optional parent agent ID for child-agent launches."),
			"agent_type":   StringSchema("Optional stable agent identity. Required for agent memory routing; display name is not used as a fallback."),
			"agent_key":    StringSchema("Optional router agent_key. When set, thread/start looks up the matching prompt_template and injects its prompt_text as base_instructions."),
			"memory_scope": EnumStringSchema("Optional agent memory scope for child-agent launches.", "project", "user", "local"),
			"cwd":          StringSchema("Optional working directory for the launched agent."),
			"provider":     EnumStringSchema("Provider for the launched agent. Defaults to codex when omitted.", "codex", "claude"),
			"model":        StringSchema("Optional model identifier for the launched agent (e.g. 'sonnet', 'opus', 'claude-opus-4-7[1m]'). When omitted, the provider falls back to its own default (for claude: ~/.claude/settings.json `model`)."),
		}, "name"), HandleLaunchAgent(svc)),
		defineTool("orchestration_send_message", "Submit a text turn to an existing orchestration agent.", ObjectSchema(map[string]Schema{
			"agent_id": StringSchema("Target orchestration agent ID."),
			"message":  StringSchema("Message content to submit as a text input."),
		}, "agent_id", "message"), HandleSendMessage(svc)),
		defineTool("orchestration_stop_agent", "Stop a running orchestration agent.", ObjectSchema(map[string]Schema{
			"agent_id": StringSchema("Target orchestration agent ID."),
		}, "agent_id"), HandleStopAgent(svc)),
		defineTool("orchestration_list_agents", "List orchestration agents and their current runtime snapshots, including persisted agent_id and display name.", ObjectSchema(nil), HandleListAgents(svc)),
		defineTool("orchestration_get_agent_report", "Read the last known report for an orchestration agent. Pass the persisted agent_id returned by launch/list; display name is not an identifier.", ObjectSchema(map[string]Schema{
			"agent_id": StringSchema("Persisted target orchestration agent ID returned by launch/list; do not pass name."),
		}, "agent_id"), HandleGetAgentReport(svc)),
	)
}

func launchRequestFromInput(in LaunchAgentInput) (contract.LaunchRequest, error) {
	exe, err := osExecutable()
	if err != nil {
		return contract.LaunchRequest{}, err
	}
	return launchRequestFromExecutable(in, exe)
}

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
	return contract.LaunchRequest{
		AgentID:     agentID,
		Name:        name,
		Prompt:      strings.TrimSpace(in.Prompt),
		ParentID:    strings.TrimSpace(in.ParentID),
		AgentType:   strings.TrimSpace(in.AgentType),
		AgentKey:    strings.TrimSpace(in.AgentKey),
		MemoryScope: memoryScope,
		Cwd:         strings.TrimSpace(in.CWD),
		Command:     []string{strings.TrimSpace(exe)},
		Env:         launchEnv(provider, strings.TrimSpace(in.Model)),
	}, nil
}

func validateLaunchProvider(raw string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(raw))
	if p == "" {
		return "", nil // empty → downstream defaults to codex
	}
	switch p {
	case "codex", "claude":
		return p, nil
	default:
		return "", fmt.Errorf("invalid provider %q: must be codex or claude", raw)
	}
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

func launchEnv(provider, model string) []string {
	var env []string
	if provider = strings.TrimSpace(provider); provider != "" {
		env = append(env, "AGENT_PROVIDER="+provider)
	}
	if model = strings.TrimSpace(model); model != "" {
		// remoteLauncher.Launch reads this back via envValue(req.Env, "AGENT_MODEL")
		// and forwards it as the `model` field on thread/start.
		env = append(env, "AGENT_MODEL="+model)
	}
	return env
}

func submissionFromMessage(
	ctx context.Context,
	svc contract.OrchestrationService,
	in SendMessageInput,
) (contract.TurnSubmission, error) {
	agentID, err := requireTrimmed(in.AgentID, "agent_id")
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
