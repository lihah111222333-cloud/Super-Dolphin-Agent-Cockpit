package codexapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/resultguard"
	"github.com/anthropic-ai/super-agent-v3/internal/util"
	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
)

const (
	toolMetadataKeyAgentID        = "_agentId"
	toolMetadataKeyThreadID       = "_threadId"
	toolMetadataKeyCallID         = "_callId"
	toolMetadataKeyCWD            = "_cwd"
	toolMetadataKeyWorkspaceRoots = "_workspaceRoots"
)

// enrichToolCallParams 把 session 元数据注入到 codex item/tool/call 的 msg.Params 中。
//
// Codex app-server 的协议消息只含 name + arguments，不含 agentId（codex 不知道
// "agent" 概念）。toolbridge.decodeToolCallRequest 期望从 params 取 agentId 来解析 cwd
// （host-direct skill 工具分支强依赖此字段）。LSP peer 还需要 per-call _cwd，
// 否则共享 mcp-lsp 进程会退回自己的启动目录。本函数填补这两个缺口。
//
// 行为：
//   - agentID 非空且 msg.Params 是 JSON object → 覆盖写入 "agentId": "<agentID>"，
//     并移除 alias "agent_id"，避免信任 Codex payload / fixture 里的外部 agentId。
//   - cwd 非空且 msg.Params 是 JSON object → 覆盖写入 "_cwd": "<cwd>"，并移除
//     legacy/public alias "cwd"，避免模型 payload 覆盖宿主绑定的工作目录。
//   - msg.Params 为空 / 反序列化失败 / agentID 与 cwd 都为空 → 原样返回，不报错。
//
// 错误路径全部 fail-soft（返回原 msg），让 toolbridge 用其它字段（threadID / 工具名）
// 继续诊断或失败；本函数不应成为新的崩溃源。
func enrichToolCallParams(msg RawMessage, agentID, cwd string) RawMessage {
	agentID = strings.TrimSpace(agentID)
	cwd = strings.TrimSpace(cwd)
	if skipToolCallEnrichment(agentID, cwd, msg.Params) {
		return msg
	}
	payload, ok := decodeToolCallParamObject(msg.Params)
	if !ok || !injectToolCallMetadata(payload, agentID, cwd) {
		skillmetrics.IncEnrichFailure()
		return msg
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		skillmetrics.IncEnrichFailure()
		return msg
	}
	enriched := msg
	enriched.Params = raw
	return enriched
}

func skipToolCallEnrichment(agentID, cwd string, params json.RawMessage) bool {
	return (agentID == "" && cwd == "") || len(params) == 0
}

func decodeToolCallParamObject(params json.RawMessage) (map[string]json.RawMessage, bool) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(params, &payload); err != nil {
		return nil, false
	}
	if payload == nil {
		payload = map[string]json.RawMessage{}
	}
	return payload, true
}

func injectToolCallMetadata(payload map[string]json.RawMessage, agentID, cwd string) bool {
	if agentID != "" {
		if !setToolCallStringParam(payload, "agentId", agentID) {
			return false
		}
		delete(payload, "agent_id")
	}
	if cwd != "" {
		if !setToolCallStringParam(payload, "_cwd", cwd) {
			return false
		}
		delete(payload, "cwd")
	}
	return true
}

func setToolCallStringParam(payload map[string]json.RawMessage, key, value string) bool {
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	payload[key] = encoded
	return true
}

