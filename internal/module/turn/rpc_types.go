package turn

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
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
	MCPSnapshot                  contract.MCPSnapshot `json:"mcp_snapshot,omitzero"`
	SessionFlags                 map[string]bool      `json:"session_flags,omitempty"`
	Effort                       string               `json:"effort,omitempty"`
	OutputSchema                 json.RawMessage      `json:"output_schema,omitempty"`
}

// UnmarshalJSON 先解码当前字段，再把历史 camelCase 字段补到空值位置。
func (p *turnStartParams) UnmarshalJSON(data []byte) error {
	if err := rejectUnknownTurnFields(data, "turn/start", rawTurnStartParams{}, legacyTurnStartParams{}); err != nil {
		return err
	}
	payload, err := decodeTurnCompatPayload(data)
	if err != nil {
		return err
	}
	if err := validateTurnBoolFields("turn/start", payload); err != nil {
		return err
	}
	var legacy legacyTurnStartParams
	return decodeLegacyTurnParams(data, (*rawTurnStartParams)(p), &legacy, func(current *rawTurnStartParams, legacy *legacyTurnStartParams) error {
		return mergeTurnStartLegacy(current, legacy, payload)
	})
}

type rawTurnStartParams turnStartParams

type legacyTurnStartParams struct {
	ThreadID                     string               `json:"threadId"`
	ThreadIDUpper                string               `json:"threadID"`
	SelectedSkills               []string             `json:"selectedSkills"`
	SelectedSkillRefs            []skillRefParams     `json:"selectedSkillRefs"`
	ManualSkillSelection         *bool                `json:"manualSkillSelection"`
	ApprovalPolicy               string               `json:"approvalPolicy"`
	GitRoot                      string               `json:"gitRoot"`
	IsWorktree                   *bool                `json:"isWorktree"`
	EnabledTools                 []string             `json:"enabledTools"`
	AdditionalWorkingDirectories []string             `json:"additionalWorkingDirectories"`
	MCPSnapshot                  contract.MCPSnapshot `json:"mcpSnapshot"`
	SessionFlags                 map[string]bool      `json:"sessionFlags"`
	OutputSchema                 json.RawMessage      `json:"outputSchema"`
}

