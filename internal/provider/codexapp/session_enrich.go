package codexapp

import (
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
)

// enrichToolCallParams 把 session 的 agentID 注入到 codex item/tool/call 的 msg.Params 中。
//
// Codex app-server 的协议消息只含 name + arguments，不含 agentId（codex 不知道
// "agent" 概念）。toolbridge.decodeToolCallRequest 期望从 params 取 agentId 来解析 cwd
// （host-direct skill 工具分支强依赖此字段）。本函数填补这一缺口。
//
// 行为：
//   - agentID 非空且 msg.Params 是 JSON object → 覆盖写入 "agentId": "<agentID>"，
//     并移除 alias "agent_id"，避免信任 Codex payload / fixture 里的外部 agentId。
//   - msg.Params 为空 / 反序列化失败 / agentID 为空 → 原样返回，不报错。
//
// 错误路径全部 fail-soft（返回原 msg），让 toolbridge 用其它字段（threadID / 工具名）
// 继续诊断或失败；本函数不应成为新的崩溃源。
func enrichToolCallParams(msg RawMessage, agentID string) RawMessage {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" || len(msg.Params) == 0 {
		return msg
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(msg.Params, &payload); err != nil {
		skillmetrics.IncEnrichFailure()
		return msg
	}
	if payload == nil {
		payload = map[string]json.RawMessage{}
	}
	encoded, err := json.Marshal(agentID)
	if err != nil {
		skillmetrics.IncEnrichFailure()
		return msg
	}
	payload["agentId"] = encoded
	delete(payload, "agent_id")
	raw, err := json.Marshal(payload)
	if err != nil {
		skillmetrics.IncEnrichFailure()
		return msg
	}
	enriched := msg
	enriched.Params = raw
	return enriched
}
