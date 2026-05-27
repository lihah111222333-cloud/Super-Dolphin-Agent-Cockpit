package builtinprompts

import "encoding/json"

const (
	firstBuiltinID        int64 = -100000
	firstBuiltinSectionID int64 = -200000
)

type readFileFS interface {
	ReadFile(name string) ([]byte, error)
}

type manifestConfig struct {
	Version   int      `json:"version"`
	Templates []string `json:"templates"`
}

type templateConfig struct {
	ID          *int64          `json:"id"`
	PromptKey   string          `json:"prompt_key"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title"`
	AgentKey    string          `json:"agent_key"`
	ToolName    string          `json:"tool_name"`
	PromptText  string          `json:"prompt_text"`
	WhenToUse   string          `json:"when_to_use"`
	Description string          `json:"description"`
	Tags        []string        `json:"tags"`
	Enabled     *bool           `json:"enabled"`
	Scope       string          `json:"scope"`
	MatchWhen   json.RawMessage `json:"match_when"`
	Priority    int             `json:"priority"`
	Sections    []sectionConfig `json:"sections"`
}

type sectionConfig struct {
	SectionKey  string          `json:"section_key"`
	Region      string          `json:"region"`
	Ordinal     int             `json:"ordinal"`
	BodyFile    string          `json:"body_file"`
	EnableWhen  json.RawMessage `json:"enable_when"`
	Enabled     *bool           `json:"enabled"`
	TriggerType string          `json:"trigger_type"`
	RecallTopic string          `json:"recall_topic"`
}

type loadedTemplate struct {
	Path     string
	Config   templateConfig
	Sections []loadedSection
}

type loadedSection struct {
	Config sectionConfig
	Body   string
}
