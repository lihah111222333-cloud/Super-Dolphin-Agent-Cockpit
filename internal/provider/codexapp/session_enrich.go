package codexapp

import (
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
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