func toolCallParamString(params json.RawMessage, key string) string {
	if len(params) == 0 || strings.TrimSpace(key) == "" {
		return ""
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(params, &payload); err != nil {
		return ""
	}
	var value string
	if err := json.Unmarshal(payload[key], &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func shouldWarnToolCWDTrace(toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	return strings.HasPrefix(toolName, "lsp_") ||
		strings.HasPrefix(toolName, "code_") ||
		toolName == "orchestration_launch_agent"
}

type preparedToolCall struct {
	header  shareddto.ToolCallHeader
	params  RawMessage
	started time.Time
}

// prepareToolCall 准备工具call。
func (s *session) prepareToolCall(msg RawMessage) (preparedToolCall, error) {
	started := time.Now()
	callID := util.FirstNonEmpty(jsonRPCIDString(msg.ID), toolCallParamStringAny(msg.Params, "callId", "call_id"), nestedToolCallString(msg.Params, "item", "callId", "call_id"))
	toolName := util.FirstNonEmpty(toolCallParamStringAny(msg.Params, "name", "toolName", "tool_name", "tool"), nestedToolCallString(msg.Params, "item", "name", "toolName", "tool"))
	if callID == "" {
		return preparedToolCall{}, fmt.Errorf("codexapp: tool call id is required")
	}
	if toolName == "" {
		return preparedToolCall{}, fmt.Errorf("codexapp: tool name is required")
	}
	agentID := strings.TrimSpace(s.agentID)
	providerThreadID := strings.TrimSpace(s.ThreadID())
	if agentID == "" || providerThreadID == "" {
		return preparedToolCall{}, fmt.Errorf("codexapp: tool call session scope is incomplete")
	}
	cwd := strings.TrimSpace(s.runtimeConfigString("cwd"))
	if cwd == "" {
		return preparedToolCall{}, fmt.Errorf("codexapp: tool call cwd is required")
	}
	workspaceRoots := trustedWorkspaceRoots(cwd, s.runtimeConfigStringSlice("additionalWorkingDirectories", "additional_working_directories"))
	turnID := util.FirstNonEmpty(toolCallParamStringAny(msg.Params, "turnId", "turn_id"), nestedToolCallString(msg.Params, "item", "turnId", "turn_id"), s.activeTurnSnapshot())
	header := toolCallHeader(agentID, turnID, callID, toolName, started)
	enriched, err := enrichToolCallParamsStrict(msg, agentID, providerThreadID, callID, cwd, workspaceRoots)
	if err != nil {
		return preparedToolCall{}, err
	}
	return preparedToolCall{header: header, params: enriched, started: started}, nil
}

func toolCallHeader(agentID, turnID, callID, toolName string, ts time.Time) shareddto.ToolCallHeader {
	return shareddto.ToolCallHeader{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{
				ThreadHeader: shareddto.ThreadHeader{
					EventHeader: shareddto.EventHeader{Timestamp: ts},
					ThreadID:    agentID,
				},
				AgentID: agentID,
			},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: turnID},
		},
		CallID:   callID,
		ToolName: toolName,
	}
}

func (s *session) publishToolCallBegin(call preparedToolCall) {
	if s == nil || s.dispatcher == nil {
		return
	}
	s.dispatcher.Publish(tooldto.ToolCallBegin{
		ToolCallHeader:   call.header,
		ArgumentsPreview: jsonPreviewFromRaw(call.params.Params, "arguments", "args"),
	})
}

func (s *session) publishToolCallEnd(call preparedToolCall, result any, callErr error) {
	if s == nil || s.dispatcher == nil {
		return
	}
	header := call.header
	header.Timestamp = time.Now()
	success, errorText := toolCallEndOutcome(result, callErr)
	success, errorText, resultPreview := resultguard.ApplyEmptyFileReadFromRaw(success, errorText, previewAny(result), call.header.ToolName, call.params.Params, result)
	ev := tooldto.ToolCallEnd{
		ToolCallHeader: header,
		Success:        success,
		Result:         resultPreview,
		ElapsedMS:      time.Since(call.started).Milliseconds(),
	}
	if !success {
		ev.Error = errorText
	}
	s.dispatcher.Publish(ev)
}

type toolCallResultEnvelope struct {
	Success      *bool  `json:"success"`
	IsError      *bool  `json:"isError"`
	Error        string `json:"error"`
	Message      string `json:"message"`
	Reason       string `json:"reason"`
	ContentItems []struct {
		Text string `json:"text"`
	} `json:"contentItems"`
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func toolCallEndOutcome(result any, callErr error) (bool, string) {
	if callErr != nil {
		return false, callErr.Error()
	}
	envelope, ok, err := decodeToolCallResultEnvelope(result)
	if err != nil {
		return false, err.Error()
	}
	if !ok || !envelope.explicitFailure() {
		return true, ""
	}
	return false, envelope.failureText(result)
}

// decodeToolCallResultEnvelope 解码工具call结果包装。
func decodeToolCallResultEnvelope(result any) (toolCallResultEnvelope, bool, error) {
	if result == nil {
		return toolCallResultEnvelope{}, false, nil
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return toolCallResultEnvelope{}, false, fmt.Errorf("decode tool result envelope: marshal: %w", err)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return toolCallResultEnvelope{}, false, nil
	}
	var envelope toolCallResultEnvelope
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return toolCallResultEnvelope{}, false, fmt.Errorf("decode tool result envelope: unmarshal: %w", err)
	}
	return envelope, true, nil
}

func (e toolCallResultEnvelope) explicitFailure() bool {
	return (e.Success != nil && !*e.Success) || (e.IsError != nil && *e.IsError)
}

// failureText 提取失败说明文本。
func (e toolCallResultEnvelope) failureText(result any) string {
	if text := util.FirstNonEmpty(e.Error, e.Message, e.Reason); text != "" {
		return text
	}
	for _, item := range e.ContentItems {
		if text := strings.TrimSpace(item.Text); text != "" {
			return text
		}
	}
	for _, item := range e.Content {
		if text := strings.TrimSpace(item.Text); text != "" {
			return text
		}
	}
	return previewAny(result)
}

func (s *session) activeTurnSnapshot() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(s.activeTurnID)
}

