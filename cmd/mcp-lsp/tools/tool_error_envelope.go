package tools

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"

// ToolErrorEnvelope 复用 common 包里的结构化工具错误协议。
// 这里仅为工具层测试和 helper 暴露类型别名，保证 stdio、HTTP、bootstrap 工具调用共用同一 wire 格式。
type ToolErrorEnvelope = common.ToolErrorEnvelope

// newToolErrorEnvelope 通过 common 包生成统一工具错误，保持各 transport 的错误格式一致。
func newToolErrorEnvelope(toolName, languageID string, err error) ToolErrorEnvelope {
	return common.NewToolErrorEnvelopeWithMeta(toolName, languageID, err, nil)
}
