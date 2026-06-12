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

// EmbeddedPostgresConfig describes the app-managed PostgreSQL runtime used
// when no external DATABASE_URL is supplied.
type EmbeddedPostgresConfig struct {
	Enabled               bool
	Owner                 bool
	RecoverRunningDataDir bool
	BinDir                string
	ShareDir              string
	DataDir               string
	RuntimeDir            string
	LogPath               string
	DatabaseName          string
	UserName              string
	Port                  int
	ResolveError          string
}

const (
	LSPServiceJSTS   = "jsts"
	LSPServicePython = "python"
	LSPServiceRust   = "rust"
	LSPServiceJava   = "java"
	LSPServiceCSS    = "css"
	LSPServiceShell  = "shell"
)

// LSPConfig holds language-service startup and indexing configuration.
type LSPConfig struct {
	NoiseDirNames                    []string
	GoDirectoryFilters               []string
	ProjectAdapters                  map[string]LSPProjectAdapterConfig
	DocumentFallbackLanguageIDs      []string
	DisableInitialWorkspaceBootstrap bool
}

// LSPProjectAdapterConfig holds per-language project discovery configuration.
type LSPProjectAdapterConfig struct {
	RootMarkers           []string
	IgnoredDirNames       []string
	FirstSourceExtensions []string
}

// Config is the root configuration struct shared across the application.
// The canonical constructor (New) lives in internal/platform/config; this
// file only hosts the type definitions so that lower layers (module, store)
// can depend on them without importing a platform package.
type Config struct {
	DatabaseURL      string
	SQLitePath       string
	RPCAddr          string
	LogLevel         string
	ProjectRoot      string
	EmbeddedPostgres EmbeddedPostgresConfig
	Skill            SkillConfig
	Agent            AgentConfig
	Notify           NotifyConfig
	LSP              LSPConfig
}
