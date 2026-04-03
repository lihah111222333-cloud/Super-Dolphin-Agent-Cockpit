package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

const standaloneMCPOrchLaunchError = "orchestration_launch_agent is not supported in standalone mcp-orch mode; use the main agent-terminal binary to launch agents"

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
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		if svc == nil {
			return nil, errors.New("orchestration service is not configured")
		}
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
		if svc == nil {
			return nil, errors.New("orchestration service is not configured")
		}
		var in SendMessageInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		submission, err := submissionFromMessage(ctx, svc, in)
		if err != nil {
			return nil, err
		}
		if err := svc.SubmitTurn(ctx, submission); err != nil {
			return nil, err
		}
		return successResult(map[string]any{"agent_id": submission.AgentID}), nil
	}
}

func HandleStopAgent(svc contract.OrchestrationService) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		if svc == nil {
			return nil, errors.New("orchestration service is not configured")
		}
		var in AgentIDInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		agentID, err := requireTrimmed(in.AgentID, "agent_id")
		if err != nil {
			return nil, err
		}
		if err := svc.StopAgent(ctx, agentID); err != nil {
			return nil, err
		}
		return successResult(map[string]any{"agent_id": agentID}), nil
	}
}

func HandleListAgents(svc contract.OrchestrationService) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		if svc == nil {
			return nil, errors.New("orchestration service is not configured")
		}
		var in struct{}
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return svc.ListAgents(ctx)
	}
}

func HandleGetAgentReport(svc contract.OrchestrationService) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		if svc == nil {
			return nil, errors.New("orchestration service is not configured")
		}
		var in AgentIDInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		agentID, err := requireTrimmed(in.AgentID, "agent_id")
		if err != nil {
			return nil, err
		}
		return svc.GetReport(ctx, agentID)
	}
}

func orchestrationToolDefinitions(svc contract.OrchestrationService) []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "orchestration_launch_agent",
			Description: "Launch a managed orchestration agent.",
			InputSchema: ObjectSchema(map[string]Schema{
				"name":     StringSchema("Agent name. Used as the orchestration agent ID; no separate agent_id field is required."),
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
	exe, err := osExecutable()
	if err != nil {
		return contract.LaunchRequest{}, err
	}
	return launchRequestFromExecutable(in, exe)
}

func launchRequestFromExecutable(in LaunchAgentInput, exe string) (contract.LaunchRequest, error) {
	if isStandaloneMCPOrchExecutable(exe) {
		return contract.LaunchRequest{}, errors.New(standaloneMCPOrchLaunchError)
	}
	name, err := requireTrimmed(in.Name, "name")
	if err != nil {
		return contract.LaunchRequest{}, err
	}
	// The MCP launch tool does not expose agent_id/command, so mirror the thread
	// module's launch path by using the current binary and the provided name as ID.
	return contract.LaunchRequest{
		AgentID: name,
		Name:    name,
		Prompt:  strings.TrimSpace(in.Prompt),
		Cwd:     strings.TrimSpace(in.CWD),
		Command: []string{strings.TrimSpace(exe)},
		Env:     launchEnv(in.Provider),
	}, nil
}

func isStandaloneMCPOrchExecutable(exe string) bool {
	trimmed := strings.TrimSpace(exe)
	normalized := strings.ReplaceAll(trimmed, "\\", "/")
	switch strings.ToLower(filepath.Base(normalized)) {
	case "mcp-orch", "mcp-orch.exe":
		return true
	default:
		return false
	}
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
