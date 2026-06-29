package turn

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// turnStartParams 是 turn/start 的 RPC 入参，兼容 snake_case 与部分旧 camelCase 字段。
type turnStartParams struct {
	ThreadID string                `json:"thread_id"`
	Prompt   string                `json:"prompt,omitempty"`
	Images   []string              `json:"images,omitempty"`
	Files    []string              `json:"files,omitempty"`
	Input    []turnInputItemParams `json:"input,omitempty"`

	SelectedSkills               []string             `json:"selected_skills,omitempty"`
	SelectedSkillRefs            []skillRefParams     `json:"selected_skill_refs,omitempty"`
	ManualSkillSelection         bool                 `json:"manual_skill_selection,omitempty"`
	CWD                          string               `json:"cwd,omitempty"`
	ApprovalPolicy               string               `json:"approval_policy,omitempty"`
	Provider                     string               `json:"provider,omitempty"`
	Model                        string               `json:"model,omitempty"`
	GitRoot                      string               `json:"git_root,omitempty"`
	IsWorktree                   bool                 `json:"is_worktree,omitempty"`
	Language                     string               `json:"language,omitempty"`
	EnabledTools                 []string             `json:"enabled_tools,omitempty"`
	AdditionalWorkingDirectories []string             `json:"additional_working_directories,omitempty"`
	MCPSnapshot                  contract.MCPSnapshot `json:"mcp_snapshot,omitempty"`
	SessionFlags                 map[string]bool      `json:"session_flags,omitempty"`
	Effort                       string               `json:"effort,omitempty"`
	OutputSchema                 json.RawMessage      `json:"output_schema,omitempty"`
}

// UnmarshalJSON 先解码当前字段，再把历史 camelCase 字段补到空值位置。
func (p *turnStartParams) UnmarshalJSON(data []byte) error {
	if err := rejectUnknownTurnFields(data, "turn/start", turnStartParamFields); err != nil {
		return err
	}
	var legacy legacyTurnStartParams
	return decodeLegacyTurnParams(data, (*rawTurnStartParams)(p), &legacy, mergeTurnStartLegacy)
}

type rawTurnStartParams turnStartParams

type legacyTurnStartParams struct {
	ThreadID             string           `json:"threadId"`
	SelectedSkills       []string         `json:"selectedSkills"`
	SelectedSkillRefs    []skillRefParams `json:"selectedSkillRefs"`
	ManualSkillSelection *bool            `json:"manualSkillSelection"`
	ApprovalPolicy       string           `json:"approvalPolicy"`
	OutputSchema         json.RawMessage  `json:"outputSchema"`
}

// mergeTurnStartLegacy 把旧版 camelCase 字段补进 turn/start 新版参数。
// 只有新版字段为空时才补值，避免旧客户端兼容逻辑覆盖当前 wire 格式。
func mergeTurnStartLegacy(current *rawTurnStartParams, legacy *legacyTurnStartParams) error {
	if strings.TrimSpace(current.ThreadID) == "" {
		current.ThreadID = strings.TrimSpace(legacy.ThreadID)
	}
	if len(current.SelectedSkills) == 0 && len(legacy.SelectedSkills) > 0 {
		current.SelectedSkills = append([]string(nil), legacy.SelectedSkills...)
	}
	if len(current.SelectedSkillRefs) == 0 && len(legacy.SelectedSkillRefs) > 0 {
		current.SelectedSkillRefs = append([]skillRefParams(nil), legacy.SelectedSkillRefs...)
	}
	if !current.ManualSkillSelection && legacy.ManualSkillSelection != nil {
		current.ManualSkillSelection = *legacy.ManualSkillSelection
	}
	if strings.TrimSpace(current.ApprovalPolicy) == "" {
		current.ApprovalPolicy = strings.TrimSpace(legacy.ApprovalPolicy)
	}
	if len(current.OutputSchema) == 0 {
		current.OutputSchema = append(json.RawMessage(nil), legacy.OutputSchema...)
	}
	return nil
}

// turnInputItemParams 是 text/image/mention/skill 等输入项的宽松 RPC 形态。
type turnInputItemParams struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	URL     string `json:"url,omitempty"`
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

