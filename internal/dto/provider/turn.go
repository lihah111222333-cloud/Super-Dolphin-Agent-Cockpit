package provider

import (
	"encoding/json"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

type TurnRequest struct {
	LocalID              string          `json:"localId,omitempty"`
	ThreadID             string          `json:"threadId"`
	CWD                  string          `json:"cwd,omitempty"`
	Inputs               []InputItem     `json:"inputs"`
	Skills               []SkillRef      `json:"skills,omitempty"`
	TurnAssembly         TurnAssembly    `json:"turnAssembly"`
	ManualSkillSelection bool            `json:"manualSkillSelection,omitempty"`
	OutputSchema         json.RawMessage `json:"outputSchema,omitempty"`
	Overrides            TurnOverrides   `json:"overrides"`
	// AdditionalWorkingDirectories carries the trusted per-turn workspace
	// expansion used by local tools. It is not forwarded to Codex app-server.
	AdditionalWorkingDirectories []string    `json:"additionalWorkingDirectories,omitempty"`
	MCP                          MCPManifest `json:"mcp"`
	// DedupeKey carries the turn layer's in-memory idempotency token so
	// StartTurn can register it on the tracker. It is intentionally not
	// forwarded to the provider wire format today — codex / claudecli
	// driver idempotency is a follow-up once the SQL persistence lands.
	DedupeKey string `json:"-"`
}

type TurnOverrides struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

type InputItem = shareddto.InputItem

// SkillRef 是 turn / steer 请求中携带的 skill 引用。
//
// P2/P3 cutover 之后，注入模式不再由 Mode 字段驱动：
//   - Codex 走 base instructions L1-C 元数据 + skill_read_section 动态工具按需取节
//   - Claude 走原生 .claude/skills/ 目录 + CLI 自动加载
//   - 旧 Mode/Effective() 三态已无生产消费方，spec §11 同步清理。
//
// 字段语义：
//   - Name：skill 标识符。
//   - Version：skill 版本或内容 hash（可选）；用于去重键 `name@version`。
//   - Prompt：全文 SKILL.md body（仅 hydration 路径用作 fallback 容器；
//     codex/claude provider 都不再消费此字段拼 turn input）。
//   - Summary：摘要文本（manifest L1-C 描述与 hydration 输出）。
//   - Source：决策来源，供观测性日志划分 manual/force/trigger/expand/native。
type SkillRef struct {
	Name    string      `json:"name"`
	Version string      `json:"version,omitempty"`
	Prompt  string      `json:"prompt,omitempty"`
	Summary string      `json:"summary,omitempty"`
	Source  SkillSource `json:"source,omitempty"`
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
	TurnID   string `json:"turnId,omitempty"`
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
