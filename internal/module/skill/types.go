package skill

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
	// Security / progressive-disclosure fields (P20 Phase 1).
	// Trust is the trust scope: "user" (trusted) / "project" (untrusted, from git clone) / "signed".
	// Default is inferred from the skill root directory; explicit frontmatter `trust:` overrides.
	Trust TrustScope `json:"trust,omitempty"`
	// AllowedTools whitelist names (e.g. "Read", "skill_expand"). Empty = inherit session defaults.
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// DisableModelInvocation: when true, the skill is not exposed for the model to auto-call;
	// only a user-issued slash command (`/name`) can trigger it. Mirrors Claude Code frontmatter semantics.
	DisableModelInvocation bool `json:"disable_model_invocation,omitempty"`
	// ContentHash is the SHA-256 of the SKILL.md body (hex lowercase), used by the approval cache
	// to key on (name, hash) and force re-approval when body mutates (TOCTOU defense).
	ContentHash string `json:"content_hash,omitempty"`
	// DisclosureTier is a non-realtime usage-frequency snapshot for UI display.
	DisclosureTier string `json:"disclosure_tier,omitempty"`
}
