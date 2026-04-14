package memory

import (
	"os"
	"path/filepath"
	"strings"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

const (
	envMemoryRoot        = "MULTI_AGENT_MEMORY_DIR"
	envEnableMemory      = "ENABLE_MEMORY_SYSTEM"
	envEnableMemoryTools = "ENABLE_MEMORY_TOOLS"
)

type Config struct {
	Enabled     bool
	EnableTools bool
	RootDir     string
	ProjectRoot string
	MachineID   string
}

func NewConfig(platformCfg *platformconfig.Config) *Config {
	cfg := &Config{
		Enabled:     parseBoolEnv(envEnableMemory, false),
		EnableTools: parseBoolEnv(envEnableMemoryTools, false),
		RootDir:     defaultRootDir(platformCfg),
		ProjectRoot: projectRoot(platformCfg),
		MachineID:   machineID(),
	}
	if root := strings.TrimSpace(os.Getenv(envMemoryRoot)); root != "" {
		cfg.RootDir = root
	}
	return cfg
}

func defaultRootDir(platformCfg *platformconfig.Config) string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".multi-agent", "memory")
	}
	if platformCfg != nil && strings.TrimSpace(platformCfg.ProjectRoot) != "" {
		return filepath.Join(platformCfg.ProjectRoot, ".multi-agent", "memory")
	}
	return ""
}

func projectRoot(platformCfg *platformconfig.Config) string {
	if platformCfg != nil && strings.TrimSpace(platformCfg.ProjectRoot) != "" {
		return platformCfg.ProjectRoot
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return ""
}

func machineID() string {
	if host, err := os.Hostname(); err == nil && strings.TrimSpace(host) != "" {
		return host
	}
	return "local-machine"
}

func parseBoolEnv(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
