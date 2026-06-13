package turn

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

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

// UnmarshalJSON 解码JSON。
func (p *turnStartParams) UnmarshalJSON(data []byte) error {
	var legacy struct {
		ThreadID             string           `json:"threadId"`
		SelectedSkills       []string         `json:"selectedSkills"`
		SelectedSkillRefs    []skillRefParams `json:"selectedSkillRefs"`
		ManualSkillSelection *bool            `json:"manualSkillSelection"`
		ApprovalPolicy       string           `json:"approvalPolicy"`
		OutputSchema         json.RawMessage  `json:"outputSchema"`
	}
	type raw turnStartParams
	return decodeLegacyTurnParams(data, (*raw)(p), &legacy, func(current *raw, legacy *struct {
		ThreadID             string           `json:"threadId"`
		SelectedSkills       []string         `json:"selectedSkills"`
		SelectedSkillRefs    []skillRefParams `json:"selectedSkillRefs"`
		ManualSkillSelection *bool            `json:"manualSkillSelection"`
		ApprovalPolicy       string           `json:"approvalPolicy"`
		OutputSchema         json.RawMessage  `json:"outputSchema"`
	}) error {
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
	})
}

type turnInputItemParams struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	URL     string `json:"url,omitempty"`
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

type skillRefParams struct {
	Key          string `json:"key,omitempty"`
	Name         string `json:"name"`
	Scope        string `json:"scope,omitempty"`
	PersonalType string `json:"personalType,omitempty"`
	Path         string `json:"path,omitempty"`
	Source       string `json:"source,omitempty"`
}

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

// UnmarshalJSON 解码JSON。
func (p *turnSteerParams) UnmarshalJSON(data []byte) error {
	var legacy struct {
		ThreadID             string           `json:"threadId"`
		ExpectedTurnID       string           `json:"expectedTurnId"`
		SelectedSkills       []string         `json:"selectedSkills"`
		SelectedSkillRefs    []skillRefParams `json:"selectedSkillRefs"`
		ManualSkillSelection *bool            `json:"manualSkillSelection"`
	}
	type raw turnSteerParams
	return decodeLegacyTurnParams(data, (*raw)(p), &legacy, func(current *raw, legacy *struct {
		ThreadID             string           `json:"threadId"`
		ExpectedTurnID       string           `json:"expectedTurnId"`
		SelectedSkills       []string         `json:"selectedSkills"`
		SelectedSkillRefs    []skillRefParams `json:"selectedSkillRefs"`
		ManualSkillSelection *bool            `json:"manualSkillSelection"`
	}) error {
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
	})
}

type turnInterruptParams struct {
	ThreadID string `json:"thread_id"`
	Source   string `json:"source,omitempty"`
}

// UnmarshalJSON 解码JSON。
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

var turnInterruptParamFields = map[string]struct{}{
	"source":    {},
	"thread_id": {},
	"threadId":  {},
	"threadID":  {},
}

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

type threadIDOnlyParams struct {
	ThreadID string `json:"thread_id"`
}

// UnmarshalJSON 解码JSON。
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

type approvalRespondParams struct {
	CallID    string          `json:"call_id,omitempty"`
	RequestID *int64          `json:"request_id,omitempty"`
	Approved  *bool           `json:"approved,omitempty"`
	Decision  json.RawMessage `json:"decision,omitempty"`
}

// UnmarshalJSON 解码JSON。
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

type turnForceCompleteResult struct {
	OK             bool `json:"ok"`
	ForceCompleted bool `json:"forceCompleted"`
}

type turnStartResult struct {
	TurnID string `json:"turn_id"`
	// Routing surfaced only for pending_launch threads whose first turn/start
	// triggers SpawnIfNeeded. Eager-path threads already receive routing on
	// thread/start; repeating it here is pointless and harmless (all four
	// fields are zero/nil when turn/start is a no-op on the spawn axis and
	// get elided by omitempty).
	AgentKey            string `json:"agent_key,omitempty"`
	AgentTitle          string `json:"agent_title,omitempty"`
	PromptKey           string `json:"prompt_key,omitempty"`
	PromptVersionID     *int64 `json:"prompt_version_id,omitempty"`
	PromptKeyStale      *bool  `json:"prompt_key_stale,omitempty"`
	PromptKeyStaleCamel *bool  `json:"promptKeyStale,omitempty"`
}

func attachTurnPromptKeyStale(resp *turnStartResult, stale bool) {
	if resp == nil || !stale {
		return
	}
	resp.PromptKeyStale = &stale
	resp.PromptKeyStaleCamel = &stale
}