// mergeTurnStartLegacy 把旧版 camelCase 字段补进 turn/start 新版参数。
// 只有新版字段为空时才补值，避免旧客户端兼容逻辑覆盖当前 wire 格式。
func mergeTurnStartLegacy(current *rawTurnStartParams, legacy *legacyTurnStartParams, payload map[string]json.RawMessage) error {
	if strings.TrimSpace(current.ThreadID) == "" {
		current.ThreadID = firstTrimmed(legacy.ThreadID, legacy.ThreadIDUpper)
	}
	if len(current.SelectedSkills) == 0 && len(legacy.SelectedSkills) > 0 {
		current.SelectedSkills = append([]string(nil), legacy.SelectedSkills...)
	}
	if len(current.SelectedSkillRefs) == 0 && len(legacy.SelectedSkillRefs) > 0 {
		current.SelectedSkillRefs = append([]skillRefParams(nil), legacy.SelectedSkillRefs...)
	}
	if err := mergeTurnCompatBool("turn/start", payload, &current.ManualSkillSelection, "manual skill selection", "manual_skill_selection", "manualSkillSelection"); err != nil {
		return err
	}
	if strings.TrimSpace(current.ApprovalPolicy) == "" {
		current.ApprovalPolicy = strings.TrimSpace(legacy.ApprovalPolicy)
	}
	if err := mergeRuntimeLegacyFields(
		"turn/start",
		payload,
		&current.GitRoot,
		&current.IsWorktree,
		&current.EnabledTools,
		&current.AdditionalWorkingDirectories,
		&current.MCPSnapshot,
		&current.SessionFlags,
		legacy.GitRoot,
		legacy.EnabledTools,
		legacy.AdditionalWorkingDirectories,
		legacy.MCPSnapshot,
		legacy.SessionFlags,
	); err != nil {
		return err
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
	MCPSnapshot                  contract.MCPSnapshot  `json:"mcp_snapshot,omitzero"`
	SessionFlags                 map[string]bool       `json:"session_flags,omitempty"`
}

// UnmarshalJSON 兼容旧版 turn/steer camelCase 字段，同时保留新版 snake_case 优先级。
func (p *turnSteerParams) UnmarshalJSON(data []byte) error {
	if err := rejectUnknownTurnFields(data, "turn/steer", rawTurnSteerParams{}, legacyTurnSteerParams{}); err != nil {
		return err
	}
	payload, err := decodeTurnCompatPayload(data)
	if err != nil {
		return err
	}
	if err := validateTurnBoolFields("turn/steer", payload); err != nil {
		return err
	}
	var legacy legacyTurnSteerParams
	return decodeLegacyTurnParams(data, (*rawTurnSteerParams)(p), &legacy, func(current *rawTurnSteerParams, legacy *legacyTurnSteerParams) error {
		return mergeTurnSteerLegacy(current, legacy, payload)
	})
}

type rawTurnSteerParams turnSteerParams

type legacyTurnSteerParams struct {
	ThreadID                     string               `json:"threadId"`
	ThreadIDUpper                string               `json:"threadID"`
	ExpectedTurnID               string               `json:"expectedTurnId"`
	SelectedSkills               []string             `json:"selectedSkills"`
	SelectedSkillRefs            []skillRefParams     `json:"selectedSkillRefs"`
	ManualSkillSelection         *bool                `json:"manualSkillSelection"`
	GitRoot                      string               `json:"gitRoot"`
	IsWorktree                   *bool                `json:"isWorktree"`
	EnabledTools                 []string             `json:"enabledTools"`
	AdditionalWorkingDirectories []string             `json:"additionalWorkingDirectories"`
	MCPSnapshot                  contract.MCPSnapshot `json:"mcpSnapshot"`
	SessionFlags                 map[string]bool      `json:"sessionFlags"`
}

// mergeTurnSteerLegacy 把旧版 camelCase 字段补进 turn/steer 新版参数。
// 兼容字段只作为兜底填充，确保新版 snake_case 请求拥有更高优先级。
func mergeTurnSteerLegacy(current *rawTurnSteerParams, legacy *legacyTurnSteerParams, payload map[string]json.RawMessage) error {
	if strings.TrimSpace(current.ThreadID) == "" {
		current.ThreadID = firstTrimmed(legacy.ThreadID, legacy.ThreadIDUpper)
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
	if err := mergeTurnCompatBool("turn/steer", payload, &current.ManualSkillSelection, "manual skill selection", "manual_skill_selection", "manualSkillSelection"); err != nil {
		return err
	}
	if err := mergeRuntimeLegacyFields(
		"turn/steer",
		payload,
		&current.GitRoot,
		&current.IsWorktree,
		&current.EnabledTools,
		&current.AdditionalWorkingDirectories,
		&current.MCPSnapshot,
		&current.SessionFlags,
		legacy.GitRoot,
		legacy.EnabledTools,
		legacy.AdditionalWorkingDirectories,
		legacy.MCPSnapshot,
		legacy.SessionFlags,
	); err != nil {
		return err
	}
	return nil
}

func firstTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// mergeRuntimeLegacyFields 把旧版运行时字段补到新版 turn 参数的空位。
// 这里不覆盖当前字段，避免 camelCase 兼容路径反向改变新版 snake_case 请求。
func mergeRuntimeLegacyFields(
	method string,
	payload map[string]json.RawMessage,
	currentGitRoot *string,
	currentIsWorktree *bool,
	currentEnabledTools *[]string,
	currentAdditionalWorkingDirectories *[]string,
	currentMCPSnapshot *contract.MCPSnapshot,
	currentSessionFlags *map[string]bool,
	legacyGitRoot string,
	legacyEnabledTools []string,
	legacyAdditionalWorkingDirectories []string,
	legacyMCPSnapshot contract.MCPSnapshot,
	legacySessionFlags map[string]bool,
) error {
	mergeLegacyString(currentGitRoot, legacyGitRoot)
	if err := mergeTurnCompatBool(method, payload, currentIsWorktree, "is worktree", "is_worktree", "isWorktree"); err != nil {
		return err
	}
	mergeLegacyStringSlice(currentEnabledTools, legacyEnabledTools)
	mergeLegacyStringSlice(currentAdditionalWorkingDirectories, legacyAdditionalWorkingDirectories)
	mergeLegacyMCPSnapshot(currentMCPSnapshot, legacyMCPSnapshot)
	mergeLegacySessionFlags(currentSessionFlags, legacySessionFlags)
	return nil
}

func decodeTurnCompatPayload(data []byte) (map[string]json.RawMessage, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// validateTurnBoolFields 先用原始 JSON 校验 bool 别名，避免结构体解码提前返回通用错误。
func validateTurnBoolFields(method string, payload map[string]json.RawMessage) error {
	if _, _, err := resolveTurnCompatBool(method, payload, "manual skill selection", "manual_skill_selection", "manualSkillSelection"); err != nil {
		return err
	}
	_, _, err := resolveTurnCompatBool(method, payload, "is worktree", "is_worktree", "isWorktree")
	return err
}

type turnCompatBoolValue struct {
	key     string
	value   bool
	present bool
}

func mergeTurnCompatBool(method string, payload map[string]json.RawMessage, current *bool, field string, keys ...string) error {
	value, present, err := resolveTurnCompatBool(method, payload, field, keys...)
	if err != nil {
		return err
	}
	if present {
		*current = value
	}
	return nil
}

// resolveTurnCompatBool 根据原始 JSON 合并 turn 入参里的布尔别名。
// 它用字段是否出现来判断优先级，冲突时 fail-fast，避免旧 camelCase 覆盖显式 false。
func resolveTurnCompatBool(method string, payload map[string]json.RawMessage, field string, keys ...string) (bool, bool, error) {
	var resolved turnCompatBoolValue
	for _, key := range keys {
		item, err := readTurnCompatBool(method, payload, key)
		if err != nil {
			return false, false, err
		}
		if !item.present {
			continue
		}
		if !resolved.present {
			resolved = item
			continue
		}
		if resolved.value != item.value {
			return false, false, platformrpc.ErrInvalidParams(fmt.Sprintf("%s: conflicting %s values for %q and %q", method, field, resolved.key, item.key))
		}
	}
	return resolved.value, resolved.present, nil
}

func readTurnCompatBool(method string, payload map[string]json.RawMessage, key string) (turnCompatBoolValue, error) {
	raw, ok := payload[key]
	if !ok {
		return turnCompatBoolValue{}, nil
	}
	var value *bool
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return turnCompatBoolValue{}, platformrpc.ErrInvalidParams(fmt.Sprintf("%s: %s must be a boolean", method, key))
	}
	return turnCompatBoolValue{key: key, value: *value, present: true}, nil
}

func mergeLegacyString(current *string, legacy string) {
	if strings.TrimSpace(*current) == "" {
		*current = strings.TrimSpace(legacy)
	}
}

func mergeLegacyStringSlice(current *[]string, legacy []string) {
	if len(*current) == 0 && len(legacy) > 0 {
		*current = append([]string(nil), legacy...)
	}
}

func mergeLegacyMCPSnapshot(current *contract.MCPSnapshot, legacy contract.MCPSnapshot) {
	if turnMCPSnapshotEmpty(*current) && !turnMCPSnapshotEmpty(legacy) {
		*current = cloneMCPSnapshot(legacy)
	}
}

func mergeLegacySessionFlags(current *map[string]bool, legacy map[string]bool) {
	if len(*current) == 0 && len(legacy) > 0 {
		*current = clonePrepareFlags(legacy)
	}
}

// turnMCPSnapshotEmpty 判断兼容字段是否真的携带 MCP 快照内容。
// bool 开关也纳入判断，避免空 slice 但 delta 标记为 true 的快照被误当作空值。
func turnMCPSnapshotEmpty(snapshot contract.MCPSnapshot) bool {
	return len(snapshot.Servers) == 0 &&
		len(snapshot.Tools) == 0 &&
		len(snapshot.Instructions) == 0 &&
		len(snapshot.ServerConfigs) == 0 &&
		!snapshot.InstructionsDeltaEnabled &&
		len(snapshot.InstructionAttachments) == 0
}

// turnInterruptParams 是 turn/interrupt 入参。Stop identity is mandatory so a
// delayed request cannot be applied to a replacement turn.
type turnInterruptParams struct {
	ThreadID       string `json:"thread_id"`
	ExpectedTurnID string `json:"expected_turn_id"`
	RequestID      string `json:"request_id"`
	Source         string `json:"source,omitempty"`
}

// UnmarshalJSON 对 turn/interrupt 启用字段白名单，避免拼错字段被静默忽略。
func (p *turnInterruptParams) UnmarshalJSON(data []byte) error {
	var legacy legacyTurnInterruptParams
	type raw turnInterruptParams
	if err := rejectUnknownTurnFields(data, "turn/interrupt", turnInterruptParams{}, legacyTurnInterruptParams{}); err != nil {
		return err
	}
	return decodeLegacyTurnParams(data, (*raw)(p), &legacy, func(current *raw, legacy *legacyTurnInterruptParams) error {
		if strings.TrimSpace(current.ThreadID) == "" {
			current.ThreadID = firstTrimmed(legacy.ThreadID, legacy.ThreadIDUpper)
		}
		if strings.TrimSpace(current.ExpectedTurnID) == "" {
			current.ExpectedTurnID = strings.TrimSpace(legacy.ExpectedTurnID)
		}
		if strings.TrimSpace(current.RequestID) == "" {
			current.RequestID = strings.TrimSpace(legacy.RequestID)
		}
		if strings.TrimSpace(current.ExpectedTurnID) == "" {
			return platformrpc.ErrInvalidParams("turn/interrupt: expectedTurnId is required")
		}
		if strings.TrimSpace(current.RequestID) == "" {
			return platformrpc.ErrInvalidParams("turn/interrupt: requestId is required")
		}
		return nil
	})
}

type legacyTurnInterruptParams struct {
	ThreadID       string `json:"threadId"`
	ThreadIDUpper  string `json:"threadID"`
	ExpectedTurnID string `json:"expectedTurnId"`
	RequestID      string `json:"requestId"`
}

// rejectUnknownTurnFields 在轻量 RPC 参数上做 fail-fast 字段检查，防止客户端误以为字段生效。
func rejectUnknownTurnFields(data []byte, method string, wireShapes ...any) error {
	return platformrpc.RejectUnknownJSONFields(data, method, wireShapes...)
}

// threadIDOnlyParams 是只需要线程 ID 的 RPC 入参，保留旧 camelCase 兼容。
type threadIDOnlyParams struct {
	ThreadID string `json:"thread_id"`
}

// UnmarshalJSON 兼容 threadId，同时让新版 thread_id 优先。
func (p *threadIDOnlyParams) UnmarshalJSON(data []byte) error {
	var legacy legacyTurnThreadIDParams
	type raw threadIDOnlyParams
	if err := rejectUnknownTurnFields(data, "turn/forceComplete", threadIDOnlyParams{}, legacyTurnThreadIDParams{}); err != nil {
		return err
	}
	return decodeLegacyTurnParams(data, (*raw)(p), &legacy, func(current *raw, legacy *legacyTurnThreadIDParams) error {
		if strings.TrimSpace(current.ThreadID) == "" {
			current.ThreadID = strings.TrimSpace(legacy.ThreadID)
		}
		return nil
	})
}

type legacyTurnThreadIDParams struct {
	ThreadID string `json:"threadId"`
}

// approvalRespondParams 是工具审批响应入参，Approved 与 Decision 至少要提供一种。
type approvalRespondParams struct {
	SessionScope string          `json:"session_scope,omitempty"`
	CallID       string          `json:"call_id,omitempty"`
	RequestID    *int64          `json:"request_id,omitempty"`
	Approved     *bool           `json:"approved,omitempty"`
	Decision     json.RawMessage `json:"decision,omitempty"`
}

// UnmarshalJSON 兼容旧版 callId/requestId，并复制 RawMessage 防止共享底层 buffer。
func (p *approvalRespondParams) UnmarshalJSON(data []byte) error {
	var legacy legacyApprovalRespondParams
	type raw approvalRespondParams
	if err := rejectUnknownTurnFields(data, "approval/respond", approvalRespondParams{}, legacyApprovalRespondParams{}); err != nil {
		return err
	}
	payload, err := decodeTurnCompatPayload(data)
	if err != nil {
		return err
	}
	return decodeLegacyTurnParams(data, (*raw)(p), &legacy, func(current *raw, legacy *legacyApprovalRespondParams) error {
		return mergeApprovalRespondLegacy(payload, (*approvalRespondParams)(current), legacy)
	})
}

// mergeApprovalRespondLegacy 校验新旧字段别名的一致性，并把缺失的兼容字段合并到当前参数。
func mergeApprovalRespondLegacy(payload map[string]json.RawMessage, current *approvalRespondParams, legacy *legacyApprovalRespondParams) error {
	if err := rejectConflictingApprovalRespondAliases(payload, current, legacy); err != nil {
		return err
	}
	if strings.TrimSpace(current.SessionScope) == "" {
		current.SessionScope = strings.TrimSpace(legacy.SessionScope)
	}
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
}

// rejectConflictingApprovalRespondAliases 拒绝同一审批身份的新旧字段别名携带不同值。
func rejectConflictingApprovalRespondAliases(payload map[string]json.RawMessage, current *approvalRespondParams, legacy *legacyApprovalRespondParams) error {
	if _, snake := payload["session_scope"]; snake {
		if _, camel := payload["sessionScope"]; camel && strings.TrimSpace(current.SessionScope) != strings.TrimSpace(legacy.SessionScope) {
			return platformrpc.ErrInvalidParams(`approval/respond: conflicting sessionScope values for "session_scope" and "sessionScope"`)
		}
	}
	if _, snake := payload["call_id"]; snake {
		if _, camel := payload["callId"]; camel && strings.TrimSpace(current.CallID) != strings.TrimSpace(legacy.CallID) {
			return platformrpc.ErrInvalidParams(`approval/respond: conflicting callId values for "call_id" and "callId"`)
		}
	}
	if _, snake := payload["request_id"]; snake {
		if _, camel := payload["requestId"]; camel && !equalOptionalInt64(current.RequestID, legacy.RequestID) {
			return platformrpc.ErrInvalidParams(`approval/respond: conflicting requestId values for "request_id" and "requestId"`)
		}
	}
	return nil
}

func equalOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

type legacyApprovalRespondParams struct {
	SessionScope string          `json:"sessionScope"`
	CallID       string          `json:"callId"`
	RequestID    *int64          `json:"requestId"`
	Approved     *bool           `json:"approved"`
	Decision     json.RawMessage `json:"decision"`
}

// turnInterruptResult 是 turn/interrupt 返回给 UI 的状态和 settle 摘要。
type turnInterruptResult struct {
	OK             bool   `json:"ok"`
	Accepted       bool   `json:"accepted"`
	RequestID      string `json:"requestId,omitempty"`
	ExpectedTurnID string `json:"expectedTurnId,omitempty"`
	ErrorCode      string `json:"errorCode,omitempty"`
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
	OK             bool   `json:"ok"`
	ForceCompleted bool   `json:"forceCompleted"`
	ErrorCode      string `json:"errorCode,omitempty"`
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
