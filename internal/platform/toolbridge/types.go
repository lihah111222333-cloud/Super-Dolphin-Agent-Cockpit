package toolbridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

const toolCallTimeout = 120 * time.Second

// peer 可用性错误只在 toolbridge 内部流转。
// 需要跨模块 errors.Is 判断的 runtime sentinel 放在 internal/contract；
// 这里保留 owner-internal 错误，避免其它包依赖平台实现细节。
var (
	ErrNoPeerAvailable = errors.New("toolbridge: no active peer")
	ErrAmbiguousPeer   = errors.New("toolbridge: multiple active peers")
)

// ToolCallRequest 是 toolbridge 内部路由使用的规范化工具调用请求。
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

// normalizeToolCallRequest 清理工具调用请求，并补齐 workspace roots。
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

// normalizeToolCallWorkspaceRoots 规范化工具调用的工作区根目录列表。
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
	add("", primary)
	for _, root := range roots {
		add(primary, root)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeToolCallWorkspaceRoot 将相对 root 绑定到 cwd，并拒绝无法解析为绝对路径的值。
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

// firstStringSlice 从多个 JSON 字段名中读取第一个非空字符串数组。
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

// ToolCallContentItem 是 toolbridge 内部的 MCP content block。
// 每个 variant 只使用其协议规定字段；mapper 会在跨边界时严格校验并深拷贝 JSON 字段。
type ToolCallContentItem struct {
	Type        string          `json:"type"`
	Text        string          `json:"text,omitempty"`
	Data        string          `json:"data,omitempty"`
	MIMEType    string          `json:"mimeType,omitempty"`
	Resource    json.RawMessage `json:"resource,omitempty"`
	URI         string          `json:"uri,omitempty"`
	Name        string          `json:"name,omitempty"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	Size        *int64          `json:"size,omitempty"`
	Icons       json.RawMessage `json:"icons,omitempty"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
	Meta        json.RawMessage `json:"_meta,omitempty"`
}

// ToolCallResult 是 toolbridge 内部统一工具结果结构。
type ToolCallResult struct {
	ContentItems      []ToolCallContentItem `json:"contentItems,omitempty"`
	StructuredContent json.RawMessage       `json:"structuredContent,omitempty"`
	Success           bool                  `json:"success"`
}

// peerToolsListResult 是 peer tools/list 返回的工具列表外壳。
type peerToolsListResult struct {
	Tools        []dto.MCPTool `json:"tools"`
	toolsPresent bool
}

// UnmarshalJSON 严格解析 peer tools/list 响应。
// tools 字段缺失或不是数组时返回错误，避免把坏 peer 静默当作空工具集。
func (r *peerToolsListResult) UnmarshalJSON(raw []byte) error {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	toolsRaw, ok := payload["tools"]
	if !ok || bytes.Equal(bytes.TrimSpace(toolsRaw), []byte("null")) {
		return fmt.Errorf("tools array is required")
	}
	var rawTools []json.RawMessage
	if err := json.Unmarshal(toolsRaw, &rawTools); err != nil {
		return fmt.Errorf("tools array is required: %w", err)
	}
	r.Tools = make([]dto.MCPTool, 0, len(rawTools))
	for _, rawTool := range rawTools {
		r.Tools = append(r.Tools, dto.NewRawTool(rawTool))
	}
	r.toolsPresent = true
	return nil
}

// decodePeerToolsListResult 严格解码 MCP tools/list，避免 peer 吞字段后暴露空或畸形工具面。
func decodePeerToolsListResult(raw json.RawMessage, source string) ([]dto.MCPTool, error) {
	var result peerToolsListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	if err := validatePeerToolsListResult(result, source); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func validatePeerToolsListResult(result peerToolsListResult, source string) error {
	if !result.toolsPresent {
		return fmt.Errorf("%s: tools array is required", source)
	}
	return nil
}

// peerToolCallResponse 是 peer tools/call 返回的 MCP 结果外壳。
type peerToolCallResponse struct {
	Content           []peerToolCallContent `json:"content"`
	IsError           bool                  `json:"isError,omitempty"`
	StructuredContent json.RawMessage       `json:"structuredContent,omitempty"`
}

// peerToolCallContent 与内部 content block 共用同一字段定义，避免 wire mapper 字段漂移。
type peerToolCallContent = ToolCallContentItem

// canonicalToolName 规范化当前 LSP 工具名。
func canonicalToolName(name string) string {
	return strings.TrimSpace(name)
}

var currentLSPToolNames = map[string]struct{}{
	"file":       {},
	"grep":       {},
	"inspect":    {},
	"xref":       {},
	"structure":  {},
	"patch_edit": {},
	"completion": {},
}

func isCurrentLSPToolName(name string) bool {
	_, ok := currentLSPToolNames[strings.TrimSpace(name)]
	return ok
}

// classifyTool 根据工具名推断所属 MCP client family。
func classifyTool(name string) string {
	trimmed := strings.TrimSpace(name)
	if namespace, ok := SplitMCPToolName(trimmed); ok {
		return strings.TrimSpace(namespace.Server)
	}
	if isCurrentLSPToolName(canonicalToolName(trimmed)) {
		return dto.ClientKindLSP
	}
	if strings.HasPrefix(trimmed, "ida_") {
		return dto.ClientKindIDA
	}
	return dto.ClientKindOrch
}

// resolveToolClientKind 校验请求指定的 clientKind 与工具名分类一致。
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
	if requested != dto.ClientKindLSP && requested != dto.ClientKindOrch && requested != dto.ClientKindIDA && requested != classified {
		return "", fmt.Errorf("toolbridge: unsupported tool family %q", requested)
	}
	if requested != classified {
		return "", fmt.Errorf("toolbridge: tool %q belongs to %q, not %q", name, classified, requested)
	}
	return requested, nil
}
