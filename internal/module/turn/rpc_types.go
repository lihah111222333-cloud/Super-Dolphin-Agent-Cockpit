package turn

import (
	"encoding/json"
	"strings"
)

type turnStartParams struct {
	ThreadID string                `json:"thread_id"`
	Prompt   string                `json:"prompt,omitempty"`
	Images   []string              `json:"images,omitempty"`
	Files    []string              `json:"files,omitempty"`
	Input    []turnInputItemParams `json:"input,omitempty"`

	SelectedSkills       []string        `json:"selected_skills,omitempty"`
	ManualSkillSelection bool            `json:"manual_skill_selection,omitempty"`
	CWD                  string          `json:"cwd,omitempty"`
	ApprovalPolicy       string          `json:"approval_policy,omitempty"`
	Model                string          `json:"model,omitempty"`
	Effort               string          `json:"effort,omitempty"`
	OutputSchema         json.RawMessage `json:"output_schema,omitempty"`
}

func (p *turnStartParams) UnmarshalJSON(data []byte) error {
	type raw turnStartParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = turnStartParams(current)
	var legacy struct {
		ThreadID             string          `json:"threadId"`
		SelectedSkills       []string        `json:"selectedSkills"`
		ManualSkillSelection *bool           `json:"manualSkillSelection"`
		ApprovalPolicy       string          `json:"approvalPolicy"`
		OutputSchema         json.RawMessage `json:"outputSchema"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if err := fillLegacyThreadID(data, &p.ThreadID); err != nil {
		return err
	}
	if len(p.SelectedSkills) == 0 && len(legacy.SelectedSkills) > 0 {
		p.SelectedSkills = append([]string(nil), legacy.SelectedSkills...)
	}
	if !p.ManualSkillSelection && legacy.ManualSkillSelection != nil {
		p.ManualSkillSelection = *legacy.ManualSkillSelection
	}
	if strings.TrimSpace(p.ApprovalPolicy) == "" {
		p.ApprovalPolicy = strings.TrimSpace(legacy.ApprovalPolicy)
	}
	if len(p.OutputSchema) == 0 {
		p.OutputSchema = append(json.RawMessage(nil), legacy.OutputSchema...)
	}
	return nil
}

type turnInputItemParams struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	URL     string `json:"url,omitempty"`
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

type turnSteerParams struct {
	ThreadID             string                `json:"thread_id"`
	ExpectedTurnID       string                `json:"expected_turn_id,omitempty"`
	Prompt               string                `json:"prompt,omitempty"`
	Input                []turnInputItemParams `json:"input,omitempty"`
	SelectedSkills       []string              `json:"selected_skills,omitempty"`
	ManualSkillSelection bool                  `json:"manual_skill_selection,omitempty"`
}

func (p *turnSteerParams) UnmarshalJSON(data []byte) error {
	type raw turnSteerParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = turnSteerParams(current)
	var legacy struct {
		ThreadID             string   `json:"threadId"`
		ExpectedTurnID       string   `json:"expectedTurnId"`
		SelectedSkills       []string `json:"selectedSkills"`
		ManualSkillSelection *bool    `json:"manualSkillSelection"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if err := fillLegacyThreadID(data, &p.ThreadID); err != nil {
		return err
	}
	if strings.TrimSpace(p.ExpectedTurnID) == "" {
		p.ExpectedTurnID = strings.TrimSpace(legacy.ExpectedTurnID)
	}
	if len(p.SelectedSkills) == 0 && len(legacy.SelectedSkills) > 0 {
		p.SelectedSkills = append([]string(nil), legacy.SelectedSkills...)
	}
	if !p.ManualSkillSelection && legacy.ManualSkillSelection != nil {
		p.ManualSkillSelection = *legacy.ManualSkillSelection
	}
	return nil
}

type turnInterruptParams struct {
	ThreadID string `json:"thread_id"`
	Source   string `json:"source,omitempty"`
}

func (p *turnInterruptParams) UnmarshalJSON(data []byte) error {
	type raw turnInterruptParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = turnInterruptParams(current)
	return fillLegacyThreadID(data, &p.ThreadID)
}

type threadIDOnlyParams struct {
	ThreadID string `json:"thread_id"`
}

func (p *threadIDOnlyParams) UnmarshalJSON(data []byte) error {
	type raw threadIDOnlyParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = threadIDOnlyParams(current)
	return fillLegacyThreadID(data, &p.ThreadID)
}

type approvalRespondParams struct {
	CallID    string          `json:"call_id,omitempty"`
	RequestID *int64          `json:"request_id,omitempty"`
	Approved  *bool           `json:"approved,omitempty"`
	Decision  json.RawMessage `json:"decision,omitempty"`
}

func (p *approvalRespondParams) UnmarshalJSON(data []byte) error {
	type raw approvalRespondParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = approvalRespondParams(current)
	var legacy struct {
		CallID    string          `json:"callId"`
		RequestID *int64          `json:"requestId"`
		Approved  *bool           `json:"approved"`
		Decision  json.RawMessage `json:"decision"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if strings.TrimSpace(p.CallID) == "" {
		p.CallID = strings.TrimSpace(legacy.CallID)
	}
	if p.RequestID == nil && legacy.RequestID != nil {
		value := *legacy.RequestID
		p.RequestID = &value
	}
	if p.Approved == nil && legacy.Approved != nil {
		value := *legacy.Approved
		p.Approved = &value
	}
	if len(p.Decision) == 0 {
		p.Decision = append(json.RawMessage(nil), legacy.Decision...)
	}
	return nil
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
