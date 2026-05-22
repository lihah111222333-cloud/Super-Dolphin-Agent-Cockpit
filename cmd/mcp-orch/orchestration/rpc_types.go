package orchestration

import (
	"encoding/json"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type launchParams struct {
	AgentID      string            `json:"id"`
	Name         string            `json:"name,omitempty"`
	Prompt       string            `json:"prompt,omitempty"`
	CWD          string            `json:"cwd,omitempty"`
	Instructions string            `json:"instructions,omitempty"`
	Command      []string          `json:"command,omitempty"`
	ParentID     string            `json:"parent_id,omitempty"`
	AgentType    string            `json:"agent_type,omitempty"`
	AgentKey     string            `json:"agent_key,omitempty"`
	PromptKey    string            `json:"prompt_key,omitempty"`
	MemoryScope  string            `json:"memory_scope,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
}

type launchConfigParams struct {
	ParentID       string `json:"parent_id,omitempty"`
	ParentIDAlt    string `json:"parentId,omitempty"`
	ParentIDLegacy string `json:"parentID,omitempty"`
	AgentType      string `json:"agent_type,omitempty"`
	AgentTypeAlt   string `json:"agentType,omitempty"`
	PromptKey      string `json:"prompt_key,omitempty"`
	PromptKeyAlt   string `json:"promptKey,omitempty"`
	MemoryScope    string `json:"memory_scope,omitempty"`
	MemoryScopeAlt string `json:"memoryScope,omitempty"`
	AgentScope     string `json:"agent_memory_scope,omitempty"`
	AgentScopeAlt  string `json:"agentMemoryScope,omitempty"`
}

func (p *launchParams) UnmarshalJSON(data []byte) error {
	type current launchParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		AgentID      string             `json:"agentId"`
		AgentIDSnake string             `json:"agent_id"`
		ParentID     string             `json:"parentId"`
		ParentIDAlt  string             `json:"parentID"`
		AgentType    string             `json:"agentType"`
		AgentTypeAlt string             `json:"agent_type"`
		PromptKey    string             `json:"promptKey"`
		PromptKeyAlt string             `json:"prompt_key"`
		MemoryScope  string             `json:"memoryScope"`
		MemoryAlt    string             `json:"memory_scope"`
		AgentScope   string             `json:"agent_memory_scope"`
		Config       launchConfigParams `json:"config"`
	}) error {
		*p = launchParams(*raw)
		if strings.TrimSpace(p.AgentID) == "" {
			p.AgentID = shared.FirstTrimmed(legacy.AgentIDSnake, legacy.AgentID)
		}
		if strings.TrimSpace(p.ParentID) == "" {
			p.ParentID = shared.FirstTrimmed(
				legacy.ParentID,
				legacy.ParentIDAlt,
				legacy.Config.ParentID,
				legacy.Config.ParentIDAlt,
				legacy.Config.ParentIDLegacy,
			)
		}
		if strings.TrimSpace(p.AgentType) == "" {
			p.AgentType = shared.FirstTrimmed(
				legacy.AgentTypeAlt,
				legacy.AgentType,
				legacy.Config.AgentType,
				legacy.Config.AgentTypeAlt,
			)
		}
		if strings.TrimSpace(p.PromptKey) == "" {
			p.PromptKey = shared.FirstTrimmed(
				legacy.PromptKeyAlt,
				legacy.PromptKey,
				legacy.Config.PromptKey,
				legacy.Config.PromptKeyAlt,
			)
		}
		if strings.TrimSpace(p.MemoryScope) == "" {
			p.MemoryScope = shared.FirstTrimmed(
				legacy.MemoryAlt,
				legacy.MemoryScope,
				legacy.AgentScope,
				legacy.Config.MemoryScope,
				legacy.Config.MemoryScopeAlt,
				legacy.Config.AgentScope,
				legacy.Config.AgentScopeAlt,
			)
		}
		return nil
	})
}

type agentIDParams struct {
	AgentID string `json:"agent_id"`
}

func (p *agentIDParams) UnmarshalJSON(data []byte) error {
	type current agentIDParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		AgentID string `json:"agentId"`
	}) error {
		*p = agentIDParams(*raw)
		if strings.TrimSpace(p.AgentID) == "" {
			p.AgentID = strings.TrimSpace(legacy.AgentID)
		}
		return nil
	})
}

type dagKeyParams struct {
	DagKey string `json:"dag_key"`
}

func (p *dagKeyParams) UnmarshalJSON(data []byte) error {
	type current dagKeyParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		DagKey string `json:"dagKey"`
	}) error {
		*p = dagKeyParams(*raw)
		if strings.TrimSpace(p.DagKey) == "" {
			p.DagKey = strings.TrimSpace(legacy.DagKey)
		}
		return nil
	})
}

type dagNodeParams struct {
	DagKey  string `json:"dag_key"`
	NodeKey string `json:"node_key"`
}

func (p *dagNodeParams) UnmarshalJSON(data []byte) error {
	type current dagNodeParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		DagKey  string `json:"dagKey"`
		NodeKey string `json:"nodeKey"`
	}) error {
		*p = dagNodeParams(*raw)
		if strings.TrimSpace(p.DagKey) == "" {
			p.DagKey = strings.TrimSpace(legacy.DagKey)
		}
		if strings.TrimSpace(p.NodeKey) == "" {
			p.NodeKey = strings.TrimSpace(legacy.NodeKey)
		}
		return nil
	})
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
	type current submitParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		AgentID              string          `json:"agentId"`
		Input                json.RawMessage `json:"input"`
		SelectedSkills       []string        `json:"selectedSkills"`
		ManualSkillSelection *bool           `json:"manualSkillSelection"`
		OutputSchema         json.RawMessage `json:"outputSchema"`
	}) error {
		*p = submitParams(*raw)
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
	})
}

type reportParams struct {
	AgentID string `json:"agent_id"`
	Report  string `json:"report,omitempty"`
}

func (p *reportParams) UnmarshalJSON(data []byte) error {
	type current reportParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		AgentID string `json:"agentId"`
	}) error {
		*p = reportParams(*raw)
		if strings.TrimSpace(p.AgentID) == "" {
			p.AgentID = strings.TrimSpace(legacy.AgentID)
		}
		return nil
	})
}

type rememberReportRequestParams struct {
	AgentID     string `json:"worker_id"`
	RequesterID string `json:"sender_id"`
}

func (p *rememberReportRequestParams) UnmarshalJSON(data []byte) error {
	type current rememberReportRequestParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		AgentID          string `json:"agentId"`
		RequesterID      string `json:"requesterId"`
		AgentIDSnake     string `json:"agent_id"`
		RequesterIDSnake string `json:"requester_id"`
	}) error {
		*p = rememberReportRequestParams(*raw)
		if strings.TrimSpace(p.AgentID) == "" {
			p.AgentID = shared.FirstTrimmed(legacy.AgentID, legacy.AgentIDSnake)
		}
		if strings.TrimSpace(p.RequesterID) == "" {
			p.RequesterID = shared.FirstTrimmed(legacy.RequesterID, legacy.RequesterIDSnake)
		}
		return nil
	})
}

type reportEventParams struct {
	AgentID   string          `json:"agent_id"`
	Report    string          `json:"report,omitempty"`
	EventType string          `json:"event_type,omitempty"`
	EventData json.RawMessage `json:"event_data,omitempty"`
}

func (p *reportEventParams) UnmarshalJSON(data []byte) error {
	type current reportEventParams
	return decodeLegacyAlias(data, new(current), func(raw *current, legacy *struct {
		AgentID   string          `json:"agentId"`
		EventType string          `json:"eventType"`
		EventData json.RawMessage `json:"eventData"`
	}) error {
		*p = reportEventParams(*raw)
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
	})
}
