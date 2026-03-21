package orchestration

import (
	"encoding/json"
	"strings"
)

type launchParams struct {
	AgentID      string            `json:"id"`
	Name         string            `json:"name,omitempty"`
	Prompt       string            `json:"prompt,omitempty"`
	CWD          string            `json:"cwd,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Command      []string          `json:"command,omitempty"`
	ParentID     string            `json:"parent_id,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
}

type launchConfigParams struct {
	ParentID       string `json:"parent_id,omitempty"`
	ParentIDAlt    string `json:"parentId,omitempty"`
	ParentIDLegacy string `json:"parentID,omitempty"`
}

func (p *launchParams) UnmarshalJSON(data []byte) error {
	type raw launchParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = launchParams(current)
	var legacy struct {
		AgentID      string             `json:"agentId"`
		AgentIDSnake string             `json:"agent_id"`
		ParentID     string             `json:"parentId"`
		ParentIDAlt  string             `json:"parentID"`
		Config       launchConfigParams `json:"config"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if strings.TrimSpace(p.AgentID) == "" {
		p.AgentID = firstTrimmed(legacy.AgentIDSnake, legacy.AgentID)
	}
	if strings.TrimSpace(p.ParentID) == "" {
		p.ParentID = firstTrimmed(
			legacy.ParentID,
			legacy.ParentIDAlt,
			legacy.Config.ParentID,
			legacy.Config.ParentIDAlt,
			legacy.Config.ParentIDLegacy,
		)
	}
	return nil
}

type agentIDParams struct {
	AgentID string `json:"agent_id"`
}

func (p *agentIDParams) UnmarshalJSON(data []byte) error {
	type raw agentIDParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = agentIDParams(current)
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

type dagKeyParams struct {
	DagKey string `json:"dag_key"`
}

func (p *dagKeyParams) UnmarshalJSON(data []byte) error {
	type raw dagKeyParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = dagKeyParams(current)
	var legacy struct {
		DagKey string `json:"dagKey"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if strings.TrimSpace(p.DagKey) == "" {
		p.DagKey = strings.TrimSpace(legacy.DagKey)
	}
	return nil
}

type dagNodeParams struct {
	DagKey  string `json:"dag_key"`
	NodeKey string `json:"node_key"`
}

func (p *dagNodeParams) UnmarshalJSON(data []byte) error {
	type raw dagNodeParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = dagNodeParams(current)
	var legacy struct {
		DagKey  string `json:"dagKey"`
		NodeKey string `json:"nodeKey"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if strings.TrimSpace(p.DagKey) == "" {
		p.DagKey = strings.TrimSpace(legacy.DagKey)
	}
	if strings.TrimSpace(p.NodeKey) == "" {
		p.NodeKey = strings.TrimSpace(legacy.NodeKey)
	}
	return nil
}

type createDAGParams struct {
	DagKey      string                `json:"dag_key"`
	Title       string                `json:"title"`
	Description string                `json:"description,omitempty"`
	CreatedBy   string                `json:"created_by,omitempty"`
	Metadata    json.RawMessage       `json:"metadata,omitempty"`
	Nodes       []createDAGNodeParams `json:"nodes,omitempty"`
}

func (p *createDAGParams) UnmarshalJSON(data []byte) error {
	type raw createDAGParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = createDAGParams(current)
	var legacy struct {
		DagKey    string `json:"dagKey"`
		CreatedBy string `json:"createdBy"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if strings.TrimSpace(p.DagKey) == "" {
		p.DagKey = strings.TrimSpace(legacy.DagKey)
	}
	if strings.TrimSpace(p.CreatedBy) == "" {
		p.CreatedBy = strings.TrimSpace(legacy.CreatedBy)
	}
	return nil
}

type createDAGNodeParams struct {
	NodeKey    string          `json:"node_key"`
	Title      string          `json:"title"`
	NodeType   string          `json:"node_type,omitempty"`
	AssignedTo string          `json:"assigned_to,omitempty"`
	DependsOn  []string        `json:"depends_on,omitempty"`
	CommandRef string          `json:"command_ref,omitempty"`
	Config     json.RawMessage `json:"config,omitempty"`
}

func (p *createDAGNodeParams) UnmarshalJSON(data []byte) error {
	type raw createDAGNodeParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = createDAGNodeParams(current)
	var legacy struct {
		NodeKey    string   `json:"nodeKey"`
		NodeType   string   `json:"nodeType"`
		AssignedTo string   `json:"assignedTo"`
		DependsOn  []string `json:"dependsOn"`
		CommandRef string   `json:"commandRef"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if strings.TrimSpace(p.NodeKey) == "" {
		p.NodeKey = strings.TrimSpace(legacy.NodeKey)
	}
	if strings.TrimSpace(p.NodeType) == "" {
		p.NodeType = strings.TrimSpace(legacy.NodeType)
	}
	if strings.TrimSpace(p.AssignedTo) == "" {
		p.AssignedTo = strings.TrimSpace(legacy.AssignedTo)
	}
	if len(p.DependsOn) == 0 && len(legacy.DependsOn) > 0 {
		p.DependsOn = append([]string(nil), legacy.DependsOn...)
	}
	if strings.TrimSpace(p.CommandRef) == "" {
		p.CommandRef = strings.TrimSpace(legacy.CommandRef)
	}
	return nil
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

func (p *updateNodeParams) UnmarshalJSON(data []byte) error {
	type raw updateNodeParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = updateNodeParams(current)
	var legacy struct {
		DagKey  string `json:"dagKey"`
		NodeKey string `json:"nodeKey"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if strings.TrimSpace(p.DagKey) == "" {
		p.DagKey = strings.TrimSpace(legacy.DagKey)
	}
	if strings.TrimSpace(p.NodeKey) == "" {
		p.NodeKey = strings.TrimSpace(legacy.NodeKey)
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