// enrichToolCallParamsStrict 补充工具callparamsstrict。
func enrichToolCallParamsStrict(msg RawMessage, agentID, threadID, callID, cwd string, workspaceRoots []string) (RawMessage, error) {
	var payload map[string]json.RawMessage
	if len(bytes.TrimSpace(msg.Params)) == 0 {
		return RawMessage{}, fmt.Errorf("codexapp: missing tool call params")
	}
	if err := json.Unmarshal(msg.Params, &payload); err != nil {
		return RawMessage{}, fmt.Errorf("codexapp: decode tool call params: %w", err)
	}
	if payload == nil {
		return RawMessage{}, fmt.Errorf("codexapp: tool call params must be an object")
	}
	for _, key := range []string{"agentId", "agent_id", "threadId", "thread_id", "callId", "call_id", "cwd", "workspaceRoots", "workspace_roots", "_workspaceRoots", "_workspace_roots"} {
		delete(payload, key)
	}
	workspaceRoots = trustedWorkspaceRoots(cwd, workspaceRoots)
	for key, value := range map[string]string{
		toolMetadataKeyAgentID:  agentID,
		toolMetadataKeyThreadID: threadID,
		toolMetadataKeyCallID:   callID,
		toolMetadataKeyCWD:      cwd,
	} {
		raw, err := json.Marshal(strings.TrimSpace(value))
		if err != nil {
			return RawMessage{}, err
		}
		payload[key] = raw
	}
	rawRoots, err := json.Marshal(workspaceRoots)
	if err != nil {
		return RawMessage{}, err
	}
	payload[toolMetadataKeyWorkspaceRoots] = rawRoots
	raw, err := json.Marshal(payload)
	if err != nil {
		return RawMessage{}, fmt.Errorf("codexapp: encode tool call params: %w", err)
	}
	enriched := msg
	enriched.Params = raw
	return enriched, nil
}

func trustedWorkspaceRoots(cwd string, additional []string) []string {
	roots := make([]string, 0, len(additional)+1)
	seen := map[string]struct{}{}
	primary := normalizeTrustedWorkspaceRoot("", cwd)
	if primary == "" {
		return nil
	}
	add := func(root string) {
		root = normalizeTrustedWorkspaceRoot(primary, root)
		if root == "" {
			return
		}
		if _, ok := seen[root]; ok {
			return
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	add(primary)
	for _, root := range additional {
		add(root)
	}
	return roots
}

// normalizeTrustedWorkspaceRoot 规范化trusted工作区根目录。
func normalizeTrustedWorkspaceRoot(base, root string) string {
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
	if strings.TrimSpace(base) == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return ""
}

// toolCallParamStringAny 处理工具callparamstring任意值。
func toolCallParamStringAny(params json.RawMessage, keys ...string) string {
	var payload map[string]json.RawMessage
	if len(params) == 0 || json.Unmarshal(params, &payload) != nil {
		return ""
	}
	for _, key := range keys {
		var value string
		if json.Unmarshal(payload[key], &value) == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// nestedToolCallString 处理nested工具callstring。
func nestedToolCallString(params json.RawMessage, objectKey string, keys ...string) string {
	var payload map[string]json.RawMessage
	if len(params) == 0 || json.Unmarshal(params, &payload) != nil {
		return ""
	}
	var nested map[string]json.RawMessage
	if json.Unmarshal(payload[objectKey], &nested) != nil {
		return ""
	}
	for _, key := range keys {
		var value string
		if json.Unmarshal(nested[key], &value) == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func jsonRPCIDString(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return strings.TrimSpace(string(raw))
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func jsonPreviewFromRaw(raw json.RawMessage, keys ...string) string {
	var payload map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	for _, key := range keys {
		if value := bytes.TrimSpace(payload[key]); len(value) > 0 {
			return string(value)
		}
	}
	return ""
}

func previewAny(value any) string {
	if value == nil {
		return ""
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(raw)
}
