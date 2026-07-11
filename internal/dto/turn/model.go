package turn

import (
	"encoding/json"

	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
)

// InputItem 复用 shared DTO 的输入项定义，保持 turn payload 与 provider payload 字段一致。
type InputItem = shareddto.InputItem

// TurnSubmission 是提交 turn 时进入 turn 模块的内部 DTO，保留 skill 选择和 schema 约束。
type TurnSubmission struct {
	AgentID              string          `json:"agentId"`
	ThreadID             string          `json:"threadId"`
	ExpectedTurnID       string          `json:"expectedTurnId,omitempty"`
	Inputs               []InputItem     `json:"input,omitempty"`
	SelectedSkills       []string        `json:"selectedSkills,omitempty"`
	ManualSkillSelection bool            `json:"manualSkillSelection,omitempty"`
	OutputSchema         json.RawMessage `json:"outputSchema,omitempty"`
}
