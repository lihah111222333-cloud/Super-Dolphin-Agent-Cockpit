package toolbridge

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

const toolCallTimeout = 120 * time.Second

var (
	ErrNoPeerAvailable = errors.New("toolbridge: no active peer")
	ErrAmbiguousPeer   = errors.New("toolbridge: multiple active peers")
)

type ToolCallRequest struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	AgentID   string          `json:"agentId,omitempty"`
	ThreadID  string          `json:"threadId,omitempty"`
	CallID    string          `json:"callId,omitempty"`
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
	Tools []common.MCPTool `json:"tools"`
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
