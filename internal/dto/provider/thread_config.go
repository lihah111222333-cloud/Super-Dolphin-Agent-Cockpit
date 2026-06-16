package provider

// ThreadConfigPatch represents optional per-thread provider overrides.
type ThreadConfigPatch struct {
	Model       *string `json:"model,omitempty"`
	Effort      *string `json:"effort,omitempty"`
	Personality *string `json:"personality,omitempty"`
	Approvals   *string `json:"approvals,omitempty"`
}

// ThreadConfigValues records concrete thread configuration values after
// defaults and overrides are resolved.
type ThreadConfigValues struct {
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
	Approvals string `json:"approvals,omitempty"`
}

// ThreadConfig reports both requested override and effective provider config
// for a thread.
type ThreadConfig struct {
	ThreadID               string             `json:"threadId"`
	Provider               string             `json:"provider,omitempty"`
	SupportsThreadOverride bool               `json:"supportsThreadOverride"`
	Override               ThreadConfigValues `json:"override"`
	Effective              ThreadConfigValues `json:"effective"`
}

// ThreadCompactResult reports the outcome of a provider-side context compact
// request.
type ThreadCompactResult struct {
	ThreadID     string `json:"threadId"`
	Command      string `json:"command"`
	BeforeTokens int    `json:"beforeTokens"`
	AfterTokens  int    `json:"afterTokens"`
	Compacted    bool   `json:"compacted"`
	Estimated    bool   `json:"estimated,omitempty"`
}