// skillRefParams 描述带作用域或路径的精确 skill 引用。
type skillRefParams struct {
	Key          string `json:"key,omitempty"`
	Name         string `json:"name"`
	Scope        string `json:"scope,omitempty"`
	PersonalType string `json:"personalType,omitempty"`
	Path         string `json:"path,omitempty"`
	Source       string `json:"source,omitempty"`
}

// turnSteerParams 是 turn/steer 入参，结构跟 start 接近但要求 expected turn ID。
type turnSteerParams struct {
	ThreadID                     string                `json:"thread_id"`
	ExpectedTurnID               string                `json:"expected_turn_id,omitempty"`
	Prompt                       string                `json:"prompt,omitempty"`
	Input                        []turnInputItemParams `json:"input,omitempty"`
	SelectedSkills               []string              `json:"selected_skills,omitempty"`
	SelectedSkillRefs            []skillRefParams      `json:"selected_skill_refs,omitempty"`
	ManualSkillSelection         bool                  `json:"manual_skill_selection,omitempty"`
	Provider                     string                `json:"provider,omitempty"`
	CWD                          string                `json:"cwd,omitempty"`
	Model                        string                `json:"model,omitempty"`
	GitRoot                      string                `json:"git_root,omitempty"`
	IsWorktree                   bool                  `json:"is_worktree,omitempty"`
	Language                     string                `json:"language,omitempty"`
	EnabledTools                 []string              `json:"enabled_tools,omitempty"`
	AdditionalWorkingDirectories []string              `json:"additional_working_directories,omitempty"`
	MCPSnapshot                  contract.MCPSnapshot  `json:"mcp_snapshot,omitempty"`
	SessionFlags                 map[string]bool       `json:"session_flags,omitempty"`
}

// UnmarshalJSON 兼容旧版 turn/steer camelCase 字段，同时保留新版 snake_case 优先级。
func (p *turnSteerParams) UnmarshalJSON(data []byte) error {
	if err := rejectUnknownTurnFields(data, "turn/steer", turnSteerParamFields); err != nil {
		return err
	}
	var legacy legacyTurnSteerParams
	return decodeLegacyTurnParams(data, (*rawTurnSteerParams)(p), &legacy, mergeTurnSteerLegacy)
}

type rawTurnSteerParams turnSteerParams

type legacyTurnSteerParams struct {
	ThreadID             string           `json:"threadId"`
	ExpectedTurnID       string           `json:"expectedTurnId"`
	SelectedSkills       []string         `json:"selectedSkills"`
	SelectedSkillRefs    []skillRefParams `json:"selectedSkillRefs"`
	ManualSkillSelection *bool            `json:"manualSkillSelection"`
}

// mergeTurnSteerLegacy 把旧版 camelCase 字段补进 turn/steer 新版参数。
// 兼容字段只作为兜底填充，确保新版 snake_case 请求拥有更高优先级。
func mergeTurnSteerLegacy(current *rawTurnSteerParams, legacy *legacyTurnSteerParams) error {
	if strings.TrimSpace(current.ThreadID) == "" {
		current.ThreadID = strings.TrimSpace(legacy.ThreadID)
	}
	if strings.TrimSpace(current.ExpectedTurnID) == "" {
		current.ExpectedTurnID = strings.TrimSpace(legacy.ExpectedTurnID)
	}
	if len(current.SelectedSkills) == 0 && len(legacy.SelectedSkills) > 0 {
		current.SelectedSkills = append([]string(nil), legacy.SelectedSkills...)
	}
	if len(current.SelectedSkillRefs) == 0 && len(legacy.SelectedSkillRefs) > 0 {
		current.SelectedSkillRefs = append([]skillRefParams(nil), legacy.SelectedSkillRefs...)
	}
	if !current.ManualSkillSelection && legacy.ManualSkillSelection != nil {
		current.ManualSkillSelection = *legacy.ManualSkillSelection
	}
	return nil
}

