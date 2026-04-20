package provider

import (
	"encoding/json"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
)

type TurnRequest struct {
	LocalID              string          `json:"localId,omitempty"`
	ThreadID             string          `json:"threadId"`
	Inputs               []InputItem     `json:"inputs"`
	Skills               []SkillRef      `json:"skills,omitempty"`
	SkillPrompt          string          `json:"skillPrompt,omitempty"`
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
// 策略（P20.1 §3.5 加固后）：
//   - Unspecified （空字符串）→ Full：兼容旧 payload `{name, prompt}`（旧代码没有
//     mode 字段，正常路径应走全文注入）。
//   - Full / Summary / None → 返回原值。
//   - 其他非法值（如 wire 中 `mode: "banana"` / 伪造 `mode: "skip"`）→ **保守降级为 None**，
//     不注入任何内容；同时调用方应记录 warn 日志 / `skill_invalid_mode_total` 指标
//     （指标将在 Phase 10 被接入，当前仅布置语义）。
//
// 这相比 P20 原“失败展开 Full”语义更保守——避免恶意 payload 通过伪造 mode 值让未审
// 批的 skill 全文被强制注入。调用方如需失败隐藏，应显式传 SkillModeNone。
func (m SkillMode) Effective() SkillMode {
	switch m {
	case SkillModeUnspecified:
		return SkillModeFull
	case SkillModeFull, SkillModeSummary, SkillModeNone:
		return m
	default:
		// P20.1 Phase 10 Step C：非法 mode 字面量计数。计数点放在降级分支
		// ——确保每次真正触发 "unknown mode → None" 时才 +1，
		// 空值 / 合法值 不干扰计数。
		skillmetrics.IncSkillInvalidMode()
		return SkillModeNone
	}
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

// Valid 拒绝未知来源字符串（对称 SkillMode.Valid）。空值视为合法（后兼容：旧 server
// 发的 SkillRef 无 source 字段）。观测 / 日志层拿到未知值时可告警：说明上游
// 可能在正在迭代新来源分类，或 payload 被伪造。
func (s SkillSource) Valid() bool {
	switch s {
	case SkillSourceUnspecified, SkillSourceManual, SkillSourceForce,
		SkillSourceTrigger, SkillSourceExpand, SkillSourceNative:
		return true
	}
	return false
}

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
	SkillPrompt          string          `json:"skillPrompt,omitempty"`
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
