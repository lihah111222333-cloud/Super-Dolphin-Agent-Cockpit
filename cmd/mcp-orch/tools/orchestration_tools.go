package tools

import (
	"context"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

var osExecutable = os.Executable

type LaunchAgentInput struct {
	Name     string `json:"name"`
	Prompt   string `json:"prompt,omitempty"`
	CWD      string `json:"cwd,omitempty"`
	Provider string `json:"provider,omitempty"`
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
		if err := svc.LaunchAgent(ctx, req); err != nil {
			return nil, err
		}
		return successResult(map[string]any{"agent_id": req.AgentID}), nil
	})
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
			"name":     StringSchema("Agent name. Used as the orchestration agent ID; no separate agent_id field is required."),
			"prompt":   StringSchema("Optional initial prompt to persist on the launch request."),
			"cwd":      StringSchema("Optional working directory for the launched agent."),
			"provider": StringSchema("Optional provider name. Passed via AGENT_PROVIDER."),
		}, "name"), HandleLaunchAgent(svc)),
		defineTool("orchestration_send_message", "Submit a text turn to an existing orchestration agent.", ObjectSchema(map[string]Schema{
			"agent_id": StringSchema("Target orchestration agent ID."),
			"message":  StringSchema("Message content to submit as a text input."),
		}, "agent_id", "message"), HandleSendMessage(svc)),
		defineTool("orchestration_stop_agent", "Stop a running orchestration agent.", ObjectSchema(map[string]Schema{
			"agent_id": StringSchema("Target orchestration agent ID."),
		}, "agent_id"), HandleStopAgent(svc)),
		defineTool("orchestration_list_agents", "List orchestration agents and their current runtime snapshots.", ObjectSchema(nil), HandleListAgents(svc)),
		defineTool("orchestration_get_agent_report", "Read the last known report for an orchestration agent.", ObjectSchema(map[string]Schema{
			"agent_id": StringSchema("Target orchestration agent ID."),
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
	return contract.LaunchRequest{
		AgentID: name,
		Name:    name,
		Prompt:  strings.TrimSpace(in.Prompt),
		Cwd:     strings.TrimSpace(in.CWD),
		Command: []string{strings.TrimSpace(exe)},
		Env:     launchEnv(in.Provider),
	}, nil
}


func launchEnv(provider string) []string {
	if provider = strings.TrimSpace(provider); provider != "" {
		return []string{"AGENT_PROVIDER=" + provider}
	}
	return nil
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
