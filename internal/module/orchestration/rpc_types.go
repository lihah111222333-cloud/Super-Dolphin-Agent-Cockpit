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
		AgentID string          `json:"agentId"`
		Input   json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if strings.TrimSpace(p.AgentID) == "" {
		p.AgentID = strings.TrimSpace(legacy.AgentID)
	}
	p.legacyInput = append([]byte(nil), legacy.Input...)
	return nil
}

type reportParams struct {
	AgentID string `json:"agentId"`
	Report  string `json:"report"`
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
