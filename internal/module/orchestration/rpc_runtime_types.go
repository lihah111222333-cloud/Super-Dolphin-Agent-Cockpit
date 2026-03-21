package orchestration

import (
	"encoding/json"
	"strings"
)

type runtimeReportParams struct {
	AgentID  string `json:"agent_id"`
	Port     int    `json:"port,omitempty"`
	Provider string `json:"provider,omitempty"`
}

func (p *runtimeReportParams) UnmarshalJSON(data []byte) error {
	type raw runtimeReportParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = runtimeReportParams(current)
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
