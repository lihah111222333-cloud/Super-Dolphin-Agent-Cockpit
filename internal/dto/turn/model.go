package turn

import (
	"encoding/json"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/eventcore"
)

type InputItem = shareddto.InputItem

type TurnSubmission struct {
	AgentID              string          `json:"agentId"`
	ThreadID             string          `json:"threadId"`
	ExpectedTurnID       string          `json:"expectedTurnId,omitempty"`
	Inputs               []InputItem     `json:"input,omitempty"`
	SelectedSkills       []string        `json:"selectedSkills,omitempty"`
	ManualSkillSelection bool            `json:"manualSkillSelection,omitempty"`
	OutputSchema         json.RawMessage `json:"outputSchema,omitempty"`
}
