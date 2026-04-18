package provider

import (
	"encoding/json"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

type TurnRequest struct {
	LocalID              string          `json:"localId,omitempty"`
	ThreadID             string          `json:"threadId"`
	Inputs               []InputItem     `json:"inputs"`
	Skills               []SkillRef      `json:"skills,omitempty"`
	TurnAssembly         TurnAssembly    `json:"turnAssembly"`
	ManualSkillSelection bool            `json:"manualSkillSelection,omitempty"`
	OutputSchema         json.RawMessage `json:"outputSchema,omitempty"`
	Overrides            TurnOverrides   `json:"overrides"`
	MCP                  MCPManifest     `json:"mcp"`
}

type TurnOverrides struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

type InputItem = shareddto.InputItem

// SkillRef 是 turn / steer 请求中携带的 skill 引用。
//
// P20 Phase 2 扩展历史：之前只有 {Name, Prompt}，无法表达“摘要注入”与“全文注入”的分支。
// 新增的 Mode/Summary/Version/Source 全部 omitempty：旧 server 反序列化与忽略未知字段，
// 新 server 反序列化旧 payload 时 Mode 为空字串，等价于 Full 行为兼容旧语义。
//
// 字段语义：
//   - Name：skill 标识符，对齐 validateSkillName 白名单（在 Phase 6 skill_expand 入口校验）。
//   - Version：skill 版本或内容 hash（可选）；Phase 7 Resolver 用于去重键 `name@version`。
//   - Mode：注入模式；Full = Prompt 填全文；Summary = Summary 填摘要指针；None = 跳过。
//     空值等价 Full（向后兼容旧 payload）。
//   - Prompt：全文 SKILL.md body。P20 方案文档称之为 Body，为不破坏 wire format
//     实施上保留字段名 Prompt 与 JSON tag `prompt`；语义上等价 Body。
//   - Summary：Mode=Summary 时填入的摘要文本（≤ 160c）。
//   - Source：决策来源，供观测性日志划分 manual/force/trigger/expand/native。
type SkillRef struct {
	Name    string      `json:"name"`
	Version string      `json:"version,omitempty"`
	Mode    SkillMode   `json:"mode,omitempty"`
	Prompt  string      `json:"prompt,omitempty"`
	Summary string      `json:"summary,omitempty"`
	Source  SkillSource `json:"source,omitempty"`
}

// SkillMode 拼接到 turn input 时的注入模式。空值等价 SkillModeFull（向后兼容旧 payload）。
type SkillMode string

const (
	// SkillModeUnspecified 空值，实际按 Full 处理。
	SkillModeUnspecified SkillMode = ""
	// SkillModeFull 注入完整 Prompt（SKILL.md body）。
	SkillModeFull SkillMode = "full"
	// SkillModeSummary 仅注入 Summary + `skill_expand` 指针（Phase 4 实现）。
	SkillModeSummary SkillMode = "summary"
	// SkillModeNone 不注入任何内容（例如 Claude 原生 skill 由 CLI 自动注入）。
	SkillModeNone SkillMode = "none"
)

// Valid 拒绝未知模式字符串。空值视为合法（后兼容）。
func (m SkillMode) Valid() bool {
	switch m {
	case SkillModeUnspecified, SkillModeFull, SkillModeSummary, SkillModeNone:
		return true
	}
	return false
}

// Effective 规范化原始值为 Phase 4 写端可用的枚举字面量。
//
// 兑底策略：Unspecified 与未知非法值（如 wire 中 `mode: "banana"`）均返回 SkillModeFull，
// 遵循“失败展开”策略——宁可注全文不漏掉，也不隐藏信息。这样考虑：
//   - 前向兼容：旧 server 读新 client 发的未知 mode 时不会静默跳过。
//   - 防御式：恶意 payload 伪造 mode="skip" 不能让我们无声丢失 skill 选中状态。
//
// Summary/None 模式如果真需要失败隐藏语义，应显式用 SkillModeNone 而非依赖未知值回落。
func (m SkillMode) Effective() SkillMode {
	if !m.Valid() || m == SkillModeUnspecified {
		return SkillModeFull
	}
	return m
}

// SkillSource 追踪 SkillRef 的决策来源，供日志 / 断点 / 断言使用。
type SkillSource string

const (
	SkillSourceUnspecified SkillSource = ""
	// SkillSourceManual：用户在 UI 显式勾选。
	SkillSourceManual SkillSource = "manual"
	// SkillSourceForce：skillResolver 匹配 ForceWords 命中，必走全文。
	SkillSourceForce SkillSource = "force"
	// SkillSourceTrigger：匹配 TriggerWords，软命中，默认走 Summary。
	SkillSourceTrigger SkillSource = "trigger"
	// SkillSourceExpand：模型调用 skill_expand 后的二次注入。
	SkillSourceExpand SkillSource = "expand"
	// SkillSourceNative：Claude CLI 原生 .claude/skills/，本 harness 不注入 body 仅列出元数据。
	SkillSourceNative SkillSource = "native"
)

type TurnResult struct {
	LocalID    string `json:"localId"`
	ProviderID string `json:"providerId,omitempty"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
}

type InterruptRequest struct {
	ThreadID string `json:"threadId"`
	Source   string `json:"source,omitempty"`
}

type SteerRequest struct {
	ThreadID             string          `json:"threadId"`
	ExpectedTurnID       string          `json:"expectedTurnId,omitempty"`
	Inputs               []InputItem     `json:"inputs"`
	Skills               []SkillRef      `json:"skills,omitempty"`
	TurnAssembly         TurnAssembly    `json:"turnAssembly"`
	ManualSkillSelection bool            `json:"manualSkillSelection,omitempty"`
	OutputSchema         json.RawMessage `json:"outputSchema,omitempty"`
	Overrides            TurnOverrides   `json:"overrides"`
}

type ForceCompleteRequest struct {
	ThreadID   string `json:"threadId"`
	ProviderID string `json:"providerId,omitempty"`
}

type ForkRequest struct {
	ThreadID string `json:"threadId"`
}

type ForkResult struct {
	NewThreadID string `json:"newThreadId"`
}
