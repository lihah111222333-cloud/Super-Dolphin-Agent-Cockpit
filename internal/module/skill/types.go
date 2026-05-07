package skill

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

type ExecResult struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Command  string `json:"command,omitempty"`
	CWD      string `json:"cwd,omitempty"`
}

// SkillInfo is a type alias for contract.SkillInfo. The canonical definition
// now lives in internal/contract so that cross-module consumers (dashboard,
// prompt) do not need to import internal/module/skill.
type SkillInfo = contract.SkillInfo
