package skill

import "encoding/json"

type cardKeyParams struct {
	Key string `json:"key"`
}

type cardPayload struct {
	Key             string          `json:"key"`
	Title           string          `json:"title"`
	Description     string          `json:"description,omitempty"`
	CommandTemplate string          `json:"command_template"`
	ArgsSchema      json.RawMessage `json:"args_schema,omitempty"`
	RiskLevel       string          `json:"risk_level,omitempty"`
	Enabled         *bool           `json:"enabled,omitempty"`
	CreatedBy       string          `json:"created_by,omitempty"`
	UpdatedBy       string          `json:"updated_by,omitempty"`
}
type createCardParams cardPayload
type updateCardParams cardPayload
type runCardParams struct {
	Key  string         `json:"key"`
	Args map[string]any `json:"args,omitempty"`
}
type execParams struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	CWD     string   `json:"cwd,omitempty"`
}
