package toolbridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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
	Name           string          `json:"name"`
	Arguments      json.RawMessage `json:"arguments"`
	AgentID        string          `json:"agentId,omitempty"`
	ThreadID       string          `json:"threadId,omitempty"`
	TurnID         string          `json:"turnId,omitempty"`
	CallID         string          `json:"callId,omitempty"`
	CWD            string          `json:"_cwd,omitempty"`
	WorkspaceRoots []string        `json:"_workspaceRoots,omitempty"`
	ClientKind     string          `json:"clientKind,omitempty"`
	Scoped         bool            `json:"-"`
}

func normalizeToolCallRequest(req ToolCallRequest) ToolCallRequest {
	req.Name = strings.TrimSpace(req.Name)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	req.TurnID = strings.TrimSpace(req.TurnID)
	req.CallID = strings.TrimSpace(req.CallID)
	req.CWD = normalizeToolCallCWD(req.CWD)
	req.WorkspaceRoots = normalizeToolCallWorkspaceRoots(req.CWD, req.WorkspaceRoots)
	req.ClientKind = strings.TrimSpace(req.ClientKind)
	return req
}

// normalizeToolCallWorkspaceRoots 规范化工具call工作区根目录。
func normalizeToolCallWorkspaceRoots(cwd string, roots []string) []string {
	out := make([]string, 0, len(roots)+1)
	seen := map[string]struct{}{}
	add := func(base, root string) {
		root = normalizeToolCallWorkspaceRoot(base, root)
		if root == "" {
			return
		}
		if _, ok := seen[root]; ok {
			return
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	primary := normalizeToolCallWorkspaceRoot("", cwd)
	if primary == "" {
		return nil
	}
	add("", primary)
	for _, root := range roots {
		add(primary, root)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeToolCallWorkspaceRoot(base, root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if strings.TrimSpace(base) != "" && !filepath.IsAbs(root) {
		root = filepath.Join(base, root)
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	return ""
}

// firstStringSlice 处理firststringslice。
func firstStringSlice(payload map[string]json.RawMessage, keys ...string) []string {
	for _, key := range keys {
		raw := bytes.TrimSpace(payload[key])
		if len(raw) == 0 {
			continue
		}
		var values []string
		if err := json.Unmarshal(raw, &values); err != nil {
			continue
		}
		out := make([]string, 0, len(values))
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				out = append(out, value)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

type ToolCallContentItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type ToolCallResult struct {
	ContentItems      []ToolCallContentItem `json:"contentItems,omitempty"`
	StructuredContent json.RawMessage       `json:"structuredContent,omitempty"`
	Success           bool                  `json:"success"`
}

type peerToolsListResult struct {
	Tools []dto.MCPTool `json:"tools"`
}

type peerToolCallResponse struct {
	Content           []peerToolCallContent `json:"content"`
	IsError           bool                  `json:"isError,omitempty"`
	StructuredContent json.RawMessage       `json:"structuredContent,omitempty"`
}

type peerToolCallContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

var legacyLSPToolAliases = map[string]string{
	"lsp_file":           "file",
	"lsp_grep":           "grep",
	"lsp_inspect":        "inspect",
	"lsp_xref":           "xref",
	"lsp_structure":      "structure",
	"lsp_edit":           "edit",
	"lsp_format_preview": "format_preview",
	"lsp_completion":     "completion",
}

func canonicalToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	if alias, ok := legacyLSPToolAliases[trimmed]; ok {
		return alias
	}
	return trimmed
}

func classifyTool(name string) string {
	trimmed := strings.TrimSpace(name)
	switch canonicalToolName(trimmed) {
	case "file", "grep", "inspect", "xref", "structure", "edit", "format_preview", "completion":
		return dto.ClientKindLSP
	default:
		if strings.HasPrefix(trimmed, "lsp_") {
			return dto.ClientKindLSP
		}
		if strings.HasPrefix(trimmed, "ida_") {
			return dto.ClientKindIDA
		}
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
