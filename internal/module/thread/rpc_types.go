package thread

import (
	"encoding/json"
	"fmt"
	"strings"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

type threadIDParams struct {
	ThreadID string `json:"thread_id"`
}

func (p *threadIDParams) UnmarshalJSON(data []byte) error {
	type raw threadIDParams
	var current raw
	if err := decodeLegacyThreadParams(data, &current, &current.ThreadID); err != nil {
		return err
	}
	*p = threadIDParams(current)
	return nil
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
	// json.RawMessage: justified -- polymorphic wire shape (object {"type":"..."} OR plain string);
	// consumed via isDangerFullAccessSandbox and forwarded opaquely to provider config.
	Sandbox     json.RawMessage `json:"sandbox,omitempty"`
	Summary     string          `json:"summary,omitempty"`
	Effort      string          `json:"effort,omitempty"`
	Personality string          `json:"personality,omitempty"`
	Language    string          `json:"language,omitempty"`
	// ToolSurfaceMode controls the provider tool surface for this thread.
	// Supported values: chat, auto, agent.
	ToolSurfaceMode string `json:"tool_surface_mode,omitempty"`
	// json.RawMessage: justified -- open-ended key/value config bag decoded via
	// decodeConfigMap into map[string]any; schema is caller-defined, not fixed.
	Config json.RawMessage `json:"config,omitempty"`

	Name string `json:"name,omitempty"`
	// Deprecated: use Name for display-name semantics; Prompt is kept only for legacy callers.
	Prompt string `json:"-"`
	// SelectedSkills / ManualSkillSelection p20.3 §4.3：launch 时 UI 已知的 skill 载荷。
	// 主使用 snake_case；`fillLegacyFields` 额外读 camelCase 别名（与
	// send path `selectedSkills` / `manualSkillSelection` 对齐）。
	SelectedSkills       []string         `json:"selected_skills,omitempty"`
	SelectedSkillRefs    []skillRefParams `json:"selected_skill_refs,omitempty"`
	ManualSkillSelection bool             `json:"manual_skill_selection,omitempty"`
	// Optional explicit agent_key override. Empty = let the router decide.
	AgentKey string `json:"agent_key,omitempty"`
	// Optional explicit prompt_key pin. Surfaces the SystemPromptPage's
	// "set as launch prompt" preference. Takes precedence over agent_key:
	// the router looks up this exact prompt_template row and injects its
	// PromptText as BaseInstructions. Empty = fall back to agent_key /
	// default routing.
	PromptKey string `json:"prompt_key,omitempty"`
	// DeferSpawn creates a pending row; the actual spawn happens on first turn.
	DeferSpawn     bool   `json:"defer_spawn,omitempty"`
	LaunchIntentID string `json:"launch_intent_id,omitempty"`
}

// handoffParams is the RPC payload for thread/handoff.
type handoffParams struct {
	ThreadID       string `json:"thread_id"`
	AgentKey       string `json:"agent_key"`
	InitialMessage string `json:"initial_message,omitempty"`
}

func (p *startParams) UnmarshalJSON(data []byte) error {
	type raw startParams
	var current raw
	if err := rejectUnknownStartParamFields(data); err != nil {
		return err
	}
	if err := decodeLegacyParams(data, &current, nil); err != nil {
		return err
	}
	*p = startParams(current)
	return p.fillLegacyFields(data)
}

var startParamWireFields = map[string]struct{}{
	"agent_id":               {},
	"agent_type":             {},
	"agent_key":              {},
	"agent_memory_scope":     {},
	"approval_policy":        {},
	"base_instructions":      {},
	"config":                 {},
	"cwd":                    {},
	"defer_spawn":            {},
	"developer_instructions": {},
	"effort":                 {},
	"language":               {},
	"launch_intent_id":       {},
	"manual_skill_selection": {},
	"model":                  {},
	"model_provider":         {},
	"name":                   {},
	"parent_agent_id":        {},
	"personality":            {},
	"prompt":                 {},
	"prompt_key":             {},
	"provider":               {},
	"sandbox":                {},
	"selected_skill_refs":    {},
	"selected_skills":        {},
	"summary":                {},
	"tool_surface_mode":      {},
	"agentId":                {},
	"agentMemoryScope":       {},
	"agentType":              {},
	"approvalPolicy":         {},
	"baseInstructions":       {},
	"developerInstructions":  {},
	"instructions":           {},
	"launchIntentId":         {},
	"manualSkillSelection":   {},
	"memoryScope":            {},
	"memory_scope":           {},
	"modelProvider":          {},
	"parentAgentId":          {},
	"parentID":               {},
	"parentId":               {},
	"selectedSkillRefs":      {},
	"selectedSkills":         {},
	"toolSurfaceMode":        {},
}

func rejectUnknownStartParamFields(data []byte) error {
	payload, err := decodeCompatPayload(data)
	if err != nil {
		return err
	}
	for key := range payload {
		if _, ok := startParamWireFields[key]; !ok {
			return fmt.Errorf("thread/start: unknown field %q", key)
		}
	}
	return nil
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

// fillLegacyLaunchSkillFields p20.3 §4.3：容忍 camelCase `selectedSkills` /
// `manualSkillSelection` 别名。主 tag 仍为 snake_case，前端 send path 早已发
// camelCase，launch payload 对齐后不会额外介绍接口表面。
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
	if !p.ManualSkillSelection {
		if raw, ok := payload["manualSkillSelection"]; ok {
			var flag bool
			if err := json.Unmarshal(raw, &flag); err != nil {
				return fmt.Errorf("thread/start: manualSkillSelection must be a boolean")
			}
			p.ManualSkillSelection = flag
		}
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

type resumeParams struct {
	ThreadID string `json:"thread_id"`
	Path     string `json:"path,omitempty"`
	CWD      string `json:"cwd,omitempty"`
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
}

func (p *resumeParams) UnmarshalJSON(data []byte) error {
	type raw resumeParams
	var current raw
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

func (p *messagesParams) UnmarshalJSON(data []byte) error {
	type raw struct {
		ThreadID string          `json:"thread_id"`
		Limit    int             `json:"limit,omitempty"`
		Before   json.RawMessage `json:"before,omitempty"`
	}
	var current raw
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

func (p *nameSetParams) UnmarshalJSON(data []byte) error {
	type raw nameSetParams
	var current raw
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

func (p *commandParams) UnmarshalJSON(data []byte) error {
	type raw commandParams
	var current raw
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

func (p *approvalsSetParams) UnmarshalJSON(data []byte) error {
	type raw approvalsSetParams
	var current raw
	if err := decodeLegacyThreadParams(data, &current, &current.ThreadID); err != nil {
		return err
	}
	*p = approvalsSetParams(current)
	return nil
}

type configGetParams struct {
	ThreadID string `json:"thread_id"`
}

func (p *configGetParams) UnmarshalJSON(data []byte) error {
	type raw configGetParams
	var current raw
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

func (p *configSetParams) UnmarshalJSON(data []byte) error {
	type raw configSetParams
	var current raw
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

func (p *modelSetParams) UnmarshalJSON(data []byte) error {
	type raw modelSetParams
	var current raw
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

func (p *compactStartParams) UnmarshalJSON(data []byte) error {
	type raw compactStartParams
	var current raw
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
// Response structs — wire-compatible replacements for map[string]any returns.
// JSON tags MUST match the original map keys exactly.
// ---------------------------------------------------------------------------

// startEffectiveResponse is the nested "effective" object inside startResponse.
type startEffectiveResponse struct {
	Model          string `json:"model"`
	Provider       string `json:"provider"`
	ModelProvider  string `json:"modelProvider"`
	CWD            string `json:"cwd"`
	ApprovalPolicy string `json:"approvalPolicy"`
}

// startResponse is the wire response for thread/start.
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

	// Optional fields — omitted from JSON when zero/nil.
	AgentKey         *string `json:"agent_key,omitempty"`
	AgentKeyCamel    *string `json:"agentKey,omitempty"`
	AgentTitle       *string `json:"agent_title,omitempty"`
	AgentTitleCamel  *string `json:"agentTitle,omitempty"`
	PromptKey        *string `json:"prompt_key,omitempty"`
	PromptKeyCamel   *string `json:"promptKey,omitempty"`
	PromptVersionID  *int64  `json:"prompt_version_id,omitempty"`
	PromptVersionIDC *int64  `json:"promptVersionId,omitempty"`
	// PromptKeyStale: true when the caller-supplied prompt_key did not
	// resolve to an enabled prompt_template row. The UI listens for either
	// the snake_case or camelCase variant and clears its activePromptKey
	// pref + notifies the user when it sees true.
	PromptKeyStale      *bool `json:"prompt_key_stale,omitempty"`
	PromptKeyStaleCamel *bool `json:"promptKeyStale,omitempty"`
	PendingLaunch       *bool `json:"pending_launch,omitempty"`
	PendingLaunchC      *bool `json:"pendingLaunch,omitempty"`
}

// attachPromptKeyStale stamps the dual-key prompt_key_stale pointers on a
// thread/start response when the router flagged the caller-supplied
// prompt_key as stale (template deleted / disabled). Sits in rpc_types.go
// alongside startResponse so buildStartResponse can keep its cyclomatic
// ratchet baseline; tests in rpc_types_test.go cover both snake/camel wire
// keys + the happy-path omitempty contract.
func attachPromptKeyStale(resp *startResponse, stale bool) {
	if !stale {
		return
	}
	resp.PromptKeyStale = &stale
	resp.PromptKeyStaleCamel = &stale
}

// forkResponse is the wire response for thread/fork.
type forkResponse struct {
	Thread threadInfo `json:"thread"`
}

// handoffResponse is the wire response for thread/handoff.
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

// recoverResponse is the wire response for thread/recover.
type recoverResponse struct {
	Thread    threadInfo `json:"thread"`
	Recovered bool       `json:"recovered"`
	Mode      string     `json:"mode"`
}

// resumeResponse is the wire response for thread/resume.
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
