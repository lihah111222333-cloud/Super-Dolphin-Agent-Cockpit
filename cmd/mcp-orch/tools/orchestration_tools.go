package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

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
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in LaunchAgentInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		req, err := launchRequestFromInput(in)
		if err != nil {
			return nil, err
		}
		if err := svc.LaunchAgent(ctx, req); err != nil {
			return nil, err
		}
		return successResult(map[string]any{"agent_id": req.AgentID}), nil
	}
}

func HandleSendMessage(svc contract.OrchestrationService) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in SendMessageInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		if err := svc.SubmitTurn(ctx, submissionFromMessage(ctx, svc, in)); err != nil {
			return nil, err
		}
		return successResult(map[string]any{"agent_id": strings.TrimSpace(in.AgentID)}), nil
	}
}

func HandleStopAgent(svc contract.OrchestrationService) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in AgentIDInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		if err := svc.StopAgent(ctx, in.AgentID); err != nil {
			return nil, err
		}
		return successResult(map[string]any{"agent_id": strings.TrimSpace(in.AgentID)}), nil
	}
}

func HandleListAgents(svc contract.OrchestrationService) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in struct{}
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return svc.ListAgents(ctx)
	}
}

func HandleGetAgentReport(svc contract.OrchestrationService) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in AgentIDInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return svc.GetReport(ctx, in.AgentID)
	}
}

func orchestrationToolDefinitions(svc contract.OrchestrationService) []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "orchestration_launch_agent",
			Description: "Launch a managed orchestration agent.",
			InputSchema: ObjectSchema(map[string]Schema{
				"name":     StringSchema("Agent name. Used as the orchestration agent ID."),
				"prompt":   StringSchema("Optional initial prompt to persist on the launch request."),
				"cwd":      StringSchema("Optional working directory for the launched agent."),
				"provider": StringSchema("Optional provider name. Passed via AGENT_PROVIDER."),
			}, "name"),
			Handler: HandleLaunchAgent(svc),
		},
		{
			Name:        "orchestration_send_message",
			Description: "Submit a text turn to an existing orchestration agent.",
			InputSchema: ObjectSchema(map[string]Schema{
				"agent_id": StringSchema("Target orchestration agent ID."),
				"message":  StringSchema("Message content to submit as a text input."),
			}, "agent_id", "message"),
			Handler: HandleSendMessage(svc),
		},
		{
			Name:        "orchestration_stop_agent",
			Description: "Stop a running orchestration agent.",
			InputSchema: ObjectSchema(map[string]Schema{
				"agent_id": StringSchema("Target orchestration agent ID."),
			}, "agent_id"),
			Handler: HandleStopAgent(svc),
		},
		{
			Name:        "orchestration_list_agents",
			Description: "List orchestration agents and their current runtime snapshots.",
			InputSchema: ObjectSchema(nil),
			Handler:     HandleListAgents(svc),
		},
		{
			Name:        "orchestration_get_agent_report",
			Description: "Read the last known report for an orchestration agent.",
			InputSchema: ObjectSchema(map[string]Schema{
				"agent_id": StringSchema("Target orchestration agent ID."),
			}, "agent_id"),
			Handler: HandleGetAgentReport(svc),
		},
	}
}

func launchRequestFromInput(in LaunchAgentInput) (contract.LaunchRequest, error) {
	exe, err := os.Executable()
	if err != nil {
		return contract.LaunchRequest{}, err
	}
	name := strings.TrimSpace(in.Name)
	// The MCP launch tool does not expose agent_id/command, so mirror the thread
	// module's launch path by using the current binary and the provided name as ID.
	return contract.LaunchRequest{
		AgentID: name,
		Name:    name,
		Prompt:  strings.TrimSpace(in.Prompt),
		Cwd:     strings.TrimSpace(in.CWD),
		Command: []string{exe},
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
) contract.TurnSubmission {
	agentID := strings.TrimSpace(in.AgentID)
	return contract.TurnSubmission{
		AgentID:  agentID,
		ThreadID: submissionThreadID(ctx, svc, agentID),
		Inputs: []shareddto.InputItem{{
			Type:    "text",
			Content: strings.TrimSpace(in.Message),
		}},
	}
}

func submissionThreadID(ctx context.Context, svc contract.OrchestrationService, agentID string) string {
	snapshot, err := svc.Snapshot(ctx, agentID)
	if err == nil && strings.TrimSpace(snapshot.ThreadID) != "" {
		return strings.TrimSpace(snapshot.ThreadID)
	}
	return agentID
}
