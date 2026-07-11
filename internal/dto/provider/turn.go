package provider

import (
	"encoding/json"

	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
)

// TurnRequest 是发起一次 turn 的请求 DTO。
// DedupeKey 只在 turn 层内存注册，不写入 provider 线格式。
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
	// AdditionalWorkingDirectories 承载单次 turn 允许本地工具进入的额外可信目录。
	// 字段只约束本机工具执行边界，不转发给 Codex app-server。
	AdditionalWorkingDirectories []string    `json:"additionalWorkingDirectories,omitempty"`
	MCP                          MCPManifest `json:"mcp"`
	// DedupeKey 是 turn 层内存幂等键，用来把 StartTurn 绑定到本地执行跟踪。
	// 字段不进入 provider 线格式，避免把本地调度状态泄漏给 codex/claudecli 驱动。
	DedupeKey string `json:"-"`
}

// TurnOverrides 携带单次 turn 的模型和 effort 覆盖参数。
type TurnOverrides struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

// InputItem 是 shareddto.InputItem 的类型别名，供 turn/steer 请求直接引用。
type InputItem = shareddto.InputItem

// SkillRef 是 turn / steer 请求中携带的 skill 引用。
//
// SkillRef 进入 provider 前只保留稳定引用元数据，注入内容由 provider 自己从镜像目录发现：
//   - Codex 通过 .agents/skills 镜像目录发现 skills。
//   - Claude 通过 .claude/skills 或 provider 主目录 skills 发现 skills。
//   - 旧 Mode/Effective() 三态不再参与生产链路，保留值只用于兼容历史 payload。
//
// 字段语义：
//   - Name：skill 标识符。
//   - Key / Scope / PersonalType / Path：UI 明确选择某个同名 skill 时传入的
//     稳定引用元数据；provider 侧不读取正文，但 turn 层用它避免同名选择退化为
//     name-only。
//   - Version：skill 版本或内容 hash（可选）；用于去重键 `name@version`。
//   - Prompt：兼容旧 payload 的全文 SKILL.md carrier；生产 turn/provider-native
//     链路不消费该字段注入正文，PrepareTurn 会在归一化阶段清空它。
//   - Summary：摘要文本（UI/观测与 hydration 输出）。
//   - Source：决策来源，供观测性日志划分 manual/force/trigger/native；
//     expand 仅作为旧观测值兼容保留，当前 provider 镜像链路不再产生它。
type SkillRef struct {
	Key          string      `json:"key,omitempty"`
	Name         string      `json:"name"`
	Scope        string      `json:"scope,omitempty"`
	PersonalType string      `json:"personalType,omitempty"`
	Path         string      `json:"path,omitempty"`
	Version      string      `json:"version,omitempty"`
	Prompt       string      `json:"prompt,omitempty"`
	Summary      string      `json:"summary,omitempty"`
	Source       SkillSource `json:"source,omitempty"`
}

// SkillSource 追踪 SkillRef 的决策来源，供日志 / 断点 / 断言使用。
type SkillSource string

// SkillSource 常量列出 SkillRef 来源分类，未知来源应由 Valid 拒绝。
const (
	SkillSourceUnspecified SkillSource = ""
	// SkillSourceManual：用户在 UI 显式勾选。
	SkillSourceManual SkillSource = "manual"
	// SkillSourceForce：强匹配/强意图来源标记；生产链路不因此注入全文。
	SkillSourceForce SkillSource = "force"
	// SkillSourceTrigger：软匹配来源标记；生产链路仅保留元数据，不驱动摘要注入语义。
	SkillSourceTrigger SkillSource = "trigger"
	// SkillSourceExpand：兼容旧 skill_expand 二次注入来源；当前生产链路不再产生。
	SkillSourceExpand SkillSource = "expand"
	// SkillSourceNative：provider 原生 skill 来源；当前链路只记录元数据，不注入正文。
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

// TurnResult 是 turn 执行的结果摘要，包含本地和 provider 侧的 ID 及成败状态。
type TurnResult struct {
	LocalID    string `json:"localId"`
	ProviderID string `json:"providerId,omitempty"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
}

// InterruptRequest 是中断当前 turn 的请求 DTO。
type InterruptRequest struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId,omitempty"`
	Source   string `json:"source,omitempty"`
}

// SteerRequest 是向当前 turn 注入新输入（steer）的请求 DTO。
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

// ForceCompleteRequest 强制结束指定 thread 的当前 turn。
type ForceCompleteRequest struct {
	ThreadID   string `json:"threadId"`
	ProviderID string `json:"providerId,omitempty"`
}

// ForkRequest 是 fork thread 的请求 DTO。
type ForkRequest struct {
	ThreadID string `json:"threadId"`
}

// ForkResult 是 fork thread 操作的结果，携带新生成的 thread ID。
type ForkResult struct {
	NewThreadID string `json:"newThreadId"`
}
