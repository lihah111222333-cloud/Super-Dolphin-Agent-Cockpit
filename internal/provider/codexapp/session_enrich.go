package codexapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/resultguard"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
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

// enrichToolCallParams 保留旧版 fail-soft 注入路径供守卫和兼容测试覆盖。
func enrichToolCallParams(msg RawMessage, agentID, cwd string) RawMessage {
	agentID, cwd = strings.TrimSpace(agentID), strings.TrimSpace(cwd)
	if (agentID == "" && cwd == "") || len(msg.Params) == 0 {
		return msg
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(msg.Params, &payload); err != nil || payload == nil || !injectToolCallMetadata(payload, agentID, cwd) {
		skillmetrics.IncEnrichFailure()
		return msg
	}
	if raw, err := json.Marshal(payload); err == nil {
		msg.Params = raw
		return msg
	}
	skillmetrics.IncEnrichFailure()
	return msg
}

func injectToolCallMetadata(payload map[string]json.RawMessage, agentID, cwd string) bool {
	if agentID != "" {
		payload["agentId"] = mustJSON(agentID)
		delete(payload, "agent_id")
	}
	if cwd != "" {
		payload["_cwd"] = mustJSON(cwd)
		delete(payload, "cwd")
	}
	return true
}

func shouldWarnToolCWDTrace(toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	switch toolName {
	case "file", "grep", "inspect", "xref", "structure", "patch_edit", "completion":
		return true
	}
	return strings.HasPrefix(toolName, "code_") ||
		contract.IsOrchestrationLaunchTool(toolName)
}

type preparedToolCall struct {
	header  shareddto.ToolCallHeader
	params  RawMessage
	started time.Time
}

// prepareToolCall 校验 Codex 工具调用并补齐宿主侧追踪头。
// 缺 callID、toolName、agent/thread scope 或 cwd 时会 fail-fast，避免 toolbridge 在未知上下文执行。
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
		ArgumentsPreview: providershared.SafeToolArgumentsPreviewString(jsonPreviewFromRaw(call.params.Params, "arguments", "args")),
	})
}

// publishToolCallEnd 捕获工具结果并只向事件总线发布安全错误与有界预览。
func (s *session) publishToolCallEnd(call preparedToolCall, result any, callErr error) {
	if s == nil || s.dispatcher == nil {
		return
	}
	header := call.header
	header.Timestamp = time.Now()
	success, errorText := toolCallEndOutcome(result, callErr)
	success, errorText, resultPreview := resultguard.ApplyEmptyFileReadFromRaw(success, errorText, previewAny(result), call.header.ToolName, call.params.Params, result)
	record, captureErr := captureSessionToolResult(s.runtimeHooks, header, resultPreview)
	if captureErr != nil {
		success = false
		errorText = appendProviderRuntimeError(errorText, captureErr)
	}
	if !success && strings.TrimSpace(errorText) == "" {
		errorText = "tool execution failed"
	}
	raw := dto.RawProviderEvent{EventType: "tool.call.end", Data: map[string]any{
		"params":        call.params.Params,
		"result":        result,
		"error":         errorText,
		"persist_error": record.PersistError,
	}}
	ev := tooldto.ToolCallEnd{
		ToolCallHeader: header,
		Success:        success,
		Result:         record.Preview,
		PersistedPath:  record.PersistedPath,
		PersistFailed:  record.PersistFailed,
		PersistError:   publicToolError(raw, record.PersistError),
		Truncated:      record.Truncated,
		OriginalSize:   record.OriginalSize,
		ElapsedMS:      time.Since(call.started).Milliseconds(),
	}
	if !success {
		ev.Error = publicToolError(raw, errorText)
	}
	s.dispatcher.Publish(ev)
}

// captureSessionToolResult 通过 provider/shared 运行时依赖捕获 host-direct 工具结果。
// 未装配或捕获失败必须显式返回错误，禁止以原 preview 掩盖结果未持久化。
func captureSessionToolResult(hooks providershared.RuntimeHooks, header shareddto.ToolCallHeader, preview string) (providershared.ToolResultRecord, error) {
	return hooks.CaptureToolResult(providershared.ToolResultMeta{ThreadID: header.ThreadID, TurnID: header.TurnID, CallID: header.CallID, ToolName: header.ToolName, Timestamp: header.Timestamp}, preview)
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

// decodeToolCallResultEnvelope 解析工具结果中的成功/失败包络。
// nil、空 JSON 和 null 视为没有包络；marshal/unmarshal 失败会返回错误供调用方标记工具失败。
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

// failureText 从工具结果包络中提取最适合展示的失败说明。
// 优先使用 error/message/reason，再回退到 content 文本或完整结果预览。
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

// enrichToolCallParamsStrict 以 fail-fast 方式写入工具调用的可信元数据。
// 会删除模型可控的同名公开字段，只保留宿主生成的 _agentId/_threadId/_cwd/_workspaceRoots。
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

// normalizeTrustedWorkspaceRoot 把工具可用 workspace root 规范成绝对 clean 路径。
// 相对 root 必须基于主 cwd 解析，无法定位时返回空值并由调用方丢弃。
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

// toolCallParamStringAny 从工具调用参数中按候选键读取第一个非空字符串。
// 解析失败或字段类型不匹配时返回空字符串，调用方负责决定是否 fail-fast。
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

// nestedToolCallString 从嵌套对象中读取工具调用字符串字段。
// 该兼容层用于 Codex item 包装形态，缺对象或缺字段时返回空字符串。
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
