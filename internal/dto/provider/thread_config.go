package provider

// ThreadConfigPatch 是 thread 配置的局部更新载荷，指针字段表示"不传则不覆盖"。
type ThreadConfigPatch struct {
	Model       *string `json:"model,omitempty"`
	Effort      *string `json:"effort,omitempty"`
	Personality *string `json:"personality,omitempty"`
	Approvals   *string `json:"approvals,omitempty"`
}

// ThreadConfigValues 是 thread 配置的值快照，用于展示当前生效或覆盖配置。
type ThreadConfigValues struct {
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
	Approvals string `json:"approvals,omitempty"`
}

// ThreadConfig 是 thread 完整配置视图，包含覆盖值和最终生效值。
type ThreadConfig struct {
	ThreadID               string             `json:"threadId"`
	Provider               string             `json:"provider,omitempty"`
	SupportsThreadOverride bool               `json:"supportsThreadOverride"` // 当前 provider 是否支持 thread 级配置覆盖。
	AvailableModels        []string           `json:"availableModels"`        // 当前 provider session 实时返回的全部可选模型。
	Override               ThreadConfigValues `json:"override"`               // 用户显式设置的覆盖配置。
	Effective              ThreadConfigValues `json:"effective"`              // 合并后最终生效的配置。
}

// ThreadCompactResult 是 thread 上下文压缩操作的结果。
type ThreadCompactResult struct {
	ThreadID     string `json:"threadId"`
	Command      string `json:"command"`
	BeforeTokens int    `json:"beforeTokens"`        // 压缩前 token 数。
	AfterTokens  int    `json:"afterTokens"`         // 压缩后 token 数。
	Compacted    bool   `json:"compacted"`           // 是否实际执行了压缩。
	Estimated    bool   `json:"estimated,omitempty"` // token 数是否为估算值。
}
