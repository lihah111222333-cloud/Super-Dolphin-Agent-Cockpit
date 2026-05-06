package contract

// SkillConfig holds skill-specific configuration.
type SkillConfig struct {
	ProgressiveDisclosure bool
	TokenBudget           int
}

// AgentConfig holds agent-specific configuration.
type AgentConfig struct {
	PersistentSubagentDefault bool
}

// NotifyConfig carries the P21 P2 external-webhook egress settings.
type NotifyConfig struct {
	ChannelsJSON     string
	AllowPrivateCIDR bool
	TimeoutSeconds   int
	QueueCapacity    int
	DrainSeconds     int
}

// Config is the root configuration struct shared across the application.
// The canonical constructor (New) lives in internal/platform/config; this
// file only hosts the type definitions so that lower layers (module, store)
// can depend on them without importing a platform package.
type Config struct {
	DatabaseURL string
	RPCAddr     string
	LogLevel    string
	ProjectRoot string
	Skill       SkillConfig
	Agent       AgentConfig
	Notify      NotifyConfig
}