// turnStartParamFields 列出 turn/start 的 wire 字段，包含明确保留的 camelCase 兼容别名。
var turnStartParamFields = map[string]struct{}{
	"additionalWorkingDirectories":   {},
	"additional_working_directories": {},
	"approvalPolicy":                 {},
	"approval_policy":                {},
	"cwd":                            {},
	"effort":                         {},
	"enabledTools":                   {},
	"enabled_tools":                  {},
	"files":                          {},
	"gitRoot":                        {},
	"git_root":                       {},
	"images":                         {},
	"input":                          {},
	"isWorktree":                     {},
	"is_worktree":                    {},
	"language":                       {},
	"manualSkillSelection":           {},
	"manual_skill_selection":         {},
	"mcpSnapshot":                    {},
	"mcp_snapshot":                   {},
	"model":                          {},
	"outputSchema":                   {},
	"output_schema":                  {},
	"prompt":                         {},
	"provider":                       {},
	"selectedSkillRefs":              {},
	"selectedSkills":                 {},
	"selected_skill_refs":            {},
	"selected_skills":                {},
	"sessionFlags":                   {},
	"session_flags":                  {},
	"threadID":                       {},
	"threadId":                       {},
	"thread_id":                      {},
}

// turnSteerParamFields 列出 turn/steer 的 wire 字段，避免拼写错误在 steer 时被忽略。
var turnSteerParamFields = map[string]struct{}{
	"additionalWorkingDirectories":   {},
	"additional_working_directories": {},
	"cwd":                            {},
	"enabledTools":                   {},
	"enabled_tools":                  {},
	"expectedTurnId":                 {},
	"expected_turn_id":               {},
	"gitRoot":                        {},
	"git_root":                       {},
	"input":                          {},
	"isWorktree":                     {},
	"is_worktree":                    {},
	"language":                       {},
	"manualSkillSelection":           {},
	"manual_skill_selection":         {},
	"mcpSnapshot":                    {},
	"mcp_snapshot":                   {},
	"model":                          {},
	"prompt":                         {},
	"provider":                       {},
	"selectedSkillRefs":              {},
	"selectedSkills":                 {},
	"selected_skill_refs":            {},
	"selected_skills":                {},
	"sessionFlags":                   {},
	"session_flags":                  {},
	"threadID":                       {},
	"threadId":                       {},
	"thread_id":                      {},
}

// turnInterruptParams 是 turn/interrupt 入参，只允许线程标识和中断来源。
type turnInterruptParams struct {
	ThreadID string `json:"thread_id"`
	Source   string `json:"source,omitempty"`
}

// UnmarshalJSON 对 turn/interrupt 启用字段白名单，避免拼错字段被静默忽略。
func (p *turnInterruptParams) UnmarshalJSON(data []byte) error {
	var legacy struct {
		ThreadID string `json:"threadId"`
	}
	type raw turnInterruptParams
	if err := rejectUnknownTurnFields(data, "turn/interrupt", turnInterruptParamFields); err != nil {
		return err
	}
	return decodeLegacyTurnParams(data, (*raw)(p), &legacy, func(current *raw, legacy *struct {
		ThreadID string `json:"threadId"`
	}) error {
		if strings.TrimSpace(current.ThreadID) == "" {
			current.ThreadID = strings.TrimSpace(legacy.ThreadID)
		}
		return nil
	})
}

// turnInterruptParamFields 列出 turn/interrupt 允许的字段名，兼容历史 threadId/threadID。
var turnInterruptParamFields = map[string]struct{}{
	"source":    {},
	"thread_id": {},
	"threadId":  {},
	"threadID":  {},
}

// rejectUnknownTurnFields 在轻量 RPC 参数上做 fail-fast 字段检查，防止客户端误以为字段生效。
func rejectUnknownTurnFields(data []byte, method string, allowed map[string]struct{}) error {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	for key := range payload {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("%s: unknown field %q", method, key)
		}
	}
	return nil
}

// threadIDOnlyParams 是只需要线程 ID 的 RPC 入参，保留旧 camelCase 兼容。
type threadIDOnlyParams struct {
	ThreadID string `json:"thread_id"`
}

