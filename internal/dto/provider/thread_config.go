package provider

type ThreadConfigPatch struct {
	Model       *string `json:"model,omitempty"`
	Effort      *string `json:"effort,omitempty"`
	Personality *string `json:"personality,omitempty"`
	Approvals   *string `json:"approvals,omitempty"`
}

type ThreadConfigValues struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

type ThreadConfig struct {
	ThreadID               string             `json:"threadId"`
	Provider               string             `json:"provider,omitempty"`
	SupportsThreadOverride bool               `json:"supportsThreadOverride"`
	Override               ThreadConfigValues `json:"override"`
	Effective              ThreadConfigValues `json:"effective"`
}

type ThreadCompactResult struct {
	ThreadID     string `json:"threadId"`
	Command      string `json:"command"`
	BeforeTokens int    `json:"beforeTokens"`
	AfterTokens  int    `json:"afterTokens"`
	Compacted    bool   `json:"compacted"`
	Estimated    bool   `json:"estimated,omitempty"`
}
