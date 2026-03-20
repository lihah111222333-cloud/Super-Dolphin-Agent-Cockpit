package skill

import commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"

type Card = commandcardstore.CommandCard
type CardVersion = commandcardstore.CommandCardVersion

type CardRunResult struct {
	CardKey         string     `json:"card_key"`
	RenderedCommand string     `json:"rendered_command"`
	Exec            ExecResult `json:"exec"`
}

type ExecResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Command  string `json:"command,omitempty"`
	CWD      string `json:"cwd,omitempty"`
}

type SkillInfo struct {
	Name         string   `json:"name"`
	Dir          string   `json:"dir"`
	Description  string   `json:"description"`
	Summary      string   `json:"summary"`
	TriggerWords []string `json:"trigger_words,omitempty"`
	ForceWords   []string `json:"force_words,omitempty"`
}
