package toolbridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

const toolCallTimeout = 120 * time.Second

// P22 P4 S3a: ErrThreadRuntimeRequired and ErrPersistentSubagentRuntime
// Required moved to internal/contract (see
// contract/toolbridge_runtime_required.go) so the fail-closed sentinels
// are available to any consumer that needs to errors.Is-check them
// without importing this platform package. Peer-availability errors stay
// here because they are strictly owner-internal to toolbridge.
var (
	ErrNoPeerAvailable = errors.New("toolbridge: no active peer")
	ErrAmbiguousPeer   = errors.New("toolbridge: multiple active peers")
)

type ToolCallRequest struct {
	Name       string          `json:"name"`
	Arguments  json.RawMessage `json:"arguments"`
	AgentID    string          `json:"agentId,omitempty"`
	ThreadID   string          `json:"threadId,omitempty"`
	TurnID     string          `json:"turnId,omitempty"`
	CallID     string          `json:"callId,omitempty"`
	CWD        string          `json:"_cwd,omitempty"`
	ClientKind string          `json:"clientKind,omitempty"`
}

type ToolCallContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type ToolCallResult struct {
	ContentItems []ToolCallContentItem `json:"contentItems,omitempty"`
	Success      bool                  `json:"success"`
}

type peerToolsListResult struct {
	Tools []dto.MCPTool `json:"tools"`
}

type peerToolCallResponse struct {
	Content []peerToolCallContent `json:"content"`
}

type peerToolCallContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func classifyTool(name string) string {
	trimmed := strings.TrimSpace(name)
	switch {
	case strings.HasPrefix(trimmed, "lsp_"):
		return dto.ClientKindLSP
	case trimmed == "code_run", trimmed == "code_run_test":
		return dto.ClientKindLSP
	default:
		return dto.ClientKindOrch
	}
}

func resolveToolClientKind(req ToolCallRequest) (string, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return "", fmt.Errorf("toolbridge: missing tool name")
	}
	classified := classifyTool(name)
	requested := strings.TrimSpace(req.ClientKind)
	if requested == "" {
		return classified, nil
	}
	switch requested {
	case dto.ClientKindLSP, dto.ClientKindOrch, dto.ClientKindIDA:
	default:
		return "", fmt.Errorf("toolbridge: unsupported tool family %q", requested)
	}
	if requested != classified {
		return "", fmt.Errorf("toolbridge: tool %q belongs to %q, not %q", name, classified, requested)
	}
	return requested, nil
}
