package thread

import (
	"encoding/json"
	"fmt"
	"strings"

	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

type threadIDParams struct {
	ThreadID string `json:"thread_id"`
}

// UnmarshalJSON 解码 thread id 参数，并兼容旧 camelCase threadId。
func (p *threadIDParams) UnmarshalJSON(data []byte) error {
	type raw threadIDParams
	var current raw
	if err := rejectUnknownThreadFields(data, "thread id", threadIDParams{}, legacyThreadIDParams{}); err != nil {
		return err
	}
	if err := decodeLegacyThreadParams(data, &current, &current.ThreadID); err != nil {
		return err
	}
	*p = threadIDParams(current)
	return nil
}

type legacyThreadIDParams struct {
	ThreadID string `json:"threadId"`
}

type startParams struct {
	AgentID               string `json:"agent_id,omitempty"`
	Provider              string `json:"provider,omitempty"`
	CWD                   string `json:"cwd,omitempty"`
	Model                 string `json:"model,omitempty"`
	ModelProvider         string `json:"model_provider,omitempty"`
	ApprovalPolicy        string `json:"approval_policy,omitempty"`
	ParentAgentID         string `json:"parent_agent_id,omitempty"`
	AgentType             string `json:"agent_type,omitempty"`
	AgentMemoryScope      string `json:"agent_memory_scope,omitempty"`
	BaseInstructions      string `json:"base_instructions,omitempty"`
	DeveloperInstructions string `json:"developer_instructions,omitempty"`
	// Sandbox 是多形态 wire 字段，可为 {"type":"..."} 或字符串；这里只透传原始 JSON 给启动配置校验。
	Sandbox     json.RawMessage `json:"sandbox,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	Effort      string          `json:"effort,omitempty"`
	Personality string          `json:"personality,omitempty"`
	Language    string          `json:"language,omitempty"`
	// ToolSurfaceMode 控制本 thread 的 provider 工具面，支持 chat、auto、agent。
	ToolSurfaceMode string `json:"tool_surface_mode,omitempty"`
	// Config 是开放 key/value wire 包，schema 由调用方和 provider 约定，decodeConfigMap 只校验对象形状。
	Config json.RawMessage `json:"config,omitempty"`

	Name string `json:"name,omitempty"`
	// Prompt 仅接收旧调用方传入的 display name 别名；新请求使用 Name。
	Prompt string `json:"-"`
	// SelectedSkills / ManualSkillSelection 是启动时 UI 已知的 skill 载荷。
	// 主字段使用 snake_case，fillLegacyFields 额外读取 camelCase 以兼容旧 send path。
	SelectedSkills       []string         `json:"selected_skills,omitempty"`
	SelectedSkillRefs    []skillRefParams `json:"selected_skill_refs,omitempty"`
	ManualSkillSelection bool             `json:"manual_skill_selection,omitempty"`
	// AgentKey 是显式 agent_key 覆盖；空值表示交给路由器决定。
	AgentKey string `json:"agent_key,omitempty"`
	// PromptKey 是显式 prompt_key pin，优先级高于 agent_key。
	// 路由器必须查到该 prompt_template 才会注入；空值才回到 agent_key 或默认路由。
	PromptKey string `json:"prompt_key,omitempty"`
	// DeferSpawn 创建 pending row，真实 provider spawn 延迟到首个 turn。
	DeferSpawn     bool   `json:"defer_spawn,omitempty"`
	LaunchIntentID string `json:"launch_intent_id,omitempty"`
}

// handoffParams 是 thread/handoff 的 wire payload，保留 snake_case 字段。
type handoffParams struct {
	ThreadID       string `json:"thread_id"`
	AgentKey       string `json:"agent_key"`
	InitialMessage string `json:"initial_message,omitempty"`
}

// UnmarshalJSON 解码 thread/start 参数。
// 未知字段会被拒绝；兼容字段只在显式允许的 snake/camel 别名集合内读取。
func (p *startParams) UnmarshalJSON(data []byte) error {
	type raw startParams
	var current raw
	if err := rejectUnknownStartFields(data); err != nil {
		return err
	}
	if err := validateStartBoolFields(data); err != nil {
		return err
	}
	if err := decodeLegacyParams(data, &current, nil); err != nil {
		return err
	}
	*p = startParams(current)
	return p.fillLegacyFields(data)
}

type startParamCompatFields struct {
	AgentID               string           `json:"agentId"`
	AgentMemoryScope      string           `json:"agentMemoryScope"`
	AgentType             string           `json:"agentType"`
	ApprovalPolicy        string           `json:"approvalPolicy"`
	BaseInstructions      string           `json:"baseInstructions"`
	DeveloperInstructions string           `json:"developerInstructions"`
	Instructions          string           `json:"instructions"`
	LaunchIntentID        string           `json:"launchIntentId"`
	ManualSkillSelection  bool             `json:"manualSkillSelection"`
	MemoryScope           string           `json:"memoryScope"`
	MemoryScopeSnake      string           `json:"memory_scope"`
	ModelProvider         string           `json:"modelProvider"`
	ParentAgentID         string           `json:"parentAgentId"`
	ParentID              string           `json:"parentID"`
	ParentId              string           `json:"parentId"`
	Prompt                string           `json:"prompt"`
	SelectedSkillRefs     []skillRefParams `json:"selectedSkillRefs"`
	SelectedSkills        []string         `json:"selectedSkills"`
	ToolSurfaceMode       string           `json:"toolSurfaceMode"`
}

func rejectUnknownStartFields(data []byte) error {
	return rejectUnknownThreadFields(data, "thread/start", startParams{}, startParamCompatFields{})
}

// validateStartBoolFields 先用原始 JSON 校验 bool 别名，避免结构体解码的默认错误绕过统一 fail-fast 文案。
func validateStartBoolFields(data []byte) error {
	payload, err := decodeCompatPayload(data)
	if err != nil {
		return err
	}
	_, _, err = resolveCompatBool(payload, "manual skill selection", "manual_skill_selection", "manualSkillSelection")
	return err
}

func rejectUnknownThreadFields(data []byte, method string, wireShapes ...any) error {
	return platformrpc.RejectUnknownJSONFields(data, method, wireShapes...)
}

func (p *startParams) fillLegacyFields(data []byte) error {
	payload, err := decodeCompatPayload(data)
	if err != nil {
		return err
	}
	if err := p.fillLegacyStringFields(payload); err != nil {
		return err
	}
	if err := p.fillLegacyPromptField(payload); err != nil {
		return err
	}
	return p.fillLegacyLaunchSkillFields(payload)
}

// fillLegacyLaunchSkillFields 读取 selected skill 的 camelCase wire 别名。
// 主 JSON tag 保持 snake_case；旧 UI 的 selectedSkills/manualSkillSelection 只作为兼容输入。
func (p *startParams) fillLegacyLaunchSkillFields(payload map[string]json.RawMessage) error {
	if len(p.SelectedSkills) == 0 {
		if raw, ok := payload["selectedSkills"]; ok {
			var names []string
			if err := json.Unmarshal(raw, &names); err != nil {
				return fmt.Errorf("thread/start: selectedSkills must be a string array")
			}
			p.SelectedSkills = names
		}
	}
	if len(p.SelectedSkillRefs) == 0 {
		if raw, ok := payload["selectedSkillRefs"]; ok {
			var refs []skillRefParams
			if err := json.Unmarshal(raw, &refs); err != nil {
				return fmt.Errorf("thread/start: selectedSkillRefs must be an object array")
			}
			p.SelectedSkillRefs = refs
		}
	}
	flag, present, err := resolveCompatBool(payload, "manual skill selection", "manual_skill_selection", "manualSkillSelection")
	if err != nil {
		return err
	}
	if present {
		p.ManualSkillSelection = flag
	}
	return nil
}

type skillRefParams struct {
	Key          string `json:"key,omitempty"`
	Name         string `json:"name"`
	Scope        string `json:"scope,omitempty"`
	PersonalType string `json:"personalType,omitempty"`
	Path         string `json:"path,omitempty"`
	Source       string `json:"source,omitempty"`
}

func (p *startParams) fillLegacyStringFields(payload map[string]json.RawMessage) error {
	return assignCompatStrings(payload,
		compatStringAssignment{target: &p.ModelProvider, field: "model provider", keys: []string{"model_provider", "modelProvider"}},
		compatStringAssignment{target: &p.ApprovalPolicy, field: "approval policy", keys: []string{"approval_policy", "approvalPolicy"}},
		compatStringAssignment{target: &p.AgentID, field: "agent id", keys: []string{"agent_id", "agentId"}},
		compatStringAssignment{target: &p.ParentAgentID, field: "parent agent id", keys: []string{"parent_agent_id", "parentAgentId", "parentId", "parentID"}},
		compatStringAssignment{target: &p.LaunchIntentID, field: "launch intent id", keys: []string{"launch_intent_id", "launchIntentId"}},
		compatStringAssignment{target: &p.AgentType, field: "agent type", keys: []string{"agent_type", "agentType"}},
		compatStringAssignment{target: &p.AgentMemoryScope, field: "agent memory scope", keys: []string{"agent_memory_scope", "agentMemoryScope", "memory_scope", "memoryScope"}},
		compatStringAssignment{target: &p.BaseInstructions, field: "base instructions", keys: []string{"base_instructions", "baseInstructions", "instructions"}},
		compatStringAssignment{target: &p.DeveloperInstructions, field: "developer instructions", keys: []string{"developer_instructions", "developerInstructions"}},
		compatStringAssignment{target: &p.Name, field: "display name", keys: []string{"name"}},
		compatStringAssignment{target: &p.ToolSurfaceMode, field: "tool surface mode", keys: []string{"tool_surface_mode", "toolSurfaceMode"}},
	)
}

func (p *startParams) fillLegacyPromptField(payload map[string]json.RawMessage) error {
	prompt, present, err := resolveCompatString(payload, "prompt", "prompt")
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	p.Prompt = prompt
	return nil
}

type compatStringValue struct {
	key, value string
	present    bool
}

type compatStringAssignment struct {
	target *string
	field  string
	keys   []string
}

func decodeCompatPayload(data []byte) (map[string]json.RawMessage, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func assignCompatStrings(payload map[string]json.RawMessage, assignments ...compatStringAssignment) error {
	for _, assignment := range assignments {
		if err := assignCompatString(payload, assignment.target, assignment.field, assignment.keys...); err != nil {
			return err
		}
	}
	return nil
}

func assignCompatString(payload map[string]json.RawMessage, target *string, field string, keys ...string) error {
	value, present, err := resolveCompatString(payload, field, keys...)
	if err != nil {
		return err
	}
	if present {
		*target = value
	}
	return nil
}

// resolveCompatString 在一组兼容 key 中解析同一个字符串字段。
// 多个 key 同时出现且值不同会 fail-fast，避免新旧字段冲突时静默选择其中一个。
func resolveCompatString(payload map[string]json.RawMessage, field string, keys ...string) (string, bool, error) {
	var resolved compatStringValue
	for _, key := range keys {
		item, err := readCompatString(payload, key)
		if err != nil {
			return "", false, err
		}
		if !item.present {
			continue
		}
		if !resolved.present {
			resolved = item
			continue
		}
		if resolved.value != item.value {
			return "", false, fmt.Errorf("thread/start: conflicting %s values for %q and %q", field, resolved.key, item.key)
		}
	}
	if !resolved.present {
		return "", false, nil
	}
	return resolved.value, true, nil
}

func readCompatString(payload map[string]json.RawMessage, key string) (compatStringValue, error) {
	raw, ok := payload[key]
	if !ok {
		return compatStringValue{}, nil
	}
	var value *string
	if err := json.Unmarshal(raw, &value); err != nil {
		return compatStringValue{}, fmt.Errorf("thread/start: %s must be a string", key)
	}
	if value == nil {
		return compatStringValue{key: key, present: true}, nil
	}
	return compatStringValue{key: key, value: strings.TrimSpace(*value), present: true}, nil
}

type compatBoolValue struct {
	key     string
	value   bool
	present bool
}

// resolveCompatBool 根据原始 JSON 判断布尔别名是否出现并合并值。
// 两个别名同时出现且取值不同会直接报错，避免把显式 false 误当作未传。
func resolveCompatBool(payload map[string]json.RawMessage, field string, keys ...string) (bool, bool, error) {
	var resolved compatBoolValue
	for _, key := range keys {
		item, err := readCompatBool(payload, key)
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
			return false, false, fmt.Errorf("thread/start: conflicting %s values for %q and %q", field, resolved.key, item.key)
		}
	}
	return resolved.value, resolved.present, nil
}

func readCompatBool(payload map[string]json.RawMessage, key string) (compatBoolValue, error) {
	raw, ok := payload[key]
	if !ok {
		return compatBoolValue{}, nil
	}
	var value *bool
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return compatBoolValue{}, fmt.Errorf("thread/start: %s must be a boolean", key)
	}
	return compatBoolValue{key: key, value: *value, present: true}, nil
}

type resumeParams struct {
	ThreadID string `json:"thread_id"`
	Path     string `json:"path,omitempty"`
	CWD      string `json:"cwd,omitempty"`
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// UnmarshalJSON 解码 thread/resume 参数，并兼容旧 camelCase threadId。
func (p *resumeParams) UnmarshalJSON(data []byte) error {
	type raw resumeParams
	var current raw
	if err := rejectUnknownThreadFields(data, "thread/resume", resumeParams{}, legacyThreadIDParams{}); err != nil {
		return err
	}
	if err := decodeLegacyThreadParams(data, &current, &current.ThreadID); err != nil {
		return err
	}
	*p = resumeParams(current)
	return nil
}

type messagesParams struct {
	ThreadID string `json:"thread_id"`
	Limit    int    `json:"limit,omitempty"`
	Before   string `json:"before,omitempty"`
}

type threadInfo struct {
	ID         string `json:"id"`
	Status     string `json:"status,omitempty"`
	ForkedFrom string `json:"forkedFrom,omitempty"`
}

// UnmarshalJSON 解码 thread/messages 参数。
// before 允许字符串或整数 cursor，thread id 仍兼容旧 camelCase 字段。
func (p *messagesParams) UnmarshalJSON(data []byte) error {
	type raw struct {
		ThreadID string          `json:"thread_id"`
		Limit    int             `json:"limit,omitempty"`
		Before   json.RawMessage `json:"before,omitempty"`
	}
	var current raw
	if err := rejectUnknownThreadFields(data, "thread/messages", raw{}, legacyThreadIDParams{}); err != nil {
		return err
	}
	if err := decodeLegacyParams(data, &current, nil); err != nil {
		return err
	}
	p.ThreadID = current.ThreadID
	p.Limit = current.Limit
	before, err := decodeMessagesBefore(current.Before)
	if err != nil {
		return err
	}
	p.Before = before
	return fillLegacyThreadID(data, &p.ThreadID)
}

func decodeMessagesBefore(raw json.RawMessage) (string, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return "", nil
	}
	var cursor string
	if err := json.Unmarshal(raw, &cursor); err == nil {
		return strings.TrimSpace(cursor), nil
	}
	dec := json.NewDecoder(strings.NewReader(value))
	dec.UseNumber()
	var number json.Number
	if err := dec.Decode(&number); err == nil {
		return strings.TrimSpace(number.String()), nil
	}
	return "", platformrpc.ErrInvalidParams("thread/messages: before must be a string or integer")
}

type nameSetParams struct {
	ThreadID string `json:"thread_id"`
	Name     string `json:"name"`
}

// UnmarshalJSON 解码 thread/name/set 参数，并兼容旧 camelCase threadId。
func (p *nameSetParams) UnmarshalJSON(data []byte) error {
	type raw nameSetParams
	var current raw
	if err := rejectUnknownThreadFields(data, "thread/name/set", nameSetParams{}, legacyThreadIDParams{}); err != nil {
		return err
	}
	if err := decodeLegacyThreadParams(data, &current, &current.ThreadID); err != nil {
		return err
	}
	*p = nameSetParams(current)
	return nil
}

type commandParams struct {
	ThreadID string `json:"thread_id"`
	Args     string `json:"args,omitempty"`
}

// UnmarshalJSON 解码低频 command 参数，并兼容旧 camelCase threadId。
func (p *commandParams) UnmarshalJSON(data []byte) error {
	type raw commandParams
	var current raw
	if err := rejectUnknownThreadFields(data, "thread command", commandParams{}, legacyThreadIDParams{}); err != nil {
		return err
	}
	if err := decodeLegacyThreadParams(data, &current, &current.ThreadID); err != nil {
		return err
	}
	*p = commandParams(current)
	return nil
}

type approvalsSetParams struct {
	ThreadID string `json:"thread_id"`
	Args     string `json:"args,omitempty"`
	Policy   string `json:"policy,omitempty"`
}

// UnmarshalJSON 解码 approvals 更新参数，并兼容旧 camelCase threadId。
func (p *approvalsSetParams) UnmarshalJSON(data []byte) error {
	type raw approvalsSetParams
	var current raw
	if err := rejectUnknownThreadFields(data, "thread approvals", approvalsSetParams{}, legacyThreadIDParams{}); err != nil {
		return err
	}
	if err := decodeLegacyThreadParams(data, &current, &current.ThreadID); err != nil {
		return err
	}
	*p = approvalsSetParams(current)
	return nil
}

type configGetParams struct {
	ThreadID string `json:"thread_id"`
}

// UnmarshalJSON 解码 thread/config/get 参数，并兼容旧 camelCase threadId。
func (p *configGetParams) UnmarshalJSON(data []byte) error {
	type raw configGetParams
	var current raw
	if err := rejectUnknownThreadFields(data, "thread/config/get", configGetParams{}, legacyThreadIDParams{}); err != nil {
		return err
	}
	if err := decodeLegacyThreadParams(data, &current, &current.ThreadID); err != nil {
		return err
	}
	*p = configGetParams(current)
	return nil
}

type configSetParams struct {
	ThreadID string  `json:"thread_id"`
	Model    *string `json:"model,omitempty"`
	Effort   *string `json:"effort,omitempty"`
}

// UnmarshalJSON 解码 thread/config/set 参数，并兼容旧 camelCase threadId。
func (p *configSetParams) UnmarshalJSON(data []byte) error {
	type raw configSetParams
	var current raw
	if err := rejectUnknownThreadFields(data, "thread/config/set", configSetParams{}, legacyThreadIDParams{}); err != nil {
		return err
	}
	if err := decodeLegacyThreadParams(data, &current, &current.ThreadID); err != nil {
		return err
	}
	*p = configSetParams(current)
	return nil
}

type modelSetParams struct {
	ThreadID string `json:"thread_id"`
	Model    string `json:"model,omitempty"`
	Args     string `json:"args,omitempty"`
}

// UnmarshalJSON 解码 thread/model/set 参数，并兼容旧 camelCase threadId。
func (p *modelSetParams) UnmarshalJSON(data []byte) error {
	type raw modelSetParams
	var current raw
	if err := rejectUnknownThreadFields(data, "thread/model/set", modelSetParams{}, legacyThreadIDParams{}); err != nil {
		return err
	}
	if err := decodeLegacyThreadParams(data, &current, &current.ThreadID); err != nil {
		return err
	}
	*p = modelSetParams(current)
	return nil
}

type compactStartParams struct {
	ThreadID string `json:"thread_id"`
	Args     string `json:"args,omitempty"`
}

// UnmarshalJSON 解码 thread/compact/start 参数，并兼容旧 camelCase threadId。
func (p *compactStartParams) UnmarshalJSON(data []byte) error {
	type raw compactStartParams
	var current raw
	if err := rejectUnknownThreadFields(data, "thread/compact/start", compactStartParams{}, legacyThreadIDParams{}); err != nil {
		return err
	}
	if err := decodeLegacyThreadParams(data, &current, &current.ThreadID); err != nil {
		return err
	}
	*p = compactStartParams(current)
	return nil
}

func decodeLegacyThreadParams[T any](data []byte, current *T, threadID *string) error {
	return decodeLegacyParams(data, current, func(rawData []byte, _ *T) error {
		return fillLegacyThreadID(rawData, threadID)
	})
}

func fillLegacyThreadID(data []byte, threadID *string) error {
	if strings.TrimSpace(*threadID) != "" {
		return nil
	}
	var legacy struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	*threadID = strings.TrimSpace(legacy.ThreadID)
	return nil
}

// ---------------------------------------------------------------------------
// 响应结构体是 map[string]any 返回值的 wire-compatible 替代。
// JSON tag 必须保持原有 key，避免旧前端或测试 fixture 读不到字段。
// ---------------------------------------------------------------------------

// startEffectiveResponse 是 thread/start 响应中的 effective 子对象。
type startEffectiveResponse struct {
	Model          string `json:"model"`
	Provider       string `json:"provider"`
	ModelProvider  string `json:"modelProvider"`
	CWD            string `json:"cwd"`
	ApprovalPolicy string `json:"approvalPolicy"`
}

// startResponse 是 thread/start 的 wire 响应。
// snake_case 与 camelCase 身份字段并存，用于兼容不同 UI 版本。
type startResponse struct {
	Thread         threadInfo             `json:"thread"`
	ThreadID       string                 `json:"threadId"`
	ThreadIDSnake  string                 `json:"thread_id"`
	SessionID      string                 `json:"sessionId"`
	SessionIDSnake string                 `json:"session_id"`
	Status         string                 `json:"status"`
	AgentID        string                 `json:"agentId"`
	AgentIDSnake   string                 `json:"agent_id"`
	Model          string                 `json:"model"`
	Provider       string                 `json:"provider"`
	ModelProvider  string                 `json:"modelProvider"`
	CWD            string                 `json:"cwd"`
	ApprovalPolicy string                 `json:"approvalPolicy"`
	Effective      startEffectiveResponse `json:"effective"`

	// 可选字段为 nil 时省略，避免旧调用方看到空指针语义。
	AgentKey         *string `json:"agent_key,omitempty"`
	AgentKeyCamel    *string `json:"agentKey,omitempty"`
	AgentTitle       *string `json:"agent_title,omitempty"`
	AgentTitleCamel  *string `json:"agentTitle,omitempty"`
	PromptKey        *string `json:"prompt_key,omitempty"`
	PromptKeyCamel   *string `json:"promptKey,omitempty"`
	PromptVersionID  *int64  `json:"prompt_version_id,omitempty"`
	PromptVersionIDC *int64  `json:"promptVersionId,omitempty"`
	// PromptKeyStale 表示调用方传入的 prompt_key 未命中启用模板。
	// UI 同时监听 snake_case 和 camelCase，看到 true 后清理本地 activePromptKey 偏好并提示用户。
	PromptKeyStale      *bool `json:"prompt_key_stale,omitempty"`
	PromptKeyStaleCamel *bool `json:"promptKeyStale,omitempty"`
	PendingLaunch       *bool `json:"pending_launch,omitempty"`
	PendingLaunchC      *bool `json:"pendingLaunch,omitempty"`
}

// attachPromptKeyStale 在 router 判定 prompt_key 失效时写入双 key 指针。
// 放在响应 DTO 旁边可让 buildStartResponse 保持简单，同时测试覆盖 snake/camel 和 omitempty 行为。
func attachPromptKeyStale(resp *startResponse, stale bool) {
	if !stale {
		return
	}
	resp.PromptKeyStale = &stale
	resp.PromptKeyStaleCamel = &stale
}

// forkResponse 是 thread/fork 的 wire 响应。
type forkResponse struct {
	Thread            threadInfo `json:"thread"`
	KickoffState      string     `json:"kickoff_state,omitempty"`
	KickoffStateCamel string     `json:"kickoffState,omitempty"`
}

// handoffResponse 是 thread/handoff 的 wire 响应，保留 snake/camel 双字段给不同 UI 版本读取。
type handoffResponse struct {
	SourceThreadID      string     `json:"source_thread_id"`
	SourceThreadIDCamel string     `json:"sourceThreadId"`
	NewThreadID         string     `json:"new_thread_id"`
	NewThreadIDCamel    string     `json:"newThreadId"`
	Thread              threadInfo `json:"thread"`
	AgentID             string     `json:"agent_id"`
	AgentIDCamel        string     `json:"agentId"`
	Status              string     `json:"status"`

	AgentKey         *string `json:"agent_key,omitempty"`
	AgentKeyCamel    *string `json:"agentKey,omitempty"`
	PromptKey        *string `json:"prompt_key,omitempty"`
	PromptKeyCamel   *string `json:"promptKey,omitempty"`
	PromptVersionID  *int64  `json:"prompt_version_id,omitempty"`
	PromptVersionIDC *int64  `json:"promptVersionId,omitempty"`
}

// recoverResponse 是 thread/recover 的 wire 响应。
type recoverResponse struct {
	Thread    threadInfo `json:"thread"`
	Recovered bool       `json:"recovered"`
	Mode      string     `json:"mode"`
}

// resumeResponse 是 thread/resume 的 wire 响应，保留 thread/session 的 snake/camel 双字段。
type resumeResponse struct {
	Thread         threadInfo `json:"thread"`
	ThreadID       string     `json:"threadId"`
	ThreadIDSnake  string     `json:"thread_id"`
	SessionID      string     `json:"sessionId"`
	SessionIDSnake string     `json:"session_id"`
	Status         string     `json:"status"`
	Model          string     `json:"model"`
	CWD            string     `json:"cwd"`
}
