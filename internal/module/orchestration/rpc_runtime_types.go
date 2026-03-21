package orchestration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type runtimeReportParams struct {
	AgentID  string `json:"agent_id"`
	Port     int    `json:"port,omitempty"`
	Provider string `json:"provider,omitempty"`
}

func (p *runtimeReportParams) UnmarshalJSON(data []byte) error {
	var payload struct {
		AgentID       string `json:"agent_id"`
		AgentIDLegacy string `json:"agentId"`
		Port          int    `json:"port,omitempty"`
		Provider      string `json:"provider,omitempty"`
	}
	if err := decodeStrictRuntimeReportJSON(data, &payload); err != nil {
		return err
	}
	agentID := strings.TrimSpace(payload.AgentID)
	legacyAgentID := strings.TrimSpace(payload.AgentIDLegacy)
	if agentID != "" && legacyAgentID != "" && agentID != legacyAgentID {
		return fmt.Errorf("runtime report agent id aliases conflict: agent_id=%q agentId=%q", agentID, legacyAgentID)
	}
	p.AgentID = firstTrimmed(agentID, legacyAgentID)
	p.Port = payload.Port
	p.Provider = payload.Provider
	return nil
}

func decodeStrictRuntimeReportJSON(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(new(struct{})); err != io.EOF {
		if err == nil {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}
