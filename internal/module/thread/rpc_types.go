package thread

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type threadIDParams struct {
	ThreadID string `json:"thread_id"`
}

func (p *threadIDParams) UnmarshalJSON(data []byte) error {
	type raw threadIDParams
	var current raw
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
		return err
	}
	*p = threadIDParams(current)
	return nil
}

type startParams struct {
	Provider              string          `json:"provider,omitempty"`
	CWD                   string          `json:"cwd,omitempty"`
	Model                 string          `json:"model,omitempty"`
	ModelProvider         string          `json:"model_provider,omitempty"`
	ApprovalPolicy        string          `json:"approval_policy,omitempty"`
	ParentAgentID         string          `json:"parent_agent_id,omitempty"`
	AgentType             string          `json:"agent_type,omitempty"`
	AgentMemoryScope      string          `json:"agent_memory_scope,omitempty"`
	BaseInstructions      string          `json:"base_instructions,omitempty"`
	DeveloperInstructions string          `json:"developer_instructions,omitempty"`
	Sandbox               json.RawMessage `json:"sandbox,omitempty"`
	Summary               string          `json:"summary,omitempty"`
	Effort                string          `json:"effort,omitempty"`
	Personality           string          `json:"personality,omitempty"`
	Config                json.RawMessage `json:"config,omitempty"`
	Name                  string          `json:"name,omitempty"`
	// Deprecated: use Name for display-name semantics; Prompt is kept only for legacy callers.
	Prompt string `json:"-"`
	// SelectedSkills / ManualSkillSelection p20.3 §4.3：launch 时 UI 已知的 skill 载荷。
	// 主使用 snake_case；`fillLegacyFields` 额外读 camelCase 别名（与
	// send path `selectedSkills` / `manualSkillSelection` 对齐）。
	SelectedSkills       []string `json:"selected_skills,omitempty"`
	ManualSkillSelection bool     `json:"manual_skill_selection,omitempty"`
	// Optional explicit agent_key override. Empty = let the router decide.
	AgentKey string `json:"agent_key,omitempty"`
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
	if err := decodeLegacyParams(data, &current, nil); err != nil {
		return err
	}
	*p = startParams(current)
	return p.fillLegacyFields(data)
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

func (p *startParams) fillLegacyStringFields(payload map[string]json.RawMessage) error {
	return assignCompatStrings(payload,
		compatStringAssignment{target: &p.ModelProvider, field: "model provider", keys: []string{"model_provider", "modelProvider"}},
		compatStringAssignment{target: &p.ApprovalPolicy, field: "approval policy", keys: []string{"approval_policy", "approvalPolicy"}},
		compatStringAssignment{target: &p.ParentAgentID, field: "parent agent id", keys: []string{"parent_agent_id", "parentAgentId", "parentId", "parentID"}},
		compatStringAssignment{target: &p.AgentType, field: "agent type", keys: []string{"agent_type", "agentType"}},
		compatStringAssignment{target: &p.AgentMemoryScope, field: "agent memory scope", keys: []string{"agent_memory_scope", "agentMemoryScope", "memory_scope", "memoryScope"}},
		compatStringAssignment{target: &p.BaseInstructions, field: "base instructions", keys: []string{"base_instructions", "baseInstructions", "instructions"}},
		compatStringAssignment{target: &p.DeveloperInstructions, field: "developer instructions", keys: []string{"developer_instructions", "developerInstructions"}},
		compatStringAssignment{target: &p.Name, field: "display name", keys: []string{"name", "prompt"}},
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
	key     string
	value   string
	present bool
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
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
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
	return "", errors.New("thread/messages: before must be a string or integer")
}

type nameSetParams struct {
	ThreadID string `json:"thread_id"`
	Name     string `json:"name"`
}

func (p *nameSetParams) UnmarshalJSON(data []byte) error {
	type raw nameSetParams
	var current raw
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
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
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
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
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
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
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
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
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
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
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
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
	if err := decodeLegacyParams(data, &current, func(rawData []byte, current *raw) error {
		return fillLegacyThreadID(rawData, &current.ThreadID)
	}); err != nil {
		return err
	}
	*p = compactStartParams(current)
	return nil
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