// UnmarshalJSON 兼容 threadId，同时让新版 thread_id 优先。
func (p *threadIDOnlyParams) UnmarshalJSON(data []byte) error {
	var legacy struct {
		ThreadID string `json:"threadId"`
	}
	type raw threadIDOnlyParams
	return decodeLegacyTurnParams(data, (*raw)(p), &legacy, func(current *raw, legacy *struct {
		ThreadID string `json:"threadId"`
	}) error {
		if strings.TrimSpace(current.ThreadID) == "" {
			current.ThreadID = strings.TrimSpace(legacy.ThreadID)
		}
		return nil
	})
}

// approvalRespondParams 是工具审批响应入参，Approved 与 Decision 至少要提供一种。
type approvalRespondParams struct {
	CallID    string          `json:"call_id,omitempty"`
	RequestID *int64          `json:"request_id,omitempty"`
	Approved  *bool           `json:"approved,omitempty"`
	Decision  json.RawMessage `json:"decision,omitempty"`
}

// UnmarshalJSON 兼容旧版 callId/requestId，并复制 RawMessage 防止共享底层 buffer。
func (p *approvalRespondParams) UnmarshalJSON(data []byte) error {
	var legacy struct {
		CallID    string          `json:"callId"`
		RequestID *int64          `json:"requestId"`
		Approved  *bool           `json:"approved"`
		Decision  json.RawMessage `json:"decision"`
	}
	type raw approvalRespondParams
	return decodeLegacyTurnParams(data, (*raw)(p), &legacy, func(current *raw, legacy *struct {
		CallID    string          `json:"callId"`
		RequestID *int64          `json:"requestId"`
		Approved  *bool           `json:"approved"`
		Decision  json.RawMessage `json:"decision"`
	}) error {
		if strings.TrimSpace(current.CallID) == "" {
			current.CallID = strings.TrimSpace(legacy.CallID)
		}
		if current.RequestID == nil && legacy.RequestID != nil {
			value := *legacy.RequestID
			current.RequestID = &value
		}
		if current.Approved == nil && legacy.Approved != nil {
			value := *legacy.Approved
			current.Approved = &value
		}
		if len(current.Decision) == 0 {
			current.Decision = append(json.RawMessage(nil), legacy.Decision...)
		}
		return nil
	})
}

// turnInterruptResult 是 turn/interrupt 返回给 UI 的状态和 settle 摘要。
type turnInterruptResult struct {
	OK             bool   `json:"ok"`
	TurnID         string `json:"turnId,omitempty"`
	Status         string `json:"status,omitempty"`
	Confirmed      bool   `json:"confirmed"`
	Mode           string `json:"mode"`
	InterruptSent  bool   `json:"interruptSent"`
	StateBefore    string `json:"stateBefore"`
	StateAfter     string `json:"stateAfter"`
	WaitedMS       *int64 `json:"waitedMs,omitempty"`
	ActiveObserved *bool  `json:"activeObserved,omitempty"`
}

// turnForceCompleteResult 是 turn/force_complete 的最小成功响应。
type turnForceCompleteResult struct {
	OK             bool `json:"ok"`
	ForceCompleted bool `json:"forceCompleted"`
}

// turnStartResult 返回本地 turnID，并在 pending launch 首 turn 时附带路由信息。
type turnStartResult struct {
	TurnID string `json:"turn_id"`
	// 路由字段只在 pending_launch 线程的首个 turn/start 触发 SpawnIfNeeded 时返回。
	// eager 启动线程已从 thread/start 获得路由；这里的零值字段会被 omitempty 隐去。
	AgentKey            string `json:"agent_key,omitempty"`
	AgentTitle          string `json:"agent_title,omitempty"`
	PromptKey           string `json:"prompt_key,omitempty"`
	PromptVersionID     *int64 `json:"prompt_version_id,omitempty"`
	PromptKeyStale      *bool  `json:"prompt_key_stale,omitempty"`
	PromptKeyStaleCamel *bool  `json:"promptKeyStale,omitempty"`
}

// attachTurnPromptKeyStale 同时填充 snake/camel 两个 stale 字段，兼容新旧前端读取。
func attachTurnPromptKeyStale(resp *turnStartResult, stale bool) {
	if resp == nil || !stale {
		return
	}
	resp.PromptKeyStale = &stale
	resp.PromptKeyStaleCamel = &stale
}
