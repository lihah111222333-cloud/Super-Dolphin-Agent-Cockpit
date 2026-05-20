package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const defaultDatabaseURL = "postgres://ai:123@127.0.0.1:5432/agent_test2?sslmode=disable"

// Type aliases – canonical definitions live in contract.
type (
	Config       = contract.Config
	SkillConfig  = contract.SkillConfig
	AgentConfig  = contract.AgentConfig
	NotifyConfig = contract.NotifyConfig
)

func New() *Config {
	projectRoot := resolveProjectRoot()
	loadDotEnv(projectRoot)

	cfg := &Config{
		DatabaseURL: databaseURLFromEnv(defaultDatabaseURL),
		RPCAddr:     envOrCompat("GO_AGENT_CTL_RPC_ADDR", "RPC_ADDR", "127.0.0.1:8090"),
		LogLevel:    envOr("LOG_LEVEL", "info"),
		ProjectRoot: projectRoot,
		Skill: SkillConfig{
			ProgressiveDisclosure: envBoolOr("SKILL_PROGRESSIVE_DISCLOSURE", false),
			TokenBudget:           envPositiveIntOr("SKILL_TOKEN_BUDGET", 3000),
		},
		Agent: AgentConfig{
			PersistentSubagentDefault: envBoolOr("PERSISTENT_SUBAGENT_DEFAULT", false),
		},
		Notify: NotifyConfig{
			ChannelsJSON:     os.Getenv("NOTIFY_CHANNELS_JSON"),
			AllowPrivateCIDR: envBoolOr("NOTIFY_ALLOW_PRIVATE_CIDR", false),
			TimeoutSeconds:   envPositiveIntOr("NOTIFY_TIMEOUT_SECONDS", 10),
			QueueCapacity:    envPositiveIntOr("NOTIFY_QUEUE_CAPACITY", 512),
			DrainSeconds:     envPositiveIntOr("NOTIFY_DRAIN_SECONDS", 5),
		},
	}
	exportRPCAddrIfMissing(cfg.RPCAddr)
	exportDatabaseURLIfMissing(cfg.DatabaseURL)
	return cfg
}

func loadDotEnv(projectRoot string) {
	path := filepath.Join(strings.TrimSpace(projectRoot), ".env")
	if strings.TrimSpace(projectRoot) == "" {
		return
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(content), "\n") {
		key, value, ok := parseDotEnvLine(line)
		if !ok {
			continue
		}
		if strings.TrimSpace(os.Getenv(key)) != "" {
			continue
		}
		_ = os.Setenv(key, value)
	}
}

func parseDotEnvLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false
	}
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	return key, value, true
}

func databaseURLFromEnv(fallback string) string {
	if value := strings.TrimSpace(os.Getenv("DATABASE_URL")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("POSTGRES_CONNECTION_STRING")); value != "" {
		pkglogger.Get().Warn("config env POSTGRES_CONNECTION_STRING is deprecated; use DATABASE_URL instead")
		return value
	}
	return fallback
}

// exportRPCAddrIfMissing sets GO_AGENT_CTL_RPC_ADDR in the process environment
// when neither the canonical nor legacy env var is present. This ensures that
// normalizeManifestEnv (dto/provider/manifest.go) can propagate the resolved
// RPC address to MCP child processes (mcp-orch, mcp-lsp) automatically.
// Without this, mcp-orch falls back to localLauncher and spawns the desktop
// binary as a subprocess, which crashes immediately.
func exportRPCAddrIfMissing(addr string) {
	if strings.TrimSpace(os.Getenv("GO_AGENT_CTL_RPC_ADDR")) != "" {
		return
	}
	if strings.TrimSpace(os.Getenv("RPC_ADDR")) != "" {
		return
	}
	os.Setenv("GO_AGENT_CTL_RPC_ADDR", addr)
}

func exportDatabaseURLIfMissing(databaseURL string) {
	if strings.TrimSpace(os.Getenv("DATABASE_URL")) != "" {
		return
	}
	if strings.TrimSpace(databaseURL) == "" {
		return
	}
	os.Setenv("DATABASE_URL", databaseURL)
}

func resolveProjectRoot() string {
	if root := strings.TrimSpace(os.Getenv("PROJECT_ROOT")); root != "" {
		return root
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envOrCompat(canonical, legacy, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(canonical)); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv(legacy)); value != "" {
		pkglogger.Get().Warn("config env deprecated", "legacy", legacy, "canonical", canonical)
		return value
	}
	return fallback
}

func envBoolOr(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envPositiveIntOr(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}
