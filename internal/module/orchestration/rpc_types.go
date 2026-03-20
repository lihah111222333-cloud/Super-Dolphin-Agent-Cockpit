package orchestration

import (
	"encoding/json"
	"strings"
)

// TODO(p5-wave2): V2 agent.launch also carries prompt/instructions/dynamic_tools/config.
// Exposing those fields here requires a richer orchestration LaunchRequest contract.
type launchParams struct {
	AgentID  string            `json:"agentId"`
	Name     string            `json:"name,omitempty"`
	CWD      string            `json:"cwd,omitempty"`
	Command  []string          `json:"command,omitempty"`
	ParentID string            `json:"parentId,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

type agentIDParams struct {
	AgentID string `json:"agentId"`
}

func (p *agentIDParams) UnmarshalJSON(data []byte) error {
	type raw agentIDParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = agentIDParams(current)
	var legacy struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if strings.TrimSpace(p.AgentID) == "" {
		p.AgentID = strings.TrimSpace(legacy.AgentID)
	}
	return nil
}

type dagKeyParams struct {
	DagKey string `json:"dagKey"`
}

type dagNodeParams struct {
	DagKey  string `json:"dagKey"`
	NodeKey string `json:"nodeKey"`
}

type createDAGParams struct {
	DagKey      string                `json:"dagKey"`
	Title       string                `json:"title"`
	Description string                `json:"description,omitempty"`
	CreatedBy   string                `json:"createdBy,omitempty"`
	Metadata    json.RawMessage       `json:"metadata,omitempty"`
	Nodes       []createDAGNodeParams `json:"nodes,omitempty"`
}

type createDAGNodeParams struct {
	NodeKey    string          `json:"nodeKey"`
	Title      string          `json:"title"`
	NodeType   string          `json:"nodeType,omitempty"`
	AssignedTo string          `json:"assignedTo,omitempty"`
	DependsOn  []string        `json:"dependsOn,omitempty"`
	CommandRef string          `json:"commandRef,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
}

type submitParams struct {
	AgentID string   `json:"agent_id"`
	Prompt  string   `json:"prompt"`
	Images  []string `json:"images"`
	Files   []string `json:"files"`

	SelectedSkills       []string        `json:"selected_skills,omitempty"`
	ManualSkillSelection bool            `json:"manual_skill_selection,omitempty"`
	OutputSchema         json.RawMessage `json:"output_schema,omitempty"`

	legacyInput json.RawMessage
}

type submitPromptParams = submitParams

func (p *submitParams) UnmarshalJSON(data []byte) error {
	type raw submitParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = submitParams(current)
	var legacy struct {
		AgentID              string          `json:"agentId"`
		Input                json.RawMessage `json:"input"`
		SelectedSkills       []string        `json:"selectedSkills"`
		ManualSkillSelection *bool           `json:"manualSkillSelection"`
		OutputSchema         json.RawMessage `json:"outputSchema"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if strings.TrimSpace(p.AgentID) == "" {
		p.AgentID = strings.TrimSpace(legacy.AgentID)
	}
	if len(p.SelectedSkills) == 0 && len(legacy.SelectedSkills) > 0 {
		p.SelectedSkills = append([]string(nil), legacy.SelectedSkills...)
	}
	if !p.ManualSkillSelection && legacy.ManualSkillSelection != nil {
		p.ManualSkillSelection = *legacy.ManualSkillSelection
	}
	if len(p.OutputSchema) == 0 {
		p.OutputSchema = append(json.RawMessage(nil), legacy.OutputSchema...)
	}
	p.legacyInput = append([]byte(nil), legacy.Input...)
	return nil
}

type reportParams struct {
	AgentID string `json:"agent_id"`
	Report  string `json:"report,omitempty"`
}

func (p *reportParams) UnmarshalJSON(data []byte) error {
	type raw reportParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = reportParams(current)
	var legacy struct {
		AgentID string `json:"agentId"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if strings.TrimSpace(p.AgentID) == "" {
		p.AgentID = strings.TrimSpace(legacy.AgentID)
	}
	return nil
}

type rememberReportRequestParams struct {
	AgentID     string `json:"worker_id"`
	RequesterID string `json:"sender_id"`
}

func (p *rememberReportRequestParams) UnmarshalJSON(data []byte) error {
	type raw rememberReportRequestParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = rememberReportRequestParams(current)
	var legacy struct {
		AgentID          string `json:"agentId"`
		RequesterID      string `json:"requesterId"`
		AgentIDSnake     string `json:"agent_id"`
		RequesterIDSnake string `json:"requester_id"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if strings.TrimSpace(p.AgentID) == "" {
		p.AgentID = firstTrimmed(legacy.AgentID, legacy.AgentIDSnake)
	}
	if strings.TrimSpace(p.RequesterID) == "" {
		p.RequesterID = firstTrimmed(legacy.RequesterID, legacy.RequesterIDSnake)
	}
	return nil
}

type reportEventParams struct {
	AgentID   string          `json:"agent_id"`
	Report    string          `json:"report,omitempty"`
	EventType string          `json:"event_type,omitempty"`
	EventData json.RawMessage `json:"event_data,omitempty"`
}

func (p *reportEventParams) UnmarshalJSON(data []byte) error {
	type raw reportEventParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = reportEventParams(current)
	var legacy struct {
		AgentID   string          `json:"agentId"`
		EventType string          `json:"eventType"`
		EventData json.RawMessage `json:"eventData"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if strings.TrimSpace(p.AgentID) == "" {
		p.AgentID = strings.TrimSpace(legacy.AgentID)
	}
	if strings.TrimSpace(p.EventType) == "" {
		p.EventType = strings.TrimSpace(legacy.EventType)
	}
	if len(p.EventData) == 0 {
		p.EventData = append([]byte(nil), legacy.EventData...)
	}
	return nil
}

type listDAGsParams struct {
	Status  string `json:"status,omitempty"`
	Keyword string `json:"keyword,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type updateNodeParams struct {
	dagNodeParams
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
}

func firstTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
